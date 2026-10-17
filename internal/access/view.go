package access

import (
	"sort"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

// The view models. Member-facing pages (and the print view and the API)
// get these and never a store.Person, so a field can only reach a template
// after Visible said yes: the constructors below are the only code that
// reads Email, Phone, Photo, Birth and Anniversary for display.

// DateView is a birthday or anniversary as far as the viewer may see it.
// Month and Day are 0 unless shown (the birthdays page needs both); Text
// is "March 14", "March 1985", "" and so on.
type DateView struct {
	Text       string
	Month, Day int
}

// PersonView is a person as far as the viewer may see them. The name
// always shows.
type PersonView struct {
	ID          int64
	Name        string
	Role        string // in the household it is shown under; "" outside one
	Child       bool
	Email       string
	Phone       string
	Photo       string // file name, "" if none or hidden
	Birthday    DateView
	Anniversary DateView
}

func dateView(v *Viewer, a *PersonAccess, p dates.Partial, month, day, year store.Field) DateView {
	show := dates.Shown{
		Month: Visible(v, a, month),
		Day:   Visible(v, a, day),
		Year:  Visible(v, a, year),
	}
	dv := DateView{Text: p.Format(show)}
	if show.Month && p.Month != 0 {
		dv.Month = p.Month
		if show.Day {
			dv.Day = p.Day
		}
	}
	return dv
}

// NewPersonView builds what the viewer may see of a person. Every field
// goes through Visible.
func NewPersonView(v *Viewer, a *PersonAccess) PersonView {
	pv := PersonView{ID: a.Person.ID, Name: a.Person.Name(), Child: a.Child()}
	if Visible(v, a, store.FieldEmail) {
		pv.Email = a.Person.Email
	}
	if Visible(v, a, store.FieldPhone) {
		pv.Phone = a.Person.Phone
	}
	if Visible(v, a, store.FieldPhoto) {
		pv.Photo = a.Person.Photo
	}
	pv.Birthday = dateView(v, a, a.Person.Birth, store.FieldBirthMonth, store.FieldBirthDay, store.FieldBirthYear)
	pv.Anniversary = dateView(v, a, a.Person.Anniversary, store.FieldAnniversaryMon, store.FieldAnniversaryDay, store.FieldAnniversaryYear)
	return pv
}

// AddressGroup is the people who live at one address. Address is "" for
// people with no address or one the viewer may not see.
type AddressGroup struct {
	Address string
	Label   string
	People  []PersonView
}

// FamilyView is a household card: its people grouped under their address,
// heads first.
type FamilyView struct {
	ID          int64
	Name        string
	Contact     bool   // a contact household (only shown to those who may see it)
	Photo       string // "" unless the viewer may see the household photo
	Groups      []AddressGroup
	Anniversary string // shared by the heads, shown once; their own then leave it out
	Related     []RelatedHousehold
	sortKey     string
}

// Family builds a household's card, or false when the viewer may not see
// the household. People the viewer may not see are left out. With listed
// set, contacts and archived people are left out too (the directory page
// never lists them, whoever is looking).
func (d *Directory) Family(id int64, listed bool) (FamilyView, bool) {
	h, ok := d.households[id]
	if !ok || !d.CanSeeHousehold(h) {
		return FamilyView{}, false
	}
	fv := FamilyView{ID: h.Household.ID, Name: h.Household.Name,
		Contact: h.Household.Kind == store.KindContact, sortKey: nameKey(h.Household.Name)}
	if VisibleHouseholdPhoto(d.Viewer, h) {
		fv.Photo = h.Household.Photo
	}
	byAddr := map[int64][]PersonView{}
	var heads []*PersonView
	for _, m := range h.Members { // already heads first
		a := d.people[m.PersonID]
		if !d.CanSeePerson(a) || (listed && (a.Person.Kind != store.KindMember || a.Person.Archived())) {
			continue
		}
		pv := NewPersonView(d.Viewer, a)
		pv.Role = m.Role
		addr := m.AddressID
		if !addressShown(d.Viewer, h, addr) {
			addr = 0
		}
		byAddr[addr] = append(byAddr[addr], pv)
	}
	// Groups in address order, then the people with no (shown) address.
	for _, ad := range h.Addresses {
		if people := byAddr[ad.ID]; len(people) > 0 {
			fv.Groups = append(fv.Groups, AddressGroup{Address: ad.OneLine(), Label: ad.Label, People: people})
		}
	}
	if people := byAddr[0]; len(people) > 0 {
		fv.Groups = append(fv.Groups, AddressGroup{People: people})
	}
	for gi := range fv.Groups {
		for pi := range fv.Groups[gi].People {
			pv := &fv.Groups[gi].People[pi]
			if pv.Role == store.RoleHead {
				heads = append(heads, pv)
			}
		}
	}
	// The anniversary shows once when every head (at least two) shows the
	// same date.
	if len(heads) >= 2 && heads[0].Anniversary.Text != "" {
		same := true
		for _, hv := range heads[1:] {
			if hv.Anniversary.Text != heads[0].Anniversary.Text {
				same = false
			}
		}
		if same {
			fv.Anniversary = heads[0].Anniversary.Text
			for _, hv := range heads {
				hv.Anniversary = DateView{}
			}
		}
	}
	fv.Related = d.relatedHouseholds(h)
	return fv, true
}

// addressShown: address 0 ("none") is never shown as an address.
func addressShown(v *Viewer, h *HouseholdAccess, addressID int64) bool {
	if addressID == 0 {
		return false
	}
	for _, a := range h.Addresses {
		if a.ID == addressID {
			return VisibleAddress(v, h, a)
		}
	}
	return false
}

// Entry is one thing in the directory list: a family card or a person who
// is in no household.
type Entry struct {
	Letter string
	Family *FamilyView
	Person *PersonView
	key    string
}

// LetterLink is one letter of the A–Z jump bar.
type LetterLink struct {
	Letter string
	Has    bool
}

// Anchor is the id of the letter's heading, "letter-G".
func (l LetterLink) Anchor() string { return anchor(l.Letter) }

// Anchor is the id of the heading this entry is listed under.
func (e Entry) Anchor() string { return anchor(e.Letter) }

func anchor(letter string) string {
	if letter == "#" {
		return "letter-other"
	}
	return "letter-" + letter
}

// Listing is the directory page.
type Listing struct {
	Entries []Entry
	Letters []LetterLink
	Count   int // people shown
}

// nameKey is what the A–Z order uses: the folded name without a leading
// "The" ("The Goza Family" sorts under G).
func nameKey(s string) string {
	k := store.Fold(s)
	if rest, ok := strings.CutPrefix(k, "the "); ok && rest != "" {
		k = rest
	}
	return k
}

func letterOf(key string) string {
	if key != "" && key[0] >= 'a' && key[0] <= 'z' {
		return strings.ToUpper(key[:1])
	}
	return "#"
}

// Listing builds the directory page: one card per household, people in no
// household on their own, sorted A–Z. Contacts, archived people and
// archived households never appear (not even for staff: this is the
// members' directory). q narrows it: every word must be in the household's
// name or one of the shown names, ignoring case and accents. Only names are
// searched, never a field that might be hidden.
func (d *Directory) Listing(q string) Listing { return d.listing(q, nil) }

// listing is Listing, narrowed to the people in only when it isn't nil: a
// household stays when someone shown in it is in the set.
func (d *Directory) listing(q string, only map[int64]bool) Listing {
	words := strings.Fields(store.Fold(q))
	var l Listing
	inHousehold := map[int64]bool{}
	for _, hid := range d.householdOrder {
		h := d.households[hid]
		for _, m := range h.Members {
			inHousehold[m.PersonID] = true
		}
		if h.Household.Kind != store.KindMember || h.Household.Archived() {
			continue
		}
		fv, ok := d.Family(hid, true)
		if !ok || len(fv.Groups) == 0 {
			continue
		}
		hay := store.Fold(fv.Name)
		n := 0
		for _, g := range fv.Groups {
			for _, p := range g.People {
				hay += " " + store.Fold(p.Name)
				n++
			}
		}
		if !hasAll(hay, words) {
			continue
		}
		if only != nil && !anyIn(fv, only) {
			continue
		}
		f := fv
		l.Entries = append(l.Entries, Entry{Family: &f, key: f.sortKey})
		l.Count += n
	}
	for _, pid := range d.peopleOrder {
		a := d.people[pid]
		if inHousehold[pid] || a.Person.Kind != store.KindMember || a.Person.Archived() || !d.CanSeePerson(a) {
			continue
		}
		if only != nil && !only[pid] {
			continue
		}
		pv := NewPersonView(d.Viewer, a)
		if !hasAll(store.Fold(pv.Name), words) {
			continue
		}
		l.Entries = append(l.Entries, Entry{Person: &pv, key: store.Fold(a.Person.LastName + " " + a.Person.FirstName)})
		l.Count++
	}
	sort.SliceStable(l.Entries, func(i, j int) bool { return l.Entries[i].key < l.Entries[j].key })
	has := map[string]bool{}
	for i := range l.Entries {
		l.Entries[i].Letter = letterOf(l.Entries[i].key)
		has[l.Entries[i].Letter] = true
	}
	for c := 'A'; c <= 'Z'; c++ {
		l.Letters = append(l.Letters, LetterLink{Letter: string(c), Has: has[string(c)]})
	}
	if has["#"] {
		l.Letters = append(l.Letters, LetterLink{Letter: "#", Has: true})
	}
	return l
}

func hasAll(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// HouseholdLink is a household on a person's page.
type HouseholdLink struct {
	ID      int64
	Name    string
	Role    string
	Address string // "" unless the viewer may see it
}

// PersonPage is a person's own page.
type PersonPage struct {
	Person     PersonView
	Households []HouseholdLink
	Groups     []GroupLink // the groups this viewer may see them in
	Family     FamilySection
}

// Page builds a person's page, or false when the viewer may not see the
// person at all (the handler answers 404).
func (d *Directory) Page(id int64) (PersonPage, bool) {
	a, ok := d.people[id]
	if !ok || !d.CanSeePerson(a) {
		return PersonPage{}, false
	}
	pp := PersonPage{Person: NewPersonView(d.Viewer, a)}
	for _, m := range a.Memberships {
		h := d.households[m.HouseholdID]
		if !d.CanSeeHousehold(h) {
			continue
		}
		hl := HouseholdLink{ID: h.Household.ID, Name: h.Household.Name, Role: m.Role}
		if addressShown(d.Viewer, h, m.AddressID) {
			for _, ad := range h.Addresses {
				if ad.ID == m.AddressID {
					hl.Address = ad.OneLine()
				}
			}
		}
		pp.Households = append(pp.Households, hl)
	}
	sort.SliceStable(pp.Households, func(i, j int) bool { return nameKey(pp.Households[i].Name) < nameKey(pp.Households[j].Name) })
	pp.Groups = d.personGroups(id)
	pp.Family = d.familySection(id)
	return pp, true
}

// PersonLink is a name that links to a person's page.
type PersonLink struct {
	ID   int64
	Name string
}

// DayEntry is one birthday or anniversary on a day. A couple who share an
// anniversary are one entry.
type DayEntry struct {
	Day    int
	Text   string // the date as shown: "June 5" or "June 5, 2001"
	People []PersonLink
}

// MonthView is one month of the birthdays page.
type MonthView struct {
	Name          string
	Birthdays     []DayEntry
	Anniversaries []DayEntry
}

// Birthdays lists this month's and next month's birthdays and
// anniversaries. Only dates whose month and day the viewer may see appear.
func (d *Directory) Birthdays(now time.Time) []MonthView {
	this := int(now.Month())
	return d.birthdaysIn([]int{this, this%12 + 1}, nil)
}

// birthdaysIn is Birthdays for the given months (1-12), narrowed to the
// people in only when it isn't nil.
func (d *Directory) birthdaysIn(months []int, only map[int64]bool) []MonthView {
	out := make([]MonthView, len(months))
	for i, m := range months {
		out[i].Name = dates.MonthName(m)
	}
	type annKey struct {
		anchor     int64
		month, day int
		text       string
	}
	ann := map[annKey]*DayEntry{}
	annMonth := map[annKey]int{}
	for _, pid := range d.peopleOrder {
		a := d.people[pid]
		if a.Person.Kind != store.KindMember || a.Person.Archived() || !d.CanSeePerson(a) {
			continue
		}
		if only != nil && !only[pid] {
			continue
		}
		pv := NewPersonView(d.Viewer, a)
		for i, m := range months {
			if pv.Birthday.Month == m && pv.Birthday.Day != 0 {
				out[i].Birthdays = append(out[i].Birthdays, DayEntry{Day: pv.Birthday.Day, Text: pv.Birthday.Text,
					People: []PersonLink{{pv.ID, pv.Name}}})
			}
			if pv.Anniversary.Month == m && pv.Anniversary.Day != 0 {
				anchor := pv.ID
				if len(a.Memberships) > 0 {
					anchor = -a.Memberships[0].HouseholdID
				}
				k := annKey{anchor, m, pv.Anniversary.Day, pv.Anniversary.Text}
				if e := ann[k]; e != nil {
					e.People = append(e.People, PersonLink{pv.ID, pv.Name})
				} else {
					ann[k] = &DayEntry{Day: pv.Anniversary.Day, Text: pv.Anniversary.Text, People: []PersonLink{{pv.ID, pv.Name}}}
					annMonth[k] = i
				}
			}
		}
	}
	for k, e := range ann {
		out[annMonth[k]].Anniversaries = append(out[annMonth[k]].Anniversaries, *e)
	}
	for i := range out {
		sortDays(out[i].Birthdays)
		sortDays(out[i].Anniversaries)
	}
	return out
}

func sortDays(es []DayEntry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Day != es[j].Day {
			return es[i].Day < es[j].Day
		}
		return es[i].People[0].Name < es[j].People[0].Name
	})
}
