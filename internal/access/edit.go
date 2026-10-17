package access

// Who may edit what. PLAN.md "Heads of household". These two functions are
// THE rules: the /me pages use them, and the members API
// (MEMBERS_PLAN.md step 4) calls the same ones. Nothing else decides.

// CanHeadEditHousehold says whether the viewer may edit the household as
// its head: its name, addresses, photo, people and so on.
//
//   - staff always;
//   - the viewer's profile is a head of it;
//   - for a household with managed_by (a contact household, or one from
//     "Start their own household"), the managing person, until a head of
//     it has a login (HouseholdAccess.ManagedActive).
//
// Other adults, children with a login and anyone else get false.
func CanHeadEditHousehold(v *Viewer, h *HouseholdAccess) bool {
	if v == nil || h == nil {
		return false
	}
	if v.Staff {
		return true
	}
	if v.HeadOf[h.Household.ID] {
		return true
	}
	return v.PersonID != 0 && h.ManagedActive && h.Household.ManagedBy == v.PersonID
}

// headsRule: the viewer is a head of a household the person is in, or the
// active manager of one.
func headsRule(v *Viewer, a *PersonAccess) bool {
	if v.PersonID == 0 {
		return false
	}
	for _, m := range a.Memberships {
		if v.HeadOf[m.HouseholdID] {
			return true
		}
	}
	return containsID(a.ActiveManagers, v.PersonID)
}

// CanEditPerson says whether the viewer may edit the person's profile,
// visibility, photo and notes.
//
//   - staff, and the person themself;
//   - an unlinked person, or a linked person with role child: a head of one
//     of their households, or the manager of a managed household they are in;
//   - a linked adult: those same heads, but only while the person has
//     ticked "Let my household's heads edit my profile" (heads_may_edit),
//     or staff set it on. When staff locked it off it stays off.
//
// The person must be one the viewer can see at all.
func CanEditPerson(v *Viewer, a *PersonAccess) bool {
	if v == nil || a == nil || !CanSeePerson(v, a) {
		return false
	}
	if v.Staff || (v.PersonID != 0 && v.PersonID == a.Person.ID) {
		return true
	}
	if !headsRule(v, a) {
		return false
	}
	if a.Person.UserID == 0 || a.Child() {
		return true
	}
	return a.Person.HeadsMayEdit // off, and locked off, both read as false
}

// CanSetMultiHousehold says whether the viewer may tick "Lives in more than
// one household" on the person: staff, or a head of one of their households.
func CanSetMultiHousehold(v *Viewer, a *PersonAccess) bool {
	if v == nil || !CanSeePerson(v, a) {
		return false
	}
	return v.Staff || headsRule(v, a)
}

// CanRemoveFromHousehold says whether the viewer may take the person out of
// the household: staff; the person themself (leaving); a head of the
// household (CanHeadEditHousehold) for people with no login. A linked adult
// can only leave themselves, or be moved by staff.
func CanRemoveFromHousehold(v *Viewer, h *HouseholdAccess, a *PersonAccess) bool {
	if v == nil || h == nil || a == nil {
		return false
	}
	if v.Staff || (v.PersonID != 0 && v.PersonID == a.Person.ID) {
		return true
	}
	return CanHeadEditHousehold(v, h) && a.Person.UserID == 0
}
