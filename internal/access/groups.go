package access

import (
	"github.com/tylergoza/church-directory/internal/store"
)

// Groups are labels, not permissions. A group shows to members when
// visible_to is 'members' and to staff always; archived groups show to
// nobody. Every group page, the directory filter, a person's page, the
// print view and the API ask these.

func (d *Directory) loadGroups(st *store.Store) error {
	groups, err := st.AllGroups()
	if err != nil {
		return err
	}
	ms, err := st.AllGroupMemberships()
	if err != nil {
		return err
	}
	d.SetGroups(groups, ms)
	return nil
}

// SetGroups gives the directory its groups and memberships (Load does it;
// tests that use Build do it by hand). Memberships must be in order.
func (d *Directory) SetGroups(groups []store.Group, members []store.GroupMembership) {
	d.groups = groups
	d.groupMembers = map[int64][]store.GroupMembership{}
	for _, m := range members {
		d.groupMembers[m.GroupID] = append(d.groupMembers[m.GroupID], m)
	}
}

// GroupVisible reports whether the viewer may see the group at all.
func GroupVisible(v *Viewer, g store.Group) bool {
	if g.Archived() {
		return false
	}
	return g.VisibleTo == store.GroupMembers || (v != nil && v.Staff)
}

// GroupLink is a group on a list, or on a person's page.
type GroupLink struct {
	ID          int64
	Name        string
	Description string
	StaffOnly   bool
	Count       int    // people in it the viewer can see
	Label       string // the person's label, on a person's page
}

func (d *Directory) group(id int64) (store.Group, bool) {
	for _, g := range d.groups {
		if g.ID == id && GroupVisible(d.Viewer, g) {
			return g, true
		}
	}
	return store.Group{}, false
}

// listable: who of a group's members the viewer may see, listed as people
// (members, not contacts or archived).
func (d *Directory) groupPeople(id int64) []store.GroupMembership {
	var out []store.GroupMembership
	for _, m := range d.groupMembers[id] {
		a := d.people[m.PersonID]
		if a == nil || a.Person.Kind != store.KindMember || a.Person.Archived() || !d.CanSeePerson(a) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Groups lists the groups the viewer may see, in order.
func (d *Directory) Groups() []GroupLink {
	var out []GroupLink
	for _, g := range d.groups {
		if !GroupVisible(d.Viewer, g) {
			continue
		}
		out = append(out, GroupLink{ID: g.ID, Name: g.Name, Description: g.Description,
			StaffOnly: g.StaffOnly(), Count: len(d.groupPeople(g.ID))})
	}
	return out
}

// GroupPerson is a person in a group page, with their label.
type GroupPerson struct {
	Person PersonView
	Label  string
}

// GroupPage is a group and its people as far as the viewer may see them.
type GroupPage struct {
	GroupLink
	People []GroupPerson
}

// Group builds a group's page, or false when the viewer may not see the
// group (the handler answers 404). People the viewer can't see are left
// out; the rest are views that only hold what each person shows.
func (d *Directory) Group(id int64) (GroupPage, bool) {
	g, ok := d.group(id)
	if !ok {
		return GroupPage{}, false
	}
	gp := GroupPage{GroupLink: GroupLink{ID: g.ID, Name: g.Name, Description: g.Description, StaffOnly: g.StaffOnly()}}
	for _, m := range d.groupPeople(id) {
		gp.People = append(gp.People, GroupPerson{Person: NewPersonView(d.Viewer, d.people[m.PersonID]), Label: m.Label})
	}
	gp.Count = len(gp.People)
	return gp, true
}

// GroupMemberIDs is the set of visible people in a group the viewer may
// see, for the directory filter; false when the viewer may not see it.
func (d *Directory) GroupMemberIDs(id int64) (map[int64]bool, bool) {
	if _, ok := d.group(id); !ok {
		return nil, false
	}
	set := map[int64]bool{}
	for _, m := range d.groupPeople(id) {
		set[m.PersonID] = true
	}
	return set, true
}

// ListingGroup is Listing narrowed to one group. False when the viewer may
// not see the group.
func (d *Directory) ListingGroup(q string, groupID int64) (Listing, bool) {
	set, ok := d.GroupMemberIDs(groupID)
	if !ok {
		return Listing{}, false
	}
	return d.listing(q, set), true
}

// personGroups lists the groups a person is in that the viewer may see.
func (d *Directory) personGroups(personID int64) []GroupLink {
	var out []GroupLink
	for _, g := range d.groups {
		if !GroupVisible(d.Viewer, g) {
			continue
		}
		for _, m := range d.groupMembers[g.ID] {
			if m.PersonID == personID {
				out = append(out, GroupLink{ID: g.ID, Name: g.Name, StaffOnly: g.StaffOnly(), Label: m.Label})
			}
		}
	}
	return out
}

// anyIn reports whether someone on the family card is in the set.
func anyIn(fv FamilyView, set map[int64]bool) bool {
	for _, g := range fv.Groups {
		for _, p := range g.People {
			if set[p.ID] {
				return true
			}
		}
	}
	return false
}
