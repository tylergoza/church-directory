package access

import (
	"sort"
	"strconv"

	"github.com/tylergoza/church-directory/internal/store"
)

// The print view (step 10): the same view models the screens use, so it
// shows exactly what the person printing could see. It never touches
// notes, contacts or staff-only groups.

// IndexHousehold is a household an indexed person is found in.
type IndexHousehold struct {
	Anchor string // "h-12", or "p-7" for a person in no household
	Name   string
}

// IndexEntry is one line of the person index: a name and where to find them.
type IndexEntry struct {
	Name       string
	Households []IndexHousehold
	key        string
}

// PrintBook is everything the print page holds.
type PrintBook struct {
	Listing Listing
	Groups  []GroupPage // groups members can see; never staff-only ones
	Months  []MonthView // all twelve, January first
	Index   []IndexEntry
}

// Print builds the book. groupID 0 is the whole directory; otherwise the
// households and dates are narrowed to that group, which is then the only
// group printed. False when the viewer may not see the group, or it is a
// staff-only group (those are never printed).
func (d *Directory) Print(groupID int64) (PrintBook, bool) {
	var b PrintBook
	var only map[int64]bool
	if groupID != 0 {
		g, ok := d.group(groupID)
		if !ok || g.StaffOnly() {
			return b, false
		}
		only, _ = d.GroupMemberIDs(groupID)
		b.Listing = d.listing("", only)
		if gp, ok := d.Group(groupID); ok {
			b.Groups = append(b.Groups, gp)
		}
	} else {
		b.Listing = d.listing("", nil)
		for _, g := range d.Groups() {
			if g.StaffOnly {
				continue
			}
			if gp, ok := d.Group(g.ID); ok && len(gp.People) > 0 {
				b.Groups = append(b.Groups, gp)
			}
		}
	}
	all := make([]int, 12)
	for i := range all {
		all[i] = i + 1
	}
	b.Months = d.birthdaysIn(all, only)

	byID := map[int64]*IndexEntry{}
	var order []int64
	add := func(p PersonView, h IndexHousehold) {
		e := byID[p.ID]
		if e == nil {
			e = &IndexEntry{Name: p.Name}
			if a := d.people[p.ID]; a != nil {
				e.key = store.Fold(a.Person.LastName + " " + a.Person.FirstName)
			}
			byID[p.ID] = e
			order = append(order, p.ID)
		}
		e.Households = append(e.Households, h)
	}
	for _, en := range b.Listing.Entries {
		if en.Family != nil {
			for _, g := range en.Family.Groups {
				for _, p := range g.People {
					add(p, IndexHousehold{Anchor: "h-" + itoa(en.Family.ID), Name: en.Family.Name})
				}
			}
		} else if en.Person != nil {
			add(*en.Person, IndexHousehold{Anchor: "p-" + itoa(en.Person.ID), Name: en.Person.Name})
		}
	}
	for _, id := range order {
		b.Index = append(b.Index, *byID[id])
	}
	sort.SliceStable(b.Index, func(i, j int) bool { return b.Index[i].key < b.Index[j].key })
	return b, true
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
