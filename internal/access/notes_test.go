package access

import (
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

// TestCanSeeNote is the viewer x visibility x child/adult table. The adult
// (id 1) has no login; household 10 has heads (id 2) and others.
func TestCanSeeNote(t *testing.T) {
	viewers := map[string]*Viewer{
		"staff":                   {UserID: 9, Staff: true},
		"self":                    {UserID: 5, PersonID: 1, HeadOf: map[int64]bool{}},
		"head":                    {UserID: 6, PersonID: 2, HeadOf: map[int64]bool{10: true}},
		"household member":        {UserID: 7, PersonID: 3, HeadOf: map[int64]bool{}},
		"other member":            {UserID: 8, PersonID: 4, HeadOf: map[int64]bool{}},
		"head of other household": {UserID: 10, PersonID: 5, HeadOf: map[int64]bool{99: true}},
		"signed out":              nil,
	}
	// want[viewer] = household, private, directory for an adult, then a child.
	type row struct{ adult, child [3]bool }
	want := map[string]row{
		"staff":                   {[3]bool{true, true, true}, [3]bool{true, true, true}},
		"self":                    {[3]bool{true, true, true}, [3]bool{true, true, true}},
		"head":                    {[3]bool{true, false, true}, [3]bool{true, true, true}},
		"household member":        {[3]bool{false, false, true}, [3]bool{false, false, true}},
		"other member":            {[3]bool{false, false, true}, [3]bool{false, false, true}},
		"head of other household": {[3]bool{false, false, true}, [3]bool{false, false, true}},
		"signed out":              {[3]bool{}, [3]bool{}},
	}
	vis := []string{store.NoteHousehold, store.NotePrivate, store.NoteDirectory}
	for name, v := range viewers {
		for kind, a := range map[string]*PersonAccess{"adult": adult(1), "child": child(1)} {
			for i, vt := range vis {
				w := want[name].adult[i]
				if kind == "child" {
					w = want[name].child[i]
				}
				got := CanSeeNote(v, a, store.Note{PersonID: 1, VisibleTo: vt})
				if got != w {
					t.Errorf("%s sees %s's %s note = %v, want %v", name, kind, vt, got, w)
				}
			}
		}
	}
}

func TestCanSeeNoteNeedsThePerson(t *testing.T) {
	a := adult(1)
	a.Person.ArchivedAt = "2024-01-01"
	v := &Viewer{UserID: 8, PersonID: 4, HeadOf: map[int64]bool{}}
	if CanSeeNote(v, a, store.Note{VisibleTo: store.NoteDirectory}) {
		t.Error("an archived person's notes are not shown to members")
	}
}

func TestCanWriteNotes(t *testing.T) {
	linked := adult(1)
	linked.Person.UserID = 5
	cases := []struct {
		name string
		v    *Viewer
		a    *PersonAccess
		want bool
	}{
		{"staff", &Viewer{Staff: true}, adult(1), true},
		{"self", &Viewer{PersonID: 1, HeadOf: map[int64]bool{}}, linked, true},
		{"head, unlinked adult", &Viewer{PersonID: 2, HeadOf: map[int64]bool{10: true}}, adult(1), true},
		{"head, child", &Viewer{PersonID: 2, HeadOf: map[int64]bool{10: true}}, child(1), true},
		{"head, linked adult", &Viewer{PersonID: 2, HeadOf: map[int64]bool{10: true}}, linked, false},
		{"head of other household", &Viewer{PersonID: 3, HeadOf: map[int64]bool{99: true}}, adult(1), false},
		{"other member", &Viewer{PersonID: 4, HeadOf: map[int64]bool{}}, adult(1), false},
		{"signed out", nil, adult(1), false},
	}
	for _, c := range cases {
		if got := CanWriteNotes(c.v, c.a); got != c.want {
			t.Errorf("%s: CanWriteNotes = %v, want %v", c.name, got, c.want)
		}
	}
}
