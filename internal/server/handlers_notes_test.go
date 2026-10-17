package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

const (
	houseNote  = "BOB-HOUSEHOLD-NOTE-peanuts"
	dirNote    = "BOB-DIRECTORY-NOTE-ramp"
	privNote   = "BOB-PRIVATE-NOTE"
	shareLabel = "Share with sign-ups"
	shareHelp  = "Shown to the team running events this person signs up for, including their printed allergy list."
)

// notesFixture: Ann (head, has a login "hank") and Bob (unlinked adult) in
// one household; Mel is a member with no profile; Sam is staff.
func notesFixture(t *testing.T, c *client, st *store.Store) (bob int64) {
	t.Helper()
	hank, _ := st.CreateUser("hank", "Hank", "a long password", store.RoleMember)
	ann := mustPersonID(t, st, "Ann", "Lee")
	bob = mustPersonID(t, st, "Bob", "Lee")
	st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, hank, ann)
	h, err := st.CreateHousehold("The Lee Family", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	st.PlaceMember(h, ann, store.RoleHead, 0, 0)
	st.PlaceMember(h, bob, store.RoleAdult, 0, 0)
	for _, n := range []store.NoteInput{
		{Kind: store.NoteAllergy, Body: houseNote, VisibleTo: store.NoteHousehold},
		{Kind: store.NoteAccessibility, Body: dirNote, VisibleTo: store.NoteDirectory},
		{Kind: store.NoteMedical, Body: privNote, VisibleTo: store.NotePrivate},
	} {
		if _, err := st.AddNote(bob, n, 0); err != nil {
			t.Fatal(err)
		}
	}
	return bob
}

func TestNotesOnThePersonPageFollowCanSeeNote(t *testing.T) {
	c, st := staffClient(t, Config{})
	bob := notesFixture(t, c, st)
	page := "/people/" + itoa(bob)

	mel := c.as("mel").get(page, 200)
	if strings.Contains(mel, houseNote) || strings.Contains(mel, privNote) {
		t.Error("a member sees a household or private note")
	}
	if !strings.Contains(mel, dirNote) {
		t.Error("a member should see a directory note")
	}
	if strings.Contains(mel, "Add a note") {
		t.Error("a member who can't write sees the add form")
	}

	hank := c.as("hank").get(page, 200)
	if !strings.Contains(hank, houseNote) || !strings.Contains(hank, dirNote) || strings.Contains(hank, privNote) {
		t.Error("a head sees household and directory notes of an adult, not private ones")
	}
	if !strings.Contains(hank, shareLabel) || !strings.Contains(hank, shareHelp) {
		t.Error("the head's form should carry the Share with sign-ups tick and help")
	}

	sam := c.get(page, 200)
	for _, n := range []string{houseNote, dirNote, privNote} {
		if !strings.Contains(sam, n) {
			t.Errorf("staff should see %q", n)
		}
	}
	if body := c.get("/admin/people/"+itoa(bob), 200); !strings.Contains(body, privNote) || !strings.Contains(body, shareHelp) {
		t.Error("the staff person page should list notes and the form")
	}
}

func TestWritingNotes(t *testing.T) {
	c, st := staffClient(t, Config{})
	bob := notesFixture(t, c, st)
	page := "/people/" + itoa(bob)
	hank, mel := c.as("hank"), c.as("mel")

	// A member who can't write is refused, and can't touch existing notes.
	mel.post("/people/"+itoa(bob), page+"/notes", url.Values{"body": {"nope"}}, 403)
	notes, _ := st.NotesOf(bob)
	hid := notes[0].ID
	mel.post("/people/"+itoa(bob), page+"/notes/"+itoa(hid)+"/delete", url.Values{}, 403)

	// A head adds, edits and deletes for an unlinked adult.
	hank.post(page, page+"/notes", url.Values{"kind": {"dietary"}, "body": {"no gluten"}, "visible_to": {"household"}, "share_with_signups": {"1"}}, 200)
	notes, _ = st.NotesOf(bob)
	var added store.Note
	for _, n := range notes {
		if n.Body == "no gluten" {
			added = n
		}
	}
	if added.ID == 0 || !added.ShareWithSignups || added.Kind != store.NoteDietary || added.CreatedBy == 0 {
		t.Fatalf("note not saved as asked: %+v", added)
	}
	hank.post(page, page+"/notes/"+itoa(added.ID), url.Values{"kind": {"dietary"}, "body": {"no dairy"}, "visible_to": {"directory"}}, 200)
	if n, _ := st.GetNote(added.ID); n.Body != "no dairy" || n.ShareWithSignups || n.VisibleTo != store.NoteDirectory {
		t.Errorf("edit not saved: %+v", n)
	}
	// A head can't save a note they couldn't read (private, adult).
	body := hank.post(page, page+"/notes", url.Values{"body": {"secret"}, "visible_to": {"private"}}, 200)
	if !strings.Contains(body, "be able to see that note yourself") {
		t.Error("expected the head to be told off")
	}
	// A head can't open a private note by id.
	var priv store.Note
	for _, n := range notes {
		if n.VisibleTo == store.NotePrivate {
			priv = n
		}
	}
	hank.post(page, page+"/notes/"+itoa(priv.ID)+"/delete", url.Values{}, 404)
	hank.post(page, page+"/notes/"+itoa(added.ID)+"/delete", url.Values{}, 200)
	if _, err := st.GetNote(added.ID); err != store.ErrNotFound {
		t.Error("note not deleted")
	}
	// Staff use the admin route and come back to the admin page.
	admin := "/admin/people/" + itoa(bob)
	if body := c.post(admin, admin+"/notes", url.Values{"body": {"staff note"}}, 200); !strings.Contains(body, "staff note") {
		t.Error("staff note should show on the admin page")
	}
}

// bufClient runs a server whose logs go to the returned buffer.
func bufClient(t *testing.T) (*client, *store.Store, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var buf bytes.Buffer
	srv, err := New(Config{Env: func(string) string { return "" }}, st, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}}
	st.CreateUser("sam", "Sam", "a long password", store.RoleEditor)
	st.CreateUser("mel", "Mel", "a long password", store.RoleMember)
	return c.as("sam"), st, &buf
}

func TestNoteBodiesNeverInLogsOrChanges(t *testing.T) {
	c, st, logs := bufClient(t)
	bob := mustPersonID(t, st, "Bob", "Lee")
	admin := "/admin/people/" + itoa(bob)
	const m1, m2 = "MARKER-first-body", "MARKER-second-body"
	c.post(admin, admin+"/notes", url.Values{"body": {m1}, "share_with_signups": {"1"}}, 200)
	notes, _ := st.NotesOf(bob)
	c.post(admin, admin+"/notes/"+itoa(notes[0].ID), url.Values{"body": {m2}}, 200)
	c.get("/people/"+itoa(bob), 200)
	// An invalid post goes through the error path too.
	c.post(admin, admin+"/notes", url.Values{"body": {m1}, "kind": {"bogus"}}, 200)

	if strings.Contains(logs.String(), "MARKER") {
		t.Errorf("a note body reached the logs:\n%s", logs.String())
	}
	if logs.Len() == 0 {
		t.Fatal("the log buffer saw nothing, so the check above means nothing")
	}
	rows, _ := st.DB.Query(`SELECT what FROM changes`)
	defer rows.Close()
	for rows.Next() {
		var w string
		rows.Scan(&w)
		if strings.Contains(w, "MARKER") {
			t.Errorf("a note body is in the changes table: %q", w)
		}
	}
	// The history on the staff page shows "added note", with no text.
	page := c.get(admin, 200)
	if !strings.Contains(page, "added note") || !strings.Contains(page, "edited note") {
		t.Error("history should say a note was added and edited")
	}
}
