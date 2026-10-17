package server

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

func userID(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	us, _ := st.ListUsers()
	for _, u := range us {
		if u.Username == name {
			return u.ID
		}
	}
	t.Fatalf("no user %s", name)
	return 0
}

func setBirthYear(t *testing.T, st *store.Store, id int64, year int) {
	t.Helper()
	p, _ := st.GetPerson(id)
	err := st.UpdatePerson(id, store.PersonInput{FirstName: p.FirstName, LastName: p.LastName, Birth: dates.Partial{Year: year}, Kind: store.KindMember}, 0)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStaffLinkAndUnlinkPages(t *testing.T) {
	sam, st := staffClient(t, Config{})
	pid := mustPersonID(t, st, "Ann", "Adult")
	kid := mustPersonID(t, st, "Kid", "Young")
	setBirthYear(t, st, pid, 1980)
	setBirthYear(t, st, kid, time.Now().Year()-6)
	mel := userID(t, st, "mel")

	for _, id := range []int64{pid, kid} {
		page := sam.get("/admin/people/"+itoa(id), 200)
		if !strings.Contains(page, "Children under 13 can&#39;t have their own login. If we learn that someone under 13 has one, it will be turned off and may be deleted.") {
			t.Errorf("person %d: the under-13 warning is missing", id)
		}
	}
	if page := sam.get("/admin/people/"+itoa(kid), 200); strings.Contains(page, "Link to user</button>") {
		t.Error("a known under-13 must not get a Link to user button")
	}
	// Posting anyway is refused.
	sam.post("/admin/people/"+itoa(kid), "/admin/people/"+itoa(kid)+"/link", url.Values{"user_id": {itoa(mel)}}, 200)
	if p, _ := st.GetPerson(kid); p.UserID != 0 {
		t.Fatal("an under-13 was linked")
	}
	sam.post("/admin/people/"+itoa(kid), "/admin/people/"+itoa(kid)+"/login", url.Values{"action": {"allow"}, "attest13": {"1"}}, 200)
	if p, _ := st.GetPerson(kid); p.LoginAllowed {
		t.Fatal("login_allowed turned on for an under-13")
	}

	sam.post("/admin/people/"+itoa(pid), "/admin/people/"+itoa(pid)+"/link", url.Values{"user_id": {itoa(mel)}}, 200)
	if p, _ := st.GetPerson(pid); p.UserID != mel {
		t.Fatal("not linked")
	}
	sam.post("/admin/people/"+itoa(pid), "/admin/people/"+itoa(pid)+"/unlink", url.Values{}, 200)
	if p, _ := st.GetPerson(pid); p.UserID != 0 {
		t.Fatal("not unlinked")
	}
}

func TestClaimLinkFlow(t *testing.T) {
	sam, st := staffClient(t, Config{})
	st.CreateUser("zoe", "Zoe", "a long password", store.RoleMember)
	pid := mustPersonID(t, st, "Ann", "Adult")
	setBirthYear(t, st, pid, 1980)
	page := "/admin/people/" + itoa(pid)
	sam.post(page, page+"/login", url.Values{"action": {"allow"}}, 200)
	body := sam.post(page, page+"/login", url.Values{"action": {"claim"}}, 200)
	m := regexp.MustCompile(`http://[^/]+(/me/claim/[A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no claim link in the page:\n%s", body)
	}
	link := m[1]
	if strings.Contains(sam.get(page, 200), link) {
		t.Error("the claim link must be shown once only")
	}

	mel, zoe := sam.as("mel"), sam.as("zoe")
	if body := mel.get(link, 200); !strings.Contains(body, "Ann Adult") {
		t.Error("confirm page should name the profile")
	}
	mel.post(link, link, url.Values{}, 200)
	if p, _ := st.GetPerson(pid); p.UserID != userID(t, st, "mel") {
		t.Fatal("claim didn't link")
	}
	// One time only.
	zoe.get(link, 422)
	zoe.post("/me", link, url.Values{}, 422)
	if p, _ := st.GetPerson(pid); p.UserID != userID(t, st, "mel") {
		t.Error("a used link changed the profile")
	}
}

func TestSelfSignupHeldAndSamePerson(t *testing.T) {
	sam, st := staffClient(t, Config{})
	st.CreateUser("tyler", "Tyler", "a long password", store.RoleMember)
	hid, _ := st.CreateHousehold("The Secret Family", "", 0)
	ex, _ := st.CreatePerson(store.PersonInput{FirstName: "Tyler", LastName: "Goza", GoesBy: "Ty", Email: "ty@example.com"}, 0)
	st.PlaceMember(hid, ex, store.RoleHead, 0, 0)
	tyler := sam.as("tyler")

	// Off by default.
	tyler.post("/me", "/me/profile", url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "attest13": {"1"}}, 403)
	if body := tyler.get("/me", 200); strings.Contains(body, "Make my profile</button>") {
		t.Error("self sign-up should be off by default")
	}
	st.SetSetting("self_signup", "1")
	if body := tyler.get("/me", 200); !strings.Contains(body, "Make my profile") {
		t.Fatal("expected Make my profile")
	}
	before := countPeople(t, st)
	form := url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "phone": {"555-0100"}, "attest13": {"1"}}
	tyler.post("/me", "/me/profile", form, 200)
	if countPeople(t, st) != before+1 {
		t.Fatal("expected one new (held) profile")
	}
	me := tyler.get("/me", 200)
	if !strings.Contains(me, "Waiting for the office to check this.") {
		t.Error("the waiting message is missing")
	}
	// Never the matching entry: not its name, household or email.
	for _, secret := range []string{"Ty Goza", "The Secret Family", "ty@example.com", "matches"} {
		if strings.Contains(me, secret) {
			t.Errorf("/me shows %q, which would reveal the match", secret)
		}
	}
	// Other members don't see the held profile at all.
	held, _ := st.PersonByUser(userID(t, st, "tyler"))
	if !held.Pending || held.PendingReason != store.PendingNameMatch {
		t.Fatalf("held profile: %+v", held)
	}
	mel := sam.as("mel")
	mel.get("/people/"+itoa(held.ID), 404)

	// Staff see both side by side.
	na := sam.get("/admin/needs-action", 200)
	for _, want := range []string{"Ty Goza", "The Secret Family", "Same person as Ty Goza", "Different people", "Turn down", "Copy Phone"} {
		if !strings.Contains(na, want) {
			t.Errorf("Needs action is missing %q", want)
		}
	}
	total := countPeople(t, st)
	sam.post("/admin/needs-action", "/admin/needs-action/people/"+itoa(held.ID)+"/same",
		url.Values{"existing_id": {itoa(ex)}, "copy": {"phone"}, "attest13": {"1"}}, 200)
	if countPeople(t, st) != total-1 {
		t.Error("Same person must not leave two profiles")
	}
	p, _ := st.GetPerson(ex)
	if p.UserID != userID(t, st, "tyler") || p.Phone != "555-0100" {
		t.Errorf("existing profile after Same person: %+v", p)
	}
	me = tyler.get("/me", 200)
	if !strings.Contains(me, "Ty Goza") || strings.Contains(me, "Waiting for the office") {
		t.Error("after Same person /me should show the existing profile, no longer waiting")
	}
}

func TestDifferentPeopleAndTurnDown(t *testing.T) {
	sam, st := staffClient(t, Config{})
	st.CreateUser("tyler", "Tyler", "a long password", store.RoleMember)
	st.CreateUser("tina", "Tina", "a long password", store.RoleMember)
	st.SetSetting("self_signup", "1")
	ex := mustPersonID(t, st, "Tyler", "Goza")
	tyler, tina := sam.as("tyler"), sam.as("tina")

	tyler.post("/me", "/me/profile", url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "attest13": {"1"}}, 200)
	held, _ := st.PersonByUser(userID(t, st, "tyler"))
	sam.post("/admin/needs-action", "/admin/needs-action/people/"+itoa(held.ID)+"/different", url.Values{"suffix": {"Jr."}}, 200)
	h, _ := st.GetPerson(held.ID)
	if h.Pending || h.Suffix != "Jr." {
		t.Fatalf("after Different people: %+v", h)
	}
	if m, _ := st.NameMatches("Tyler", "Goza", "", "", held.ID); len(m) != 0 {
		t.Errorf("a different pair is flagged again: %v", m)
	}
	_ = ex

	// Turn down: the entry goes and a note shows once on /me.
	tina.post("/me", "/me/profile", url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "attest13": {"1"}}, 200)
	tp, _ := st.PersonByUser(userID(t, st, "tina"))
	sam.post("/admin/needs-action", "/admin/needs-action/people/"+itoa(tp.ID)+"/turn-down", url.Values{}, 200)
	if _, err := st.GetPerson(tp.ID); err == nil {
		t.Fatal("turned-down entry still exists")
	}
	me := tina.get("/me", 200)
	if !strings.Contains(me, "The office didn&#39;t accept the entry for Tyler Goza.") {
		t.Errorf("no notice on /me:\n%s", me)
	}
	if again := tina.get("/me", 200); strings.Contains(again, "didn&#39;t accept") {
		t.Error("the notice should show once")
	}
}

func TestFoundLaterUnder13ShowsOnNeedsAction(t *testing.T) {
	sam, st := staffClient(t, Config{})
	st.CreateUser("kid", "Kid", "a long password", store.RoleMember)
	pid := mustPersonID(t, st, "Kay", "Young")
	if err := st.LinkUser(pid, userID(t, st, "kid"), 0, true); err != nil {
		t.Fatal(err)
	}
	// SSO-style user: can't be switched off from here.
	st.DB.Exec(`UPDATE users SET sso_subject = 'um-9' WHERE username = 'kid'`)
	p := "/admin/people/" + itoa(pid)
	sam.post(p, p, url.Values{"first_name": {"Kay"}, "last_name": {"Young"}, "birth_year": {itoa(int64(time.Now().Year() - 7))}, "kind": {"member"}}, 200)
	if got, _ := st.GetPerson(pid); got.UserID != 0 {
		t.Fatal("still linked")
	}
	if body := sam.get("/admin/needs-action", 200); !strings.Contains(body, "um-9") || !strings.Contains(body, "Kay Young") {
		t.Error("the under-13 item is missing from Needs action")
	}
}
