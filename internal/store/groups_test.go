package store

import (
	"errors"
	"testing"
)

func TestGroupsMembersAndOrder(t *testing.T) {
	st := openTest(t)
	mk := func(first, kind string) int64 {
		id, err := st.CreatePerson(PersonInput{FirstName: first, LastName: "Lee", Kind: kind}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ann, bob, cy := mk("Ann", KindMember), mk("Bob", KindMember), mk("Cy", KindMember)
	conny := mk("Conny", KindContact)
	if _, err := st.CreateGroup(GroupInput{Name: " "}, 0); err == nil {
		t.Error("a group needs a name")
	}
	gid, err := st.CreateGroup(GroupInput{Name: "Singers"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGroup(GroupInput{Name: "singers"}, 0); err == nil {
		t.Error("group names are unique, ignoring case")
	}
	for _, id := range []int64{cy, ann, bob} {
		if err := st.AddGroupMember(gid, id, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	names := func() string {
		ms, err := st.GroupMembersOf(gid)
		if err != nil {
			t.Fatal(err)
		}
		s := ""
		for _, m := range ms {
			s += m.Person.FirstName + " "
		}
		return s
	}
	if got := names(); got != "Cy Ann Bob " {
		t.Errorf("order = %q, want the order they were added", got)
	}
	st.MoveGroupMember(gid, bob, -1, 0)
	if got := names(); got != "Cy Bob Ann " {
		t.Errorf("after move up = %q", got)
	}
	st.SetGroupMemberPosition(gid, ann, 1, 0)
	if got := names(); got != "Ann Cy Bob " {
		t.Errorf("after set position = %q", got)
	}
	if err := st.AddGroupMember(gid, ann, "Chair", 0); err != nil { // already in: label only
		t.Fatal(err)
	}
	if ms, _ := st.GroupMembersOf(gid); len(ms) != 3 || ms[0].Label != "Chair" {
		t.Errorf("re-adding should set the label, got %+v", ms)
	}
	if err := st.AddGroupMember(gid, conny, "", 0); !errors.Is(err, ErrGroupContact) {
		t.Errorf("contact: err = %v, want ErrGroupContact", err)
	}
	st.ArchivePerson(cy, true, 0)
	other, _ := st.CreateGroup(GroupInput{Name: "Deacons", VisibleTo: GroupStaff}, 0)
	if err := st.AddGroupMember(other, cy, "", 0); !errors.Is(err, ErrGroupArchived) {
		t.Errorf("archived person: err = %v", err)
	}
	st.ArchiveGroup(other, true, 0)
	if err := st.AddGroupMember(other, ann, "", 0); !errors.Is(err, ErrGroupArchived) {
		t.Errorf("archived group: err = %v", err)
	}
	if rows, _ := st.ListGroups(false); len(rows) != 1 || rows[0].Count != 3 {
		t.Errorf("active groups = %+v", rows)
	}
	if rows, _ := st.ListGroups(true); len(rows) != 1 || rows[0].Name != "Deacons" {
		t.Errorf("archived groups = %+v", rows)
	}
	if gs, _ := st.PersonGroups(ann); len(gs) != 1 || gs[0].Name != "Singers" {
		t.Errorf("person groups = %+v", gs)
	}
	if err := st.RemoveGroupMember(gid, bob, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveGroupMember(gid, bob, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice: %v", err)
	}
}
