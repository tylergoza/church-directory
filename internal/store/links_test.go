package store

import (
	"errors"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) }

func TestAgeOnYoungestAgeRule(t *testing.T) {
	cases := []struct {
		name  string
		birth dates.Partial
		today time.Time
		age   int
		known bool
		under bool
	}{
		{"no year is unknown", dates.Partial{Month: 3, Day: 4}, day(2026, 10, 16), 0, false, false},
		{"empty is unknown", dates.Partial{}, day(2026, 10, 16), 0, false, false},
		// Year only: born Dec 31, so 13 only on Dec 31 of year+13.
		{"year only, day before 13th birthday", dates.Partial{Year: 2013}, day(2026, 12, 30), 12, true, true},
		{"year only, on 13th birthday", dates.Partial{Year: 2013}, day(2026, 12, 31), 13, true, false},
		{"year only, same year", dates.Partial{Year: 2026}, day(2026, 10, 16), 0, true, true},
		{"year only, old", dates.Partial{Year: 1980}, day(2026, 10, 16), 45, true, false},
		// Year and month: last day of the month.
		{"year+month, before the end of the month", dates.Partial{Year: 2013, Month: 10}, day(2026, 10, 30), 12, true, true},
		{"year+month, last day", dates.Partial{Year: 2013, Month: 10}, day(2026, 10, 31), 13, true, false},
		{"year+Feb in a leap year", dates.Partial{Year: 2012, Month: 2}, day(2025, 2, 28), 12, true, true},
		{"year+Feb in a leap year, 29th", dates.Partial{Year: 2012, Month: 2}, day(2025, 3, 1), 13, true, false},
		// Full date.
		{"full date, the day before", dates.Partial{Year: 2013, Month: 10, Day: 17}, day(2026, 10, 16), 12, true, true},
		{"full date, the day", dates.Partial{Year: 2013, Month: 10, Day: 17}, day(2026, 10, 17), 13, true, false},
		{"future birth year never goes negative", dates.Partial{Year: 2030}, day(2026, 10, 16), 0, true, true},
	}
	for _, c := range cases {
		age, known := AgeOn(c.birth, c.today)
		if age != c.age || known != c.known {
			t.Errorf("%s: AgeOn = %d, %v; want %d, %v", c.name, age, known, c.age, c.known)
		}
		if got := KnownUnder13(c.birth, c.today); got != c.under {
			t.Errorf("%s: KnownUnder13 = %v, want %v", c.name, got, c.under)
		}
	}
}

func setBirth(t *testing.T, st *Store, id int64, birth dates.Partial) error {
	t.Helper()
	p, err := st.GetPerson(id)
	if err != nil {
		t.Fatal(err)
	}
	return st.UpdatePerson(id, PersonInput{FirstName: p.FirstName, LastName: p.LastName, Birth: birth, Kind: KindMember}, 0)
}

func mustUser(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	id, err := st.CreateUser(name, name, "a long password", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLinkRules(t *testing.T) {
	st := openTest(t)
	kid := mustPerson(t, st, "Kid", "Goza", "", "")
	adult := mustPerson(t, st, "Ann", "Goza", "", "")
	u1, u2 := mustUser(t, st, "u1"), mustUser(t, st, "u2")
	thisYear := time.Now().Year()
	setBirth(t, st, kid, dates.Partial{Year: thisYear - 5})

	if err := st.LinkUser(kid, u1, 0, true); !errors.Is(err, ErrUnder13) {
		t.Errorf("link of a known under-13 = %v, want ErrUnder13 (the tick can't override)", err)
	}
	// Unknown age needs the tick, which is recorded.
	if err := st.LinkUser(adult, u1, 0, false); !errors.Is(err, ErrNeedsAttest) {
		t.Errorf("unknown age without the tick = %v, want ErrNeedsAttest", err)
	}
	if err := st.LinkUser(adult, u1, u2, true); err != nil {
		t.Fatal(err)
	}
	p, _ := st.GetPerson(adult)
	if p.UserID != u1 || !p.LoginAllowed || p.AgeAttestedBy != u2 || p.AgeAttestedAt == "" {
		t.Errorf("after link: %+v", p)
	}
	// One profile per user; one user per profile.
	other := mustPerson(t, st, "Bob", "Goza", "", "")
	setBirth(t, st, other, dates.Partial{Year: 1970})
	if err := st.LinkUser(other, u1, 0, false); !errors.Is(err, ErrUserHasProfile) {
		t.Errorf("second profile for a user = %v", err)
	}
	if err := st.LinkUser(adult, u2, 0, true); !errors.Is(err, ErrAlreadyLinked) {
		t.Errorf("second user for a profile = %v", err)
	}
	// Known adult needs no tick; unlink keeps both.
	if err := st.UnlinkUser(adult, 0); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.GetPerson(adult); p.UserID != 0 || p.LoginAllowed {
		t.Errorf("after unlink: %+v", p)
	}
	if _, err := st.GetUser(u1); err != nil {
		t.Error("unlink removed the user")
	}
	if err := st.LinkUser(other, u1, 0, false); err != nil {
		t.Errorf("known adult link: %v", err)
	}
}

func TestLoginAllowedAndClaims(t *testing.T) {
	st := openTest(t)
	thisYear := time.Now().Year()
	kid := mustPerson(t, st, "Kid", "Goza", "", "")
	setBirth(t, st, kid, dates.Partial{Year: thisYear - 5})
	if err := st.SetLoginAllowed(kid, true, true, 0); !errors.Is(err, ErrUnder13) {
		t.Errorf("login_allowed for an under-13 = %v", err)
	}
	if _, err := st.CreateClaim(kid, 0); !errors.Is(err, ErrUnder13) {
		t.Errorf("claim for an under-13 = %v", err)
	}

	ann := mustPerson(t, st, "Ann", "Goza", "", "")
	if _, err := st.CreateClaim(ann, 0); !errors.Is(err, ErrNoLogin) {
		t.Errorf("claim without login_allowed = %v", err)
	}
	if err := st.SetLoginAllowed(ann, true, false, 0); !errors.Is(err, ErrNeedsAttest) {
		t.Errorf("unknown age without tick = %v", err)
	}
	if err := st.SetLoginAllowed(ann, true, true, 0); err != nil {
		t.Fatal(err)
	}
	code, err := st.CreateClaim(ann, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	st.DB.QueryRow(`SELECT code_hash FROM claims`).Scan(&stored)
	if stored == "" || stored == code {
		t.Errorf("the code must be stored hashed, got %q", stored)
	}

	user := mustUser(t, st, "ann")
	if _, err := st.ClaimPreview(code, user); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptClaim("wrong", user); !errors.Is(err, ErrClaimInvalid) {
		t.Errorf("wrong code = %v", err)
	}
	if _, err := st.AcceptClaim(code, user); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(ann); p.UserID != user {
		t.Error("claim didn't link")
	}
	// One time only.
	st.UnlinkUser(ann, 0)
	if _, err := st.AcceptClaim(code, user); !errors.Is(err, ErrClaimInvalid) {
		t.Errorf("reused code = %v, want ErrClaimInvalid", err)
	}

	// A new code replaces the old one; a user with a profile can't claim.
	st.SetLoginAllowed(ann, true, true, 0)
	c1, _ := st.CreateClaim(ann, 0)
	c2, _ := st.CreateClaim(ann, 0)
	if _, err := st.ClaimPreview(c1, user); !errors.Is(err, ErrClaimInvalid) {
		t.Errorf("replaced code = %v", err)
	}
	bob := mustPerson(t, st, "Bob", "Goza", "", "")
	setBirth(t, st, bob, dates.Partial{Year: 1970})
	st.LinkUser(bob, user, 0, false)
	if _, err := st.AcceptClaim(c2, user); !errors.Is(err, ErrUserHasProfile) {
		t.Errorf("claim by a user with a profile = %v", err)
	}

	// Expiry.
	st.UnlinkUser(bob, 0)
	if _, err := st.DB.Exec(`UPDATE claims SET expires_at = datetime('now', '-1 minute')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptClaim(c2, user); !errors.Is(err, ErrClaimInvalid) {
		t.Errorf("expired code = %v", err)
	}
	// Found under 13 after the link was made: the claim is refused too.
	c3, _ := st.CreateClaim(ann, 0)
	if _, err := st.DB.Exec(`UPDATE people SET birth_year = ? WHERE id = ?`, thisYear-3, ann); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptClaim(c3, user); !errors.Is(err, ErrUnder13) {
		t.Errorf("claim of a now-under-13 = %v", err)
	}
}

func TestFoundLaterUnder13IsUnlinked(t *testing.T) {
	st := openTest(t)
	thisYear := time.Now().Year()
	local := mustPerson(t, st, "Lo", "Cal", "", "")
	sso := mustPerson(t, st, "Sso", "Kid", "", "")
	ul := mustUser(t, st, "local")
	us := mustUser(t, st, "sso")
	if _, err := st.DB.Exec(`UPDATE users SET sso_subject = 'um-7' WHERE id = ?`, us); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkUser(local, ul, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkUser(sso, us, 0, true); err != nil {
		t.Fatal(err)
	}
	sess, _ := st.CreateSession(ul, time.Hour)

	if err := setBirth(t, st, local, dates.Partial{Year: thisYear - 20}); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(local); p.UserID != ul {
		t.Fatal("an adult birthday must not unlink")
	}
	if err := setBirth(t, st, local, dates.Partial{Year: thisYear - 8}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.GetPerson(local)
	if p.UserID != 0 || p.LoginAllowed {
		t.Errorf("under-13 still linked: %+v", p)
	}
	if u, _ := st.GetUser(ul); u.Active {
		t.Error("the local user should be inactive")
	}
	if _, err := st.GetSession(sess.Token); err == nil {
		t.Error("the local user's session should be gone")
	}
	if items, _ := st.Under13Items(); len(items) != 0 {
		t.Errorf("a local user needs no Needs action item, got %v", items)
	}

	// SSO: unlinked, user left alone, an item for staff.
	if err := setBirth(t, st, sso, dates.Partial{Year: thisYear - 8}); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(sso); p.UserID != 0 {
		t.Error("the SSO user should be unlinked")
	}
	if u, _ := st.GetUser(us); !u.Active {
		t.Error("an SSO user is never deactivated here")
	}
	items, _ := st.Under13Items()
	if len(items) != 1 || items[0].SSOSubject != "um-7" || items[0].PersonName != "Sso Kid" {
		t.Fatalf("items = %+v", items)
	}
	if err := st.ResolveUnder13(items[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if items, _ = st.Under13Items(); len(items) != 0 {
		t.Error("resolved item still listed")
	}
}

func TestSelfSignupHeldSamePersonAndDifferent(t *testing.T) {
	st := openTest(t)
	ex := mustPerson(t, st, "Tyler", "Goza", "", "")
	setBirth(t, st, ex, dates.Partial{Year: 1985})
	staff, _ := st.CreateUser("sam", "Sam", "a long password", RoleEditor)
	user := mustUser(t, st, "tyler")

	// No match: pending as self_signup, linked.
	id0, reason, err := st.MakeMyProfile(mustUser(t, st, "zed"), PersonInput{FirstName: "Zed", LastName: "Other", Kind: KindMember}, true)
	if err != nil || reason != PendingSelfSignup {
		t.Fatalf("no match: %v, %q", err, reason)
	}
	if p, _ := st.GetPerson(id0); !p.Pending || p.UserID == 0 {
		t.Errorf("self sign-up profile: %+v", p)
	}

	// Under 13 can't make a profile; unknown age needs the tick.
	thisYear := time.Now().Year()
	k := mustUser(t, st, "kid")
	if _, _, err := st.MakeMyProfile(k, PersonInput{FirstName: "K", LastName: "Id", Birth: dates.Partial{Year: thisYear - 4}, Kind: KindMember}, true); !errors.Is(err, ErrUnder13) {
		t.Errorf("under-13 self sign-up = %v", err)
	}
	if _, _, err := st.MakeMyProfile(k, PersonInput{FirstName: "K", LastName: "Id", Kind: KindMember}, false); !errors.Is(err, ErrNeedsAttest) {
		t.Errorf("unknown age self sign-up = %v", err)
	}

	// Match: held as name_match, a second profile exists only until staff decide.
	held, reason, err := st.MakeMyProfile(user, PersonInput{FirstName: "tyler", LastName: "GOZA", Email: "t@example.com", Kind: KindMember}, true)
	if err != nil || reason != PendingNameMatch {
		t.Fatalf("match: %v, %q", err, reason)
	}
	na, _ := st.ListNeedsAction()
	if len(na.Held) != 1 || len(na.Held[0].Matches) != 1 || na.Held[0].Matches[0].Person.ID != ex {
		t.Fatalf("needs action: %+v", na.Held)
	}
	cp := na.Held[0].Matches[0].Copyable
	if len(cp) != 1 || cp[0].Key != CopyEmail {
		t.Errorf("copyable = %+v", cp)
	}
	n := countRows(t, st, "people")

	if _, err := st.ResolveSamePerson(held, ex, []string{CopyEmail, CopyPhone}, false, staff); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st, "people") != n-1 {
		t.Error("Same person must not leave a second profile")
	}
	p, _ := st.GetPerson(ex)
	if p.UserID != user || p.Email != "t@example.com" {
		t.Errorf("existing person after merge: %+v", p)
	}
	if _, err := st.GetPerson(held); !errors.Is(err, ErrNotFound) {
		t.Error("the held entry should be deleted")
	}

	// Different people: approved, pair recorded, never flagged again.
	other := mustUser(t, st, "ty2")
	h2, reason, _ := st.MakeMyProfile(other, PersonInput{FirstName: "Tyler", LastName: "Goza", Kind: KindMember}, true)
	if reason != PendingNameMatch {
		t.Fatalf("second match reason %q", reason)
	}
	if err := st.ApproveDifferent(h2, "", "", staff); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(h2); p.Pending {
		t.Error("still pending")
	}
	m, _ := st.NameMatches("Tyler", "Goza", "", "", h2)
	for _, x := range m {
		if x.ID == ex {
			t.Error("a recorded different pair is flagged again")
		}
	}
	if countRows(t, st, "not_same_people") != 1 {
		t.Error("pair not recorded")
	}

	// Turn down leaves a notice for the creator and deletes the entry.
	third := mustUser(t, st, "ty3")
	h3, _, _ := st.MakeMyProfile(third, PersonInput{FirstName: "Tyler", LastName: "Goza", Kind: KindMember}, true)
	if _, err := st.TurnDownPerson(h3, staff); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPerson(h3); !errors.Is(err, ErrNotFound) {
		t.Error("turned-down entry still there")
	}
	notes, _ := st.UnseenNotices(third)
	if len(notes) != 1 {
		t.Fatalf("notices = %+v", notes)
	}
	st.MarkNoticesSeen(third)
	if notes, _ = st.UnseenNotices(third); len(notes) != 0 {
		t.Error("notice not marked seen")
	}
}

func TestHeldRenameAndHouseholdAdd(t *testing.T) {
	st := openTest(t)
	ex := mustPerson(t, st, "Tyler", "Goza", "", "")
	me := mustPerson(t, st, "Tim", "Goza", "", "")
	head := mustUser(t, st, "head")
	if held, err := st.HoldRename(me, "Tyler", "Goza", "", "", head); err != nil || !held {
		t.Fatalf("matching rename held = %v, %v", held, err)
	}
	if held, _ := st.HoldRename(me, "Tom", "Goza", "", "", head); held {
		t.Error("a rename that matches nobody must go ahead")
	}
	if p, _ := st.GetPerson(me); p.Name() != "Tim Goza" || p.PendingName == "" {
		t.Errorf("old name stays while held: %+v", p)
	}
	if err := st.RejectRename(me, 0); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(me); p.PendingName != "" || p.Name() != "Tim Goza" {
		t.Errorf("after turn down: %+v", p)
	}
	if n, _ := st.UnseenNotices(head); len(n) != 1 {
		t.Error("no notice for the rename")
	}
	st.HoldRename(me, "Tyler", "Goza", "", "", head)
	if err := st.ApproveRename(me, 0); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(me); p.Name() != "Tyler Goza" || p.PendingName != "" {
		t.Errorf("after approve: %+v", p)
	}
	if m, _ := st.NameMatches("Tyler", "Goza", "", "", me); len(m) != 1 || m[0].ID != ex {
		// the recorded pair removes the exclusion for me; ex is still excluded vs me
		for _, x := range m {
			if x.ID == ex {
				t.Error("approved rename pair should not flag again")
			}
		}
	}

	// A head adds someone whose name matches: held; Same person adds the
	// existing person to the household and deletes the entry.
	hh, _ := st.CreateHousehold("The Heads", "", 0)
	id, reason, err := st.CreateByMember(PersonInput{FirstName: "Tyler", LastName: "Goza", Email: "x@y.z", Kind: KindMember}, head, false)
	if err != nil || reason != PendingNameMatch {
		t.Fatalf("%v %q", err, reason)
	}
	if err := st.PlaceMember(hh, id, RoleChild, 0, head); err != nil {
		t.Fatal(err)
	}
	// 'me' (renamed to Tyler Goza) is also a match for the held entry? the
	// pair is recorded for me/ex only, so both match the new entry.
	if _, err := st.ResolveSamePerson(id, ex, nil, false, 0); err != nil {
		t.Fatal(err)
	}
	if ms, _ := st.HouseholdMembers(hh); len(ms) != 1 || ms[0].ID != ex || ms[0].Role != RoleChild {
		t.Errorf("household members after Same person: %+v", ms)
	}
}

func TestSelfHouseholdPending(t *testing.T) {
	st := openTest(t)
	u := mustUser(t, st, "u")
	if _, _, err := st.MakeMyProfile(u, PersonInput{FirstName: "Una", LastName: "Unique", Kind: KindMember}, true); err != nil {
		t.Fatal(err)
	}
	hid, err := st.CreateSelfHousehold(u, "The Uniques")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSelfHousehold(u, "Again"); !errors.Is(err, ErrHasHousehold) {
		t.Errorf("second household = %v", err)
	}
	kid, reason, err := st.AddToSelfHousehold(u, hid, PersonInput{FirstName: "Kid", LastName: "Unique", Kind: KindMember}, RoleChild)
	if err != nil || reason != PendingSelfSignup {
		t.Fatalf("%v %q", err, reason)
	}
	if _, _, err := st.AddToSelfHousehold(mustUser(t, st, "v"), hid, PersonInput{FirstName: "X", LastName: "Y", Kind: KindMember}, RoleChild); !errors.Is(err, ErrNotYours) {
		t.Errorf("someone else's household = %v", err)
	}
	if err := st.ApproveHousehold(hid, 0); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(kid); p.Pending {
		t.Error("household approval should approve its self sign-up members")
	}
	h, _ := st.GetHousehold(hid)
	if h.Pending {
		t.Error("household still pending")
	}
}

func countRows(t *testing.T, st *Store, table string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
