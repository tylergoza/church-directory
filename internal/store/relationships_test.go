package store

import (
	"errors"
	"testing"
)

func TestRelationshipRules(t *testing.T) {
	st := openTest(t)
	a, b, c := mustPerson(t, st, "Ann", "Lee", "", ""), mustPerson(t, st, "Bob", "Lee", "", ""), mustPerson(t, st, "Cy", "Lee", "", "")
	if err := st.AddRelationship(RelParent, a, a, 0); !errors.Is(err, ErrSelfLink) {
		t.Errorf("self link = %v", err)
	}
	if err := st.AddRelationship("sibling", a, b, 0); !errors.Is(err, ErrBadKind) {
		t.Errorf("bad kind = %v", err)
	}
	if err := st.AddRelationship(RelParent, a, b, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRelationship(RelParent, a, b, 0); err != nil { // again: no-op
		t.Fatal(err)
	}
	if err := st.AddRelationship(RelParent, b, c, 0); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]int64{{b, a}, {c, a}, {c, b}} {
		if err := st.AddRelationship(RelParent, bad[0], bad[1], 0); !errors.Is(err, ErrCycle) {
			t.Errorf("parent %d of %d = %v, want a cycle refusal", bad[0], bad[1], err)
		}
	}
	// A couple is one row whichever way round it is given.
	if err := st.AddRelationship(RelSpouse, c, a, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRelationship(RelSpouse, a, c, 0); err != nil {
		t.Fatal(err)
	}
	rels, _ := st.AllRelationships()
	if len(rels) != 3 {
		t.Errorf("relationships = %+v", rels)
	}
	links, _ := st.ListPersonLinks(a)
	if len(links.Children) != 1 || links.Children[0].ID != b || len(links.Spouses) != 1 || links.Spouses[0].ID != c || len(links.Parents) != 0 {
		t.Errorf("links of a = %+v", links)
	}
	links, _ = st.ListPersonLinks(c)
	if len(links.Parents) != 1 || links.Parents[0].ID != b || len(links.Spouses) != 1 {
		t.Errorf("links of c = %+v", links)
	}
	if err := st.RemoveRelationship(RelSpouse, a, c, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveRelationship(RelSpouse, a, c, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice = %v", err)
	}
	if ch, _ := st.RecentChanges(a, 0, 20); len(ch) < 2 {
		t.Errorf("changes = %+v", ch)
	}
}

func linkLogin(t *testing.T, st *Store, person int64) {
	t.Helper()
	uid, err := st.CreateUser("u"+itoaTest(person), "", "a long password", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, uid, person); err != nil {
		t.Fatal(err)
	}
}

func itoaTest(n int64) string {
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestStartOwnHousehold(t *testing.T) {
	st := openTest(t)
	old, _ := st.CreateHousehold("The Lees", "", 0)
	dad, mom := mustPerson(t, st, "Dad", "Lee", "", ""), mustPerson(t, st, "Mom", "Lee", "", "")
	son, wife := mustPerson(t, st, "Son", "Lee", "", ""), mustPerson(t, st, "Wife", "Lee", "", "")
	kid, sis := mustPerson(t, st, "Kid", "Lee", "", ""), mustPerson(t, st, "Sis", "Lee", "", "")
	st.PlaceMember(old, dad, RoleHead, 0, 0)
	st.PlaceMember(old, mom, RoleHead, 0, 0)
	st.PlaceMember(old, son, RoleChild, 0, 0)
	st.PlaceMember(old, wife, RoleAdult, 0, 0)
	st.PlaceMember(old, kid, RoleChild, 0, 0)
	st.PlaceMember(old, sis, RoleChild, 0, 0)
	st.AddRelationship(RelParent, dad, son, 0)
	st.AddRelationship(RelParent, son, kid, 0)
	st.AddRelationship(RelSpouse, son, wife, 0)

	if _, err := st.StartOwnHousehold(son, " ", 0, nil, 0, 0); err == nil {
		t.Error("a household needs a name")
	}
	if _, err := st.StartOwnHousehold(son, "The Son Lees", son, nil, 0, dad); !errors.Is(err, ErrOwnHousehold) {
		t.Errorf("spouse = self: %v", err)
	}
	hid, err := st.StartOwnHousehold(son, "The Son Lees", wife, []int64{kid}, 0, dad)
	if err != nil {
		t.Fatal(err)
	}
	roles := memberRoles(t, st, hid)
	if roles[son] != RoleHead || roles[wife] != RoleHead || roles[kid] != RoleChild || len(roles) != 3 {
		t.Errorf("new household = %v", roles)
	}
	if r := memberRoles(t, st, old); len(r) != 3 || r[sis] != RoleChild {
		t.Errorf("old household = %v", r)
	}
	// Links untouched.
	if rels, _ := st.AllRelationships(); len(rels) != 3 {
		t.Errorf("links changed: %+v", rels)
	}
	h, _ := st.GetHousehold(hid)
	if h.ManagedBy != dad {
		t.Errorf("managed_by = %d, want %d", h.ManagedBy, dad)
	}
	if by, active, _ := st.ManagedByActive(hid); by != dad || !active {
		t.Errorf("active = %d %v", by, active)
	}
	// A linked head ends it: here, by giving the son a login.
	linkLogin(t, st, son)
	if _, active, _ := st.ManagedByActive(hid); active {
		t.Error("managed_by should not be active once a head has a login")
	}
	if err := st.SettleManagedByPerson(son, 0); err != nil {
		t.Fatal(err)
	}
	if h, _ := st.GetHousehold(hid); h.ManagedBy != 0 {
		t.Errorf("managed_by not cleared: %d", h.ManagedBy)
	}
}

func TestManagedByClearsWhenLinkedHeadIsMade(t *testing.T) {
	st := openTest(t)
	dad := mustPerson(t, st, "Dad", "Lee", "", "")
	son, friend := mustPerson(t, st, "Son", "Lee", "", ""), mustPerson(t, st, "Pal", "Lee", "", "")
	hid, err := st.StartOwnHousehold(son, "The Son Lees", 0, nil, 0, dad)
	if err != nil {
		t.Fatal(err)
	}
	linkLogin(t, st, friend)
	// An adult with a login doesn't end it; a head with one does.
	st.PlaceMember(hid, friend, RoleAdult, 0, 0)
	if h, _ := st.GetHousehold(hid); h.ManagedBy != dad {
		t.Error("an adult with a login must not end managed_by")
	}
	st.PlaceMember(hid, friend, RoleHead, 0, 0)
	if h, _ := st.GetHousehold(hid); h.ManagedBy != 0 {
		t.Error("making a linked head should clear managed_by")
	}
}

func TestStartOwnHouseholdByStaffLeavesManagedByNull(t *testing.T) {
	st := openTest(t)
	p := mustPerson(t, st, "Pat", "Lee", "", "")
	hid, err := st.StartOwnHousehold(p, "The Pat Lees", 0, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := st.GetHousehold(hid); h.ManagedBy != 0 {
		t.Error("staff start leaves managed_by NULL")
	}
}
