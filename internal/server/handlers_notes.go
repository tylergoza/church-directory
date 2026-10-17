package server

import (
	"net/http"
	"strings"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
)

// Notes on a person's page (/people/{id}) and on the staff person page
// (/admin/people/{id}). Every handler asks access.CanSeeNote /
// access.CanWriteNotes; note text is never logged.

// noteChoice is an option of a select.
type noteChoice struct{ Value, Label string }

// noteView is a note as the template shows it.
type noteView struct {
	store.Note
	KindLabel, VisibleLabel string
}

// notesSection is what the notes partial needs.
type notesSection struct {
	PersonID     int64
	Base         string // "/people/1" or "/admin/people/1"
	Notes        []noteView
	CanWrite     bool
	Kinds        []noteChoice
	Visibilities []noteChoice
}

// notesSection builds the section for a page: only the notes the viewer
// may see.
func (s *Server) notesSection(v *access.Viewer, a *access.PersonAccess, base string) (*notesSection, error) {
	notes, err := access.NotesFor(s.store, v, a)
	if err != nil {
		return nil, err
	}
	sec := &notesSection{PersonID: a.Person.ID, Base: base, CanWrite: access.CanWriteNotes(v, a)}
	for _, n := range notes {
		sec.Notes = append(sec.Notes, noteView{Note: n, KindLabel: store.NoteKindLabel(n.Kind), VisibleLabel: store.NoteVisibilityLabel(n.VisibleTo)})
	}
	for _, k := range store.NoteKinds {
		sec.Kinds = append(sec.Kinds, noteChoice{k, store.NoteKindLabel(k)})
	}
	for _, k := range store.NoteVisibilities {
		sec.Visibilities = append(sec.Visibilities, noteChoice{k, store.NoteVisibilityLabel(k)})
	}
	return sec, nil
}

// personNotes is notesSection for a person by id, or nil when the viewer
// may not see the person.
func (s *Server) personNotes(r *http.Request, d *access.Directory, id int64, base string) (*notesSection, error) {
	a, ok := d.Person(id)
	if !ok || !d.CanSeePerson(a) {
		return nil, nil
	}
	return s.notesSection(d.Viewer, a, base)
}

func noteInputFrom(r *http.Request) store.NoteInput {
	return store.NoteInput{
		Kind: formStr(r, "kind"), Body: formStr(r, "body"), VisibleTo: formStr(r, "visible_to"),
		ShareWithSignups: r.PostFormValue("share_with_signups") == "1",
	}.Clean()
}

// notesTarget is the page to come back to: the staff page when the form
// was posted to /admin/…, the member page otherwise.
func (s *Server) notesTarget(r *http.Request, personID int64) string {
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return "/admin/people/" + itoa(personID)
	}
	if strings.HasPrefix(r.URL.Path, "/me/") {
		return s.personBack(r, personID)
	}
	return "/people/" + itoa(personID)
}

// noteWriter loads the person and checks the viewer may write notes about
// them. When it returns ok false the answer has been sent.
func (s *Server) noteWriter(w http.ResponseWriter, r *http.Request) (*access.Viewer, *access.PersonAccess, bool) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	a, ok := d.Person(pathID(r))
	if !ok || !d.CanSeePerson(a) {
		s.notFound(w, r)
		return nil, nil, false
	}
	if !access.CanWriteNotes(d.Viewer, a) {
		s.render(w, r, http.StatusForbidden, "error", map[string]any{
			"Title": "Not allowed", "Message": "You can't change notes about this person.",
		})
		return nil, nil, false
	}
	return d.Viewer, a, true
}

// checkNote validates a note form. The writer must be able to read what
// they save, so a head can't leave a note that only the person sees.
func checkNote(v *access.Viewer, a *access.PersonAccess, in store.NoteInput) []string {
	errs := in.Validate()
	if len(errs) == 0 && !access.CanSeeNote(v, a, store.Note{PersonID: a.Person.ID, VisibleTo: in.VisibleTo}) {
		errs = append(errs, "You wouldn't be able to see that note yourself. Pick a wider choice for who can see it.")
	}
	return errs
}

func (s *Server) handleNoteAdd(w http.ResponseWriter, r *http.Request) {
	v, a, ok := s.noteWriter(w, r)
	if !ok {
		return
	}
	to := s.notesTarget(r, a.Person.ID)
	in := noteInputFrom(r)
	if errs := checkNote(v, a, in); len(errs) > 0 {
		s.redirectError(w, r, to, strings.Join(errs, " "))
		return
	}
	if _, err := s.store.AddNote(a.Person.ID, in, v.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, to, "Note added.")
}

// ownNote loads the note named in the path and makes sure it is about the
// person in the path and the viewer can see it.
func (s *Server) ownNote(w http.ResponseWriter, r *http.Request, v *access.Viewer, a *access.PersonAccess) (store.Note, bool) {
	n, err := s.store.GetNote(pathValueID(r, "nid"))
	if err != nil && err != store.ErrNotFound {
		s.serverError(w, r, err)
		return n, false
	}
	if err != nil || n.PersonID != a.Person.ID || !access.CanSeeNote(v, a, n) {
		s.notFound(w, r)
		return n, false
	}
	return n, true
}

func (s *Server) handleNoteUpdate(w http.ResponseWriter, r *http.Request) {
	v, a, ok := s.noteWriter(w, r)
	if !ok {
		return
	}
	n, ok := s.ownNote(w, r, v, a)
	if !ok {
		return
	}
	to := s.notesTarget(r, a.Person.ID)
	in := noteInputFrom(r)
	if errs := checkNote(v, a, in); len(errs) > 0 {
		s.redirectError(w, r, to, strings.Join(errs, " "))
		return
	}
	if err := s.store.UpdateNote(n.ID, in, v.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, to, "Note saved.")
}

func (s *Server) handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	v, a, ok := s.noteWriter(w, r)
	if !ok {
		return
	}
	n, ok := s.ownNote(w, r, v, a)
	if !ok {
		return
	}
	if err := s.store.DeleteNote(n.ID, v.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, s.notesTarget(r, a.Person.ID), "Note deleted.")
}
