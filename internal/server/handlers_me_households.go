package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
)

// Household management for heads of household: /me/households/{id}. Every
// handler starts with headHousehold (access.CanHeadEditHousehold).

func hhBack(id int64) string { return "/me/households/" + itoa(id) }

// meMember is a person in the household as the head's page shows them.
type meMember struct {
	store.Member
	Editable   bool // access.CanEditPerson
	CanRemove  bool // access.CanRemoveFromHousehold
	CanPromote bool // an adult who can be made a head
	IsMe       bool
	Waiting    bool
}

func (s *Server) handleMeHouseholdPage(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	s.renderMeHousehold(w, r, http.StatusOK, d, h, nil)
}

func (s *Server) renderMeHousehold(w http.ResponseWriter, r *http.Request, status int, d *access.Directory, h *access.HouseholdAccess, errs []string) {
	hh, err := s.store.GetHousehold(h.Household.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	addrs, err := s.store.ListAddresses(hh.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	all, err := s.store.HouseholdMembers(hh.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := d.Viewer
	var members []meMember
	for _, m := range all {
		a, ok := d.Person(m.ID)
		if !ok || !d.CanSeePerson(a) {
			continue
		}
		members = append(members, meMember{Member: m,
			Editable:   access.CanEditPerson(v, a),
			CanRemove:  access.CanRemoveFromHousehold(v, h, a),
			CanPromote: m.Role == store.RoleAdult,
			IsMe:       v.PersonID != 0 && v.PersonID == m.ID,
			Waiting:    m.Pending || m.PendingName != ""})
	}
	defaults, err := s.store.LoadDefaults()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hset, err := s.store.AllHouseholdSettings()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"Title": hh.Name, "Household": hh, "Addresses": addrs, "Members": members, "Errors": errs,
		"BlankAddress":   store.Address{},
		"AddressDefault": defaults.Shown(store.AudienceAdult, store.FieldAddress),
		"PhotoVis": visRow{Field: store.FieldHouseholdPhoto, Label: "Household photo", State: visState(hset[hh.ID][store.FieldHouseholdPhoto].Shown),
			Force: hset[hh.ID][store.FieldHouseholdPhoto].ForceHidden, Default: defaults.Shown(store.AudienceAdult, store.FieldHouseholdPhoto)},
		"IsContact":   hh.Kind == store.KindContact,
		"ManagedNote": hh.ManagedBy != 0 && h.ManagedActive,
		"Staff":       v.Staff,
		"Waiting":     waitingText,
	}
	if hh.Kind == store.KindContact {
		// The children the viewer can edit who live in more than one household
		// and aren't here yet: the ones who can be added to this household.
		var kids []store.Person
		in := map[int64]bool{}
		for _, m := range members {
			in[m.ID] = true
		}
		for _, id := range d.PeopleIDs() {
			a, _ := d.Person(id)
			if a.Child() && a.Person.MultiHousehold && !in[id] && access.CanEditPerson(v, a) {
				kids = append(kids, a.Person)
			}
		}
		data["CanAddKids"] = kids
	}
	sg, err := s.familySuggestions(hh.ID, queryInt(r, "suggest"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Suggest"] = sg
	s.render(w, r, status, "me/household", data)
}

func (s *Server) handleMeHouseholdUpdate(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	// Heads change the name only; the kind is staff's.
	name, _, errs := store.ValidateHousehold(formStr(r, "name"), h.Household.Kind)
	if len(errs) > 0 {
		s.renderMeHousehold(w, r, http.StatusUnprocessableEntity, d, h, errs)
		return
	}
	if err := s.store.UpdateHousehold(h.Household.ID, name, h.Household.Kind, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Saved.")
}

// Photo ------------------------------------------------------------------

func (s *Server) handleMeHouseholdPhoto(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	msg, err := s.saveHouseholdPhoto(r, h.Household.ID, d.Viewer.UserID)
	s.photoResult(w, r, hhBack(h.Household.ID), "Photo saved.", msg, err)
}

func (s *Server) handleMeHouseholdPhotoRemove(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	if err := s.removeHouseholdPhoto(h.Household.ID, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Photo removed.")
}

// handleMeHouseholdPhotoVisibility sets whether the photo shows (shown),
// leaving staff's force hide exactly as it is.
func (s *Server) handleMeHouseholdPhotoVisibility(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	cur := h.Settings[store.FieldHouseholdPhoto]
	set := store.FieldSetting{Shown: parseShown(r.PostFormValue("shown")), ForceHidden: cur.ForceHidden}
	if err := s.store.SetHouseholdPhotoSetting(h.Household.ID, set, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Photo visibility saved.")
}

// Addresses ----------------------------------------------------------------

func (s *Server) handleMeAddressAdd(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	in := addressInputFrom(r)
	if errs := in.Validate(); len(errs) > 0 {
		s.redirectError(w, r, hhBack(h.Household.ID), strings.Join(errs, " "))
		return
	}
	if _, err := s.store.AddAddress(h.Household.ID, in, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Address added.")
}

// meAddress loads the address in the path, which must belong to the household.
func (s *Server) meAddress(w http.ResponseWriter, r *http.Request, h *access.HouseholdAccess) (store.Address, bool) {
	a, err := s.store.GetAddress(pathValueID(r, "aid"))
	if err != nil || a.HouseholdID != h.Household.ID {
		s.notFound(w, r)
		return a, false
	}
	return a, true
}

func (s *Server) handleMeAddressUpdate(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	a, ok := s.meAddress(w, r, h)
	if !ok {
		return
	}
	in := addressInputFrom(r)
	if errs := in.Validate(); len(errs) > 0 {
		s.redirectError(w, r, hhBack(h.Household.ID), strings.Join(errs, " "))
		return
	}
	if err := s.store.UpdateAddress(a.ID, in, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Address saved.")
}

func (s *Server) handleMeAddressDelete(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	a, ok := s.meAddress(w, r, h)
	if !ok {
		return
	}
	reassign := formInt(r, "reassign")
	if r.PostFormValue("reassign") == "none" {
		reassign = store.NoAddress
	}
	if err := s.store.DeleteAddress(a.ID, reassign, d.Viewer.UserID); err != nil {
		if err == store.ErrNeedsReassign {
			s.redirectError(w, r, hhBack(h.Household.ID), "Choose where the people at that address go before removing it.")
			return
		}
		s.failed(w, r, hhBack(h.Household.ID), err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Address removed.")
}

// handleMeAddressVisibility sets whether an address shows, leaving staff's
// force hide as it is.
func (s *Server) handleMeAddressVisibility(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	a, ok := s.meAddress(w, r, h)
	if !ok {
		return
	}
	if err := s.store.SetAddressVisibility(a.ID, parseShown(r.PostFormValue("shown")), a.ForceHidden, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Address visibility saved.")
}

// People ---------------------------------------------------------------------

// memberOf finds the person in the path's household, or answers 404.
func (s *Server) memberOf(w http.ResponseWriter, r *http.Request, d *access.Directory, h *access.HouseholdAccess) (*access.PersonAccess, store.Membership, bool) {
	pid := pathValueID(r, "pid")
	a, ok := d.Person(pid)
	if ok && d.CanSeePerson(a) {
		for _, m := range a.Memberships {
			if m.HouseholdID == h.Household.ID {
				return a, m, true
			}
		}
	}
	s.notFound(w, r)
	return nil, store.Membership{}, false
}

// handleMeAddPerson adds a new person to the household: unlinked, kind
// member (a contact household's other parent is a contact). Someone who isn't
// staff whose name matches a person already here is held for the office, and
// is told only "Waiting for the office to check this."
func (s *Server) handleMeAddPerson(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	hid, back := h.Household.ID, hhBack(h.Household.ID)
	form := personFormFrom(r)
	form.Kind = store.KindMember
	role := formStr(r, "role")
	if role != store.RoleAdult && role != store.RoleChild {
		role = store.RoleChild
	}
	if h.Household.Kind == store.KindContact && formStr(r, "other_parent") == "1" {
		form.Kind, role = store.KindContact, store.RoleAdult
	}
	in, errs := form.input()
	if len(errs) > 0 {
		s.redirectError(w, r, back, strings.Join(errs, " "))
		return
	}
	by := d.Viewer.UserID
	var id int64
	var reason string
	var err error
	if d.Viewer.Staff {
		id, err = s.store.CreatePerson(in, by)
	} else {
		id, reason, err = s.store.CreateByMember(in, by, false)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.PlaceMember(hid, id, role, formInt(r, "address_id"), by); err != nil {
		s.failed(w, r, back, err)
		return
	}
	if reason != "" {
		s.redirect(w, r, back, "Added. "+waitingText)
		return
	}
	s.redirect(w, r, back+suggestQuery(id, role), "Added "+in.FirstName+" to the household.")
}

// handleMeMemberAddress says which address a person lives at.
func (s *Server) handleMeMemberAddress(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	a, m, ok := s.memberOf(w, r, d, h)
	if !ok {
		return
	}
	addr := formInt(r, "address_id")
	if err := s.store.PlaceMember(h.Household.ID, a.Person.ID, m.Role, addr, d.Viewer.UserID); err != nil {
		s.failed(w, r, hhBack(h.Household.ID), err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Saved.")
}

// handleMeMakeHead makes another adult in the household a head too (and
// suggests the spouse link).
func (s *Server) handleMeMakeHead(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	a, m, ok := s.memberOf(w, r, d, h)
	if !ok {
		return
	}
	if m.Role != store.RoleAdult {
		s.redirectError(w, r, hhBack(h.Household.ID), "Only an adult in the household can be made a head.")
		return
	}
	if err := s.store.PlaceMember(h.Household.ID, a.Person.ID, store.RoleHead, m.AddressID, d.Viewer.UserID); err != nil {
		s.failed(w, r, hhBack(h.Household.ID), err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID)+suggestQuery(a.Person.ID, store.RoleHead), a.Person.Name()+" is a head of the household now.")
}

// handleMeRemoveMember takes an unlinked person out of the household. Linked
// adults can only leave themselves (handleMeLeave), or be moved by staff.
func (s *Server) handleMeRemoveMember(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	a, _, ok := s.memberOf(w, r, d, h)
	if !ok {
		return
	}
	if !access.CanRemoveFromHousehold(d.Viewer, h, a) {
		s.denied(w, r, "They have their own login, so they can only leave the household themselves. Ask the office to move them.")
		return
	}
	if err := s.store.RemoveMember(h.Household.ID, a.Person.ID, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), "Removed from the household. They're still in the directory.")
}

// handleMeLeave lets the viewer take themself out of a household they are in.
func (s *Server) handleMeLeave(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := d.Viewer
	h, ok := d.Household(pathID(r))
	me, okMe := d.Person(v.PersonID)
	if !ok || !okMe || v.PersonID == 0 {
		s.notFound(w, r)
		return
	}
	var mine *store.Membership
	for i := range me.Memberships {
		if me.Memberships[i].HouseholdID == h.Household.ID {
			mine = &me.Memberships[i]
		}
	}
	if mine == nil {
		s.notFound(w, r)
		return
	}
	if mine.Role == store.RoleHead {
		heads := 0
		for _, m := range h.Members {
			if m.Role == store.RoleHead {
				heads++
			}
		}
		if heads < 2 && len(h.Members) > 1 {
			s.redirectError(w, r, "/me", "You're the only head. Make another adult a head first, or ask the office to help.")
			return
		}
	}
	if err := s.store.RemoveMember(h.Household.ID, v.PersonID, v.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/me", "You've left "+h.Household.Name+".")
}

func (s *Server) handleMeFamilyLinksApply(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	msg, ok := s.applyFamilyLinks(r, h.Household.ID, d.Viewer.UserID, editFn(d))
	if !ok {
		s.redirectError(w, r, hhBack(h.Household.ID), msg)
		return
	}
	s.redirect(w, r, hhBack(h.Household.ID), msg)
}

// parseIDs reads a list of ids from a form field.
func parseIDs(r *http.Request, key string) []int64 {
	r.ParseForm()
	var out []int64
	for _, v := range r.PostForm[key] {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}
