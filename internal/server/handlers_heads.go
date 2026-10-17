package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
)

// Heads of household (PLAN.md step 5): editing a person from /me. Who may do
// what is decided only by access.CanEditPerson and access.CanHeadEditHousehold
// (internal/access/edit.go); the handlers here ask them and never decide.

// friendlyError turns the expected refusals (link rules, heads rules) into a
// sentence for the person; ok is false for anything unexpected.
func friendlyError(err error) (string, bool) {
	if msg, ok := friendlyLinkError(err); ok {
		return msg, true
	}
	for _, known := range []error{
		store.ErrHeadsLocked, store.ErrNeedReason, store.ErrNotMulti, store.ErrChildOnly, store.ErrBadReason,
		store.ErrStillMulti, store.ErrAlreadyIn, store.ErrRequestClosed, store.ErrRequestOpen,
		store.ErrWrongHousehold, store.ErrBadRole,
	} {
		if errors.Is(err, known) {
			msg := err.Error()
			return strings.ToUpper(msg[:1]) + msg[1:] + ".", true
		}
	}
	return "", false
}

// failed answers an error from the store: a sentence for the expected ones
// (back to the page), a server error otherwise.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, back string, err error) {
	if msg, ok := friendlyError(err); ok {
		s.redirectError(w, r, back, msg)
		return
	}
	s.serverError(w, r, err)
}

func (s *Server) denied(w http.ResponseWriter, r *http.Request, msg string) {
	s.render(w, r, http.StatusForbidden, "error", map[string]any{"Title": "Not allowed", "Message": msg})
}

// editFn is the heads' rule as the func the family helpers take.
func editFn(d *access.Directory) func(int64) bool {
	return func(id int64) bool {
		a, ok := d.Person(id)
		return ok && access.CanEditPerson(d.Viewer, a)
	}
}

// editablePerson loads the directory and the person in the path. Someone the
// viewer can't see is a 404 (no hint they exist); someone they can see but not
// edit is a 403. ok false means the answer has been sent.
func (s *Server) editablePerson(w http.ResponseWriter, r *http.Request) (*access.Directory, *access.PersonAccess, bool) {
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
	if !access.CanEditPerson(d.Viewer, a) {
		s.denied(w, r, "You can't change this person's profile.")
		return nil, nil, false
	}
	return d, a, true
}

// headHousehold is editablePerson for a household and CanHeadEditHousehold.
func (s *Server) headHousehold(w http.ResponseWriter, r *http.Request) (*access.Directory, *access.HouseholdAccess, bool) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	h, ok := d.Household(pathID(r))
	if !ok || !d.CanSeeHousehold(h) {
		s.notFound(w, r)
		return nil, nil, false
	}
	if !access.CanHeadEditHousehold(d.Viewer, h) {
		s.denied(w, r, "Only a head of this household can change it.")
		return nil, nil, false
	}
	return d, h, true
}

// personBack is where a person's edit forms return to: /me for your own
// profile, their page otherwise.
func (s *Server) personBack(r *http.Request, personID int64) string {
	if me, err := s.store.PersonByUser(currentUser(r).ID); err == nil && me.ID == personID {
		return "/me"
	}
	return "/me/people/" + itoa(personID)
}

// Who the person is, for the editor ----------------------------------------

type meMembership struct {
	ID        int64
	Name      string
	Role      string
	Primary   bool
	Contact   bool
	CanManage bool // the viewer heads (or manages) this household
	CanLeave  bool // the viewer may take this person out of it
}

type meTarget struct {
	ID     int64
	Name   string
	Direct bool // the viewer heads it: added at once; otherwise a request is sent
}

// meEdit is everything the person editor shows. The viewer passed
// access.CanEditPerson, so they may see and change all of it.
type meEdit struct {
	Person         store.Person
	Self           bool
	Base           string // /me/people/ID
	Form           personForm
	Errors         []string
	Vis            []visRow
	Notes          *notesSection
	Under13Warning string
	AgeKnown       bool
	KnownUnder13   bool
	Waiting        bool
	CanLogin       bool // a head (or staff) of an unlinked person
	HeadsToggle    bool // the person themself, linked
	HeadsEditing   bool // heads may edit this linked adult right now
	Households     []meMembership
	CanMulti       bool
	Targets        []meTarget
	Requests       []store.JoinRequest
	CanFamily      bool
}

func (s *Server) personEdit(r *http.Request, d *access.Directory, a *access.PersonAccess, form personForm, errs []string) (*meEdit, error) {
	p, err := s.store.GetPerson(a.Person.ID)
	if err != nil {
		return nil, err
	}
	v := d.Viewer
	self := v.PersonID != 0 && v.PersonID == p.ID
	e := &meEdit{Person: p, Self: self, Base: "/me/people/" + itoa(p.ID), Form: form, Errors: errs,
		Under13Warning: store.Under13Warning, AgeKnown: store.AgeKnown(p.Birth),
		KnownUnder13: store.KnownUnder13(p.Birth, time.Now()), Waiting: p.Pending || p.PendingName != "",
		CanLogin: !self && p.UserID == 0, HeadsToggle: self && p.UserID != 0,
		HeadsEditing: p.UserID != 0 && !a.Child() && p.HeadsMayEdit, CanFamily: true}
	hh, err := s.store.PersonHouseholds(p.ID)
	if err != nil {
		return nil, err
	}
	if e.Vis, err = s.visRows(p.ID, hh); err != nil {
		return nil, err
	}
	if e.Notes, err = s.notesSection(v, a, e.Base); err != nil {
		return nil, err
	}
	in := map[int64]bool{}
	for _, m := range a.Memberships {
		h, ok := d.Household(m.HouseholdID)
		if !ok || !d.CanSeeHousehold(h) {
			continue
		}
		in[h.Household.ID] = true
		e.Households = append(e.Households, meMembership{
			ID: h.Household.ID, Name: h.Household.Name, Role: m.Role, Primary: m.IsPrimary,
			Contact: h.Household.Kind == store.KindContact, CanManage: access.CanHeadEditHousehold(v, h),
			CanLeave: access.CanRemoveFromHousehold(v, h, a),
		})
	}
	e.CanMulti = a.Child() && access.CanSetMultiHousehold(v, a)
	if e.CanMulti && p.MultiHousehold {
		// Households this child could be added to: any the viewer can see (a
		// head's own at once, someone else's by request).
		for _, h := range d.ListedHouseholds() {
			if id := h.Household.ID; in[id] {
				continue
			}
			id := h.Household.ID
			e.Targets = append(e.Targets, meTarget{ID: id, Name: h.Household.Name, Direct: access.CanHeadEditHousehold(v, h)})
		}
		for _, h := range d.EditableHouseholds() { // contact households the viewer manages
			if h.Household.Kind == store.KindContact && !in[h.Household.ID] {
				e.Targets = append(e.Targets, meTarget{ID: h.Household.ID, Name: h.Household.Name + " (contact household)", Direct: true})
			}
		}
		if e.Requests, err = s.store.JoinRequestsAskedBy(v.UserID); err != nil {
			return nil, err
		}
		var mine []store.JoinRequest
		for _, rq := range e.Requests {
			if rq.PersonID == p.ID {
				mine = append(mine, rq)
			}
		}
		e.Requests = mine
	}
	return e, nil
}

func (s *Server) renderPersonEdit(w http.ResponseWriter, r *http.Request, status int, d *access.Directory, a *access.PersonAccess, form personForm, errs []string) {
	e, err := s.personEdit(r, d, a, form, errs)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "me/person", map[string]any{"Title": e.Person.Name(), "E": e})
}

func (s *Server) handleMePerson(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	if d.Viewer.PersonID == a.Person.ID {
		http.Redirect(w, r, "/me", http.StatusSeeOther)
		return
	}
	s.renderPersonEdit(w, r, http.StatusOK, d, a, formFromPerson(a.Person), nil)
}

// handleMePersonUpdate saves the profile. Anyone who isn't staff who changes
// the name to one that matches someone else here has the rename held: the
// old name stays until staff approve, and the person is told only "Waiting
// for the office to check this." (never the match).
func (s *Server) handleMePersonUpdate(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	p := a.Person
	form := personFormFrom(r)
	form.Kind = p.Kind // only staff change this
	in, errs := form.input()
	if len(errs) > 0 {
		if d.Viewer.PersonID == p.ID {
			s.renderMe(w, r, http.StatusUnprocessableEntity, errs, form)
			return
		}
		s.renderPersonEdit(w, r, http.StatusUnprocessableEntity, d, a, form, errs)
		return
	}
	by := d.Viewer.UserID
	held := false
	if !d.Viewer.Staff {
		var err error
		if held, err = s.store.HoldRename(p.ID, in.FirstName, in.LastName, in.GoesBy, in.Suffix, by); err != nil {
			s.serverError(w, r, err)
			return
		}
		if held {
			in.FirstName, in.LastName, in.GoesBy, in.Suffix = p.FirstName, p.LastName, p.GoesBy, p.Suffix
		}
	}
	if err := s.store.UpdatePerson(p.ID, in, by); err != nil {
		s.serverError(w, r, err)
		return
	}
	if !held && p.PendingName != "" && (in.FirstName != p.FirstName || in.LastName != p.LastName || in.GoesBy != p.GoesBy || in.Suffix != p.Suffix) {
		if err := s.store.ClearHeldRename(p.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	back := s.personBack(r, p.ID)
	if after, err := s.store.GetPerson(p.ID); err == nil && p.UserID != 0 && after.UserID == 0 {
		s.redirectError(w, r, back, "Saved. They are under 13, so their login was unlinked and turned off.")
		return
	}
	if held {
		s.redirect(w, r, back, "Saved. "+waitingText)
		return
	}
	s.redirect(w, r, back, "Saved.")
}

// handleMePersonVisibility saves what shows. The heads' choices are the
// "shown" ones; staff's force hide is kept exactly as it is.
func (s *Server) handleMePersonVisibility(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	r.ParseForm()
	cur, err := s.store.PersonFieldSettings(a.Person.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	settings := map[store.Field]store.FieldSetting{}
	for _, f := range store.PersonFields {
		set := store.FieldSetting{Shown: cur[f].Shown, ForceHidden: cur[f].ForceHidden}
		if _, present := r.PostForm["shown:"+string(f)]; present {
			set.Shown = parseShown(r.PostFormValue("shown:" + string(f)))
		}
		settings[f] = set
	}
	if err := s.store.SetPersonFieldSettings(a.Person.ID, settings, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, s.personBack(r, a.Person.ID), "Visibility saved.")
}

func (s *Server) handleMePersonPhoto(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	msg, err := s.savePersonPhoto(r, a.Person.ID, d.Viewer.UserID)
	s.photoResult(w, r, s.personBack(r, a.Person.ID), "Photo saved.", msg, err)
}

func (s *Server) handleMePersonPhotoRemove(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	if err := s.removePersonPhoto(a.Person.ID, d.Viewer.UserID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, s.personBack(r, a.Person.ID), "Photo removed.")
}

// handleMeHeadsMayEdit is the linked adult's own choice, "Let my household's
// heads edit my profile". It is only ever for the viewer's own profile, and
// the store refuses it while staff have locked it.
func (s *Server) handleMeHeadsMayEdit(w http.ResponseWriter, r *http.Request) {
	v, err := s.viewer(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.PersonID == 0 {
		s.denied(w, r, "You don't have a profile to change.")
		return
	}
	on := r.PostFormValue("heads_may_edit") == "1"
	if err := s.store.SetHeadsMayEditSelf(v.PersonID, on, v.UserID); err != nil {
		s.failed(w, r, "/me", err)
		return
	}
	s.redirect(w, r, "/me", "Saved.")
}

// handleMePersonLogin is "Can have their own login" for a head: turn it on
// or off for an unlinked person in their household, and make the claim link.
// Under-13 and the 13+ tick are the store's rules, as for staff.
func (s *Server) handleMePersonLogin(w http.ResponseWriter, r *http.Request) {
	d, a, ok := s.editablePerson(w, r)
	if !ok {
		return
	}
	id, by := a.Person.ID, d.Viewer.UserID
	back := s.personBack(r, id)
	if a.Person.UserID != 0 || d.Viewer.PersonID == id {
		s.denied(w, r, "They already have a login.")
		return
	}
	switch r.PostFormValue("action") {
	case "allow":
		if err := s.store.SetLoginAllowed(id, true, r.PostFormValue("attest13") == "1", by); err != nil {
			s.failed(w, r, back, err)
			return
		}
		s.redirect(w, r, back, "They can have their own login now. Make a claim link for them.")
	case "disallow":
		if err := s.store.SetLoginAllowed(id, false, false, by); err != nil {
			s.failed(w, r, back, err)
			return
		}
		s.redirect(w, r, back, "Turned off. Any unused claim link stopped working.")
	case "claim":
		code, err := s.store.CreateClaim(id, by)
		if err != nil {
			s.failed(w, r, back, err)
			return
		}
		// Rendered straight away, never redirected to: the code is shown once.
		s.render(w, r, http.StatusOK, "me/claim_link", map[string]any{
			"Title": "Claim link", "Person": a.Person, "Back": back, "ClaimURL": s.baseURL(r) + "/me/claim/" + code,
		})
	default:
		s.redirectError(w, r, back, "Choose what to do.")
	}
}
