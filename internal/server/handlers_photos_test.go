package server

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/photos"
	"github.com/tylergoza/church-directory/internal/store"
)

const exifMarker = "GPSLatitude-MARKER"

// jpegWithEXIF is a small JPEG with an APP1 Exif segment holding the marker.
func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{200, 30, 30, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	j := buf.Bytes()
	payload := append([]byte("Exif\x00\x00"), exifMarker...)
	n := len(payload) + 2
	out := append([]byte{}, j[:2]...)
	out = append(out, 0xFF, 0xE1, byte(n>>8), byte(n))
	out = append(out, payload...)
	return append(out, j[2:]...)
}

// upload posts a multipart photo form as the client, with a CSRF token
// taken from tokenPage.
func (c *client) upload(tokenPage, path string, file []byte) (int, string) {
	c.t.Helper()
	m := csrfRe.FindStringSubmatch(c.get(tokenPage, 200))
	if m == nil {
		c.t.Fatalf("no csrf token on %s", tokenPage)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("_csrf", m[1])
	fw, _ := mw.CreateFormFile("photo", "photo.jpg")
	fw.Write(file)
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	noFollow := *c.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *client) status(path string) int {
	c.t.Helper()
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func personPhoto(t *testing.T, st *store.Store, id int64) string {
	t.Helper()
	p, err := st.GetPerson(id)
	if err != nil {
		t.Fatal(err)
	}
	return p.Photo
}

func fileExists(dir photos.Dir, name string, thumb bool) bool {
	suffix := ".jpg"
	if thumb {
		suffix = "_t.jpg"
	}
	_, err := os.Stat(filepath.Join(dir.Path, name+suffix))
	return err == nil
}

func TestPhotoUploadStripsEXIF(t *testing.T) {
	dir := photos.Dir{Path: filepath.Join(t.TempDir(), "photos")}
	c, st := staffClient(t, Config{Photos: dir})
	id := mustPersonID(t, st, "Tyler", "Goza")
	page := "/admin/people/" + itoa(id)

	in := jpegWithEXIF(t)
	if !bytes.Contains(in, []byte(exifMarker)) {
		t.Fatal("fixture has no marker")
	}
	if code, body := c.upload(page, page+"/photo", in); code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", code, body)
	}
	name := personPhoto(t, st, id)
	if !photos.ValidName(name) {
		t.Fatalf("stored name %q", name)
	}
	for _, thumb := range []bool{false, true} {
		f, err := dir.Open(name, thumb)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		f.Close()
		if len(b) == 0 || bytes.Contains(b, []byte(exifMarker)) || bytes.Contains(b, []byte("Exif")) {
			t.Errorf("stored file (thumb %v) is empty or kept metadata", thumb)
		}
	}
	var what string
	st.DB.QueryRow(`SELECT what FROM changes WHERE person_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&what)
	if what != "changed photo" {
		t.Errorf("change log = %q", what)
	}
	// Staff see it, with the headers photos always carry.
	resp, err := c.http.Get(c.base + "/photos/" + name)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" ||
		resp.Header.Get("Cache-Control") != "private, max-age=31536000, immutable" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("photo response: %d %v", resp.StatusCode, resp.Header)
	}
	if c.status("/photos/"+name+"?size=thumb") != 200 {
		t.Error("thumbnail should serve")
	}
}

func TestPhotoUploadRefusals(t *testing.T) {
	dir := photos.Dir{Path: filepath.Join(t.TempDir(), "photos")}
	c, st := staffClient(t, Config{Photos: dir})
	id := mustPersonID(t, st, "Tyler", "Goza")
	page := "/admin/people/" + itoa(id)

	// Over the cap: refused with a message, nothing stored.
	if code, body := c.upload(page, page+"/photo", make([]byte, photos.MaxUpload+2<<20)); code != http.StatusRequestEntityTooLarge || !strings.Contains(body, "too large") {
		t.Errorf("huge upload: %d", code)
	}
	// Just over the photo cap but inside the request cap: friendly flash.
	big := jpegWithEXIF(t)
	big = append(big, make([]byte, photos.MaxUpload)...)
	if code, _ := c.upload(page, page+"/photo", big); code != http.StatusSeeOther {
		t.Errorf("oversized photo: %d", code)
	}
	if !strings.Contains(c.get(page, 200), "too large") {
		t.Error("expected the too-large message on the page")
	}
	// Not a picture.
	if code, _ := c.upload(page, page+"/photo", []byte("hello, not a picture")); code != http.StatusSeeOther {
		t.Errorf("text upload: %d", code)
	}
	if !strings.Contains(c.get(page, 200), "isn&#39;t a JPEG or PNG") {
		t.Error("expected the wrong-format message")
	}
	if personPhoto(t, st, id) != "" {
		t.Error("a refused upload changed the photo")
	}
	if entries, _ := os.ReadDir(dir.Path); len(entries) != 0 {
		t.Errorf("%d files stored for refused uploads", len(entries))
	}
}

func TestPhotoVisibilityAndReplace(t *testing.T) {
	dir := photos.Dir{Path: filepath.Join(t.TempDir(), "photos")}
	c, st := staffClient(t, Config{Photos: dir})
	st.CreateUser("jo", "Jo", "a long password", store.RoleMember)
	id := mustPersonID(t, st, "Jo", "Goza")
	st.DB.Exec(`UPDATE people SET user_id = (SELECT id FROM users WHERE username = 'jo') WHERE id = ?`, id)
	page := "/admin/people/" + itoa(id)

	c.upload(page, page+"/photo", jpegWithEXIF(t))
	first := personPhoto(t, st, id)
	if first == "" {
		t.Fatal("no photo stored")
	}
	mel, jo := c.as("mel"), c.as("jo")

	// Shown by their own setting: a member sees it, and the person page links it.
	yes, no := true, false
	st.SetPersonFieldSettings(id, map[store.Field]store.FieldSetting{store.FieldPhoto: {Shown: &yes}}, 0)
	if mel.status("/photos/"+first) != 200 {
		t.Error("a member should see a shown photo")
	}
	if !strings.Contains(mel.get("/people/"+itoa(id), 200), "/photos/"+first) {
		t.Error("person page should show the photo")
	}

	// Hidden: 404 for the member (and the page leaves it out); staff and the person still see it.
	st.SetPersonFieldSettings(id, map[store.Field]store.FieldSetting{store.FieldPhoto: {Shown: &no}}, 0)
	if mel.status("/photos/"+first) != 404 || mel.status("/photos/"+first+"?size=thumb") != 404 {
		t.Error("a hidden photo should 404 for a member")
	}
	if strings.Contains(mel.get("/people/"+itoa(id), 200), first) {
		t.Error("person page should not mention a hidden photo")
	}
	if c.status("/photos/"+first) != 200 || jo.status("/photos/"+first) != 200 {
		t.Error("staff and the person should still see their photo")
	}

	// Replace: new name, old name gone from the folder and 404 everywhere.
	c.upload(page, page+"/photo", jpegWithEXIF(t))
	second := personPhoto(t, st, id)
	if second == "" || second == first {
		t.Fatalf("photo name after replace = %q (was %q)", second, first)
	}
	if fileExists(dir, first, false) || fileExists(dir, first, true) {
		t.Error("old files are still there")
	}
	if !fileExists(dir, second, false) || !fileExists(dir, second, true) {
		t.Error("new files missing")
	}
	if c.status("/photos/"+first) != 404 {
		t.Error("the old name should 404")
	}

	// Remove clears it.
	c.post(page, page+"/photo/remove", map[string][]string{}, 200)
	if personPhoto(t, st, id) != "" || fileExists(dir, second, false) {
		t.Error("remove should clear the photo and delete its files")
	}
}

func TestHouseholdPhoto(t *testing.T) {
	dir := photos.Dir{Path: filepath.Join(t.TempDir(), "photos")}
	c, st := staffClient(t, Config{Photos: dir})
	hid, err := st.CreateHousehold("Goza family", store.KindMember, 0)
	if err != nil {
		t.Fatal(err)
	}
	page := "/admin/households/" + itoa(hid)
	if code, body := c.upload(page, page+"/photo", jpegWithEXIF(t)); code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", code, body)
	}
	h, _ := st.GetHousehold(hid)
	if !photos.ValidName(h.Photo) {
		t.Fatalf("household photo = %q", h.Photo)
	}
	mel := c.as("mel")
	yes, no := true, false
	st.SetHouseholdPhotoSetting(hid, store.FieldSetting{Shown: &yes}, 0)
	if mel.status("/photos/"+h.Photo) != 200 {
		t.Error("member should see a shown household photo")
	}
	st.SetHouseholdPhotoSetting(hid, store.FieldSetting{Shown: &no}, 0)
	if mel.status("/photos/"+h.Photo) != 404 {
		t.Error("member should not see a hidden household photo")
	}
	if c.status("/photos/"+h.Photo) != 200 {
		t.Error("staff should see it")
	}
}

func TestPhotoNamesAndSignedOut(t *testing.T) {
	dir := photos.Dir{Path: filepath.Join(t.TempDir(), "photos")}
	c, st := staffClient(t, Config{Photos: dir})
	id := mustPersonID(t, st, "Tyler", "Goza")
	page := "/admin/people/" + itoa(id)
	c.upload(page, page+"/photo", jpegWithEXIF(t))
	name := personPhoto(t, st, id)

	for _, bad := range []string{"x.jpg", "..%2f..%2fetc%2fpasswd", strings.Repeat("g", 32), strings.ToUpper(name), name + ".jpg", strings.Repeat("a", 32)} {
		if got := c.status("/photos/" + bad); got != 404 {
			t.Errorf("GET /photos/%s = %d, want 404", bad, got)
		}
	}
	// Signed out: sent to sign in, never the picture.
	out := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := out.Get(c.base + "/photos/" + name)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Errorf("signed out: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Members can't upload.
	mel := c.as("mel")
	if code, _ := mel.upload("/account", page+"/photo", jpegWithEXIF(t)); code != http.StatusForbidden {
		t.Errorf("member upload: %d", code)
	}
}
