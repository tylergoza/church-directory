package store

import (
	"errors"
	"testing"
)

func memberRoles(t *testing.T, st *Store, hid int64) map[int64]string {
	t.Helper()
	ms, err := st.HouseholdMembers(hid)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]string{}
	for _, m := range ms {
		out[m.ID] = m.Role
	}
	return out
}

func TestHouseholdLifecycle(t *testing.T) {
	st := openTest(t)
	by, _ := st.CreateUser("sam", "", "a long password", RoleEditor)
	if _, err := st.CreateHousehold("  ", "", by); err == nil {
		t.Error("a household needs a name")
	}
	id, err := st.CreateHousehold("The Goza Family", "", by)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateHousehold(id, "The Gozas", KindMember, by); err != nil {
		t.Fatal(err)
	}
	h, _ := st.GetHousehold(id)
	if h.Name != "The Gozas" || h.Kind != KindMember || h.Archived() {
		t.Errorf("household = %+v", h)
	}
	if err := st.ArchiveHousehold(id, true, by); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.ListHouseholds(HouseholdsAll, ""); len(rows) != 0 {
		t.Error("archived household listed")
	}
	if rows, _ := st.ListHouseholds(HouseholdsArchived, ""); len(rows) != 1 {
		t.Error("archived household missing from archive")
	}
	st.ArchiveHousehold(id, false, by)
	p := mustPerson(t, st, "Tyler", "Goza", "", "")
	st.PlaceMember(id, p, RoleHead, 0, by)
	if rows, _ := st.ListHouseholds(HouseholdsAll, "tyler"); len(rows) != 1 || len(rows[0].Members) != 1 {
		t.Errorf("search by member: %+v", rows)
	}
	changes, _ := st.RecentChanges(0, id, 20)
	if len(changes) < 4 {
		t.Errorf("household changes: %+v", changes)
	}
	if _, err := st.DeleteHousehold(id, by); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPerson(p); err != nil {
		t.Error("deleting a household keeps its people")
	}
	if ms, _ := st.PersonHouseholds(p); len(ms) != 0 {
		t.Error("membership should go with the household")
	}
}

func TestAddresses(t *testing.T) {
	st := openTest(t)
	hid, _ := st.CreateHousehold("The Gozas", "", 0)
	other, _ := st.CreateHousehold("The Smiths", "", 0)
	main, err := st.AddAddress(hid, AddressInput{Label: "Main", Address1: "123 Oak St", City: "Springfield", State: "IL", Postal: "62701"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	grand, _ := st.AddAddress(hid, AddressInput{Label: "Grandparents", Address1: "45 Elm Rd"}, 0)
	theirs, _ := st.AddAddress(other, AddressInput{Address1: "9 Pine"}, 0)
	addrs, _ := st.ListAddresses(hid)
	if len(addrs) != 2 || addrs[0].ID != main || addrs[1].ID != grand || addrs[0].OneLine() != "123 Oak St, Springfield, IL 62701" {
		t.Errorf("addresses = %+v", addrs)
	}
	if bad := (&AddressInput{Label: "x"}).Validate(); len(bad) == 0 {
		t.Error("an address needs something in it")
	}

	dad := mustPerson(t, st, "Tyler", "Goza", "", "")
	gran := mustPerson(t, st, "Bob", "Goza", "", "")
	if err := st.PlaceMember(hid, dad, RoleHead, main, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.PlaceMember(hid, gran, RoleAdult, grand, 0); err != nil {
		t.Fatal(err)
	}
	// Another household's address is refused.
	if err := st.PlaceMember(hid, dad, RoleHead, theirs, 0); !errors.Is(err, ErrWrongHousehold) {
		t.Errorf("foreign address: %v", err)
	}
	// Deleting an address with people needs somewhere for them to go.
	if err := st.DeleteAddress(grand, 0, 0); !errors.Is(err, ErrNeedsReassign) {
		t.Errorf("delete without reassign: %v", err)
	}
	if err := st.DeleteAddress(grand, theirs, 0); !errors.Is(err, ErrWrongHousehold) {
		t.Errorf("reassign to another household's address: %v", err)
	}
	if err := st.DeleteAddress(grand, main, 0); err != nil {
		t.Fatal(err)
	}
	ms, _ := st.HouseholdMembers(hid)
	for _, m := range ms {
		if m.AddressID != main {
			t.Errorf("%s at address %d, want %d", m.Name(), m.AddressID, main)
		}
	}
	// With nobody there, it deletes freely; "no address" clears people.
	empty, _ := st.AddAddress(hid, AddressInput{Address1: "1 Empty Way"}, 0)
	if err := st.DeleteAddress(empty, 0, 0); err != nil {
		t.Errorf("delete unused address: %v", err)
	}
	if err := st.DeleteAddress(main, NoAddress, 0); err != nil {
		t.Fatal(err)
	}
	ms, _ = st.HouseholdMembers(hid)
	for _, m := range ms {
		if m.AddressID != 0 {
			t.Errorf("%s still has address %d", m.Name(), m.AddressID)
		}
	}
	if err := st.UpdateAddress(9999, AddressInput{Address1: "x"}, 0); err != ErrNotFound {
		t.Errorf("update missing address: %v", err)
	}
}

func TestMembersAndMoving(t *testing.T) {
	st := openTest(t)
	a, _ := st.CreateHousehold("The Gozas", "", 0)
	b, _ := st.CreateHousehold("The Smiths", "", 0)
	dad := mustPerson(t, st, "Tyler", "Goza", "", "")
	mom := mustPerson(t, st, "Jane", "Goza", "", "")
	kid := mustPerson(t, st, "Sam", "Goza", "", "")

	// More than one head.
	st.PlaceMember(a, dad, RoleHead, 0, 0)
	st.PlaceMember(a, mom, RoleHead, 0, 0)
	st.PlaceMember(a, kid, RoleChild, 0, 0)
	roles := memberRoles(t, st, a)
	if roles[dad] != "head" || roles[mom] != "head" || roles[kid] != "child" {
		t.Errorf("roles = %v", roles)
	}
	ms, _ := st.HouseholdMembers(a)
	if ms[0].ID != dad || ms[1].ID != mom || ms[2].ID != kid {
		t.Errorf("order = %v %v %v", ms[0].ID, ms[1].ID, ms[2].ID)
	}
	// Changing a role in place.
	st.PlaceMember(a, mom, RoleAdult, 0, 0)
	if memberRoles(t, st, a)[mom] != "adult" {
		t.Error("role not changed")
	}
	// Moving an adult takes them out of the old household.
	if err := st.PlaceMember(b, mom, RoleHead, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, in := memberRoles(t, st, a)[mom]; in {
		t.Error("mom is still in the first household")
	}
	if memberRoles(t, st, b)[mom] != "head" {
		t.Error("mom not in the new household")
	}
	// A child moves too, unless they live in more than one household.
	st.PlaceMember(b, kid, RoleChild, 0, 0)
	if _, in := memberRoles(t, st, a)[kid]; in {
		t.Error("child should have moved")
	}
	st.DB.Exec(`UPDATE people SET multi_household = 1, multi_reason = 'divorced' WHERE id = ?`, kid)
	st.PlaceMember(a, kid, RoleChild, 0, 0)
	if memberRoles(t, st, a)[kid] != "child" || memberRoles(t, st, b)[kid] != "child" {
		t.Error("a child in two households should be in both")
	}
	// But as an adult they would leave both.
	st.PlaceMember(a, kid, RoleAdult, 0, 0)
	if _, in := memberRoles(t, st, b)[kid]; in {
		t.Error("an adult has one home")
	}
	// Remove.
	if err := st.RemoveMember(a, dad, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveMember(a, dad, 0); err != ErrNotFound {
		t.Errorf("removing twice: %v", err)
	}
	if hs, _ := st.PersonHouseholds(dad); len(hs) != 0 {
		t.Error("dad should be in no household")
	}
	if _, err := st.GetPerson(dad); err != nil {
		t.Error("removing from a household keeps the person")
	}
	if err := st.PlaceMember(a, dad, "boss", 0, 0); !errors.Is(err, ErrBadRole) {
		t.Errorf("bad role: %v", err)
	}
	if err := st.PlaceMember(a, 9999, RoleAdult, 0, 0); err != ErrNotFound {
		t.Errorf("missing person: %v", err)
	}
	// The move was written to the audit trail.
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM changes WHERE what = 'moved out of household' AND person_id = ?`, mom).Scan(&n)
	if n != 1 {
		t.Errorf("moved-out rows for mom = %d", n)
	}
}
