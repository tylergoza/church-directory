package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestImportFlow(t *testing.T) {
	c, st := staffClient(t, Config{})
	sam := c
	mel := c.as("mel")
	mel.get("/admin/import", 403)
	sam.get("/admin/import", 200)
	if !strings.Contains(sam.get("/admin", 200), "/admin/import") {
		t.Error("admin page should link to the import")
	}

	csv := "first,last,address1,city,mood\nAnn,Lee,1 Oak,Town,x\nBob,Lee,1 Oak,Town,y\n"
	m := csrfRe.FindStringSubmatch(sam.get("/admin/import", 200))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("_csrf", m[1])
	fw, _ := mw.CreateFormFile("file", "people.csv")
	fw.Write([]byte(csv))
	mw.Close()
	req, _ := http.NewRequest("POST", sam.base+"/admin/import/preview", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := sam.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "Ann Lee") || !strings.Contains(string(page), "mood") {
		t.Fatalf("preview: %d\n%s", resp.StatusCode, page)
	}
	if countPeople(t, st) != 0 {
		t.Fatal("the preview saved people")
	}
	// Import from the hidden field.
	form := url.Values{"csv": {csv}, "choice_1": {"skip"}}
	sam.post("/admin/import", "/admin/import/run", form, 200)
	if countPeople(t, st) != 1 {
		t.Errorf("people = %d, want 1 (Bob skipped)", countPeople(t, st))
	}
	// A bad row chosen for adding blocks everything.
	bad := url.Values{"csv": {"first,last,birthday\nCy,Dee,Smarch 3\nEd,Fox,\n"}, "choice_0": {"add"}, "choice_1": {"add"}}
	if body := sam.post("/admin/import", "/admin/import/run", bad, 422); !strings.Contains(body, "Nothing was imported") {
		t.Error("expected the blocked message")
	}
	if countPeople(t, st) != 1 {
		t.Error("a blocked import wrote people")
	}
}
