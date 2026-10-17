package store

import (
	"strings"
	"testing"
)

func TestNotesValidateAndDefaults(t *testing.T) {
	st := openTest(t)
	p := mustPerson(t, st, "Ann", "Lee", "", "")
	id, err := st.AddNote(p, NoteInput{Body: "  peanuts  "}, 0)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNote(id)
	if n.Kind != NoteGeneral || n.VisibleTo != NoteHousehold || n.ShareWithSignups || n.Body != "peanuts" {
		t.Errorf("defaults wrong: %+v", n)
	}
	for _, bad := range []NoteInput{{Body: ""}, {Body: "x", Kind: "gossip"}, {Body: "x", VisibleTo: "everyone"}} {
		if _, err := st.AddNote(p, bad, 0); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
}

func TestSharedNotesOnlyReturnsSharedOfTheKinds(t *testing.T) {
	st := openTest(t)
	a := mustPerson(t, st, "Ann", "Lee", "", "")
	b := mustPerson(t, st, "Bob", "Lee", "", "")
	add := func(p int64, kind, body string, share bool) {
		if _, err := st.AddNote(p, NoteInput{Kind: kind, Body: body, ShareWithSignups: share}, 0); err != nil {
			t.Fatal(err)
		}
	}
	add(a, NoteAllergy, "shared-allergy", true)
	add(a, NoteAllergy, "secret-allergy", false)
	add(a, NoteMedical, "shared-medical", true)
	add(b, NoteDietary, "shared-dietary", true)
	add(b, NoteDietary, "secret-dietary", false)
	got, err := st.SharedNotes([]int64{a, b}, []string{NoteAllergy, NoteDietary})
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, n := range got {
		bodies = append(bodies, n.Body)
	}
	if strings.Join(bodies, ",") != "shared-allergy,shared-dietary" {
		t.Errorf("SharedNotes = %v", bodies)
	}
	if got, _ := st.SharedNotes(nil, []string{NoteAllergy}); len(got) != 0 {
		t.Error("no people, no notes")
	}
	if got, _ := st.SharedNotes([]int64{a}, nil); len(got) != 0 {
		t.Error("no kinds, no notes")
	}
}

func TestNotesNeverInTheChangesTable(t *testing.T) {
	st := openTest(t)
	p := mustPerson(t, st, "Ann", "Lee", "", "")
	id, _ := st.AddNote(p, NoteInput{Body: "MARKER-note-body"}, 0)
	if err := st.UpdateNote(id, NoteInput{Body: "MARKER-edited-body"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteNote(id, 0); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.DB.Query(`SELECT what FROM changes`)
	defer rows.Close()
	var whats []string
	for rows.Next() {
		var w string
		rows.Scan(&w)
		if strings.Contains(w, "MARKER") {
			t.Errorf("a note body is in the audit trail: %q", w)
		}
		whats = append(whats, w)
	}
	joined := strings.Join(whats, "|")
	for _, want := range []string{"added note", "edited note", "deleted note"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit trail lacks %q: %s", want, joined)
		}
	}
}

func TestDeletingAPersonDeletesTheirNotes(t *testing.T) {
	st := openTest(t)
	p := mustPerson(t, st, "Ann", "Lee", "", "")
	other := mustPerson(t, st, "Bob", "Lee", "", "")
	st.AddNote(p, NoteInput{Body: "a"}, 0)
	st.AddNote(other, NoteInput{Body: "b"}, 0)
	if _, err := st.DeletePerson(p, 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.NotesOf(p); len(n) != 0 {
		t.Error("notes survived the person")
	}
	if n, _ := st.NotesOf(other); len(n) != 1 {
		t.Error("someone else's note was removed")
	}
}
