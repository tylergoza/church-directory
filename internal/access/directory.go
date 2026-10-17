package access

import (
	"sort"

	"github.com/tylergoza/church-directory/internal/store"
)

// Directory is everything the visibility rules need, loaded once per
// request: all the people, households, memberships, addresses and settings,
// with the viewer they were loaded for. A church directory is small, so one
// pass of a handful of queries beats a query per field.
type Directory struct {
	Viewer   *Viewer
	Defaults store.Defaults

	people         map[int64]*PersonAccess
	peopleOrder    []int64
	households     map[int64]*HouseholdAccess
	householdOrder []int64

	family *familyIndex // the parent and spouse links; see family.go

	groups       []store.Group // in order; see groups.go
	groupMembers map[int64][]store.GroupMembership
}

// Load reads the directory for a viewer.
func Load(st *store.Store, v *Viewer) (*Directory, error) {
	defaults, err := st.LoadDefaults()
	if err != nil {
		return nil, err
	}
	people, err := st.AllPeople()
	if err != nil {
		return nil, err
	}
	households, err := st.AllHouseholds()
	if err != nil {
		return nil, err
	}
	members, err := st.AllMemberships()
	if err != nil {
		return nil, err
	}
	addrs, err := st.AllAddresses()
	if err != nil {
		return nil, err
	}
	pset, err := st.AllFieldSettings()
	if err != nil {
		return nil, err
	}
	hset, err := st.AllHouseholdSettings()
	if err != nil {
		return nil, err
	}
	d := Build(v, defaults, people, households, members, addrs, pset, hset)
	if err := d.loadGroups(st); err != nil {
		return nil, err
	}
	rels, err := st.AllRelationships()
	if err != nil {
		return nil, err
	}
	d.SetRelationships(rels)
	return d, nil
}

// Build assembles a Directory from rows already read (tests use it too).
func Build(v *Viewer, defaults store.Defaults, people []store.Person, households []store.Household,
	members []store.Membership, addrs []store.Address,
	pset, hset map[int64]map[store.Field]store.FieldSetting) *Directory {

	d := &Directory{Viewer: v, Defaults: defaults,
		people: map[int64]*PersonAccess{}, households: map[int64]*HouseholdAccess{}}
	for _, h := range households {
		d.households[h.ID] = &HouseholdAccess{Household: h, Settings: hset[h.ID], Defaults: defaults}
		d.householdOrder = append(d.householdOrder, h.ID)
	}
	for _, p := range people {
		d.people[p.ID] = &PersonAccess{Person: p, Settings: pset[p.ID], Defaults: defaults}
		d.peopleOrder = append(d.peopleOrder, p.ID)
	}
	for _, a := range addrs {
		if h := d.households[a.HouseholdID]; h != nil {
			h.Addresses = append(h.Addresses, a)
		}
	}
	for _, m := range members {
		h, p := d.households[m.HouseholdID], d.people[m.PersonID]
		if h == nil || p == nil {
			continue
		}
		h.Members = append(h.Members, m)
		p.Memberships = append(p.Memberships, m)
		if h.Household.ManagedBy != 0 {
			p.Managers = append(p.Managers, h.Household.ManagedBy)
		}
	}
	// A managed household's manager keeps their rights until a head has a
	// login (store.ManagedByActive, worked out here from the rows read).
	for _, h := range d.households {
		if h.Household.ManagedBy == 0 {
			continue
		}
		active := true
		for _, m := range h.Members {
			if m.Role == store.RoleHead && d.people[m.PersonID].Person.UserID != 0 {
				active = false
			}
		}
		h.ManagedActive = active
		if !active {
			continue
		}
		for _, m := range h.Members {
			d.people[m.PersonID].ActiveManagers = append(d.people[m.PersonID].ActiveManagers, h.Household.ManagedBy)
		}
	}
	return d
}

// EditableHouseholds lists the households the viewer may edit as a head or
// as the person managing them (not archived), in the order they were loaded.
func (d *Directory) EditableHouseholds() []*HouseholdAccess {
	var out []*HouseholdAccess
	for _, id := range d.householdOrder {
		h := d.households[id]
		if !h.Household.Archived() && CanHeadEditHousehold(d.Viewer, h) {
			out = append(out, h)
		}
	}
	return out
}

// PeopleIDs lists every person's id in load order (callers still check
// CanSeePerson before showing anyone).
func (d *Directory) PeopleIDs() []int64 { return d.peopleOrder }

// ListedHouseholds lists the member households the viewer can see (not
// contact, archived or pending ones), by name: the choices for "add to
// another household".
func (d *Directory) ListedHouseholds() []*HouseholdAccess {
	var out []*HouseholdAccess
	for _, id := range d.householdOrder {
		h := d.households[id]
		if h.Household.Kind == store.KindMember && !h.Household.Archived() && !h.Household.Pending && d.CanSeeHousehold(h) {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return nameKey(out[i].Household.Name) < nameKey(out[j].Household.Name) })
	return out
}

// Person is the access bundle for a person, whether or not the viewer may
// see them (check CanSeePerson).
func (d *Directory) Person(id int64) (*PersonAccess, bool) {
	a, ok := d.people[id]
	return a, ok
}

// Household is the access bundle for a household.
func (d *Directory) Household(id int64) (*HouseholdAccess, bool) {
	h, ok := d.households[id]
	return h, ok
}

// CanSeePerson is CanSeePerson for the directory's own viewer.
func (d *Directory) CanSeePerson(a *PersonAccess) bool { return CanSeePerson(d.Viewer, a) }

// CanSeeHousehold is CanSeeHousehold for the directory's own viewer.
func (d *Directory) CanSeeHousehold(h *HouseholdAccess) bool { return CanSeeHousehold(d.Viewer, h) }
