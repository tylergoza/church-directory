package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

// apiGet calls the API with a bearer token ("" sends none).
func apiGet(t *testing.T, c *client, token, path string, want int) (map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status %d, want %d\n%s", path, resp.StatusCode, want, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("GET %s: not JSON: %v\n%s", path, err, body)
	}
	return out, resp.Header
}

// noPrivateKeys walks decoded JSON and fails on any key that could carry a
// birthday, contact detail, photo or address.
func noPrivateKeys(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, sub := range x {
			l := strings.ToLower(k)
			for _, bad := range []string{"birth", "email", "phone", "photo", "address", "anniversary", "month", "day", "year"} {
				if strings.Contains(l, bad) {
					t.Errorf("API output has a %q key", k)
				}
			}
			noPrivateKeys(t, sub)
		}
	case []any:
		for _, sub := range x {
			noPrivateKeys(t, sub)
		}
	}
}

func mkPerson(t *testing.T, st *store.Store, first string, in store.PersonInput) int64 {
	t.Helper()
	in.FirstName, in.LastName = first, "Lee"
	id, err := st.CreatePerson(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type apiFixture struct {
	c                                      *client
	st                                     *store.Store
	token                                  string
	ann, bob, kid, contact, gone, outsider int64
}

// Ann (head, SSO subject "sub-ann") and Bob (adult, subject "sub-bob") with
// kid (born 2015, year only) in the Lee Family; a contact and an archived
// member in the same household; an outsider in no household.
func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	c, st := newTestServer(t)
	f := &apiFixture{c: c, st: st}
	annU, _ := st.CreateUser("ann", "Ann", "a long password", store.RoleMember)
	bobU, _ := st.CreateUser("bob", "Bob", "a long password", store.RoleMember)
	st.DB.Exec(`UPDATE users SET sso_subject = 'sub-ann' WHERE id = ?`, annU)
	st.DB.Exec(`UPDATE users SET sso_subject = 'sub-bob' WHERE id = ?`, bobU)
	f.ann = mkPerson(t, st, "Ann", store.PersonInput{Birth: dates.Partial{Year: 1980, Month: 3, Day: 4},
		Email: "ann@example.org", Phone: "555-0100"})
	f.bob = mkPerson(t, st, "Bob", store.PersonInput{})
	f.kid = mkPerson(t, st, "Kit", store.PersonInput{Birth: dates.Partial{Year: 2015}})
	f.contact = mkPerson(t, st, "Cora", store.PersonInput{Kind: store.KindContact})
	f.gone = mkPerson(t, st, "Gus", store.PersonInput{})
	f.outsider = mkPerson(t, st, "Otto", store.PersonInput{})
	st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, annU, f.ann)
	st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, bobU, f.bob)
	h, err := st.CreateHousehold("The Lee Family", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	st.PlaceMember(h, f.ann, store.RoleHead, 0, 0)
	st.PlaceMember(h, f.bob, store.RoleAdult, 0, 0)
	st.PlaceMember(h, f.kid, store.RoleChild, 0, 0)
	st.PlaceMember(h, f.contact, store.RoleAdult, 0, 0)
	st.PlaceMember(h, f.gone, store.RoleAdult, 0, 0)
	if err := st.ArchivePerson(f.gone, true, 0); err != nil {
		t.Fatal(err)
	}
	f.token, err = st.CreateToken("Event planner", store.TokenApp, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func ids(list any) []int64 {
	var out []int64
	for _, p := range list.([]any) {
		out = append(out, int64(p.(map[string]any)["id"].(float64)))
	}
	return out
}

func TestAPITokenRequired(t *testing.T) {
	f := newAPIFixture(t)
	for _, tok := range []string{"", "md_nope", "nope", f.token + "x"} {
		out, h := apiGet(t, f.c, tok, "/api/v1/people?ids=1", 401)
		if out["code"] == nil || out["error"] == nil {
			t.Errorf("401 body should have error and code: %v", out)
		}
		if h.Get("Cache-Control") != "no-store" {
			t.Error("API answers must be no-store")
		}
	}
	for _, p := range []string{"/api/v1/groups", "/api/v1/groups/1", "/api/v1/people/by-user/sub-ann"} {
		apiGet(t, f.c, "", p, 401)
	}
	// A good token sets no cookie.
	req, _ := http.NewRequest("GET", f.c.base+"/api/v1/groups", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) != 0 {
		t.Errorf("status %d, cookies %v", resp.StatusCode, resp.Cookies())
	}
}

func TestAPIByUserHeadGetsHouseholdNonHeadGetsSelf(t *testing.T) {
	f := newAPIFixture(t)
	out, _ := apiGet(t, f.c, f.token, "/api/v1/people/by-user/sub-ann", 200)
	got := ids(out["people"])
	want := []int64{f.ann, f.bob, f.kid}
	if len(got) != len(want) {
		t.Fatalf("head got %v, want %v (no contact, no archived)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("head got %v, want %v", got, want)
		}
	}
	if p := out["profile"].(map[string]any); int64(p["id"].(float64)) != f.ann || p["role"] != "head" ||
		p["household"] != "The Lee Family" || p["name"] != "Ann Lee" {
		t.Errorf("profile = %v", p)
	}
	for _, p := range out["people"].([]any) {
		m := p.(map[string]any)
		if int64(m["id"].(float64)) == f.kid {
			if m["role"] != "child" {
				t.Errorf("kid role = %v", m["role"])
			}
			wantAge := time.Now().Year() - 2015
			if time.Now().Month() != 12 || time.Now().Day() != 31 {
				wantAge--
			}
			if int(m["age"].(float64)) != wantAge {
				t.Errorf("year-only age = %v, want %d (youngest that fits)", m["age"], wantAge)
			}
		}
		if int64(m["id"].(float64)) == f.bob {
			if _, has := m["age"]; has {
				t.Error("no birth year, so no age")
			}
		}
	}
	noPrivateKeys(t, out)
	if b, _ := json.Marshal(out); strings.Contains(string(b), "example.org") || strings.Contains(string(b), "555") {
		t.Error("contact details leaked")
	}

	out, _ = apiGet(t, f.c, f.token, "/api/v1/people/by-user/sub-bob", 200)
	if got := ids(out["people"]); len(got) != 1 || got[0] != f.bob {
		t.Errorf("a non-head got %v, want only themselves", got)
	}
	apiGet(t, f.c, f.token, "/api/v1/people/by-user/nobody", 404)
}

func TestAPIPeopleSharedNotesOnly(t *testing.T) {
	f := newAPIFixture(t)
	add := func(p int64, kind, body string, share bool) {
		if _, err := f.st.AddNote(p, store.NoteInput{Kind: kind, Body: body, VisibleTo: store.NoteDirectory,
			ShareWithSignups: share}, 0); err != nil {
			t.Fatal(err)
		}
	}
	add(f.kid, store.NoteAllergy, "peanuts", true)
	add(f.kid, store.NoteAllergy, "SECRET-unshared", false)
	add(f.kid, store.NoteGeneral, "SECRET-general-shared", true)
	add(f.kid, store.NoteDietary, "vegetarian", true)
	add(f.contact, store.NoteAllergy, "SECRET-contact", true)
	add(f.gone, store.NoteAllergy, "SECRET-archived", true)

	q := "/api/v1/people?ids=" + itoa(f.kid) + "," + itoa(f.contact) + "," + itoa(f.gone) + ",9999&notes=allergy,dietary,medical"
	out, h := apiGet(t, f.c, f.token, q, 200)
	if h.Get("Cache-Control") != "no-store" {
		t.Error("want no-store")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "SECRET") {
		t.Errorf("a note leaked: %s", raw)
	}
	people := out["people"].([]any)
	if len(people) != 4 {
		t.Fatalf("want 4 entries in request order, got %s", raw)
	}
	kid := people[0].(map[string]any)
	if kid["name"] != "Kit Lee" || kid["household"] != "The Lee Family" || kid["age"] == nil {
		t.Errorf("kid = %v", kid)
	}
	notes := kid["notes"].([]any)
	if len(notes) != 2 || notes[0].(map[string]any)["body"] != "peanuts" || notes[1].(map[string]any)["kind"] != "dietary" {
		t.Errorf("notes = %v", notes)
	}
	for i, id := range []int64{f.contact, f.gone, 9999} {
		g := people[i+1].(map[string]any)
		if int64(g["id"].(float64)) != id || g["gone"] != true || len(g) != 2 {
			t.Errorf("entry %d should be only id and gone: %v", i+1, g)
		}
	}
	noPrivateKeys(t, out)

	// No notes= asks for no notes; a kind filter is respected.
	out, _ = apiGet(t, f.c, f.token, "/api/v1/people?ids="+itoa(f.kid), 200)
	if _, has := out["people"].([]any)[0].(map[string]any)["notes"]; has {
		t.Error("notes only come when asked for")
	}
	out, _ = apiGet(t, f.c, f.token, "/api/v1/people?ids="+itoa(f.kid)+"&notes=dietary", 200)
	if n := out["people"].([]any)[0].(map[string]any)["notes"].([]any); len(n) != 1 || n[0].(map[string]any)["body"] != "vegetarian" {
		t.Errorf("kind filter: %v", n)
	}
	apiGet(t, f.c, f.token, "/api/v1/people?ids="+itoa(f.kid)+"&notes=bogus", 400)
	apiGet(t, f.c, f.token, "/api/v1/people?ids=abc", 400)
	apiGet(t, f.c, f.token, "/api/v1/people", 400)
}

func TestAPIPeopleLimit(t *testing.T) {
	f := newAPIFixture(t)
	var b []string
	for i := 1; i <= 500; i++ {
		b = append(b, itoa(int64(i)))
	}
	apiGet(t, f.c, f.token, "/api/v1/people?ids="+strings.Join(b, ","), 200)
	b = append(b, "501")
	out, _ := apiGet(t, f.c, f.token, "/api/v1/people?ids="+strings.Join(b, ","), 400)
	if out["code"] != "too_many_ids" {
		t.Errorf("code = %v", out["code"])
	}
}

func TestAPIGroupsMembersOnly(t *testing.T) {
	f := newAPIFixture(t)
	open, _ := f.st.CreateGroup(store.GroupInput{Name: "Singers", Description: "Sunday choir"}, 0)
	secret, _ := f.st.CreateGroup(store.GroupInput{Name: "Needs a ride", VisibleTo: store.GroupStaff}, 0)
	f.st.AddGroupMember(open, f.ann, "Leader", 0)
	f.st.AddGroupMember(open, f.bob, "", 0)
	f.st.AddGroupMember(open, f.contact, "", 0)
	f.st.AddGroupMember(secret, f.ann, "", 0)

	out, _ := apiGet(t, f.c, f.token, "/api/v1/groups", 200)
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "Needs a ride") || !strings.Contains(string(raw), "Singers") {
		t.Errorf("groups = %s", raw)
	}
	apiGet(t, f.c, f.token, "/api/v1/groups/"+itoa(secret), 404)
	out, _ = apiGet(t, f.c, f.token, "/api/v1/groups/"+itoa(open), 200)
	people := out["people"].([]any)
	if len(people) != 2 || people[0].(map[string]any)["name"] != "Ann Lee" || people[0].(map[string]any)["label"] != "Leader" {
		t.Errorf("group people = %v", people)
	}
	noPrivateKeys(t, out)
	apiGet(t, f.c, f.token, "/api/v1/groups/abc", 404)
}

func TestAgeYearsYoungestRule(t *testing.T) {
	today := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		b    dates.Partial
		want int
		ok   bool
	}{
		{dates.Partial{}, 0, false},
		{dates.Partial{Year: 2013}, 12, true},                     // Dec 31 2013: not yet 13
		{dates.Partial{Year: 2013, Month: 10}, 12, true},          // Oct 31: not yet
		{dates.Partial{Year: 2013, Month: 9}, 13, true},           // Sep 30: passed
		{dates.Partial{Year: 2013, Month: 10, Day: 16}, 13, true}, // today
		{dates.Partial{Year: 2013, Month: 10, Day: 17}, 12, true}, // tomorrow
		{dates.Partial{Year: 2030}, 0, true},                      // future clamps
	} {
		got, ok := store.AgeOn(c.b, today)
		if ok != c.ok || got != c.want {
			t.Errorf("%+v: got %d,%v want %d,%v", c.b, got, ok, c.want, c.ok)
		}
	}
}
