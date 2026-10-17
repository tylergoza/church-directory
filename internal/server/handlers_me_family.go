package server

import (
	"net/http"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
)

// Family links, "Start their own household", more than one household, join
// requests and contact households, from /me. Staff versions of a few of
// these are at the bottom.

// Family ----------------------------------------------------------------------

type famChoice struct {
	Person     store.Person
	Households []string
}

func (s *Server) handleMeFamily(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	p := a.Person
	links, err := s.store.ListPersonLinks(p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Names only, and only people the viewer can see anyway.
	seen := func(list []store.Person) []store.Person {
		var out []store.Person
		for _, x := range list {
			if xa, ok := d.Person(x.ID); ok && d.CanSeePerson(xa) {
				out = append(out, x)
			}
		}
		return out
	}
	links.Parents, links.Children, links.Spouses = seen(links.Parents), seen(links.Children), seen(links.Spouses)

	// Who could be linked: the people the viewer can edit who share a
	// household with this person or live in one the viewer looks after.
	together := map[int64]bool{}
	var cands []famChoice
	addFrom := func(h *access.HouseholdAccess) {
		var names []string
		names = append(names, h.Household.Name)
		for _, m := range h.Members {
			if m.PersonID == p.ID || together[m.PersonID] {
				continue
			}
			if xa, ok := d.Person(m.PersonID); ok && access.CanEditPerson(d.Viewer, xa) {
				together[m.PersonID] = true
				cands = append(cands, famChoice{Person: xa.Person, Households: names})
			}
		}
	}
	for _, m := range a.Memberships {
		if h, ok := d.Household(m.HouseholdID); ok && d.CanSeeHousehold(h) {
			addFrom(h)
		}
	}
	for _, h := range d.EditableHouseholds() {
		addFrom(h)
	}
	hh, err := s.store.PersonHouseholds(p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"Title": "Family of " + p.Name(), "Person": p, "Links": links, "Candidates": cands,
		"Back": s.personBack(r, p.ID), "PersonHouseholds": hh, "OwnName": store.OwnHouseholdName(p),
		"CanOwn": !a.Child() && s.canStartOwn(d, a),
	}
	s.render(w, r, http.StatusOK, "me/family", data)
}

// canStartOwn: staff, or a head of a household the person is leaving.
func (s *Server) canStartOwn(d *access.Directory, a *access.PersonAccess) bool {
	if d.Viewer.Staff {
		return true
	}
	for _, m := range a.Memberships {
		if h, ok := d.Household(m.HouseholdID); ok && access.CanHeadEditHousehold(d.Viewer, h) {
			return true
		}
	}
	return false
}

func (s *Server) handleMeLinkAdd(w http.ResponseWriter, r *http.Request) { s.meLinkChange(w, r, false) }
func (s *Server) handleMeLinkRemove(w http.ResponseWriter, r *http.Request) {
	s.meLinkChange(w, r, true)
}

func (s *Server) meLinkChange(w http.ResponseWriter, r *http.Request, remove bool) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	back := "/me/people/" + itoa(a.Person.ID) + "/family"
	other := formInt(r, "person_id")
	if oa, ok := d.Person(other); !ok || !d.CanSeePerson(oa) {
		s.redirectError(w, r, back, "Couldn't find that person.")
		return
	}
	msg, ok := s.linkPeople(formStr(r, "rel"), a.Person.ID, other, remove, d.Viewer.UserID, editFn(d))
	if !ok {
		s.redirectError(w, r, back, msg)
		return
	}
	s.redirect(w, r, back, msg)
}

// handleMeOwnHousehold is "Start their own household" for a head: the person
// (and the spouse and children picked from their links) move into a new
// household that the head keeps managing until someone in it has a login and
// is its head.
func (s *Server) handleMeOwnHousehold(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	back := "/me/people/" + itoa(a.Person.ID) + "/family"
	if a.Child() || !s.canStartOwn(d, a) {
		s.denied(w, r, "Only a head of the household they are leaving can do that, and children move with their parents.")
		return
	}
	var managedBy int64
	if !d.Viewer.Staff {
		managedBy = d.Viewer.PersonID
	}
	hid, msg := s.startOwnHousehold(a.Person.ID, formStr(r, "name"), formInt(r, "spouse_id"), parseIDs(r, "child_id"),
		d.Viewer.UserID, managedBy, editFn(d))
	if msg != "" {
		s.redirectError(w, r, back, msg)
		return
	}
	// If the person moved has a login and is the new household's head, the
	// viewer no longer manages it, so there's nothing for them to open.
	if nd, err := s.directory(r); err == nil {
		if h, ok := nd.Household(hid); ok && access.CanHeadEditHousehold(nd.Viewer, h) {
			s.redirect(w, r, hhBack(hid), "New household started. Add its address below.")
			return
		}
	}
	s.redirect(w, r, s.personBack(r, a.Person.ID), "New household started.")
}

// More than one household ----------------------------------------------------------

func (s *Server) multiPerson(w http.ResponseWriter, r *http.Request) (*access.Directory, *access.PersonAccess, bool) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	a, ok := d.Person(pathID(r))
	if !ok || !d.CanSeePerson(a) {
		s.notFound(w, r)
		return nil, nil, false
	}
	if !access.CanSetMultiHousehold(d.Viewer, a) {
		s.denied(w, r, "Only staff or a head of one of their households can do that.")
		return nil, nil, false
	}
	return d, a, true
}

// handleMeMulti ticks or unticks "Lives in more than one household".
func (s *Server) handleMeMulti(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.multiPerson(w, r)
	if !ok {
		return
	}
	s.setMulti(w, r, d.Viewer.UserID, a.Person.ID, s.personBack(r, a.Person.ID))
}

func (s *Server) setMulti(w http.ResponseWriter, r *http.Request, by, personID int64, back string) {
	on := r.PostFormValue("multi_household") == "1"
	if err := s.store.SetMultiHousehold(personID, on, formStr(r, "multi_reason"), by); err != nil {
		s.failed(w, r, back, err)
		return
	}
	s.redirect(w, r, back, "Saved.")
}

func (s *Server) handleMePrimary(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.multiPerson(w, r)
	if !ok {
		return
	}
	s.setPrimary(w, r, d.Viewer.UserID, a.Person.ID, s.personBack(r, a.Person.ID))
}

func (s *Server) setPrimary(w http.ResponseWriter, r *http.Request, by, personID int64, back string) {
	if err := s.store.SetPrimaryHousehold(personID, formInt(r, "household_id"), by); err != nil {
		s.failed(w, r, back, err)
		return
	}
	s.redirect(w, r, back, "Primary household saved.")
}

// handleMeSecondHousehold adds a child who lives in more than one household
// to another one. A household the viewer heads (or manages) takes them at
// once; anyone else's gets a join request that its heads or staff answer.
// Without the tick on the child it is refused either way.
func (s *Server) handleMeSecondHousehold(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.multiPerson(w, r)
	if !ok {
		return
	}
	back := s.personBack(r, a.Person.ID)
	if !a.Child() {
		s.redirectError(w, r, back, "Only a child can live in more than one household.")
		return
	}
	target, ok := d.Household(formInt(r, "household_id"))
	if !ok || !d.CanSeeHousehold(target) || target.Household.Archived() {
		s.redirectError(w, r, back, "Choose a household.")
		return
	}
	by := d.Viewer.UserID
	if access.CanHeadEditHousehold(d.Viewer, target) {
		if err := s.store.AddSecondHousehold(target.Household.ID, a.Person.ID, formInt(r, "address_id"), by); err != nil {
			s.failed(w, r, back, err)
			return
		}
		s.redirect(w, r, back, a.Person.FirstName+" is in "+target.Household.Name+" too.")
		return
	}
	if _, err := s.store.CreateJoinRequest(a.Person.ID, target.Household.ID, by); err != nil {
		s.failed(w, r, back, err)
		return
	}
	s.redirect(w, r, back, "Request sent. A head of "+target.Household.Name+" or the office will answer it.")
}

// Join requests --------------------------------------------------------------------

func (s *Server) handleMeRequestAnswer(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := s.directory(r)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		rq, err := s.store.GetJoinRequest(pathID(r))
		if err != nil {
			if err == store.ErrNotFound {
				s.notFound(w, r)
				return
			}
			s.serverError(w, r, err)
			return
		}
		h, ok := d.Household(rq.HouseholdID)
		if !ok || !access.CanHeadEditHousehold(d.Viewer, h) {
			s.notFound(w, r) // not yours to see
			return
		}
		if err := s.store.AnswerJoinRequest(rq.ID, accept, d.Viewer.UserID); err != nil {
			s.failed(w, r, "/me", err)
			return
		}
		msg := "Turned down."
		if accept {
			msg = rq.PersonName + " is in " + rq.HouseholdName + " now."
		}
		s.redirect(w, r, "/me", msg)
	}
}

// Contact households ----------------------------------------------------------------

// handleMeContactHousehold makes a contact household for a child's other
// parent: kind contact, never listed or printed, edited by the head who made
// it (managed_by) and by staff.
func (s *Server) handleMeContactHousehold(w http.ResponseWriter, r *http.Request) {
	v, err := s.viewer(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.PersonID == 0 || (len(v.HeadOf) == 0 && !v.Staff) {
		s.denied(w, r, "Only a head of a household can make a contact household.")
		return
	}
	id, err := s.store.CreateManagedHousehold(formStr(r, "name"), store.KindContact, v.PersonID, v.UserID)
	if err != nil {
		s.redirectError(w, r, "/me", err.Error())
		return
	}
	s.redirect(w, r, hhBack(id), "Contact household made. It is never listed or printed. Add the other parent and the child below.")
}

// handleMeAddChild adds one of the viewer's multi-household children to a
// household they manage (the contact household).
func (s *Server) handleMeAddChild(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.headHousehold(w, r)
	if !ok {
		return
	}
	back := hhBack(h.Household.ID)
	a, ok := d.Person(formInt(r, "person_id"))
	if !ok || !access.CanEditPerson(d.Viewer, a) {
		s.redirectError(w, r, back, "Choose a child you look after.")
		return
	}
	if err := s.store.AddSecondHousehold(h.Household.ID, a.Person.ID, formInt(r, "address_id"), d.Viewer.UserID); err != nil {
		s.failed(w, r, back, err)
		return
	}
	s.redirect(w, r, back, a.Person.FirstName+" is in this household too.")
}

// Staff -------------------------------------------------------------------------------

// handleAdminHeadsMayEdit: staff set "heads may edit" on or off and lock it
// (with a reason).
func (s *Server) handleAdminHeadsMayEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := "/admin/people/" + itoa(id)
	on := r.PostFormValue("heads_may_edit") == "1"
	lock := r.PostFormValue("lock") == "1"
	if err := s.store.StaffSetHeadsMayEdit(id, on, lock, r.PostFormValue("reason"), currentUser(r).ID); err != nil {
		s.failed(w, r, back, err)
		return
	}
	s.redirect(w, r, back, "Saved.")
}

func (s *Server) handleAdminMulti(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	s.setMulti(w, r, currentUser(r).ID, id, "/admin/people/"+itoa(id))
}

func (s *Server) handleAdminPrimary(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	s.setPrimary(w, r, currentUser(r).ID, id, "/admin/people/"+itoa(id))
}

// handleAdminConvertContact turns a contact household and one of its people
// into members, with that person as head. The head who made it stops being
// able to edit it.
func (s *Server) handleAdminConvertContact(w http.ResponseWriter, r *http.Request) {
	hid := pathID(r)
	back := "/admin/households/" + itoa(hid)
	if err := s.store.ConvertContact(hid, formInt(r, "person_id"), currentUser(r).ID); err != nil {
		s.failed(w, r, back, err)
		return
	}
	s.redirect(w, r, back, "Now a member household, with that person as head. The head who made it can't edit it any more.")
}

func (s *Server) handleNARequest(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.store.AnswerJoinRequest(pathID(r), accept, currentUser(r).ID)
		msg := "Request turned down."
		if accept {
			msg = "Request accepted: the child is in the household."
		}
		s.needsActionDone(w, r, msg, err)
	}
}
