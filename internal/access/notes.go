package access

import (
	"github.com/tylergoza/church-directory/internal/store"
)

// Notes. PLAN.md "Notes": a note is seen by the people CanSeeNote says,
// written by the people CanWriteNotes says. Nothing else decides.
//
// PRINT MUST NEVER CALL THIS FILE (or store.NotesOf / SharedNotes). Notes
// are never printed, never exported and never logged; the only way out is
// the sign-ups API reading store.SharedNotes (step 14).

// CanSeeNote says whether the viewer may read the note about the person.
//
//   - staff: every note.
//   - the person themselves: every note about them (no secret notes).
//   - the heads of a child's households: every note about the child.
//   - private: only the above.
//   - household: also the heads of the person's households.
//   - directory: also every signed-in member who can see the person.
//
// Anyone who can't see the person at all (CanSeePerson) sees no notes.
func CanSeeNote(v *Viewer, a *PersonAccess, n store.Note) bool {
	if v == nil || !CanSeePerson(v, a) {
		return false
	}
	if v.Staff || (v.PersonID != 0 && v.PersonID == a.Person.ID) {
		return true
	}
	head := headOfPerson(v, a)
	if head && a.Child() {
		return true
	}
	switch n.VisibleTo {
	case store.NoteDirectory:
		return true
	case store.NoteHousehold:
		return head
	}
	return false // private
}

// headOfPerson: the viewer's profile is a head of a household the person is in.
func headOfPerson(v *Viewer, a *PersonAccess) bool {
	for _, m := range a.Memberships {
		if v.HeadOf[m.HouseholdID] {
			return true
		}
	}
	return false
}

// CanWriteNotes says whether the viewer may add, edit and delete notes
// about the person: the person (if they have a login linked), staff, and
// heads for people they can edit.
func CanWriteNotes(v *Viewer, a *PersonAccess) bool {
	if v == nil || !CanSeePerson(v, a) {
		return false
	}
	if v.Staff || (v.PersonID != 0 && v.PersonID == a.Person.ID) {
		return true
	}
	return CanEditPerson(v, a)
}

// NotesFor reads the notes about a person and keeps the ones the viewer
// may see. This is the only way pages get notes.
func NotesFor(st *store.Store, v *Viewer, a *PersonAccess) ([]store.Note, error) {
	all, err := st.NotesOf(a.Person.ID)
	if err != nil {
		return nil, err
	}
	var out []store.Note
	for _, n := range all {
		if CanSeeNote(v, a, n) {
			out = append(out, n)
		}
	}
	return out, nil
}
