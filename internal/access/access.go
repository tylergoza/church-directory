// Package access decides who may see what. It is the one place the
// visibility rules of PLAN.md ("Visibility") live: every page, the print
// view and the JSON API ask Visible, VisibleAddress, VisibleHouseholdPhoto
// and CanSeePerson, and build what they show from the view models in
// view.go. Nothing else should read a person's email, phone, photo,
// birthday or anniversary for display.
package access

import (
	"errors"

	"github.com/tylergoza/church-directory/internal/store"
)

// Viewer is who is looking: built once per request (NewViewer). The fields
// are exported so the API and tests can build one by hand.
type Viewer struct {
	UserID   int64 // 0 for nobody signed in
	Staff    bool  // an editor or an admin
	Admin    bool
	PersonID int64          // the viewer's linked profile (people.user_id), 0 for none
	HeadOf   map[int64]bool // households where that profile is a head
}

// NewViewer works out who a signed-in user is. A user with no linked
// profile is a viewer with no PersonID and no households.
func NewViewer(st *store.Store, u *store.User) (*Viewer, error) {
	if u == nil {
		return &Viewer{}, nil
	}
	v := &Viewer{UserID: u.ID, Staff: u.IsStaff(), Admin: u.IsAdmin(), HeadOf: map[int64]bool{}}
	p, err := st.PersonByUser(u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	v.PersonID = p.ID
	heads, err := st.HeadHouseholds(p.ID)
	if err != nil {
		return nil, err
	}
	for _, h := range heads {
		v.HeadOf[h] = true
	}
	return v, nil
}

// IsHeadOf reports whether the viewer's profile heads the household.
func (v *Viewer) IsHeadOf(householdID int64) bool { return v != nil && v.HeadOf[householdID] }

// PersonAccess is everything Visible needs to know about one person,
// loaded once (Load) so a list of people costs no query per field.
type PersonAccess struct {
	Person      store.Person
	Memberships []store.Membership // the households they are in, with their role in each
	Managers    []int64            // people who manage (managed_by) a household they are in
	// ActiveManagers are the managers whose rights still last: the
	// household has no head with a login (ManagedByActive in the store).
	ActiveManagers []int64
	Settings       map[store.Field]store.FieldSetting
	Defaults       store.Defaults
}

// Child reports whether the person is a child (role child in any
// household). Children get the children's defaults.
func (a *PersonAccess) Child() bool {
	for _, m := range a.Memberships {
		if m.Role == store.RoleChild {
			return true
		}
	}
	return false
}

func (a *PersonAccess) audience() string {
	if a.Child() {
		return store.AudienceChild
	}
	return store.AudienceAdult
}

// AlwaysSees reports whether the viewer sees everything about the person:
// staff, the person, and the heads of any of the person's households.
func (v *Viewer) AlwaysSees(a *PersonAccess) bool {
	if v == nil {
		return false
	}
	if v.Staff || (v.PersonID != 0 && v.PersonID == a.Person.ID) {
		return true
	}
	for _, m := range a.Memberships {
		if v.HeadOf[m.HouseholdID] {
			return true
		}
	}
	// Whoever still manages a household stands in for its heads.
	return v.PersonID != 0 && containsID(a.ActiveManagers, v.PersonID)
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// entered reports whether the person has a value for the field.
func entered(p store.Person, f store.Field) bool {
	switch f {
	case store.FieldEmail:
		return p.Email != ""
	case store.FieldPhone:
		return p.Phone != ""
	case store.FieldPhoto:
		return p.Photo != ""
	case store.FieldBirthMonth:
		return p.Birth.Month != 0
	case store.FieldBirthDay:
		return p.Birth.Day != 0
	case store.FieldBirthYear:
		return p.Birth.Year != 0
	case store.FieldAnniversaryMon:
		return p.Anniversary.Month != 0
	case store.FieldAnniversaryDay:
		return p.Anniversary.Day != 0
	case store.FieldAnniversaryYear:
		return p.Anniversary.Year != 0
	}
	return false
}

// decision is the facts the one rule list is applied to.
type decision struct {
	Entered     bool  // 1: there is something to show
	Always      bool  // staff, the person, heads: see everything
	ForceHidden bool  // 2: staff hid it
	MonthHidden bool  // 3: a day whose month isn't shown
	Own         *bool // 4: the profile's (or address's) own setting
	Default     bool  // 5: what the admin set for this kind of viewer
}

// decide is the single place the order of the rules is written down. The
// first rule that matches wins.
func decide(d decision) bool {
	switch {
	case !d.Entered: // 1. nothing to show
		return false
	case d.ForceHidden: // 2. only staff, the person and heads (Always) see it
		return d.Always
	case d.Always: // staff, the person and heads see everything else too
		return true
	case d.MonthHidden: // 3. a day needs its month
		return false
	case d.Own != nil: // 4. the profile's own setting
		return *d.Own
	}
	return d.Default // 5. the defaults
}

// Visible is the one function that says whether the viewer may see a
// person's field. See PLAN.md "Visibility" for the order of the rules.
func Visible(v *Viewer, a *PersonAccess, f store.Field) bool {
	set := a.Settings[f]
	d := decision{
		Entered:     entered(a.Person, f),
		Always:      v.AlwaysSees(a),
		ForceHidden: set.ForceHidden,
		Own:         set.Shown,
		Default:     a.Defaults.Shown(a.audience(), f),
	}
	switch f {
	case store.FieldBirthDay:
		d.MonthHidden = !Visible(v, a, store.FieldBirthMonth)
	case store.FieldAnniversaryDay:
		d.MonthHidden = !Visible(v, a, store.FieldAnniversaryMon)
	}
	return decide(d)
}

// HouseholdAccess is everything the household-level visibility needs.
type HouseholdAccess struct {
	Household store.Household
	Addresses []store.Address
	Members   []store.Membership
	Settings  map[store.Field]store.FieldSetting // household.photo
	Defaults  store.Defaults
	// ManagedActive: managed_by is set and no head has a login yet, so the
	// managing person still edits it (store.ManagedByActive).
	ManagedActive bool
}

// AlwaysSeesHousehold: staff and the household's heads see all of its addresses
// and its photo.
func (v *Viewer) AlwaysSeesHousehold(householdID int64) bool {
	return v != nil && (v.Staff || v.HeadOf[householdID])
}

// VisibleAddress says whether the viewer may see one of the household's
// addresses (shown/force_hidden on the address, default field "address").
func VisibleAddress(v *Viewer, h *HouseholdAccess, a store.Address) bool {
	return decide(decision{
		Entered:     a.OneLine() != "",
		Always:      v.AlwaysSeesHousehold(h.Household.ID),
		ForceHidden: a.ForceHidden,
		Own:         a.Shown,
		Default:     h.Defaults.Shown(store.AudienceAdult, store.FieldAddress),
	})
}

// VisibleHouseholdPhoto says whether the viewer may see the household's
// photo.
func VisibleHouseholdPhoto(v *Viewer, h *HouseholdAccess) bool {
	set := h.Settings[store.FieldHouseholdPhoto]
	return decide(decision{
		Entered:     h.Household.Photo != "",
		Always:      v.AlwaysSeesHousehold(h.Household.ID),
		ForceHidden: set.ForceHidden,
		Own:         set.Shown,
		Default:     h.Defaults.Shown(store.AudienceAdult, store.FieldHouseholdPhoto),
	})
}

// CanSeePerson says whether the person may be shown to the viewer at all
// (a page, a list). Staff see everyone. Everyone else: not archived people;
// pending people only to whoever made them; contacts only to the heads of
// the households they are in, whoever manages those households, and the
// contact themselves if they have a login.
func CanSeePerson(v *Viewer, a *PersonAccess) bool {
	if v == nil {
		return false
	}
	if v.Staff {
		return true
	}
	p := a.Person
	if p.Archived() {
		return false
	}
	if p.Pending {
		return p.CreatedBy != 0 && p.CreatedBy == v.UserID
	}
	if p.Kind == store.KindContact {
		if v.PersonID != 0 && v.PersonID == p.ID {
			return true
		}
		for _, m := range a.Memberships {
			if v.HeadOf[m.HouseholdID] {
				return true
			}
		}
		for _, id := range a.Managers {
			if v.PersonID != 0 && id == v.PersonID {
				return true
			}
		}
		return false
	}
	return true
}

// CanSeeHousehold says whether a household may be shown to the viewer:
// staff see all; others not archived households, pending ones only to whoever
// made them, and contact households
// only if they head or manage them.
func CanSeeHousehold(v *Viewer, h *HouseholdAccess) bool {
	if v == nil {
		return false
	}
	if v.Staff {
		return true
	}
	if h.Household.Archived() {
		return false
	}
	if h.Household.Pending { // self sign-up: only whoever made it
		return h.Household.CreatedBy != 0 && h.Household.CreatedBy == v.UserID
	}
	if h.Household.Kind == store.KindContact {
		return v.HeadOf[h.Household.ID] || (v.PersonID != 0 && h.Household.ManagedBy == v.PersonID)
	}
	return true
}
