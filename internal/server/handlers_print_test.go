package server

import (
	"bytes"
	"github.com/tylergoza/church-directory/internal/photos"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

func TestPrintNeverHasNotesContactsOrHiddenFields(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	const shared = "SHARED-NOTE-MARKER-xyz"
	for _, n := range []store.NoteInput{
		{Kind: store.NoteAllergy, Body: houseNote, VisibleTo: store.NoteHousehold},
		{Kind: store.NoteAccessibility, Body: dirNote, VisibleTo: store.NoteDirectory},
		{Kind: store.NoteMedical, Body: privNote, VisibleTo: store.NotePrivate},
		{Kind: store.NoteAllergy, Body: shared, VisibleTo: store.NoteDirectory, ShareWithSignups: true},
	} {
		if _, err := st.AddNote(sd.ann, n, 0); err != nil {
			t.Fatal(err)
		}
	}
	open, _ := st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	secret, _ := st.CreateGroup(store.GroupInput{Name: "Needs a ride", VisibleTo: store.GroupStaff}, 0)
	st.AddGroupMember(open, sd.ann, "", 0)
	st.AddGroupMember(secret, sd.ann, "", 0)

	mel := c.as("mel")
	for who, cl := range map[string]*client{"staff": c, "member": mel} {
		body := cl.get("/print", 200)
		never := []string{houseNote, dirNote, privNote, shared, "Conny", contactMail, "Needs a ride", "Archie", archieMail}
		if who == "member" { // staff see more through Visible(), as on screen
			never = append(never, "Penny", pennyMail, bobEmail, secretAddr)
		}
		for _, m := range never {
			if strings.Contains(body, m) {
				t.Errorf("%s: print has %q", who, m)
			}
		}
		for _, m := range []string{shownEmail, "1 Oak St", "Singers", "The Lee Family", "As of ", "June", "December", "Index of people"} {
			if !strings.Contains(body, m) {
				t.Errorf("%s: print lacks %q", who, m)
			}
		}
	}
	// A staff-only group can't be printed on its own, even by staff.
	c.get("/print?group="+itoa(secret), 404)
	mel.get("/print?group="+itoa(secret), 404)
}

func TestPrintWhoMayPrint(t *testing.T) {
	c, st := staffClient(t, Config{})
	seedDirectory(t, st)
	mel := c.as("mel")
	if body := mel.get("/", 200); !strings.Contains(body, `href="/print"`) {
		t.Error("members should see the Print link by default")
	}
	mel.get("/print", 200)
	st.CreateUser("ada", "Ada", "a long password", store.RoleAdmin)
	ada := c.as("ada")
	ada.post("/admin/settings", "/admin/settings", url.Values{"site_name": {"Dir"}, "print_who": {"staff"}}, 200)
	if st.Setting("print_who", "") != "staff" {
		t.Fatal("the setting was not saved")
	}
	mel.get("/print", 403)
	mel.get("/print/footer.css", 403)
	if body := mel.get("/", 200); strings.Contains(body, `href="/print"`) {
		t.Error("the Print link should be hidden from members")
	}
	c.get("/print", 200)
}

func TestPrintOptions(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	other := mustPersonID(t, st, "Zed", "Zimmer")
	zh, _ := st.CreateHousehold("The Zimmer Family", "", 0)
	st.PlaceMember(zh, other, store.RoleHead, 0, 0)
	g, _ := st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	st.AddGroupMember(g, other, "", 0)
	st.DB.Exec(`UPDATE people SET photo = ? WHERE id = ?`, "abcdef0123456789abcdef0123456789.jpg", sd.ann)
	st.DB.Exec(`UPDATE people SET photo = ? WHERE id = ?`, "abcdef0123456789abcdef0123456789.jpg", other)

	all := c.get("/print", 200)
	if !strings.Contains(all, "The Lee Family") || !strings.Contains(all, "The Zimmer Family") {
		t.Error("whole directory should list both households")
	}
	one := c.get("/print?group="+itoa(g), 200)
	if strings.Contains(one, "Ann") && strings.Contains(one, "The Lee Family</h3>") {
		t.Error("group filter should drop the Lee household")
	}
	if !strings.Contains(one, "The Zimmer Family</h3>") {
		t.Error("group filter should keep the Zimmer household")
	}
	if !strings.Contains(c.get("/print?paper=half&gutter=on", 200), `class="book p-half-g"`) {
		t.Error("paper and gutter should pick the named page class")
	}
	if !strings.Contains(c.get("/print", 200), `class="book p-letter"`) {
		t.Error("default is letter, no gutter")
	}
	if strings.Contains(c.get("/print?photos=off", 200), `class="pperson__photo"`) {
		t.Error("photos off should print no photos")
	}
	foot := c.get("/print/footer.css", 200)
	if !strings.Contains(foot, "Please don't share.") || !strings.Contains(foot, "counter(page)") {
		t.Errorf("footer css: %s", foot)
	}
}

func TestLogoUploadAndServe(t *testing.T) {
	dir := t.TempDir()
	c, st := staffClient(t, Config{Photos: photos.Dir{Path: dir}})
	st.CreateUser("ada", "Ada", "a long password", store.RoleAdmin)
	ada := c.as("ada")
	m := csrfRe.FindStringSubmatch(ada.get("/admin/settings", 200))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("_csrf", m[1])
	mw.WriteField("site_name", "Dir")
	mw.WriteField("church_name", "Hilltop Church")
	fw, _ := mw.CreateFormFile("photo", "logo.jpg")
	fw.Write(jpegWithEXIF(t))
	mw.Close()
	req, _ := http.NewRequest("POST", ada.base+"/admin/settings", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := ada.http.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload: %v %v", err, resp)
	}
	resp.Body.Close()
	logo := st.Setting("logo", "")
	if logo == "" {
		t.Fatal("logo not saved")
	}
	mel := c.as("mel")
	mel.get("/logo/"+logo, 200)
	mel.get("/logo/nope.jpg", 404)
	page := mel.get("/print", 200)
	if !strings.Contains(page, "/logo/"+logo) || !strings.Contains(page, "Hilltop Church") {
		t.Error("cover should show the logo and church name")
	}
	if strings.Contains(mel.get("/print?photos=off", 200), "pperson__photo") {
		t.Error("photos off")
	}
}
