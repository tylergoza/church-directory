package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/sso"
	"github.com/tylergoza/church-directory/internal/store"
)

// ssoStaff is a server with SSO on and a staff member (an editor in User
// Management) signed in through the fake User Management. Besides them
// there is kim (a member), pat (suspended) and odd (a role the directory
// doesn't know).
func ssoStaff(t *testing.T) (*client, *store.Store, *fakeUM) {
	t.Helper()
	f := newFakeUM(t)
	f.set(func(f *fakeUM) {
		f.user = sso.User{Sub: "10", Username: "sam", Name: "Sam Staff", Role: "editor"}
		mk := func(sub, name, role string, active bool) sso.AppUser {
			return sso.AppUser{User: sso.User{Sub: sub, Username: name, Name: strings.ToUpper(name[:1]) + name[1:], Role: role}, Active: active, Suspended: !active}
		}
		f.appUsers = sso.AppUsers{Roles: []string{"member", "editor", "admin"}, Users: []sso.AppUser{
			mk("10", "sam", "editor", true),
			mk("20", "kim", "member", true),
			mk("30", "pat", "member", false),
			mk("40", "odd", "viewer", true),
		}}
	})
	c, st := newTestServerWith(t, ssoConfig(f))
	c.get("/login", 200)
	return c, st, f
}

func TestRoleFromSSOTable(t *testing.T) {
	for in, want := range map[string]string{
		"member": store.RoleMember, "editor": store.RoleEditor, "admin": store.RoleAdmin,
		" editor ": store.RoleEditor,
		"":         "", "user": "", "Admin": "", "viewer": "",
	} {
		if got := store.RoleFromSSO(in); got != want {
			t.Errorf("RoleFromSSO(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSSOUnknownRoleHasNoAccess(t *testing.T) {
	f := newFakeUM(t)
	f.set(func(f *fakeUM) { f.user = sso.User{Sub: "50", Username: "zed", Role: "viewer"} })
	c, st := newTestServerWith(t, ssoConfig(f))
	body := c.get("/login", 403)
	if !strings.Contains(body, "have access to the directory") {
		t.Errorf("expected the no-access page:\n%s", body)
	}
	if _, err := st.GetUserBySubject("50"); err == nil {
		t.Error("a user with no directory role was cached")
	}
	if c.sessionToken() != "" {
		t.Error("signed in without a role")
	}
}

func TestSSOGrantCheckSignsOutWhenRoleGoes(t *testing.T) {
	c, st, f := ssoStaff(t)
	f.set(func(f *fakeUM) { f.user.Role = "viewer" })
	ageGrantCheck(t, st, c.sessionToken(), time.Hour)
	if resp := c.getRaw(c.noFollow(), "/admin"); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("a session whose role is gone must be signed out, got %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestSSOLinkPickerListsUMUsersAndLinksBySubject(t *testing.T) {
	c, st, _ := ssoStaff(t)
	pid := mustPersonID(t, st, "Kim", "Adult")
	setBirthYear(t, st, pid, 1980)
	page := "/admin/people/" + itoa(pid)

	body := c.get(page, 200)
	if !strings.Contains(body, `name="sub"`) || !strings.Contains(body, `<option value="20"`) || !strings.Contains(body, "(kim)") {
		t.Fatalf("picker should list kim from User Management:\n%s", body)
	}
	for _, no := range []string{`<option value="30"`, `<option value="40"`} {
		if strings.Contains(body, no) {
			t.Errorf("picker lists someone without directory access (%s)", no)
		}
	}
	// No cache row yet for kim: linking makes it.
	if _, err := st.GetUserBySubject("20"); err == nil {
		t.Fatal("kim cached before being picked")
	}
	c.post(page, page+"/link", url.Values{"sub": {"20"}}, 200)
	u, err := st.GetUserBySubject("20")
	if err != nil || u.Role != store.RoleMember {
		t.Fatalf("cache row not made: %+v %v", u, err)
	}
	if p, _ := st.GetPerson(pid); p.UserID != u.ID {
		t.Fatal("profile not linked to the user")
	}
	// One profile per user: kim no longer appears for another person.
	other := mustPersonID(t, st, "Kimmy", "Other")
	setBirthYear(t, st, other, 1990)
	if body := c.get("/admin/people/"+itoa(other), 200); strings.Contains(body, `<option value="20"`) {
		t.Error("a user with a profile is still offered")
	}
	c.post("/admin/people/"+itoa(other), "/admin/people/"+itoa(other)+"/link", url.Values{"sub": {"20"}}, 200)
	if p, _ := st.GetPerson(other); p.UserID != 0 {
		t.Fatal("one user got two profiles")
	}
	// Someone without directory access there can't be linked by posting.
	c.post("/admin/people/"+itoa(other), "/admin/people/"+itoa(other)+"/link", url.Values{"sub": {"30"}}, 200)
	if _, err := st.GetUserBySubject("30"); err == nil {
		t.Error("a suspended user was cached by the picker")
	}
}

func TestSSOLinkRefusesKnownUnder13(t *testing.T) {
	c, st, _ := ssoStaff(t)
	kid := mustPersonID(t, st, "Kid", "Young")
	setBirthYear(t, st, kid, time.Now().Year()-6)
	page := "/admin/people/" + itoa(kid)
	if strings.Contains(c.get(page, 200), "Link to user</button>") {
		t.Error("an under-13 got a Link to user button")
	}
	c.post(page, page+"/link", url.Values{"sub": {"20"}}, 200)
	if p, _ := st.GetPerson(kid); p.UserID != 0 {
		t.Fatal("an under-13 was linked")
	}
}

// linkKim links a profile (age unknown, so attested) to the SSO user kim.
func linkKim(t *testing.T, c *client, st *store.Store) int64 {
	t.Helper()
	pid := mustPersonID(t, st, "Kim", "Adult")
	page := "/admin/people/" + itoa(pid)
	c.post(page, page+"/link", url.Values{"sub": {"20"}, "attest13": {"1"}}, 200)
	if p, _ := st.GetPerson(pid); p.UserID == 0 {
		t.Fatal("setup: not linked")
	}
	return pid
}

func saveBirthYear(c *client, pid int64, year int) string {
	page := "/admin/people/" + itoa(pid)
	return c.post(page, page, url.Values{"first_name": {"Kim"}, "last_name": {"Adult"}, "kind": {"member"}, "birth_year": {strconv.Itoa(year)}}, 200)
}

func TestUnder13FoundLaterSuspendsWithStaffGrant(t *testing.T) {
	c, st, f := ssoStaff(t)
	pid := linkKim(t, c, st)
	body := saveBirthYear(c, pid, time.Now().Year()-6)
	if !strings.Contains(body, "suspended in User Management") {
		t.Errorf("expected the suspended message:\n%s", body)
	}
	f.mu.Lock()
	patches := append([]patchCall(nil), f.patches...)
	f.mu.Unlock()
	if len(patches) != 1 {
		t.Fatalf("PATCHes = %+v", patches)
	}
	if p := patches[0]; p.sub != "20" || p.actingGrant != testGrant || p.body["suspended"] != true || len(p.body) != 1 {
		t.Errorf("PATCH = %+v", p)
	}
	if items, _ := st.Under13Items(); len(items) != 0 {
		t.Errorf("item still open: %+v", items)
	}
	kim, _ := st.GetUserBySubject("20")
	if kim.Active {
		t.Error("the cache row should be inactive")
	}
	if p, _ := st.GetPerson(pid); p.UserID != 0 || p.LoginAllowed {
		t.Error("login not unlinked")
	}
	if strings.Contains(c.get("/admin/needs-action", 200), "Under-13 logins") {
		t.Error("nothing should be left on Needs action")
	}
}

func TestUnder13PatchFailureLeavesItemWithSuspendButton(t *testing.T) {
	c, st, f := ssoStaff(t)
	pid := linkKim(t, c, st)
	f.set(func(f *fakeUM) { f.patchErr = "forbidden" })
	body := saveBirthYear(c, pid, time.Now().Year()-6)
	if !strings.Contains(body, "still on") {
		t.Errorf("expected a warning that access is still on:\n%s", body)
	}
	items, _ := st.Under13Items()
	if len(items) != 1 {
		t.Fatalf("item should stay open: %+v", items)
	}
	if na := c.get("/admin/needs-action", 200); !strings.Contains(na, "Suspend in User Management") {
		t.Error("the Suspend in User Management button is missing")
	}
	// Pressing it while it still fails changes nothing.
	suspend := "/admin/needs-action/under13/" + itoa(items[0].ID) + "/suspend"
	c.post("/admin/needs-action", suspend, url.Values{}, 200)
	if items, _ := st.Under13Items(); len(items) != 1 {
		t.Fatal("a failed suspend closed the item")
	}
	// Once User Management accepts, the button resolves it.
	f.set(func(f *fakeUM) { f.patchErr = "" })
	c.post("/admin/needs-action", suspend, url.Values{}, 200)
	if items, _ := st.Under13Items(); len(items) != 0 {
		t.Fatal("the item should be done")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.patches) != 3 {
		t.Errorf("PATCH calls = %d, want 3", len(f.patches))
	}
	for _, p := range f.patches {
		if p.actingGrant != testGrant {
			t.Errorf("PATCH without the staff grant: %+v", p)
		}
	}
}

func TestUnder13WithLocalLoginCannotSuspend(t *testing.T) {
	c, st, f := ssoStaff(t)
	pid := linkKim(t, c, st)
	// A staff member who signed in with the break-glass local login has
	// no grant to act with.
	st.SetSetting(localLoginSetting, "on")
	st.CreateUser("lou", "Lou", "a long password", store.RoleEditor)
	lou := c.as("lou")
	saveBirthYear(lou, pid, time.Now().Year()-6)
	f.mu.Lock()
	n := len(f.patches)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("a PATCH went out without a grant (%d)", n)
	}
	if items, _ := st.Under13Items(); len(items) != 1 {
		t.Fatalf("item should stay: %+v", items)
	}
	na := lou.get("/admin/needs-action", 200)
	if strings.Contains(na, "Suspend in User Management</button>") {
		t.Error("no Suspend button without a grant")
	}
	if !strings.Contains(na, ">Done</button>") {
		t.Error("Done should still be there")
	}
}

func TestLocalLoginRefusedWhenSSOOnUnlessBreakGlass(t *testing.T) {
	f := newFakeUM(t)
	c, st := newTestServerWith(t, ssoConfig(f))
	st.CreateUser("lou", "", "a long password", store.RoleAdmin)
	c.get("/signed-out", 200) // sets the CSRF cookie
	var csrf string
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == csrfCookie {
			csrf = ck.Value
		}
	}
	form := url.Values{"username": {"lou"}, "password": {"a long password"}}
	post := func() *http.Response {
		req, _ := http.NewRequest("POST", c.base+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := c.noFollow().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := post(); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("password post: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c.sessionToken() != "" {
		t.Fatal("signed in with a password while SSO is on")
	}
	st.SetSetting(localLoginSetting, "on")
	post()
	if c.sessionToken() == "" {
		t.Fatal("break-glass local login should work")
	}
}
