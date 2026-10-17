package access

import (
	"sort"

	"github.com/tylergoza/church-directory/internal/store"
)

// Family across households: parents, spouse, siblings, children,
// grandparents and grandchildren are worked out from the stored parent and
// spouse links and never stored. Everything here goes through CanSeePerson
// and CanSeeHousehold, so it shows nothing the directory doesn't already
// show this viewer; it only connects it. A link that runs through someone
// the viewer can't see (a hidden contact, an archived parent) is not
// followed either.

// familyIndex holds the links as lookups.
type familyIndex struct {
	parents  map[int64][]int64 // child -> parents
	children map[int64][]int64 // parent -> children
	spouses  map[int64][]int64
}

// SetRelationships gives the directory the stored links (Load does this).
func (d *Directory) SetRelationships(rels []store.Relationship) {
	ix := &familyIndex{parents: map[int64][]int64{}, children: map[int64][]int64{}, spouses: map[int64][]int64{}}
	for _, r := range rels {
		switch r.Kind {
		case store.RelParent:
			ix.children[r.PersonID] = append(ix.children[r.PersonID], r.RelatedID)
			ix.parents[r.RelatedID] = append(ix.parents[r.RelatedID], r.PersonID)
		case store.RelSpouse:
			ix.spouses[r.PersonID] = append(ix.spouses[r.PersonID], r.RelatedID)
			ix.spouses[r.RelatedID] = append(ix.spouses[r.RelatedID], r.PersonID)
		}
	}
	d.family = ix
}

// FamilyMember is a relative on a person's page: a name, and the
// households (that the viewer may see) they are in.
type FamilyMember struct {
	ID         int64
	Name       string
	Households []FamilyHousehold
}

// FamilyHousehold links to a household.
type FamilyHousehold struct {
	ID   int64
	Name string
}

// FamilyGroup is one kind of relative: "Parents", "Children" and so on.
type FamilyGroup struct {
	Label  string
	People []FamilyMember
}

// FamilySection is the Family section of a person's page.
type FamilySection struct {
	Groups []FamilyGroup
}

// Empty reports whether there is nothing to show.
func (f FamilySection) Empty() bool { return len(f.Groups) == 0 }

// RelatedHousehold is a household on a household page's Related households.
type RelatedHousehold struct {
	ID   int64
	Name string
}

func (d *Directory) ix() *familyIndex {
	if d.family == nil {
		d.family = &familyIndex{}
	}
	return d.family
}

// visibleIDs keeps the people the viewer may see.
func (d *Directory) visibleIDs(ids []int64) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		a, ok := d.people[id]
		if !ok || seen[id] || !d.CanSeePerson(a) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Derived relatives of a person, as IDs the viewer may see. Each step
// goes only through people the viewer may see.
func (d *Directory) parentsOf(id int64) []int64  { return d.visibleIDs(d.ix().parents[id]) }
func (d *Directory) childrenOf(id int64) []int64 { return d.visibleIDs(d.ix().children[id]) }
func (d *Directory) spousesOf(id int64) []int64  { return d.visibleIDs(d.ix().spouses[id]) }

func (d *Directory) siblingsOf(id int64) []int64 {
	var out []int64
	for _, p := range d.parentsOf(id) {
		for _, c := range d.childrenOf(p) {
			if c != id {
				out = append(out, c)
			}
		}
	}
	return d.visibleIDs(out)
}

func (d *Directory) grandparentsOf(id int64) []int64 {
	var out []int64
	for _, p := range d.parentsOf(id) {
		out = append(out, d.parentsOf(p)...)
	}
	return d.visibleIDs(out)
}

func (d *Directory) grandchildrenOf(id int64) []int64 {
	var out []int64
	for _, c := range d.childrenOf(id) {
		out = append(out, d.childrenOf(c)...)
	}
	return d.visibleIDs(out)
}

// member builds a FamilyMember: the name through the view model.
func (d *Directory) member(id int64) FamilyMember {
	a := d.people[id]
	fm := FamilyMember{ID: id, Name: NewPersonView(d.Viewer, a).Name}
	for _, m := range a.Memberships {
		h := d.households[m.HouseholdID]
		if d.CanSeeHousehold(h) {
			fm.Households = append(fm.Households, FamilyHousehold{ID: h.Household.ID, Name: h.Household.Name})
		}
	}
	sort.SliceStable(fm.Households, func(i, j int) bool { return nameKey(fm.Households[i].Name) < nameKey(fm.Households[j].Name) })
	return fm
}

// Family section of a person's page. Empty when the viewer can't see the
// person or they have no visible relatives.
func (d *Directory) familySection(id int64) FamilySection {
	var sec FamilySection
	for _, g := range []struct {
		label string
		ids   []int64
	}{
		{"Parents", d.parentsOf(id)},
		{"Spouse", d.spousesOf(id)},
		{"Siblings", d.siblingsOf(id)},
		{"Children", d.childrenOf(id)},
		{"Grandparents", d.grandparentsOf(id)},
		{"Grandchildren", d.grandchildrenOf(id)},
	} {
		if len(g.ids) == 0 {
			continue
		}
		fg := FamilyGroup{Label: g.label}
		for _, rid := range g.ids {
			fg.People = append(fg.People, d.member(rid))
		}
		sort.SliceStable(fg.People, func(i, j int) bool { return nameKey(fg.People[i].Name) < nameKey(fg.People[j].Name) })
		sec.Groups = append(sec.Groups, fg)
	}
	return sec
}

// relatedHouseholds lists the households (other than this one) where
// someone is a parent, child, grandparent or grandchild of a person in it.
func (d *Directory) relatedHouseholds(h *HouseholdAccess) []RelatedHousehold {
	var out []RelatedHousehold
	seen := map[int64]bool{h.Household.ID: true}
	for _, m := range h.Members {
		a := d.people[m.PersonID]
		if a == nil || !d.CanSeePerson(a) {
			continue
		}
		var rel []int64
		rel = append(rel, d.parentsOf(m.PersonID)...)
		rel = append(rel, d.childrenOf(m.PersonID)...)
		rel = append(rel, d.grandparentsOf(m.PersonID)...)
		rel = append(rel, d.grandchildrenOf(m.PersonID)...)
		for _, rid := range rel {
			for _, rm := range d.people[rid].Memberships {
				oh := d.households[rm.HouseholdID]
				if oh == nil || seen[oh.Household.ID] || !d.CanSeeHousehold(oh) {
					continue
				}
				seen[oh.Household.ID] = true
				out = append(out, RelatedHousehold{ID: oh.Household.ID, Name: oh.Household.Name})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return nameKey(out[i].Name) < nameKey(out[j].Name) })
	return out
}
