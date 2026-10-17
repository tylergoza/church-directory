package access

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

// subject builds the person being edited (id 1, household 10): an adult or
// a child, with or without a login, and the heads_may_edit state.
func subject(kind string, linked bool, on, locked bool) *PersonAccess {
	var a *PersonAccess
	if kind == "child" {
		a = child(1)
	} else {
		a = adult(1)
	}
	if linked {
		a.Person.UserID = 50
	}
	a.Person.HeadsMayEdit = on
	a.Person.HeadsMayEditLocked = locked
	return a
}

// TestCanEditPerson is the head rights table: viewer x (adult / child) x
// (linked / unlinked) x heads_may_edit (off, on, locked on, locked off).
func TestCanEditPerson(t *testing.T) {
	viewers := map[string]*Viewer{
		"staff":                  {UserID: 9, Staff: true},
		"self":                   {UserID: 5, PersonID: 1, HeadOf: map[int64]bool{}},
		"head":                   {UserID: 6, PersonID: 2, HeadOf: map[int64]bool{10: true}},
		"head of other":          {UserID: 7, PersonID: 3, HeadOf: map[int64]bool{99: true}},
		"adult in the household": {UserID: 8, PersonID: 4, HeadOf: map[int64]bool{}},
		"nobody":                 nil,
	}
	type state struct {
		name       string
		on, locked bool
	}
	states := []state{{"off", false, false}, {"on", true, false}, {"locked on", true, true}, {"locked off", false, true}}
	for vname, v := range viewers {
		for _, kind := range []string{"adult", "child"} {
			for _, linked := range []bool{false, true} {
				for _, st := range states {
					a := subject(kind, linked, st.on, st.locked)
					headsAllowed := !linked || kind == "child" || st.on
					want := false
					switch vname {
					case "staff", "self":
						want = true
					case "head":
						want = headsAllowed
					}
					if got := CanEditPerson(v, a); got != want {
						t.Errorf("%s edits %s linked=%v heads_may_edit %s: got %v, want %v", vname, kind, linked, st.name, got, want)
					}
				}
			}
		}
	}
}

func TestCanEditPersonManagerOnlyWhileActive(t *testing.T) {
	mgr := &Viewer{UserID: 6, PersonID: 2, HeadOf: map[int64]bool{}}
	a := subject("adult", false, false, false)
	if CanEditPerson(mgr, a) {
		t.Fatal("a stranger edited an unlinked person")
	}
	a.ActiveManagers = []int64{2}
	if !CanEditPerson(mgr, a) {
		t.Error("the active manager of a household they are in should edit an unlinked member")
	}
	a.Person.UserID = 50
	if CanEditPerson(mgr, a) {
		t.Error("a manager edited a linked adult without heads_may_edit")
	}
}

func TestCanEditPersonNeedsToSeeThem(t *testing.T) {
	head := &Viewer{UserID: 6, PersonID: 2, HeadOf: map[int64]bool{10: true}}
	a := subject("child", false, false, false)
	a.Person.Pending, a.Person.PendingReason, a.Person.CreatedBy = true, store.PendingNameMatch, 123
	if CanEditPerson(head, a) {
		t.Error("a head edited a held person someone else made")
	}
	a.Person.CreatedBy = head.UserID
	if !CanEditPerson(head, a) {
		t.Error("the head who made the held person should still edit it")
	}
}

func TestCanHeadEditHousehold(t *testing.T) {
	plain := &HouseholdAccess{Household: store.Household{ID: 10}}
	managed := &HouseholdAccess{Household: store.Household{ID: 11, Kind: store.KindContact, ManagedBy: 2}, ManagedActive: true}
	ended := &HouseholdAccess{Household: store.Household{ID: 12, ManagedBy: 2}}
	cases := []struct {
		name string
		v    *Viewer
		h    *HouseholdAccess
		want bool
	}{
		{"staff", &Viewer{Staff: true}, plain, true},
		{"head", &Viewer{PersonID: 2, HeadOf: map[int64]bool{10: true}}, plain, true},
		{"head of another household", &Viewer{PersonID: 2, HeadOf: map[int64]bool{99: true}}, plain, false},
		{"adult member", &Viewer{PersonID: 4, HeadOf: map[int64]bool{}}, plain, false},
		{"child with a login", &Viewer{PersonID: 5, HeadOf: map[int64]bool{}}, plain, false},
		{"manager of a managed household", &Viewer{PersonID: 2, HeadOf: map[int64]bool{}}, managed, true},
		{"someone else on a managed household", &Viewer{PersonID: 3, HeadOf: map[int64]bool{}}, managed, false},
		{"manager once a head has a login", &Viewer{PersonID: 2, HeadOf: map[int64]bool{}}, ended, false},
		{"no profile", &Viewer{UserID: 3}, managed, false},
		{"signed out", nil, plain, false},
	}
	for _, c := range cases {
		if got := CanHeadEditHousehold(c.v, c.h); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Build works out whether a manager's rights last from the rows.
func TestManagedActiveFromRows(t *testing.T) {
	people := []store.Person{
		{ID: 2, FirstName: "A", LastName: "Lee", Kind: store.KindMember},
		{ID: 3, FirstName: "B", LastName: "Lee", Kind: store.KindMember},
	}
	hs := []store.Household{{ID: 11, Name: "Lee", ManagedBy: 2}}
	ms := []store.Membership{{HouseholdID: 11, PersonID: 3, Role: store.RoleHead}}
	d := Build(&Viewer{PersonID: 2, HeadOf: map[int64]bool{}}, startDefaults(), people, hs, ms, nil, nil, nil)
	h, _ := d.Household(11)
	if !h.ManagedActive || !CanHeadEditHousehold(d.Viewer, h) {
		t.Fatal("manager should edit while no head has a login")
	}
	people[1].UserID = 77
	d = Build(&Viewer{PersonID: 2, HeadOf: map[int64]bool{}}, startDefaults(), people, hs, ms, nil, nil, nil)
	h, _ = d.Household(11)
	if h.ManagedActive || CanHeadEditHousehold(d.Viewer, h) {
		t.Fatal("manager still edits after a head got a login")
	}
}

func TestCanRemoveFromHousehold(t *testing.T) {
	h := &HouseholdAccess{Household: store.Household{ID: 10}}
	head := &Viewer{PersonID: 2, HeadOf: map[int64]bool{10: true}}
	linked := subject("adult", true, false, false)
	unlinked := subject("adult", false, false, false)
	if !CanRemoveFromHousehold(head, h, unlinked) {
		t.Error("a head removes an unlinked person")
	}
	if CanRemoveFromHousehold(head, h, linked) {
		t.Error("a head removed a linked adult")
	}
	if !CanRemoveFromHousehold(&Viewer{PersonID: 1, HeadOf: map[int64]bool{}}, h, linked) {
		t.Error("a linked adult can leave")
	}
	if !CanRemoveFromHousehold(&Viewer{Staff: true}, h, linked) {
		t.Error("staff move linked adults")
	}
}

// Contact households (managed_by) are for their manager only: they never
// appear in the listing, the printed book or the groups for anyone else.
func TestContactHouseholdsNeverListed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hank, _ := st.CreatePerson(store.PersonInput{FirstName: "Hank", LastName: "Lee"}, 0)
	pat, _ := st.CreatePerson(store.PersonInput{FirstName: "Zorblax", LastName: "Other", Kind: store.KindContact}, 0)
	own, _ := st.CreateHousehold("The Lee Family", "", 0)
	st.PlaceMember(own, hank, store.RoleHead, 0, 0)
	ch, err := st.CreateManagedHousehold("The Zorblax Household", store.KindContact, hank, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PlaceMember(ch, pat, store.RoleAdult, 0, 0); err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	st.AddGroupMember(g, pat, "", 0)
	st.AddGroupMember(g, hank, "", 0)

	for name, v := range map[string]*Viewer{
		"member": {UserID: 50, HeadOf: map[int64]bool{}},
		"staff":  {UserID: 51, Staff: true, HeadOf: map[int64]bool{}},
	} {
		d, err := Load(st, v)
		if err != nil {
			t.Fatal(err)
		}
		book, _ := d.Print(0)
		seen := fmt.Sprintf("%+v %+v %+v", d.Listing(""), book, d.Groups())
		if gp, ok := d.Group(g); ok {
			seen += fmt.Sprintf("%+v", gp)
		}
		if strings.Contains(seen, "Zorblax") {
			t.Errorf("%s sees the contact household", name)
		}
		if !strings.Contains(seen, "Hank") {
			t.Errorf("%s doesn't see the real household", name)
		}
	}
}
