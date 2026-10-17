package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Notes: what the church may need to know about a person (allergies and so
// on). Who may see a note is decided in the access package (CanSeeNote);
// this file only reads and writes rows. Note bodies never go into the
// audit trail or the logs: logChange gets "added note", never the text.

// The note kinds.
const (
	NoteAllergy       = "allergy"
	NoteMedical       = "medical"
	NoteDietary       = "dietary"
	NoteAccessibility = "accessibility"
	NoteGeneral       = "general"
)

// NoteKinds lists the kinds in the order a form shows them.
var NoteKinds = []string{NoteAllergy, NoteMedical, NoteDietary, NoteAccessibility, NoteGeneral}

// NoteKindLabel is a kind in words.
func NoteKindLabel(kind string) string {
	switch kind {
	case NoteAllergy:
		return "Allergy"
	case NoteMedical:
		return "Medical"
	case NoteDietary:
		return "Dietary"
	case NoteAccessibility:
		return "Accessibility"
	}
	return "General"
}

// Who may see a note (visible_to).
const (
	NoteHousehold = "household" // the person, their household heads, staff
	NotePrivate   = "private"   // the person and staff
	NoteDirectory = "directory" // every member who can see the person
)

// NoteVisibilities lists them in the order a form shows them.
var NoteVisibilities = []string{NoteHousehold, NotePrivate, NoteDirectory}

// NoteVisibilityLabel is a visibility in words.
func NoteVisibilityLabel(v string) string {
	switch v {
	case NotePrivate:
		return "Only them and staff"
	case NoteDirectory:
		return "Every member"
	}
	return "Them, their household heads and staff"
}

func validNoteKind(k string) bool {
	for _, v := range NoteKinds {
		if v == k {
			return true
		}
	}
	return false
}

func validNoteVisibility(k string) bool {
	for _, v := range NoteVisibilities {
		if v == k {
			return true
		}
	}
	return false
}

// Note is one row of notes.
type Note struct {
	ID               int64
	PersonID         int64
	Kind             string
	Body             string
	VisibleTo        string
	ShareWithSignups bool
	CreatedBy        int64
	CreatedAt        string
	UpdatedBy        int64
	UpdatedAt        string
}

// NoteInput is what a note form holds.
type NoteInput struct {
	Kind             string
	Body             string
	VisibleTo        string
	ShareWithSignups bool
}

// Clean trims the text and fills in the defaults (general, household).
func (in NoteInput) Clean() NoteInput {
	in.Body = strings.TrimSpace(in.Body)
	if in.Kind == "" {
		in.Kind = NoteGeneral
	}
	if in.VisibleTo == "" {
		in.VisibleTo = NoteHousehold
	}
	return in
}

// Validate lists the problems with a (cleaned) note, in words.
func (in NoteInput) Validate() []string {
	var errs []string
	if in.Body == "" {
		errs = append(errs, "Write the note.")
	}
	if len(in.Body) > 4000 {
		errs = append(errs, "The note is too long (4000 characters at most).")
	}
	if !validNoteKind(in.Kind) {
		errs = append(errs, "Pick a kind of note.")
	}
	if !validNoteVisibility(in.VisibleTo) {
		errs = append(errs, "Pick who can see the note.")
	}
	return errs
}

const noteCols = `id, person_id, kind, body, visible_to, share_with_signups,
	COALESCE(created_by, 0), created_at, COALESCE(updated_by, 0), updated_at`

func scanNote(sc interface{ Scan(...any) error }) (Note, error) {
	var n Note
	err := sc.Scan(&n.ID, &n.PersonID, &n.Kind, &n.Body, &n.VisibleTo, &n.ShareWithSignups,
		&n.CreatedBy, &n.CreatedAt, &n.UpdatedBy, &n.UpdatedAt)
	return n, err
}

func scanNotes(rows *sql.Rows, err error) ([]Note, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddNote adds a note to a person.
func (s *Store) AddNote(personID int64, in NoteInput, by int64) (int64, error) {
	in = in.Clean()
	if errs := in.Validate(); len(errs) > 0 {
		return 0, errors.New(errs[0])
	}
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO notes (person_id, kind, body, visible_to, share_with_signups, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, personID, in.Kind, in.Body, in.VisibleTo, in.ShareWithSignups, nullInt(by), nullInt(by))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "added note")
	})
	return id, err
}

// UpdateNote changes a note.
func (s *Store) UpdateNote(id int64, in NoteInput, by int64) error {
	in = in.Clean()
	if errs := in.Validate(); len(errs) > 0 {
		return errors.New(errs[0])
	}
	return s.inTx(func(tx *sql.Tx) error {
		var personID int64
		if err := tx.QueryRow(`SELECT person_id FROM notes WHERE id = ?`, id).Scan(&personID); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE notes SET kind = ?, body = ?, visible_to = ?, share_with_signups = ?,
			updated_by = ?, updated_at = datetime('now') WHERE id = ?`,
			in.Kind, in.Body, in.VisibleTo, in.ShareWithSignups, nullInt(by), id); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "edited note")
	})
}

// DeleteNote removes a note.
func (s *Store) DeleteNote(id, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		var personID int64
		if err := tx.QueryRow(`SELECT person_id FROM notes WHERE id = ?`, id).Scan(&personID); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`DELETE FROM notes WHERE id = ?`, id); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "deleted note")
	})
}

// GetNote reads one note. It does no viewer check: callers go through
// access.CanSeeNote / access.CanWriteNotes first (see the handlers).
func (s *Store) GetNote(id int64) (Note, error) {
	n, err := scanNote(s.DB.QueryRow(`SELECT `+noteCols+` FROM notes WHERE id = ?`, id))
	return n, notFound(err)
}

// NotesOf reads every note about a person, with no viewer check. Only the
// access package may call it (access.NotesFor filters with CanSeeNote);
// nothing that prints, exports or serves an API may.
func (s *Store) NotesOf(personID int64) ([]Note, error) {
	return scanNotes(s.DB.Query(`SELECT `+noteCols+` FROM notes WHERE person_id = ?
		ORDER BY CASE kind WHEN 'allergy' THEN 0 WHEN 'medical' THEN 1 WHEN 'dietary' THEN 2 WHEN 'accessibility' THEN 3 ELSE 4 END, id`, personID))
}

// SharedNotes returns the notes that have Share with sign-ups ticked, for
// the given people and kinds only. It is the one way notes leave the
// directory (the sign-ups API, step 14). The caller must already have
// decided the people may be shown (contacts and archived left out) and
// must still pass nothing else through.
func (s *Store) SharedNotes(personIDs []int64, kinds []string) ([]Note, error) {
	if len(personIDs) == 0 || len(kinds) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(personIDs)+len(kinds))
	for _, id := range personIDs {
		args = append(args, id)
	}
	for _, k := range kinds {
		args = append(args, k)
	}
	q := `SELECT ` + noteCols + ` FROM notes WHERE share_with_signups = 1
		AND person_id IN (` + placeholders(len(personIDs)) + `)
		AND kind IN (` + placeholders(len(kinds)) + `) ORDER BY person_id, id`
	return scanNotes(s.DB.Query(q, args...))
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
