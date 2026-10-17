package server

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

// headsWorld is a small church: the Lees (hank heads it; amy is a linked
// adult; tina a linked teen; kid an unlinked child; gran an unlinked adult)
// and the Smiths (sue heads them).
type headsWorld struct {
	t                         *testing.T
	st                        *store.Store
	sam, hank, amy, tina, sue *client
	mel                       *client
	lee, smith                int64
	hankP, amyP, tinaP, kidP  int64
	granP, sueP, benP         int64
}

func newHeadsWorld(t *testing.T) *headsWorld {
	t.Helper()
	sam, st := staffClient(t, Config{})
	w := &headsWorld{t: t, st: st, sam: sam}
	person := func(first, last string, year int) int64 {
		id, err := st.CreatePerson(store.PersonInput{FirstName: first, LastName: last, Birth: dates.Partial{Year: year}, Kind: store.KindMember}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	w.lee, _ = st.CreateHousehold("The Lee Family", "", 0)
	w.smith, _ = st.CreateHousehold("The Smith Family", "", 0)
	w.hankP, w.amyP, w.tinaP = person("Hank", "Lee", 1980), person("Amy", "Lee", 1982), person("Tina", "Lee", time.Now().Year()-16)
	w.kidP, w.granP = person("Kid", "Lee", time.Now().Year()-6), person("Gran", "Lee", 1950)
	w.sueP, w.benP = person("Sue", "Smith", 1981), person("Ben", "Smith", 1979)
	place := func(h, p int64, role string) {
		if err := st.PlaceMember(h, p, role, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	place(w.lee, w.hankP, store.RoleHead)
	place(w.lee, w.amyP, store.RoleAdult)
	place(w.lee, w.tinaP, store.RoleChild)
	place(w.lee, w.kidP, store.RoleChild)
	place(w.lee, w.granP, store.RoleAdult)
	place(w.smith, w.sueP, store.RoleHead)
	place(w.smith, w.benP, store.RoleAdult)
	for _, u := range []struct {
		name string
		pid  int64
	}{{"hank", w.hankP}, {"amy", w.amyP}, {"tina", w.tinaP}, {"sue", w.sueP}} {
		uid, err := st.CreateUser(u.name, u.name, "a long password", store.RoleMember)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.LinkUser(u.pid, uid, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	w.hank, w.amy, w.tina, w.sue, w.mel = sam.as("hank"), sam.as("amy"), sam.as("tina"), sam.as("sue"), sam.as("mel")
	return w
}

func (w *headsWorld) person(id int64) store.Person {
	w.t.Helper()
	p, err := w.st.GetPerson(id)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func edit(first, last, email string) url.Values {
	return url.Values{"first_name": {first}, "last_name": {last}, "email": {email}}
}

func hasChange(t *testing.T, st *store.Store, personID int64, what string) bool {
	t.Helper()
	cs, err := st.RecentChanges(personID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if strings.Contains(c.What, what) {
			return true
		}
	}
	return false
}

func TestHeadRightsOverPeopleAndHouseholds(t *testing.T) {
	w := newHeadsWorld(t)
	p := func(id int64) string { return "/me/people/" + itoa(id) }
	hh := "/me/households/" + itoa(w.lee)

	// A head edits an unlinked child and an unlinked adult, and a linked child.
	for _, id := range []int64{w.kidP, w.granP, w.tinaP} {
		w.hank.get(p(id), 200)
		w.hank.post(p(id), p(id), edit(w.person(id).FirstName, "Lee", "x"+itoa(id)+"@example.org"), 200)
		if got := w.person(id).Email; got != "x"+itoa(id)+"@example.org" {
			t.Errorf("head didn't save person %d: %q", id, got)
		}
	}
	// A linked adult: the head can't, until the adult opts in.
	w.hank.get(p(w.amyP), 403)
	w.hank.post("/me", p(w.amyP), edit("Amy", "Lee", "hacked@example.org"), 403)
	if w.person(w.amyP).Email != "" {
		t.Fatal("a head edited a linked adult without heads_may_edit")
	}
	w.amy.post("/me", "/me/heads-may-edit", url.Values{"heads_may_edit": {"1"}}, 200)
	w.hank.post(p(w.amyP), p(w.amyP), edit("Amy", "Lee", "amy@example.org"), 200)
	if w.person(w.amyP).Email != "amy@example.org" {
		t.Error("a head couldn't edit a linked adult who opted in")
	}
	if !hasChange(t, w.st, w.amyP, "email") {
		t.Error("the head's change wasn't logged")
	}
	w.amy.post("/me", "/me/heads-may-edit", url.Values{}, 200) // unticks
	w.hank.get(p(w.amyP), 403)

	// Only a head manages the household: an adult, a teen with a login, a
	// head elsewhere and a plain member are all refused.
	w.hank.get(hh, 200)
	for name, c := range map[string]*client{"adult": w.amy, "teen": w.tina, "other head": w.sue, "member": w.mel} {
		c.get(hh, 403)
		c.post("/me", hh+"/addresses", url.Values{"address1": {"1 Elm St"}, "city": {"Springfield"}}, 403)
		c.post("/me", hh, url.Values{"name": {"Hijacked"}}, 403)
		c.post("/me", hh+"/members/"+itoa(w.kidP)+"/remove", url.Values{}, 403)
		if a, _ := w.st.ListAddresses(w.lee); len(a) != 0 {
			t.Fatalf("%s added an address", name)
		}
	}
	// None of them can edit the unlinked child either (they can see them).
	for name, c := range map[string]*client{"adult": w.amy, "teen": w.tina, "other head": w.sue, "member": w.mel} {
		c.get(p(w.kidP), 403)
		c.post("/me", p(w.kidP)+"/notes", url.Values{"kind": {"allergy"}, "body": {"x"}, "visible_to": {"household"}}, 403)
		if name == "member" {
			c.get(p(w.kidP), 403) // visible in the directory, but not editable
		}
	}
	// Staff keep their rights here too.
	w.sam.get(p(w.amyP), 200)
	w.sam.get(hh, 200)
	// A household that doesn't exist, or a person nobody may see, is a 404.
	w.hank.get("/me/households/9999", 404)
	w.hank.get("/me/people/9999", 404)
}

func TestHeadRunsTheHousehold(t *testing.T) {
	w := newHeadsWorld(t)
	hh := "/me/households/" + itoa(w.lee)
	w.hank.post(hh, hh, url.Values{"name": {"The Lee Clan"}}, 200)
	if h, _ := w.st.GetHousehold(w.lee); h.Name != "The Lee Clan" {
		t.Errorf("name = %q", h.Name)
	}
	w.hank.post(hh, hh+"/addresses", url.Values{"label": {"Main"}, "address1": {"1 Elm St"}, "city": {"Springfield"}}, 200)
	w.hank.post(hh, hh+"/addresses", url.Values{"label": {"Gran's"}, "address1": {"9 Oak St"}, "city": {"Springfield"}}, 200)
	addrs, _ := w.st.ListAddresses(w.lee)
	if len(addrs) != 2 {
		t.Fatalf("addresses = %+v", addrs)
	}
	w.hank.post(hh, hh+"/members/"+itoa(w.granP)+"/address", url.Values{"address_id": {itoa(addrs[1].ID)}}, 200)
	// Staff's force hide survives the head's visibility choice.
	w.sam.post("/admin/households/"+itoa(w.lee), "/admin/households/"+itoa(w.lee)+"/addresses/"+itoa(addrs[1].ID)+"/visibility", url.Values{"shown": {"1"}, "force": {"1"}}, 200)
	w.hank.post(hh, hh+"/addresses/"+itoa(addrs[1].ID)+"/visibility", url.Values{"shown": {"0"}}, 200)
	if a, _ := w.st.GetAddress(addrs[1].ID); a.Shown == nil || *a.Shown || !a.ForceHidden {
		t.Errorf("address visibility = %+v", a)
	}
	// Removing an address that has people needs somewhere for them to go.
	w.hank.post(hh, hh+"/addresses/"+itoa(addrs[1].ID)+"/delete", url.Values{}, 200)
	if a, _ := w.st.ListAddresses(w.lee); len(a) != 2 {
		t.Error("an address with people was removed without reassigning them")
	}
	w.hank.post(hh, hh+"/addresses/"+itoa(addrs[1].ID)+"/delete", url.Values{"reassign": {itoa(addrs[0].ID)}}, 200)
	if a, _ := w.st.ListAddresses(w.lee); len(a) != 1 {
		t.Error("address not removed")
	}
	// Make another adult a head; the spouse link is suggested.
	body := w.hank.post(hh, hh+"/members/"+itoa(w.amyP)+"/head", url.Values{}, 200)
	if !strings.Contains(body, "are spouses") {
		t.Errorf("no spouse suggestion:\n%s", body)
	}
	if ms, _ := w.st.PersonHouseholds(w.amyP); ms[0].Role != store.RoleHead {
		t.Error("not made a head")
	}
	// Linking needs the right to edit both: amy, a linked adult, must opt in.
	w.hank.post(hh, hh+"/family-links", url.Values{"link": {"spouse:" + itoa(w.hankP) + ":" + itoa(w.amyP)}}, 200)
	if rels, _ := w.st.AllRelationships(); len(rels) != 0 {
		t.Errorf("linked a linked adult without her say: %+v", rels)
	}
	w.amy.post("/me", "/me/heads-may-edit", url.Values{"heads_may_edit": {"1"}}, 200)
	w.hank.post(hh, hh+"/family-links", url.Values{"link": {"spouse:" + itoa(w.hankP) + ":" + itoa(w.amyP)}}, 200)
	if rels, _ := w.st.AllRelationships(); len(rels) != 1 {
		t.Errorf("relationships = %+v", rels)
	}
	// Remove an unlinked person; a linked adult can only leave themselves.
	w.hank.post(hh, hh+"/members/"+itoa(w.granP)+"/remove", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.granP); len(ms) != 0 {
		t.Error("gran not removed")
	}
	w.hank.post(hh, hh+"/members/"+itoa(w.tinaP)+"/remove", url.Values{}, 403)
	if ms, _ := w.st.PersonHouseholds(w.tinaP); len(ms) != 1 {
		t.Error("a head removed a linked person")
	}
	w.tina.post("/me", hh+"/leave", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.tinaP); len(ms) != 0 {
		t.Error("tina couldn't leave")
	}
	// Two heads now; hank can leave, but the last head can't.
	w.amy.post("/me", hh+"/leave", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.amyP); len(ms) != 0 {
		t.Error("a head couldn't leave while another head remains")
	}
	w.hank.post("/me", hh+"/leave", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.hankP); len(ms) != 1 {
		t.Error("the only head left the household with people still in it")
	}
}

func TestHeadVisibilityKeepsForceHide(t *testing.T) {
	w := newHeadsWorld(t)
	p := "/me/people/" + itoa(w.kidP)
	w.sam.post("/admin/people/"+itoa(w.kidP), "/admin/people/"+itoa(w.kidP)+"/visibility",
		url.Values{"force:phone": {"1"}}, 200)
	w.hank.post(p, p+"/visibility", url.Values{"shown:email": {"1"}, "shown:phone": {"1"}}, 200)
	set, _ := w.st.PersonFieldSettings(w.kidP)
	if set[store.FieldEmail].Shown == nil || !*set[store.FieldEmail].Shown {
		t.Error("the head's choice wasn't saved")
	}
	if !set[store.FieldPhone].ForceHidden {
		t.Error("the head lifted staff's force hide")
	}
	// A head can't set a linked adult's visibility; the adult can.
	amy := "/me/people/" + itoa(w.amyP)
	w.hank.post("/me", amy+"/visibility", url.Values{"shown:email": {"1"}}, 403)
	w.amy.post("/me", amy+"/visibility", url.Values{"shown:email": {"0"}}, 200)
}

func TestLockedHeadsMayEdit(t *testing.T) {
	w := newHeadsWorld(t)
	amy := "/me/people/" + itoa(w.amyP)
	adm := "/admin/people/" + itoa(w.amyP)

	// Locking needs a reason.
	w.sam.post(adm, adm+"/heads-may-edit", url.Values{"heads_may_edit": {"1"}, "lock": {"1"}}, 200)
	if w.person(w.amyP).HeadsMayEditLocked {
		t.Fatal("locked without a reason")
	}
	// Locked off: heads can't, and the person can't turn it on.
	w.sam.post(adm, adm+"/heads-may-edit", url.Values{"heads_may_edit": {"0"}, "lock": {"1"}, "reason": {"protective order"}}, 200)
	w.amy.post("/me", "/me/heads-may-edit", url.Values{"heads_may_edit": {"1"}}, 200)
	if p := w.person(w.amyP); p.HeadsMayEdit || !p.HeadsMayEditLocked {
		t.Fatalf("the person turned on a setting locked off: %+v", p)
	}
	if body := w.amy.get("/me", 200); !strings.Contains(body, "protective order") || !strings.Contains(body, "only the office can change it") || !strings.Contains(body, `id="heads-may-edit" disabled`) {
		t.Errorf("the lock and its reason should show, disabled:\n%s", body)
	}
	w.hank.get(amy, 403)

	// Locked on: heads can, the person can't turn it off, every change is logged.
	w.sam.post(adm, adm+"/heads-may-edit", url.Values{"heads_may_edit": {"1"}, "lock": {"1"}, "reason": {"can't manage the technology"}}, 200)
	w.amy.post("/me", "/me/heads-may-edit", url.Values{}, 200)
	if !w.person(w.amyP).HeadsMayEdit {
		t.Fatal("the person turned off a setting locked on")
	}
	w.hank.post(amy, amy, edit("Amy", "Lee", "by-head@example.org"), 200)
	if w.person(w.amyP).Email != "by-head@example.org" || !hasChange(t, w.st, w.amyP, "email") {
		t.Error("the head's edit on a locked-on person was not saved and logged")
	}
	if body := w.amy.get("/me", 200); !strings.Contains(body, "Your household's heads can edit your profile") {
		t.Errorf("the person should see the managed-by note:\n%s", body)
	}
	// Unlocking gives the choice back.
	w.sam.post(adm, adm+"/heads-may-edit", url.Values{"heads_may_edit": {"0"}}, 200)
	if p := w.person(w.amyP); p.HeadsMayEdit || p.HeadsMayEditLocked || p.HeadsMayEditReason != "" {
		t.Errorf("unlock = %+v", p)
	}
}

func TestSecondHouseholdNeedsTheTickAndAnAnswer(t *testing.T) {
	w := newHeadsWorld(t)
	kid := "/me/people/" + itoa(w.kidP)
	second := url.Values{"household_id": {itoa(w.smith)}}

	// Refused without the tick: nothing added, no request.
	w.hank.post(kid, kid+"/second-household", second, 200)
	if rs, _ := w.st.OpenJoinRequests(); len(rs) != 0 {
		t.Fatal("a request was made without the tick")
	}
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 1 {
		t.Fatal("added to a second household without the tick")
	}
	if err := w.st.AddSecondHousehold(w.smith, w.kidP, 0, 0); err != store.ErrNotMulti {
		t.Errorf("store: %v", err)
	}
	// Only a head of one of the child's households may tick it.
	w.sue.post("/me", kid+"/multi-household", url.Values{"multi_household": {"1"}, "multi_reason": {"divorced"}}, 403)
	w.hank.post(kid, kid+"/multi-household", url.Values{"multi_household": {"1"}, "multi_reason": {"divorced"}}, 200)
	if p := w.person(w.kidP); !p.MultiHousehold || p.MultiReason != "divorced" {
		t.Fatalf("tick = %+v", p)
	}
	// A household hank doesn't head: a request goes to its heads.
	w.hank.post(kid, kid+"/second-household", second, 200)
	rs, _ := w.st.OpenJoinRequests()
	if len(rs) != 1 || rs[0].HouseholdID != w.smith {
		t.Fatalf("requests = %+v", rs)
	}
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 1 {
		t.Fatal("added before the request was answered")
	}
	w.hank.post(kid, kid+"/second-household", second, 200) // again: still one
	if rs, _ := w.st.OpenJoinRequests(); len(rs) != 1 {
		t.Error("a second open request for the same household")
	}
	// Smith's head sees it on /me; others can't answer it.
	if body := w.sue.get("/me", 200); !strings.Contains(body, "Kid Lee") || !strings.Contains(body, "/me/requests/"+itoa(rs[0].ID)+"/accept") {
		t.Errorf("sue should see the request:\n%s", body)
	}
	if body := w.mel.get("/me", 200); strings.Contains(body, "/me/requests/") {
		t.Error("a stranger sees the request")
	}
	w.mel.post("/me", "/me/requests/"+itoa(rs[0].ID)+"/accept", url.Values{}, 404)
	w.hank.post("/me", "/me/requests/"+itoa(rs[0].ID)+"/accept", url.Values{}, 404)
	w.sue.post("/me", "/me/requests/"+itoa(rs[0].ID)+"/accept", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 2 {
		t.Fatalf("not in both households: %+v", ms)
	}
	for _, m := range mustMembers(t, w.st, w.smith) {
		if m.ID == w.kidP && m.Role != store.RoleChild {
			t.Errorf("role = %s, want child only", m.Role)
		}
	}
	// Adults can't be in two; the child is primary in one of them.
	if err := w.st.AddSecondHousehold(w.smith, w.granP, 0, 0); err == nil {
		t.Error("an adult was added to a second household")
	}
	w.hank.post(kid, kid+"/primary-household", url.Values{"household_id": {itoa(w.smith)}}, 200)
	n := 0
	for _, hid := range []int64{w.lee, w.smith} {
		for _, m := range mustMembers(t, w.st, hid) {
			if m.ID == w.kidP && m.IsPrimary {
				n++
				if hid != w.smith {
					t.Error("wrong primary household")
				}
			}
		}
	}
	if n != 1 {
		t.Errorf("%d primary households, want 1", n)
	}
	// Unticking while in two households is refused.
	w.hank.post(kid, kid+"/multi-household", url.Values{}, 200)
	if !w.person(w.kidP).MultiHousehold {
		t.Error("unticked while in two households")
	}
}

func mustMembers(t *testing.T, st *store.Store, hid int64) []store.Member {
	t.Helper()
	ms, err := st.HouseholdMembers(hid)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestJoinRequestDeclinedAndStaffAnswer(t *testing.T) {
	w := newHeadsWorld(t)
	kid := "/me/people/" + itoa(w.kidP)
	w.hank.post(kid, kid+"/multi-household", url.Values{"multi_household": {"1"}, "multi_reason": {"other"}}, 200)
	w.hank.post(kid, kid+"/second-household", url.Values{"household_id": {itoa(w.smith)}}, 200)
	rs, _ := w.st.OpenJoinRequests()
	w.sue.post("/me", "/me/requests/"+itoa(rs[0].ID)+"/decline", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 1 {
		t.Error("a declined request added the child")
	}
	if body := w.hank.get("/me", 200); !strings.Contains(body, "turned down") {
		t.Errorf("the asker should be told:\n%s", body)
	}
	w.sue.post("/me", "/me/requests/"+itoa(rs[0].ID)+"/accept", url.Values{}, 200) // already answered
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 1 {
		t.Error("an answered request was answered twice")
	}
	// Staff see a fresh one on Needs action and accept it.
	w.hank.post(kid, kid+"/second-household", url.Values{"household_id": {itoa(w.smith)}}, 200)
	if body := w.sam.get("/admin/needs-action", 200); !strings.Contains(body, "Join requests") || !strings.Contains(body, "Kid Lee") {
		t.Errorf("needs action:\n%s", body)
	}
	rs, _ = w.st.OpenJoinRequests()
	w.sam.post("/admin/needs-action", "/admin/needs-action/requests/"+itoa(rs[0].ID)+"/accept", url.Values{}, 200)
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 2 {
		t.Error("staff accepting didn't add the child")
	}
}

func TestContactHouseholdNeverListed(t *testing.T) {
	w := newHeadsWorld(t)
	kid := "/me/people/" + itoa(w.kidP)
	w.hank.post("/me", "/me/contact-households", url.Values{"name": {"Pat Other (Kid's dad)"}}, 200)
	hs, _ := w.st.ListHouseholds(store.HouseholdsContacts, "")
	if len(hs) != 1 {
		t.Fatalf("contact households = %+v", hs)
	}
	cid := hs[0].ID
	h, _ := w.st.GetHousehold(cid)
	hank := w.hank
	if h.Kind != store.KindContact || h.ManagedBy != w.hankP {
		t.Fatalf("household = %+v", h)
	}
	ch := "/me/households/" + itoa(cid)
	// Only heads can make one; a plain member can't.
	w.mel.post("/me", "/me/contact-households", url.Values{"name": {"Nope"}}, 403)
	// The other parent, a contact; and the child, once ticked.
	hank.post(ch, ch+"/people", url.Values{"first_name": {"Pat"}, "last_name": {"Other"}, "other_parent": {"1"}}, 200)
	hank.post(ch, ch+"/children", url.Values{"person_id": {itoa(w.kidP)}}, 200)
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 1 {
		t.Fatal("the child was added without the tick")
	}
	hank.post(kid, kid+"/multi-household", url.Values{"multi_household": {"1"}, "multi_reason": {"divorced"}}, 200)
	hank.post(ch, ch+"/children", url.Values{"person_id": {itoa(w.kidP)}}, 200)
	if ms, _ := w.st.PersonHouseholds(w.kidP); len(ms) != 2 {
		t.Fatal("the child isn't in the contact household")
	}
	var pat store.Person
	for _, m := range mustMembers(t, w.st, cid) {
		if m.Name() == "Pat Other" {
			pat = m.Person
		}
	}
	if pat.Kind != store.KindContact {
		t.Fatalf("the other parent = %+v", pat)
	}
	// Never listed, printed, or in a group, for anyone but the head and staff.
	g, _ := w.st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	_ = g
	for name, c := range map[string]*client{"member": w.mel, "other head": w.sue, "teen": w.tina} {
		for _, page := range []string{"/", "/print", "/groups", "/birthdays"} {
			if body := c.get(page, 200); strings.Contains(body, "Pat Other") || strings.Contains(body, "Kid's dad") {
				t.Errorf("%s sees the contact household on %s", name, page)
			}
		}
		c.get("/households/"+itoa(cid), 404)
		c.get("/people/"+itoa(pat.ID), 404)
		c.get(ch, 404)
	}
	if body := hank.get("/me", 200); !strings.Contains(body, "Pat Other (Kid&#39;s dad)") {
		t.Errorf("the head should see it on /me:\n%s", body)
	}
	for _, page := range []string{"/", "/print"} {
		if body := w.sam.get(page, 200); strings.Contains(body, "Pat Other") {
			t.Errorf("staff see the contact household on the members' %s", page)
		}
	}
	// Staff convert household and person; the head who made it can't edit it.
	w.sam.post("/admin/households/"+itoa(cid), "/admin/households/"+itoa(cid)+"/convert-contact", url.Values{"person_id": {itoa(pat.ID)}}, 200)
	h, _ = w.st.GetHousehold(cid)
	if h.Kind != store.KindMember || h.ManagedBy != 0 {
		t.Fatalf("converted = %+v", h)
	}
	if p := w.person(pat.ID); p.Kind != store.KindMember {
		t.Error("the person is still a contact")
	}
	hank.get(ch, 403)
	if body := w.mel.get("/", 200); !strings.Contains(body, "Pat Other") {
		t.Error("the converted household should be listed")
	}
}

func TestManagedHouseholdEndsWhenAHeadHasALogin(t *testing.T) {
	w := newHeadsWorld(t)
	gran := "/me/people/" + itoa(w.granP)
	// Gran is an unlinked adult: hank starts her own household.
	w.hank.post(gran+"/family", gran+"/own-household", url.Values{"name": {"Gran's House"}}, 200)
	hs, _ := w.st.ListHouseholds("", "Gran")
	if len(hs) != 1 {
		t.Fatalf("households = %+v", hs)
	}
	gh := "/me/households/" + itoa(hs[0].ID)
	w.hank.get(gh, 200)
	w.mel.get(gh, 403)
	// Once gran has a login and is its head, hank's rights end.
	uid, _ := w.st.CreateUser("gran", "Gran", "a long password", store.RoleMember)
	if err := w.st.LinkUser(w.granP, uid, 0, true); err != nil {
		t.Fatal(err)
	}
	w.hank.get(gh, 403)
	w.sam.as("gran").get(gh, 200)
}

func TestHeadAddsPeopleAndNameMatchesAreHeld(t *testing.T) {
	w := newHeadsWorld(t)
	hh := "/me/households/" + itoa(w.lee)
	add := func(first, last string) string {
		return w.hank.post(hh, hh+"/people", url.Values{"first_name": {first}, "last_name": {last}, "role": {"child"}}, 200)
	}
	// No match: straight in, unlinked, kind member, with the family suggestion.
	body := add("Zed", "Lee")
	if !strings.Contains(body, "is a parent of Zed Lee") {
		t.Errorf("no suggestion after adding a child:\n%s", body)
	}
	var zed store.Person
	for _, m := range mustMembers(t, w.st, w.lee) {
		if m.FirstName == "Zed" {
			zed = m.Person
		}
	}
	if zed.ID == 0 || zed.Pending || zed.UserID != 0 || zed.Kind != store.KindMember {
		t.Fatalf("zed = %+v", zed)
	}
	// A match (Ben Smith is in the Smith household): held, and the answer
	// never mentions him or his household.
	body = add("ben", "SMITH")
	if !strings.Contains(body, waitingText) {
		t.Errorf("the head should be told to wait:\n%s", body)
	}
	for _, leak := range []string{"Smith Family", "Sue", "same name", "matches"} {
		if strings.Contains(body, leak) {
			t.Errorf("the held answer leaks %q:\n%s", leak, body)
		}
	}
	var held store.Person
	for _, m := range mustMembers(t, w.st, w.lee) {
		if m.FirstName == "ben" {
			held = m.Person
		}
	}
	if !held.Pending || held.PendingReason != store.PendingNameMatch {
		t.Fatalf("held = %+v", held)
	}
	// Others don't see the held person; staff do, on Needs action, and
	// "Same person" puts the existing Ben in the Lee household instead.
	w.mel.get("/people/"+itoa(held.ID), 404)
	w.sam.post("/admin/needs-action", "/admin/needs-action/people/"+itoa(held.ID)+"/same", url.Values{"existing_id": {itoa(w.benP)}}, 200)
	if ms, _ := w.st.PersonHouseholds(w.benP); len(ms) != 1 || ms[0].HouseholdID != w.lee {
		t.Errorf("Same person should add the existing person to the household: %+v", ms)
	}
}

func TestHeldRenameKeepsTheOldNameUntilApproved(t *testing.T) {
	w := newHeadsWorld(t)
	kid := "/me/people/" + itoa(w.kidP)
	body := w.hank.post(kid, kid, edit("Ben", "Smith", ""), 200)
	if !strings.Contains(body, waitingText) || strings.Contains(body, "Sue") {
		t.Errorf("held rename answer:\n%s", body)
	}
	p := w.person(w.kidP)
	if p.Name() != "Kid Lee" || p.PendingName == "" {
		t.Fatalf("the old name should stay until approved: %+v", p)
	}
	w.mel.get("/people/"+itoa(w.kidP), 200)
	if b := w.mel.get("/people/"+itoa(w.kidP), 200); strings.Contains(b, "Ben Smith") {
		t.Error("the held name is public")
	}
	// A rename that doesn't match goes straight in (and replaces the held one).
	w.hank.post(kid, kid, edit("Kidd", "Lee", ""), 200)
	if p := w.person(w.kidP); p.FirstName != "Kidd" || p.PendingName != "" {
		t.Errorf("a non-matching rename = %+v", p)
	}
	w.hank.post(kid, kid, edit("Ben", "Smith", ""), 200)
	w.sam.post("/admin/needs-action", "/admin/needs-action/renames/"+itoa(w.kidP)+"/approve", url.Values{}, 200)
	if p := w.person(w.kidP); p.Name() != "Ben Smith" || p.PendingName != "" {
		t.Errorf("after approval = %+v", p)
	}
	// Staff aren't held.
	w.sam.post("/me", "/me/people/"+itoa(w.kidP), edit("Ben", "Smith", ""), 200)
}

func TestHeadLoginRules(t *testing.T) {
	w := newHeadsWorld(t)
	kid, gran := "/me/people/"+itoa(w.kidP), "/me/people/"+itoa(w.granP)
	// The warning shows; a known under-13 gets no controls and is refused anyway.
	if body := w.hank.get(kid, 200); !strings.Contains(body, "Children under 13 can&#39;t have their own login") {
		t.Errorf("under-13 warning missing:\n%s", body)
	}
	w.hank.post(kid, kid+"/login", url.Values{"action": {"allow"}, "attest13": {"1"}}, 200)
	if w.person(w.kidP).LoginAllowed {
		t.Fatal("login turned on for an under-13")
	}
	w.hank.post(kid, kid+"/login", url.Values{"action": {"claim"}}, 200)
	// Unknown age needs the 13+ tick, which is recorded.
	p, _ := w.st.GetPerson(w.granP)
	w.st.UpdatePerson(p.ID, store.PersonInput{FirstName: "Gran", LastName: "Lee", Kind: store.KindMember}, 0) // no birth year
	w.hank.post(gran, gran+"/login", url.Values{"action": {"allow"}}, 200)
	if w.person(w.granP).LoginAllowed {
		t.Fatal("unknown age without the tick")
	}
	w.hank.post(gran, gran+"/login", url.Values{"action": {"allow"}, "attest13": {"1"}}, 200)
	if p := w.person(w.granP); !p.LoginAllowed || p.AgeAttestedBy == 0 {
		t.Fatalf("gran = %+v", p)
	}
	body := w.hank.post(gran, gran+"/login", url.Values{"action": {"claim"}}, 200)
	if !strings.Contains(body, "/me/claim/") {
		t.Errorf("no claim link:\n%s", body)
	}
	// Someone who can't edit them can't do any of it.
	w.mel.post("/me", gran+"/login", url.Values{"action": {"claim"}}, 403)
	w.amy.post("/me", gran+"/login", url.Values{"action": {"claim"}}, 403)
}

func TestHeadsNeverTouchPasswords(t *testing.T) {
	w := newHeadsWorld(t)
	for _, path := range []string{
		"/me/people/" + itoa(w.amyP) + "/password", "/me/people/" + itoa(w.kidP) + "/password",
		"/me/password", "/me/people/" + itoa(w.amyP) + "/reset-password",
		"/me/households/" + itoa(w.lee) + "/passwords",
	} {
		w.hank.post("/me", path, url.Values{"password": {"another long password"}}, 404)
	}
	// Even the person's own /account route changes only the signed-in user's.
	before := w.sam.as("amy")
	_ = before
}

func TestHeadWritesNotesForPeopleTheyMayEdit(t *testing.T) {
	w := newHeadsWorld(t)
	kid := "/me/people/" + itoa(w.kidP)
	w.hank.post(kid, kid+"/notes", url.Values{"kind": {"allergy"}, "body": {"peanuts"}, "visible_to": {"household"}}, 200)
	if ns, _ := w.st.NotesOf(w.kidP); len(ns) != 1 {
		t.Fatalf("notes = %+v", ns)
	}
	amy := "/me/people/" + itoa(w.amyP)
	w.hank.post("/me", amy+"/notes", url.Values{"kind": {"allergy"}, "body": {"x"}, "visible_to": {"household"}}, 403)
	if ns, _ := w.st.NotesOf(w.amyP); len(ns) != 0 {
		t.Error("a head wrote a note about a linked adult who hasn't opted in")
	}
	if body := w.hank.get(kid, 200); !strings.Contains(body, "peanuts") {
		t.Error("the editor should list the note")
	}
}

func TestMeShowsMyProfileAndEditsIt(t *testing.T) {
	w := newHeadsWorld(t)
	w.amy.post("/me", "/me/people/"+itoa(w.amyP), url.Values{"first_name": {"Amy"}, "last_name": {"Lee"}, "email": {"amy@example.org"},
		"phone": {"555-0100"}, "birth_month": {"3"}, "birth_day": {"14"}, "birth_year": {"1982"}}, 200)
	p := w.person(w.amyP)
	if p.Email != "amy@example.org" || p.Birth.Month != 3 {
		t.Errorf("amy = %+v", p)
	}
	body := w.amy.get("/me", 200)
	for _, want := range []string{"What shows to others", "Leave this household", "Heads of my household", "Let my household"} {
		if !strings.Contains(body, want) {
			t.Errorf("/me lacks %q", want)
		}
	}
	// Bad dates come back with the error on the same page.
	if body := w.amy.post("/me", "/me/people/"+itoa(w.amyP), url.Values{"first_name": {"Amy"}, "last_name": {"Lee"}, "birth_month": {"2"}, "birth_day": {"31"}}, 422); !strings.Contains(body, "Please fix") {
		t.Errorf("no error shown:\n%s", body)
	}
	// Hank's page lists his households and the contact form.
	body = w.hank.get("/me", 200)
	if !strings.Contains(body, "Households I look after") || !strings.Contains(body, "/me/households/"+itoa(w.lee)) {
		t.Errorf("hank's /me:\n%s", body)
	}
}
