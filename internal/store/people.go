package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/tylergoza/church-directory/internal/dates"
)

// Person is a row of people: a member profile, not a login.
type Person struct {
	ID                   int64
	FirstName, LastName  string
	GoesBy, Suffix       string
	Birth, Anniversary   dates.Partial
	Email, Phone         string
	Photo                string // file name in photos/, or ""
	Kind                 string // KindMember or KindContact
	MultiHousehold       bool
	MultiReason          string
	UserID               int64 // the linked login, 0 for none
	LoginAllowed         bool
	HeadsMayEdit         bool
	HeadsMayEditLockedBy int64
	HeadsMayEditLocked   bool
	HeadsMayEditReason   string
	AgeAttestedBy        int64
	AgeAttestedAt        string
	Pending              bool
	PendingReason        string
	PendingName          string
	ArchivedAt           string // "" unless archived
	CreatedBy            int64
	CreatedAt, UpdatedAt string
}

const (
	KindMember  = "member"
	KindContact = "contact"
)

// Name is how the person's name shows everywhere (see DisplayName).
func (p Person) Name() string { return DisplayName(p.FirstName, p.LastName, p.GoesBy, p.Suffix) }

// LegalName is the name with the first name rather than the goes-by.
func (p Person) LegalName() string { return LegalName(p.FirstName, p.LastName, p.Suffix) }

func (p Person) Archived() bool { return p.ArchivedAt != "" }

// NameKey is the person's name as the name-match rule compares it.
func (p Person) NameKey() NameKey { return NewNameKey(p.FirstName, p.LastName, p.GoesBy, p.Suffix) }

// PersonInput is what staff type into the person form.
type PersonInput struct {
	FirstName, LastName string
	GoesBy, Suffix      string
	Birth, Anniversary  dates.Partial
	Email, Phone        string
	Kind                string
}

func (in *PersonInput) clean() {
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.GoesBy = strings.TrimSpace(in.GoesBy)
	in.Suffix = strings.TrimSpace(in.Suffix)
	in.Email = strings.TrimSpace(in.Email)
	in.Phone = strings.TrimSpace(in.Phone)
	if in.Kind == "" {
		in.Kind = KindMember
	}
}

// Validate cleans the input up (trimming, the default kind) and lists what
// is wrong with it, in words for the person typing it.
func (in *PersonInput) Validate() []string {
	in.clean()
	var errs []string
	if in.FirstName == "" {
		errs = append(errs, "First name is required.")
	}
	if in.LastName == "" {
		errs = append(errs, "Last name is required.")
	}
	for label, v := range map[string]string{"Name": in.FirstName + in.LastName + in.GoesBy + in.Suffix, "Email": in.Email, "Phone": in.Phone} {
		if len(v) > 300 {
			errs = append(errs, label+" is too long.")
		}
	}
	if in.Kind != KindMember && in.Kind != KindContact {
		errs = append(errs, "Kind must be member or contact.")
	}
	if in.Email != "" && (!strings.Contains(in.Email, "@") || strings.ContainsAny(in.Email, " \t\r\n")) {
		errs = append(errs, "That email address doesn't look right.")
	}
	if err := in.Birth.Validate(); err != nil {
		errs = append(errs, "Birthday: "+err.Error()+".")
	}
	if err := in.Anniversary.Validate(); err != nil {
		errs = append(errs, "Anniversary: "+err.Error()+".")
	}
	return errs
}

const personCols = `id, first_name, last_name, goes_by, suffix,
	COALESCE(birth_month, 0), COALESCE(birth_day, 0), COALESCE(birth_year, 0),
	COALESCE(anniv_month, 0), COALESCE(anniv_day, 0), COALESCE(anniv_year, 0),
	email, phone, photo, kind, multi_household, multi_reason,
	COALESCE(user_id, 0), login_allowed, heads_may_edit,
	COALESCE(heads_may_edit_locked_by, 0), heads_may_edit_locked, heads_may_edit_reason,
	COALESCE(age_attested_by, 0), COALESCE(age_attested_at, ''),
	pending, pending_reason, pending_name,
	COALESCE(archived_at, ''), COALESCE(created_by, 0), created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

// personDests are the places personCols scans into.
func personDests(p *Person) []any {
	return []any{&p.ID, &p.FirstName, &p.LastName, &p.GoesBy, &p.Suffix,
		&p.Birth.Month, &p.Birth.Day, &p.Birth.Year,
		&p.Anniversary.Month, &p.Anniversary.Day, &p.Anniversary.Year,
		&p.Email, &p.Phone, &p.Photo, &p.Kind, &p.MultiHousehold, &p.MultiReason,
		&p.UserID, &p.LoginAllowed, &p.HeadsMayEdit,
		&p.HeadsMayEditLockedBy, &p.HeadsMayEditLocked, &p.HeadsMayEditReason,
		&p.AgeAttestedBy, &p.AgeAttestedAt,
		&p.Pending, &p.PendingReason, &p.PendingName,
		&p.ArchivedAt, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt}
}

func scanPerson(row scanner) (Person, error) {
	var p Person
	err := row.Scan(personDests(&p)...)
	return p, err
}

// dateArg is a date part for the database: NULL when empty.
func dateArg(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// GetPerson loads one person.
func (s *Store) GetPerson(id int64) (Person, error) {
	p, err := scanPerson(s.DB.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, id))
	return p, notFound(err)
}

// CreatePerson adds a person added by staff (so not pending). The input
// must already pass Validate. by is the staff user's ID.
func (s *Store) CreatePerson(in PersonInput, by int64) (int64, error) {
	in.clean()
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			INSERT INTO people (first_name, last_name, goes_by, suffix,
				birth_month, birth_day, birth_year, anniv_month, anniv_day, anniv_year,
				email, phone, kind, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.FirstName, in.LastName, in.GoesBy, in.Suffix,
			dateArg(in.Birth.Month), dateArg(in.Birth.Day), dateArg(in.Birth.Year),
			dateArg(in.Anniversary.Month), dateArg(in.Anniversary.Day), dateArg(in.Anniversary.Year),
			in.Email, in.Phone, in.Kind, nullInt(by))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, id, 0, "added person")
	})
	return id, err
}

// UpdatePerson saves the person form. The audit row names the parts that
// changed ("edited name, birthday"), never their values; nothing is written
// when nothing changed.
func (s *Store) UpdatePerson(id int64, in PersonInput, by int64) error {
	in.clean()
	return s.inTx(func(tx *sql.Tx) error {
		old, err := scanPerson(tx.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, id))
		if err != nil {
			return notFound(err)
		}
		var what []string
		if old.FirstName != in.FirstName || old.LastName != in.LastName || old.GoesBy != in.GoesBy || old.Suffix != in.Suffix {
			what = append(what, "name")
		}
		if old.Birth != in.Birth {
			what = append(what, "birthday")
		}
		if old.Anniversary != in.Anniversary {
			what = append(what, "anniversary")
		}
		if old.Email != in.Email {
			what = append(what, "email")
		}
		if old.Phone != in.Phone {
			what = append(what, "phone")
		}
		if old.Kind != in.Kind {
			what = append(what, "kind")
		}
		if len(what) == 0 {
			return nil
		}
		_, err = tx.Exec(`
			UPDATE people SET first_name = ?, last_name = ?, goes_by = ?, suffix = ?,
				birth_month = ?, birth_day = ?, birth_year = ?,
				anniv_month = ?, anniv_day = ?, anniv_year = ?,
				email = ?, phone = ?, kind = ?, updated_at = datetime('now')
			WHERE id = ?`,
			in.FirstName, in.LastName, in.GoesBy, in.Suffix,
			dateArg(in.Birth.Month), dateArg(in.Birth.Day), dateArg(in.Birth.Year),
			dateArg(in.Anniversary.Month), dateArg(in.Anniversary.Day), dateArg(in.Anniversary.Year),
			in.Email, in.Phone, in.Kind, id)
		if err != nil {
			return err
		}
		if err := logChange(tx, by, id, 0, "edited "+strings.Join(what, ", ")); err != nil {
			return err
		}
		// Every birthday save checks for a found-later under-13 (links.go).
		return enforceUnder13(tx, id, by)
	})
}

// ArchivePerson hides a person everywhere but staff's archive list
// (archived true), or brings them back (false).
func (s *Store) ArchivePerson(id int64, archived bool, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		q, what := `UPDATE people SET archived_at = NULL, updated_at = datetime('now') WHERE id = ?`, "unarchived person"
		if archived {
			q, what = `UPDATE people SET archived_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`, "archived person"
		}
		res, err := tx.Exec(q, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, id, 0, what)
	})
}

// DeletePerson really removes a person: the row, and with it (by ON DELETE
// CASCADE) their notes, field settings, household memberships,
// relationships, group memberships, claims and name-match decisions. The
// audit trail keeps its rows with the person blanked. It returns the name
// of the person's photo so the caller can remove the files (photos.Dir.Remove);
// "" if they had none.
func (s *Store) DeletePerson(id int64, by int64) (photo string, err error) {
	err = s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT photo FROM people WHERE id = ?`, id).Scan(&photo); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`DELETE FROM people WHERE id = ?`, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, 0, "deleted person")
	})
	if err != nil {
		photo = ""
	}
	return photo, err
}

// People lists ---------------------------------------------------------------

// The filters of /admin/people.
const (
	PeopleAll      = ""         // everyone not archived
	PeopleUnlinked = "unlinked" // no login linked
	PeopleContacts = "contacts"
	PeoplePending  = "pending" // waiting for staff, or a held rename
	PeopleArchived = "archived"
)

// PersonRow is a person with the households they're in, for lists.
type PersonRow struct {
	Person
	Households []string
}

// ListPeople lists people for staff: filter is one of the People*
// constants and q searches the name (every word must be in it, ignoring
// case and accents). Sorted by last then first name.
func (s *Store) ListPeople(filter, q string) ([]PersonRow, error) {
	where := `archived_at IS NULL`
	switch filter {
	case PeopleUnlinked:
		where += ` AND user_id IS NULL`
	case PeopleContacts:
		where += ` AND kind = 'contact'`
	case PeoplePending:
		where += ` AND (pending = 1 OR pending_name <> '')`
	case PeopleArchived:
		where = `archived_at IS NOT NULL`
	}
	rows, err := s.DB.Query(`SELECT ` + personCols + ` FROM people WHERE ` + where +
		` ORDER BY last_name COLLATE NOCASE, first_name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	var people []Person
	words := strings.Fields(Fold(q))
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if matchesWords(p, words) {
			people = append(people, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hh, err := s.householdNamesByPerson()
	if err != nil {
		return nil, err
	}
	out := make([]PersonRow, len(people))
	for i, p := range people {
		out[i] = PersonRow{Person: p, Households: hh[p.ID]}
	}
	return out, nil
}

func matchesWords(p Person, words []string) bool {
	if len(words) == 0 {
		return true
	}
	hay := Fold(p.FirstName + " " + p.LastName + " " + p.GoesBy + " " + p.Suffix)
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Same name ----------------------------------------------------------------

// NameMatches finds the people already in the directory who match a name:
// the same last name, the same suffix (none only matches none) and a first
// name or goes-by equal to one of the new name's, compared with Fold.
// Archived people and contacts are included. excludeID leaves out the
// person being checked (0 for someone not saved yet) and, with it, anyone
// staff have recorded as a different person (RecordNotSamePeople).
func (s *Store) NameMatches(first, last, goesBy, suffix string, excludeID int64) ([]Person, error) {
	key := NewNameKey(first, last, goesBy, suffix)
	if key.Last == "" || len(key.Given) == 0 {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT `+personCols+` FROM people WHERE id <> ? ORDER BY last_name COLLATE NOCASE, first_name COLLATE NOCASE, id`, excludeID)
	if err != nil {
		return nil, err
	}
	var out []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if key.Matches(p.NameKey()) {
			out = append(out, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if excludeID == 0 || len(out) == 0 {
		return out, nil
	}
	not, err := s.notSamePeople(excludeID)
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, p := range out {
		if !not[p.ID] {
			kept = append(kept, p)
		}
	}
	return kept, nil
}

// notSamePeople is everyone staff said is a different person from id.
func (s *Store) notSamePeople(id int64) (map[int64]bool, error) {
	rows, err := s.DB.Query(`SELECT person_b FROM not_same_people WHERE person_a = ? UNION SELECT person_a FROM not_same_people WHERE person_b = ?`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]bool{}
	for rows.Next() {
		var other int64
		if err := rows.Scan(&other); err != nil {
			return nil, err
		}
		m[other] = true
	}
	return m, rows.Err()
}

// RecordNotSamePeople records that staff decided a and b are different
// people, so their matching names aren't flagged again. Recording a pair
// twice is fine.
func (s *Store) RecordNotSamePeople(a, b, by int64) error {
	if a == b {
		return errors.New("a person is the same as themselves")
	}
	if a > b {
		a, b = b, a
	}
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT OR IGNORE INTO not_same_people (person_a, person_b, decided_by) VALUES (?, ?, ?)`, a, b, nullInt(by))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return logChange(tx, by, a, 0, "marked two people as different")
		}
		return nil
	})
}
