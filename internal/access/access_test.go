package access

import (
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

// startDefaults mirror the migration's seeded defaults.
func startDefaults() store.Defaults {
	d := store.Defaults{}
	for _, f := range store.DefaultFields {
		d["adult|"+string(f)] = f != store.FieldBirthYear
		d["child|"+string(f)] = false
	}
	return d
}

func person(id int64) store.Person {
	return store.Person{ID: id, FirstName: "Ann", LastName: "Lee", Kind: store.KindMember,
		Email: "ann@example.org", Phone: "555-0100", Photo: "abc.jpg",
		Birth:       dates.Partial{Month: 3, Day: 14, Year: 1985},
		Anniversary: dates.Partial{Month: 6, Day: 5, Year: 2001}}
}

func adult(id int64) *PersonAccess {
	return &PersonAccess{Person: person(id), Defaults: startDefaults(),
		Memberships: []store.Membership{{HouseholdID: 10, PersonID: id, Role: store.RoleAdult}}}
}

func child(id int64) *PersonAccess {
	a := adult(id)
	a.Memberships = []store.Membership{{HouseholdID: 10, PersonID: id, Role: store.RoleChild}}
	return a
}

var member = &Viewer{UserID: 4, PersonID: 99, HeadOf: map[int64]bool{}}

func TestVisibleRules(t *testing.T) {
	cases := []struct {
		name   string
		a      *PersonAccess
		v      *Viewer
		f      store.Field
		change func(a *PersonAccess)
		want   bool
	}{
		{"1 not entered: nothing to show, even for staff", adult(1), &Viewer{Staff: true}, store.FieldEmail,
			func(a *PersonAccess) { a.Person.Email = "" }, false},
		{"1 not entered beats a shown setting", adult(1), member, store.FieldPhone,
			func(a *PersonAccess) {
				a.Person.Phone = ""
				a.Settings = map[store.Field]store.FieldSetting{store.FieldPhone: {Shown: yes()}}
			}, false},
		{"2 force hidden hides from a member", adult(1), member, store.FieldEmail,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldEmail: {Shown: yes(), ForceHidden: true}}
			}, false},
		{"2 force hidden still shows to staff", adult(1), &Viewer{Staff: true}, store.FieldEmail,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldEmail: {ForceHidden: true}}
			}, true},
		{"2 force hidden still shows to the person", adult(1), &Viewer{PersonID: 1}, store.FieldEmail,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldEmail: {ForceHidden: true}}
			}, true},
		{"2 force hidden still shows to a head of the household", adult(1), &Viewer{PersonID: 7, HeadOf: map[int64]bool{10: true}}, store.FieldEmail,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldEmail: {ForceHidden: true}}
			}, true},
		{"2 force hidden hides from a head of another household", adult(1), &Viewer{PersonID: 7, HeadOf: map[int64]bool{11: true}}, store.FieldEmail,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldEmail: {ForceHidden: true}}
			}, false},
		{"3 day needs month: month hidden hides the day", adult(1), member, store.FieldBirthDay,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldBirthMonth: {Shown: no()}, store.FieldBirthDay: {Shown: yes()}}
			}, false},
		{"3 day needs month: month shown shows the day", adult(1), member, store.FieldBirthDay, nil, true},
		{"3 day needs month: anniversary too", adult(1), member, store.FieldAnniversaryDay,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldAnniversaryMon: {ForceHidden: true}}
			}, false},
		{"4 own setting hides over a shown default", adult(1), member, store.FieldPhone,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldPhone: {Shown: no()}}
			}, false},
		{"4 own setting shows over a hidden default", adult(1), member, store.FieldBirthYear,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldBirthYear: {Shown: yes()}}
			}, true},
		{"4 own setting nil falls through to the default", adult(1), member, store.FieldPhone,
			func(a *PersonAccess) { a.Settings = map[store.Field]store.FieldSetting{store.FieldPhone: {}} }, true},
		{"5 adult default shown", adult(1), member, store.FieldEmail, nil, true},
		{"5 adult default hidden (birth year)", adult(1), member, store.FieldBirthYear, nil, false},
		{"5 child default hidden", child(1), member, store.FieldEmail, nil, false},
		{"5 child default hidden: photo", child(1), member, store.FieldPhoto, nil, false},
		{"5 child default changed by the admin", child(1), member, store.FieldEmail,
			func(a *PersonAccess) { a.Defaults["child|email"] = true }, true},
		{"5 a child in a second household as adult elsewhere is still a child", child(1), member, store.FieldPhone,
			func(a *PersonAccess) {
				a.Memberships = append(a.Memberships, store.Membership{HouseholdID: 11, PersonID: 1, Role: store.RoleHead})
			}, false},
		{"always: staff sees a hidden-by-default child field", child(1), &Viewer{Staff: true}, store.FieldEmail, nil, true},
		{"always: the person sees their own hidden field", child(1), &Viewer{PersonID: 1}, store.FieldEmail,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldEmail: {Shown: no()}}
			}, true},
		{"always: a head sees a child's hidden fields", child(1), &Viewer{PersonID: 7, HeadOf: map[int64]bool{10: true}}, store.FieldBirthYear, nil, true},
		{"always: a head sees a day whose month the person hides", adult(1), &Viewer{PersonID: 7, HeadOf: map[int64]bool{10: true}}, store.FieldBirthDay,
			func(a *PersonAccess) {
				a.Settings = map[store.Field]store.FieldSetting{store.FieldBirthMonth: {Shown: no()}}
			}, true},
		{"always: a member does not", child(1), member, store.FieldBirthYear, nil, false},
		{"nobody signed in sees nothing hidden", child(1), nil, store.FieldEmail, nil, false},
	}
	for _, c := range cases {
		if c.change != nil {
			c.change(c.a)
		}
		if got := Visible(c.v, c.a, c.f); got != c.want {
			t.Errorf("%s: Visible = %v, want %v", c.name, got, c.want)
		}
	}
}

func household(kind string) *HouseholdAccess {
	return &HouseholdAccess{Household: store.Household{ID: 10, Name: "The Lees", Kind: kind, Photo: "h.jpg"}, Defaults: startDefaults()}
}

func TestVisibleAddressAndHouseholdPhoto(t *testing.T) {
	h := household(store.KindMember)
	addr := store.Address{ID: 1, HouseholdID: 10, Address1: "1 Oak St"}
	head := &Viewer{PersonID: 7, HeadOf: map[int64]bool{10: true}}
	other := &Viewer{PersonID: 8, HeadOf: map[int64]bool{11: true}}

	if !VisibleAddress(member, h, addr) {
		t.Error("an address shows by default")
	}
	if VisibleAddress(member, h, store.Address{ID: 2, HouseholdID: 10}) {
		t.Error("an empty address has nothing to show")
	}
	addr.Shown = no()
	if VisibleAddress(member, h, addr) || !VisibleAddress(head, h, addr) {
		t.Error("the heads' own choice hides it from members, not from heads")
	}
	addr.Shown = yes()
	addr.ForceHidden = true
	if VisibleAddress(member, h, addr) || VisibleAddress(other, h, addr) {
		t.Error("force hidden hides it from members and heads of other households")
	}
	if !VisibleAddress(head, h, addr) || !VisibleAddress(&Viewer{Staff: true}, h, addr) {
		t.Error("force hidden still shows to staff and this household's heads")
	}
	h.Defaults["adult|address"] = false
	addr.ForceHidden, addr.Shown = false, nil
	if VisibleAddress(member, h, addr) {
		t.Error("the default hides it")
	}

	h = household(store.KindMember)
	if !VisibleHouseholdPhoto(member, h) {
		t.Error("photo shows by default")
	}
	h.Settings = map[store.Field]store.FieldSetting{store.FieldHouseholdPhoto: {ForceHidden: true}}
	if VisibleHouseholdPhoto(member, h) || !VisibleHouseholdPhoto(head, h) {
		t.Error("force hidden photo: heads only")
	}
	h.Settings = map[store.Field]store.FieldSetting{store.FieldHouseholdPhoto: {Shown: no()}}
	if VisibleHouseholdPhoto(member, h) {
		t.Error("own setting hides the photo")
	}
	h.Household.Photo = ""
	if VisibleHouseholdPhoto(head, h) {
		t.Error("no photo, nothing to show")
	}
}

func TestCanSeePerson(t *testing.T) {
	staff := &Viewer{Staff: true, UserID: 1}
	creator := &Viewer{UserID: 5}
	head := &Viewer{UserID: 6, PersonID: 7, HeadOf: map[int64]bool{10: true}}

	a := adult(1)
	if !CanSeePerson(member, a) {
		t.Error("members are listed")
	}
	a.Person.ArchivedAt = "2026-01-01"
	if CanSeePerson(member, a) || CanSeePerson(head, a) || !CanSeePerson(staff, a) {
		t.Error("archived: staff only")
	}
	a = adult(1)
	a.Person.Pending, a.Person.PendingReason, a.Person.CreatedBy = true, "name_match", 5
	if !CanSeePerson(creator, a) || !CanSeePerson(staff, a) || CanSeePerson(member, a) || CanSeePerson(head, a) {
		t.Error("pending: its creator and staff")
	}
	a.Person.CreatedBy = 0
	if CanSeePerson(&Viewer{UserID: 0}, a) {
		t.Error("a pending person with no creator isn't everyone's")
	}
	a = adult(1)
	a.Person.Kind = store.KindContact
	a.Managers = []int64{42}
	if CanSeePerson(member, a) {
		t.Error("contacts are hidden from members")
	}
	if !CanSeePerson(staff, a) || !CanSeePerson(head, a) || !CanSeePerson(&Viewer{PersonID: 42}, a) {
		t.Error("contacts: staff, heads of their households, whoever manages them")
	}
	if CanSeePerson(&Viewer{PersonID: 7, HeadOf: map[int64]bool{11: true}}, a) {
		t.Error("a head of an unrelated household can't see the contact")
	}
	if CanSeePerson(nil, adult(1)) {
		t.Error("nobody signed in sees nobody")
	}
}

func TestCanSeeHousehold(t *testing.T) {
	staff := &Viewer{Staff: true}
	head := &Viewer{PersonID: 7, HeadOf: map[int64]bool{10: true}}
	h := household(store.KindMember)
	if !CanSeeHousehold(member, h) {
		t.Error("households are listed")
	}
	h.Household.ArchivedAt = "2026-01-01"
	if CanSeeHousehold(member, h) || !CanSeeHousehold(staff, h) {
		t.Error("archived: staff only")
	}
	h = household(store.KindContact)
	h.Household.ManagedBy = 42
	if CanSeeHousehold(member, h) || !CanSeeHousehold(head, h) || !CanSeeHousehold(&Viewer{PersonID: 42}, h) || !CanSeeHousehold(staff, h) {
		t.Error("contact households: staff, heads, whoever manages them")
	}
}

// directory builds a small directory by hand: the Lees (two heads, a
// child), a hidden contact, an archived person, a pending person and
// someone in no household.
func testDirectory(v *Viewer, tweak func(people []store.Person, sets map[int64]map[store.Field]store.FieldSetting)) *Directory {
	mk := func(id int64, first string) store.Person {
		p := person(id)
		p.FirstName = first
		p.Email = first + "@example.org"
		return p
	}
	people := []store.Person{mk(1, "Ann"), mk(2, "Bob"), mk(3, "Cy"), mk(4, "Contact"), mk(5, "Archie"), mk(6, "Penny"), mk(7, "Solo")}
	people[3].Kind = store.KindContact
	people[4].ArchivedAt = "2026-01-01"
	people[5].Pending, people[5].PendingReason, people[5].CreatedBy = true, "name_match", 77
	households := []store.Household{{ID: 10, Name: "The Lee Family", Kind: store.KindMember}}
	members := []store.Membership{
		{HouseholdID: 10, PersonID: 1, Role: store.RoleHead, AddressID: 1},
		{HouseholdID: 10, PersonID: 2, Role: store.RoleHead, AddressID: 1},
		{HouseholdID: 10, PersonID: 3, Role: store.RoleChild, AddressID: 1},
		{HouseholdID: 10, PersonID: 4, Role: store.RoleAdult},
		{HouseholdID: 10, PersonID: 5, Role: store.RoleAdult},
		{HouseholdID: 10, PersonID: 6, Role: store.RoleAdult},
	}
	addrs := []store.Address{{ID: 1, HouseholdID: 10, Address1: "1 Oak St", City: "Springfield"}}
	sets := map[int64]map[store.Field]store.FieldSetting{}
	if tweak != nil {
		tweak(people, sets)
	}
	return Build(v, startDefaults(), people, households, members, addrs, sets, nil)
}

func names(l Listing) []string {
	var out []string
	for _, e := range l.Entries {
		if e.Family != nil {
			for _, g := range e.Family.Groups {
				for _, p := range g.People {
					out = append(out, p.Name)
				}
			}
		} else {
			out = append(out, e.Person.Name)
		}
	}
	return out
}

func TestListingHidesContactsArchivedAndPending(t *testing.T) {
	got := names(testDirectory(member, nil).Listing(""))
	want := map[string]bool{"Ann Lee": true, "Bob Lee": true, "Cy Lee": true, "Solo Lee": true}
	if len(got) != len(want) {
		t.Fatalf("listed %v", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("%q should not be listed", n)
		}
	}
	// Staff see more elsewhere, but the directory page still has no contacts or archived people.
	got = names(testDirectory(&Viewer{Staff: true}, nil).Listing(""))
	for _, n := range got {
		if n == "Contact Lee" || n == "Archie Lee" {
			t.Errorf("staff's directory lists %q", n)
		}
	}
	// A pending person shows to the one who made them.
	got = names(testDirectory(&Viewer{UserID: 77}, nil).Listing(""))
	found := false
	for _, n := range got {
		found = found || n == "Penny Lee"
	}
	if !found {
		t.Errorf("the creator should see their pending person: %v", got)
	}
}

func TestListingGroupsAndSearch(t *testing.T) {
	l := testDirectory(member, nil).Listing("")
	if len(l.Entries) != 2 || l.Entries[0].Family == nil || l.Entries[1].Person == nil {
		t.Fatalf("entries = %+v", l.Entries)
	}
	f := l.Entries[0].Family
	if len(f.Groups) != 1 || f.Groups[0].Address != "1 Oak St, Springfield" || f.Groups[0].People[0].Role != store.RoleHead {
		t.Errorf("groups = %+v", f.Groups)
	}
	if l.Entries[0].Letter != "L" || !l.Letters[11].Has || l.Letters[0].Has {
		t.Errorf("letters wrong: %+v %+v", l.Entries[0].Letter, l.Letters[:3])
	}
	if got := len(testDirectory(member, nil).Listing("jose ann").Entries); got != 0 {
		t.Errorf("both words must match: %d", got)
	}
	if got := testDirectory(member, nil).Listing("lee family  ANN"); len(got.Entries) != 1 {
		t.Errorf("a household name plus a member's name should match one card: %+v", got.Entries)
	}
	// The A-Z order drops a leading "The".
	if nameKey("The Goza Family") != "goza family" {
		t.Error("nameKey")
	}
	// The address hides when its owners say so, and people drop to the group without one.
	d := testDirectory(member, nil)
	d.households[10].Addresses[0].Shown = no()
	f2, _ := d.Family(10, true)
	if len(f2.Groups) != 1 || f2.Groups[0].Address != "" {
		t.Errorf("hidden address still grouped: %+v", f2.Groups)
	}
}

func TestChildrenGetChildDefaultsInViews(t *testing.T) {
	d := testDirectory(member, nil)
	f, _ := d.Family(10, true)
	for _, p := range f.Groups[0].People {
		if p.Name == "Cy Lee" && (p.Email != "" || p.Phone != "" || p.Birthday.Text != "") {
			t.Errorf("a child's hidden-by-default fields leaked: %+v", p)
		}
		if p.Name == "Ann Lee" && (p.Email == "" || p.Birthday.Text != "March 14") {
			t.Errorf("an adult's shown fields missing: %+v", p)
		}
	}
}

func TestSharedAnniversaryShownOnce(t *testing.T) {
	d := testDirectory(member, nil)
	f, _ := d.Family(10, true)
	if f.Anniversary != "June 5, 2001" {
		t.Fatalf("anniversary = %q", f.Anniversary)
	}
	for _, p := range f.Groups[0].People {
		if p.Anniversary.Text != "" && p.Role == store.RoleHead {
			t.Errorf("%s still shows the shared anniversary", p.Name)
		}
	}
	// One head hides theirs: nothing is merged, the other shows their own.
	d = testDirectory(member, func(people []store.Person, sets map[int64]map[store.Field]store.FieldSetting) {
		sets[2] = map[store.Field]store.FieldSetting{store.FieldAnniversaryMon: {Shown: no()}}
	})
	f, _ = d.Family(10, true)
	if f.Anniversary != "" {
		t.Errorf("shared anniversary with one head hiding: %q", f.Anniversary)
	}
	// Different dates: not shared.
	d = testDirectory(member, func(people []store.Person, sets map[int64]map[store.Field]store.FieldSetting) {
		people[1].Anniversary = dates.Partial{Month: 7, Day: 5, Year: 2001}
	})
	if f, _ = d.Family(10, true); f.Anniversary != "" {
		t.Errorf("different dates shared: %q", f.Anniversary)
	}
}

func TestBirthdays(t *testing.T) {
	d := testDirectory(member, func(people []store.Person, sets map[int64]map[store.Field]store.FieldSetting) {
		people[0].Birth = dates.Partial{Month: 3, Day: 20}
		people[1].Birth = dates.Partial{Month: 4, Day: 2, Year: 1990}
		people[1].Anniversary = people[0].Anniversary
		people[6].Birth = dates.Partial{Month: 3}
		people[6].Anniversary = dates.Partial{}
		sets[1] = map[store.Field]store.FieldSetting{store.FieldAnniversaryMon: {Shown: no()}}
	})
	m := d.Birthdays(time.Date(2026, time.March, 10, 0, 0, 0, 0, time.UTC))
	if m[0].Name != "March" || m[1].Name != "April" {
		t.Fatalf("months = %+v", m)
	}
	if len(m[0].Birthdays) != 1 || m[0].Birthdays[0].People[0].Name != "Ann Lee" {
		t.Errorf("March birthdays = %+v (month-only and child birthdays must not appear)", m[0].Birthdays)
	}
	if len(m[1].Birthdays) != 1 || m[1].Birthdays[0].Text != "April 2" {
		t.Errorf("April birthdays = %+v", m[1].Birthdays)
	}
	// December rolls to January.
	if m := d.Birthdays(time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)); m[1].Name != "January" {
		t.Errorf("next month after December = %s", m[1].Name)
	}
}

func TestPageIsNotFoundForInvisible(t *testing.T) {
	d := testDirectory(member, nil)
	for _, id := range []int64{4, 5, 6, 99} {
		if _, ok := d.Page(id); ok {
			t.Errorf("person %d should not be visible", id)
		}
	}
	if _, ok := d.Page(1); !ok {
		t.Error("an ordinary member should be visible")
	}
}
