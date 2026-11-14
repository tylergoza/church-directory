package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
	"github.com/tylergoza/church-directory/internal/umtoken"
)

// fakeTokenUM answers User Management's GET /api/v1/token for the tokens
// in its map; anything else (another audience, expired) is 401.
type fakeTokenUM struct {
	srv  *httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	ok   map[string]umtoken.Answer
	down bool
}

func newFakeTokenUM(t *testing.T) *fakeTokenUM {
	f := &fakeTokenUM{ok: map[string]umtoken.Answer{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/token" {
			http.NotFound(w, r)
			return
		}
		f.hits.Add(1)
		f.mu.Lock()
		ans, ok := f.ok[r.Header.Get("X-Token")]
		down := f.down
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if id, sec, good := r.BasicAuth(); !good || id != "directory" || sec != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		switch {
		case down:
			w.WriteHeader(http.StatusBadGateway)
		case !ok:
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid_token"}`))
		default:
			json.NewEncoder(w).Encode(ans)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTokenUM) give(token, sub, username, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ok[token] = umtoken.Answer{User: umtoken.User{Sub: sub, Username: username,
		Name: strings.ToUpper(username[:1]) + username[1:], Role: role}, Via: "mcp"}
}

func (f *fakeTokenUM) set(fn func(f *fakeTokenUM)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// meServer runs the directory with single sign-on pointing at f (nil for
// none) on st (nil for a new store).
func meServer(t *testing.T, f *fakeTokenUM, st *store.Store) (*Server, *store.Store, *client) {
	t.Helper()
	if st == nil {
		var err error
		st, err = store.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
	}
	env := map[string]string{}
	if f != nil {
		env = map[string]string{"SSO_URL": f.srv.URL, "SSO_CLIENT_ID": "directory", "SSO_CLIENT_SECRET": "s3cret"}
	}
	srv, err := New(Config{Env: envOf(env)}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, st, &client{t: t, base: ts.URL, http: &http.Client{}}
}

// meGet calls path with a bearer token ("" for none), checks the status
// and the headers every /me answer must have, and returns the body.
func (c *client) meGet(path, token string, want int) (string, http.Header) {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("GET %s: status %d, want %d\n%s", path, resp.StatusCode, want, b)
	}
	h := resp.Header
	if h.Get("Set-Cookie") != "" {
		c.t.Errorf("GET %s set a cookie", path)
	}
	if h.Get("Cache-Control") != "private, no-store" {
		c.t.Errorf("GET %s: Cache-Control %q", path, h.Get("Cache-Control"))
	}
	if h.Get("Access-Control-Allow-Origin") != "" {
		c.t.Errorf("GET %s: CORS header on /me", path)
	}
	return string(b), h
}

func isJSONError(body string) bool {
	var e struct{ Error string }
	return json.Unmarshal([]byte(body), &e) == nil && e.Error != ""
}

func TestMeOffWithoutUserManagement(t *testing.T) {
	f := newAPIFixture(t) // its server has no single sign-on
	for _, p := range []string{"/api/v1/me", "/api/v1/me/people?q=lee", "/api/v1/me/households/1", "/api/v1/me/nope"} {
		if body, _ := f.c.meGet(p, "umd_whatever", 404); isJSONError(body) {
			t.Errorf("%s: off should be a plain 404, got %s", p, body)
		}
	}
	// The app-token API is unchanged, its catch-all still JSON.
	apiGet(t, f.c, f.token, "/api/v1/groups", 200)
	if out, _ := apiGet(t, f.c, f.token, "/api/v1/nope", 404); out["code"] != "not_found" {
		t.Errorf("catch-all = %v", out)
	}
}

func TestMeTokenChecks(t *testing.T) {
	um := newFakeTokenUM(t)
	srv, st, c := meServer(t, um, nil)
	um.give("umd_mel", "s-mel", "mel", "member")
	appTok, _ := st.CreateToken("Event planner", store.TokenApp, 0)

	// No token, an app token, a token for another audience, an expired
	// one: all 401 invalid_token, and nobody is added.
	for _, tok := range []string{"", appTok, "umd_for_calendar", "umd_expired"} {
		body, h := c.meGet("/api/v1/me", tok, 401)
		if !strings.Contains(body, "invalid_token") || h.Get("WWW-Authenticate") == "" {
			t.Errorf("token %q: %s", tok, body)
		}
	}
	if n, _ := st.CountUsers(); n != 0 {
		t.Errorf("refused tokens made %d users", n)
	}

	// A good token: the user is made as an SSO sign-in makes them; a second
	// call within 60 s doesn't ask User Management again.
	um.hits.Store(0)
	now := time.Now()
	srv.meTokens.Now = func() time.Time { return now }
	c.meGet("/api/v1/me", "umd_mel", 200)
	c.meGet("/api/v1/me/people?q=x", "umd_mel", 200)
	if n := um.hits.Load(); n != 1 {
		t.Errorf("second call within 60 s asked User Management again: %d calls", n)
	}
	users, _ := st.ListUsers()
	if len(users) != 1 || users[0].SSOSubject != "s-mel" || users[0].Role != store.RoleMember {
		t.Fatalf("users = %+v", users)
	}
	var sessions int
	if err := st.DB.QueryRow(`SELECT count(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Errorf("a /me call made %d sessions (%v)", sessions, err)
	}

	// After 60 s it's checked again, and a token gone bad is refused.
	now = now.Add(61 * time.Second)
	um.set(func(f *fakeTokenUM) { delete(f.ok, "umd_mel") })
	c.meGet("/api/v1/me", "umd_mel", 401)
	if n := um.hits.Load(); n != 2 {
		t.Errorf("calls = %d, want 2", n)
	}

	// A role that isn't a directory role, and another delegate: 403.
	um.give("umd_none", "s-none", "none", "scheduler")
	c.meGet("/api/v1/me", "umd_none", 403)
	um.set(func(f *fakeTokenUM) {
		f.ok["umd_other"] = umtoken.Answer{User: umtoken.User{Sub: "s-x", Username: "x", Role: "member"}, Via: "someapp"}
	})
	c.meGet("/api/v1/me", "umd_other", 403)

	// User Management down is 503 with Retry-After, never 401.
	um.set(func(f *fakeTokenUM) { f.down = true })
	if _, h := c.meGet("/api/v1/me", "umd_new", 503); h.Get("Retry-After") == "" {
		t.Error("503 without Retry-After")
	}
	um.set(func(f *fakeTokenUM) { f.down = false })

	// Unknown /me paths are a plain 404 ("not built"), unlike /api/'s JSON one.
	um.give("umd_mel2", "s-mel", "mel", "member")
	for _, p := range []string{"/api/v1/me/nope", "/api/v1/me/people/1/notes", "/api/v1/me/households"} {
		if body, _ := c.meGet(p, "umd_mel2", 404); isJSONError(body) {
			t.Errorf("unknown /me path %s: %s", p, body)
		}
	}
	// The app-token API still works with single sign-on on.
	apiGet(t, c, appTok, "/api/v1/groups", 200)
	if out, _ := apiGet(t, c, appTok, "/api/v1/nope", 404); out["code"] != "not_found" {
		t.Errorf("catch-all = %v", out)
	}
}

// meWorld is the Lee household seen from several sides.
type meWorld struct {
	um                                               *fakeTokenUM
	st                                               *store.Store
	c                                                *client
	hh                                               int64
	ann, bob, kit, cora, gus, otto, mainSt, secretRd int64
}

// newMeWorld: the Lee Family (Ann head, linked to the login "ann"; Bob
// adult, linked to "bob", who hides his email; Kit, a child born 2015,
// whose details the children's defaults hide; Cora a contact; Gus
// archived), Otto Lee in no household, two addresses (one hidden, Kit's),
// a members' group and a staff-only one, spouse and parent links, and a
// note on Ann. Local logins "wendy" (member, no profile) and "eddie"
// (editor) sign in to the web pages with a password; their tokens map to
// the same rows by username.
func newMeWorld(t *testing.T) *meWorld {
	t.Helper()
	um := newFakeTokenUM(t)
	_, st, c := meServer(t, um, nil)
	w := &meWorld{um: um, st: st, c: c}
	for _, u := range []struct{ name, role string }{{"wendy", store.RoleMember}, {"eddie", store.RoleEditor},
		{"ann", store.RoleMember}, {"bob", store.RoleMember}, {"kit", store.RoleMember}} {
		if _, err := st.CreateUser(u.name, "", "a long password", u.role); err != nil {
			t.Fatal(err)
		}
		um.give("umd_"+u.name, "s-"+u.name, u.name, u.role)
	}
	w.ann = mkPerson(t, st, "Ann", store.PersonInput{Birth: dates.Partial{Year: 1980, Month: 3, Day: 4},
		Anniversary: dates.Partial{Year: 2005, Month: 6, Day: 12}, Email: "ann@example.org", Phone: "555-0100"})
	w.bob = mkPerson(t, st, "Bob", store.PersonInput{Email: "bob@hidden.example",
		Anniversary: dates.Partial{Year: 2005, Month: 6, Day: 12}})
	w.kit = mkPerson(t, st, "Kit", store.PersonInput{Birth: dates.Partial{Year: 2015, Month: 5, Day: 5}, Email: "kit@child.example"})
	w.cora = mkPerson(t, st, "Cora", store.PersonInput{Kind: store.KindContact})
	w.gus = mkPerson(t, st, "Gus", store.PersonInput{})
	w.otto = mkPerson(t, st, "Otto", store.PersonInput{})
	for name, pid := range map[string]int64{"ann": w.ann, "bob": w.bob, "kit": w.kit} {
		st.DB.Exec(`UPDATE people SET user_id = (SELECT id FROM users WHERE username = ?) WHERE id = ?`, name, pid)
	}
	no := false
	if err := st.SetPersonFieldSettings(w.bob, map[store.Field]store.FieldSetting{store.FieldEmail: {Shown: &no}}, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	if w.hh, err = st.CreateHousehold("The Lee Family", "", 0); err != nil {
		t.Fatal(err)
	}
	if w.mainSt, err = st.AddAddress(w.hh, store.AddressInput{Label: "Home", Address1: "12 Main St", City: "Springfield", State: "PA", Postal: "19000"}, 0); err != nil {
		t.Fatal(err)
	}
	if w.secretRd, err = st.AddAddress(w.hh, store.AddressInput{Label: "Cabin", Address1: "99 Secret Rd", City: "Hideaway"}, 0); err != nil {
		t.Fatal(err)
	}
	st.SetAddressVisibility(w.secretRd, &no, false, 0)
	st.PlaceMember(w.hh, w.ann, store.RoleHead, w.mainSt, 0)
	st.PlaceMember(w.hh, w.bob, store.RoleAdult, w.mainSt, 0)
	st.PlaceMember(w.hh, w.kit, store.RoleChild, w.secretRd, 0)
	st.PlaceMember(w.hh, w.cora, store.RoleAdult, 0, 0)
	st.PlaceMember(w.hh, w.gus, store.RoleAdult, 0, 0)
	if err := st.ArchivePerson(w.gus, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRelationship(store.RelSpouse, w.ann, w.bob, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRelationship(store.RelParent, w.ann, w.kit, 0); err != nil {
		t.Fatal(err)
	}
	choir, err := st.CreateGroup(store.GroupInput{Name: "Choir", VisibleTo: store.GroupMembers}, 0)
	if err != nil {
		t.Fatal(err)
	}
	staffOnly, err := st.CreateGroup(store.GroupInput{Name: "Deacons Board", VisibleTo: store.GroupStaff}, 0)
	if err != nil {
		t.Fatal(err)
	}
	st.AddGroupMember(choir, w.ann, "", 0)
	st.AddGroupMember(staffOnly, w.ann, "", 0)
	if _, err := st.AddNote(w.ann, store.NoteInput{Kind: "general", Body: "Ann's note text", VisibleTo: store.NoteDirectory}, 0); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *meWorld) get(t *testing.T, path, tok string) map[string]any {
	t.Helper()
	body, _ := w.c.meGet(path, tok, 200)
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, body)
	}
	return m
}

// byID finds the item with id in a decoded list.
func byID(t *testing.T, list any, id int64) map[string]any {
	t.Helper()
	for _, x := range list.([]any) {
		if m := x.(map[string]any); int64(m["id"].(float64)) == id {
			return m
		}
	}
	return nil
}

// TestMeMemberSeesWhatTheWebShows: an ordinary member (no profile) gets
// exactly what the web pages show them, and nothing hidden.
func TestMeMemberSeesWhatTheWebShows(t *testing.T) {
	w := newMeWorld(t)
	const tok = "umd_wendy"

	// The web pages for the same login, for comparison: a second server on
	// the same database with the local login.
	web := startServer(t, Config{}, w.st).as("wendy")
	webAnn := web.get("/people/"+itoa(w.ann), 200)
	webHH := web.get("/households/"+itoa(w.hh), 200)

	search := w.c.mustGet(t, "/api/v1/me/people?q=lee", tok)
	household := w.c.mustGet(t, "/api/v1/me/households/"+itoa(w.hh), tok)
	annPage := w.c.mustGet(t, "/api/v1/me/people/"+itoa(w.ann), tok)
	all := search + household + annPage + w.c.mustGet(t, "/api/v1/me", tok)

	hidden := []string{"bob@hidden.example", "kit@child.example", "99 Secret", "Cabin", "1980", "Deacons",
		"Cora", "Gus", "Ann's note", "May 5", "2015", "photo", "note"}
	for _, s := range hidden {
		if strings.Contains(all, s) {
			t.Errorf("a member's API answers contain %q", s)
		}
		if strings.Contains(webAnn+webHH, s) && s != "photo" && s != "note" && s != "Ann's note" {
			t.Errorf("test is wrong: the web shows %q too", s)
		}
	}
	for _, s := range []string{"ann@example.org", "555-0100", "March 4", "12 Main St", "Choir"} {
		if !strings.Contains(webAnn+webHH, s) {
			t.Errorf("test is wrong: the web doesn't show %q", s)
		}
		if !strings.Contains(all, s) {
			t.Errorf("the API leaves out %q, which the web shows", s)
		}
	}

	// Search: the directory page's people (no contact, no archived), by name.
	var sr struct{ People []map[string]any }
	json.Unmarshal([]byte(search), &sr)
	var names []string
	for _, p := range sr.People {
		names = append(names, p["name"].(string))
	}
	if strings.Join(names, ",") != "Ann Lee,Bob Lee,Kit Lee,Otto Lee" {
		t.Errorf("search = %v", names)
	}
	ann := sr.People[0]
	if ann["email"] != "ann@example.org" || ann["phone"] != "555-0100" || ann["birthday"] != "March 4" ||
		ann["role"] != "head" || ann["household"].(map[string]any)["name"] != "The Lee Family" {
		t.Errorf("ann in search = %v", ann)
	}
	if _, ok := ann["age"]; ok {
		t.Error("ann's birth year is hidden, so no age")
	}
	for _, k := range []string{"email", "phone", "birthday", "age", "anniversary"} {
		if _, ok := sr.People[2][k]; ok {
			t.Errorf("kit (a child) has %q for a member", k)
		}
	}
	if _, ok := sr.People[1]["email"]; ok {
		t.Error("bob hid his email")
	}
	if _, ok := sr.People[3]["household"]; ok {
		t.Error("otto is in no household")
	}

	// Person page: as /people/{id}, no notes, staff-only groups left out.
	var pp map[string]any
	json.Unmarshal([]byte(annPage), &pp)
	if pp["anniversary"] != "June 12, 2005" || len(pp["groups"].([]any)) != 1 || pp["role"] != "head" {
		t.Errorf("ann's page = %v", pp)
	}
	fam := map[string]string{}
	for _, f := range pp["family"].([]any) {
		m := f.(map[string]any)
		fam[m["name"].(string)] = m["relation"].(string)
	}
	if fam["Bob Lee"] != "spouse" || fam["Kit Lee"] != "child" || len(fam) != 2 {
		t.Errorf("family = %v", fam)
	}

	// Household: Cora (a contact) and Gus (archived) aren't on the card,
	// the hidden address isn't there and Kit has no address_id.
	var hh map[string]any
	json.Unmarshal([]byte(household), &hh)
	if len(hh["members"].([]any)) != 3 || len(hh["addresses"].([]any)) != 1 {
		t.Fatalf("household = %v", hh)
	}
	addr := hh["addresses"].([]any)[0].(map[string]any)
	if addr["label"] != "Home" || strings.Join(toStrings(addr["lines"]), "|") != "12 Main St|Springfield, PA 19000" {
		t.Errorf("address = %v", addr)
	}
	if k := byID(t, hh["members"], w.kit); k["address_id"] != nil || k["email"] != nil {
		t.Errorf("kit = %v", k)
	}
	if a := byID(t, hh["members"], w.ann); int64(a["address_id"].(float64)) != w.mainSt || a["role"] != "head" {
		t.Errorf("ann = %v", a)
	}

	// Who a member may not see: 404 with a JSON error, as the page 404s.
	for _, p := range []string{"/api/v1/me/people/" + itoa(w.cora), "/api/v1/me/people/" + itoa(w.gus),
		"/api/v1/me/people/999999", "/api/v1/me/people/abc", "/api/v1/me/households/999999"} {
		if body, _ := w.c.meGet(p, tok, 404); !isJSONError(body) {
			t.Errorf("%s: %s", p, body)
		}
	}
	web.get("/people/"+itoa(w.cora), 404)

	// /me for a login with no profile: no households, still 200.
	me := w.get(t, "/api/v1/me", tok)
	if len(me["households"].([]any)) != 0 || me["app_role"] != "member" {
		t.Errorf("/me = %v", me)
	}
	// q is needed.
	if body, _ := w.c.meGet("/api/v1/me/people?q=%20", tok, 400); !isJSONError(body) {
		t.Errorf("no q: %s", body)
	}
	// limit is honoured.
	if out := w.get(t, "/api/v1/me/people?q=lee&limit=2", tok); len(out["people"].([]any)) != 2 {
		t.Errorf("limit 2 = %v", out)
	}
}

// TestMeStaffSeeWhatStaffSee: an editor gets what the web shows staff:
// hidden fields, the hidden address, the contact and archived person on the
// card, the staff-only group. Still no notes.
func TestMeStaffSeeWhatStaffSee(t *testing.T) {
	w := newMeWorld(t)
	const tok = "umd_eddie"
	web := startServer(t, Config{}, w.st).as("eddie")
	webAll := web.get("/people/"+itoa(w.ann), 200) + web.get("/households/"+itoa(w.hh), 200) + web.get("/people/"+itoa(w.kit), 200)

	household := w.c.mustGet(t, "/api/v1/me/households/"+itoa(w.hh), tok)
	all := household + w.c.mustGet(t, "/api/v1/me/people/"+itoa(w.ann), tok) + w.c.mustGet(t, "/api/v1/me/people/"+itoa(w.kit), tok)
	for _, s := range []string{"bob@hidden.example", "kit@child.example", "99 Secret", "1980", "Deacons Board", "Cora", "Gus", "May 5, 2015"} {
		if !strings.Contains(webAll, s) {
			t.Errorf("test is wrong: the web doesn't show staff %q", s)
		}
		if !strings.Contains(all, s) {
			t.Errorf("staff don't get %q", s)
		}
	}
	if strings.Contains(all, "Ann's note") {
		t.Error("notes never go out on /me")
	}
	ann := w.get(t, "/api/v1/me/people/"+itoa(w.ann), tok)
	if _, ok := ann["age"]; !ok {
		t.Error("staff see ann's birth year, so her age")
	}
	var hh map[string]any
	json.Unmarshal([]byte(household), &hh)
	if len(hh["addresses"].([]any)) != 2 || len(hh["members"].([]any)) != 5 {
		t.Errorf("staff household = %s", household)
	}
	// Search is the members' directory for everyone: no contacts or
	// archived people, as on the directory page.
	if s := w.c.mustGet(t, "/api/v1/me/people?q=lee", tok); strings.Contains(s, "Cora") || strings.Contains(s, "Gus") {
		t.Errorf("staff search = %s", s)
	}
}

// TestMeHeadsAndLinkedAdults: the household's head sees all of it (as on
// the web); a linked adult who isn't a head sees what a member sees of the
// others; a login linked to an under-13 is refused.
func TestMeHeadsAndLinkedAdults(t *testing.T) {
	w := newMeWorld(t)
	me := w.get(t, "/api/v1/me", "umd_ann")
	hs := me["households"].([]any)
	if int64(me["id"].(float64)) != w.ann || len(hs) != 1 {
		t.Fatalf("ann /me = %v", me)
	}
	if h := hs[0].(map[string]any); int64(h["id"].(float64)) != w.hh || h["role"] != "head" || h["can_edit"] != true {
		t.Errorf("ann's household = %v", h)
	}
	annHH := w.c.mustGet(t, "/api/v1/me/households/"+itoa(w.hh), "umd_ann")
	for _, s := range []string{"bob@hidden.example", "kit@child.example", "99 Secret", "Cora"} {
		if !strings.Contains(annHH, s) {
			t.Errorf("the head doesn't get %q", s)
		}
	}
	if strings.Contains(annHH, "Gus") {
		t.Error("archived people aren't shown to heads")
	}

	me = w.get(t, "/api/v1/me", "umd_bob")
	if h := me["households"].([]any)[0].(map[string]any); h["role"] != "adult" || h["can_edit"] != false {
		t.Errorf("bob's household = %v", h)
	}
	bobHH := w.c.mustGet(t, "/api/v1/me/households/"+itoa(w.hh), "umd_bob")
	if !strings.Contains(bobHH, "bob@hidden.example") {
		t.Error("bob sees his own hidden email")
	}
	for _, s := range []string{"kit@child.example", "99 Secret", "Cora"} {
		if strings.Contains(bobHH, s) {
			t.Errorf("a non-head adult gets %q", s)
		}
	}

	w.c.meGet("/api/v1/me", "umd_kit", 403)
	w.c.meGet("/api/v1/me/people?q=lee", "umd_kit", 403)
}

// TestMeFieldNames checks the JSON keys against APP_CONTRACTS.md's
// examples for the directory (the MCP server's tools read these names).
func TestMeFieldNames(t *testing.T) {
	w := newMeWorld(t)
	const tok = "umd_eddie"
	keysOf := func(m map[string]any) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	hasKeys := func(what string, m map[string]any, want string) {
		t.Helper()
		for _, k := range strings.Split(want, ",") {
			if _, ok := m[k]; !ok {
				t.Errorf("%s: no %q in %s", what, k, keysOf(m))
			}
		}
	}
	me := w.get(t, "/api/v1/me", "umd_ann")
	hasKeys("/me", me, "id,name,households")
	hasKeys("/me household", me["households"].([]any)[0].(map[string]any), "id,name,role")

	people := w.get(t, "/api/v1/me/people?q=ann", tok)["people"].([]any)
	p := people[0].(map[string]any)
	hasKeys("search person", p, "id,name,email,phone,household")
	hasKeys("search household", p["household"].(map[string]any), "id,name")

	pp := w.get(t, "/api/v1/me/people/"+itoa(w.ann), tok)
	hasKeys("person", pp, "id,name,role,age,email,phone,birthday,anniversary,households,family,groups")
	hasKeys("person household", pp["households"].([]any)[0].(map[string]any), "id,name,role")
	hasKeys("person family", pp["family"].([]any)[0].(map[string]any), "id,name,relation")
	hasKeys("person group", pp["groups"].([]any)[0].(map[string]any), "id,name")

	hh := w.get(t, "/api/v1/me/households/"+itoa(w.hh), tok)
	hasKeys("household", hh, "id,name,addresses,members")
	hasKeys("address", hh["addresses"].([]any)[0].(map[string]any), "id,label,lines")
	hasKeys("member", byID(t, hh["members"], w.ann), "id,name,role,age,email,phone,address_id")

	// Lists are [] when empty, never null.
	if s := w.c.mustGet(t, "/api/v1/me/people?q=nobodyatall", tok); !strings.Contains(s, `"people":[]`) {
		t.Errorf("empty search = %s", s)
	}
	if s := w.c.mustGet(t, "/api/v1/me/people/"+itoa(w.otto), tok); !strings.Contains(s, `"households":[]`) ||
		!strings.Contains(s, `"family":[]`) || !strings.Contains(s, `"groups":[]`) {
		t.Errorf("otto = %s", s)
	}
}

func (c *client) mustGet(t *testing.T, path, tok string) string {
	t.Helper()
	body, _ := c.meGet(path, tok, 200)
	return body
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
