package server

import (
	"errors"
	"net/http"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
)

// /me: the signed-in user's own page. Step 4 shows the profile (through the
// access view model, like every member page), self sign-up and claim links;
// step 5 adds households and join requests.

// selfSignupOn is the /admin/settings switch, off by default.
func (s *Server) selfSignupOn() bool { return s.store.Setting("self_signup", "") == "1" }

const waitingText = "Waiting for the office to check this."

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	s.renderMe(w, r, http.StatusOK, nil, personForm{})
}

func (s *Server) renderMe(w http.ResponseWriter, r *http.Request, status int, errs []string, form personForm) {
	u := currentUser(r)
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	notices, err := s.store.UnseenNotices(u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"Title": "My profile", "Notices": notices, "Errors": errs, "Form": form,
		"SelfSignup": s.selfSignupOn(), "Under13Warning": store.Under13Warning, "Waiting": waitingText,
	}
	if pid := d.Viewer.PersonID; pid != 0 {
		page, ok := d.Page(pid)
		data["Page"], data["Mine"], data["Hidden"] = page, true, !ok
		// Only the flags, never anyone else: whether staff still have to look.
		p, err := s.store.GetPerson(pid)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["IsWaiting"] = p.Pending || p.PendingName != ""
		if a, ok := d.Person(pid); ok {
			if form == (personForm{}) {
				form = formFromPerson(p)
			}
			edit, err := s.personEdit(r, d, a, form, errs)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			data["E"], data["Errors"] = edit, nil
		}
		for _, h := range page.Households {
			if hh, err := s.store.GetHousehold(h.ID); err == nil && hh.Pending && hh.CreatedBy == u.ID {
				data["PendingHousehold"] = hh
				data["IsWaiting"] = true
			}
		}
		if data["PendingHousehold"] == nil && s.selfSignupOn() && len(page.Households) == 0 {
			data["CanMakeHousehold"] = true
		}
	}
	if err := s.addMeHeads(data, d, u); err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(notices) > 0 {
		if err := s.store.MarkNoticesSeen(u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, status, "me/index", data)
}

// meSelfSignup guards the self sign-up posts: the switch is on.
func (s *Server) meSelfSignup(w http.ResponseWriter, r *http.Request) bool {
	if !s.selfSignupOn() {
		s.render(w, r, http.StatusForbidden, "error", map[string]any{
			"Title": "Not open", "Message": "Making your own profile isn't turned on. Ask the office to add you.",
		})
		return false
	}
	return true
}

func (s *Server) handleMeProfile(w http.ResponseWriter, r *http.Request) {
	if !s.meSelfSignup(w, r) {
		return
	}
	form := personFormFrom(r)
	in, errs := form.input()
	if len(errs) > 0 {
		s.renderMe(w, r, http.StatusUnprocessableEntity, errs, form)
		return
	}
	_, _, err := s.store.MakeMyProfile(currentUser(r).ID, in, r.PostFormValue("attest13") == "1")
	if err != nil {
		if msg, ok := friendlyLinkError(err); ok {
			s.renderMe(w, r, http.StatusUnprocessableEntity, []string{msg}, form)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/me", "Thanks. "+waitingText)
}

func (s *Server) handleMeHousehold(w http.ResponseWriter, r *http.Request) {
	if !s.meSelfSignup(w, r) {
		return
	}
	if _, err := s.store.CreateSelfHousehold(currentUser(r).ID, formStr(r, "name")); err != nil {
		if msg, ok := friendlyLinkError(err); ok {
			s.redirectError(w, r, "/me", msg)
			return
		}
		s.redirectError(w, r, "/me", err.Error())
		return
	}
	s.redirect(w, r, "/me", "Household made. "+waitingText)
}

func (s *Server) handleMeHouseholdPerson(w http.ResponseWriter, r *http.Request) {
	if !s.meSelfSignup(w, r) {
		return
	}
	form := personFormFrom(r)
	in, errs := form.input()
	if len(errs) > 0 {
		s.renderMe(w, r, http.StatusUnprocessableEntity, errs, form)
		return
	}
	role := formStr(r, "role")
	if !store.ValidHouseholdRole(role) {
		role = store.RoleChild
	}
	// Whether the name matched someone is never said: either way the
	// answer is the same.
	if _, _, err := s.store.AddToSelfHousehold(currentUser(r).ID, pathID(r), in, role); err != nil {
		if msg, ok := friendlyLinkError(err); ok {
			s.redirectError(w, r, "/me", msg)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/me", "Added. "+waitingText)
}

// Claim links ------------------------------------------------------------

// claimProblem explains a refused claim in words for the person.
func claimProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrUnder13):
		return "Children under 13 can't have their own login, so this link can't be used."
	case errors.Is(err, store.ErrUserHasProfile):
		return "You already have a profile, so this link can't be used."
	}
	return "This link doesn't work any more. It may have been used already or expired. Ask for a new one."
}

func (s *Server) handleClaimForm(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ClaimPreview(r.PathValue("code"), currentUser(r).ID)
	if err != nil {
		if _, ok := friendlyLinkError(err); !ok {
			s.serverError(w, r, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, "error", map[string]any{"Title": "Claim link", "Message": claimProblem(err)})
		return
	}
	// Only the name is shown, as the always-visible part of a profile.
	s.render(w, r, http.StatusOK, "me/claim", map[string]any{"Title": "Claim your profile", "PersonName": p.Name(), "Code": r.PathValue("code")})
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.AcceptClaim(r.PathValue("code"), currentUser(r).ID); err != nil {
		if _, ok := friendlyLinkError(err); !ok {
			s.serverError(w, r, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, "error", map[string]any{"Title": "Claim link", "Message": claimProblem(err)})
		return
	}
	s.redirect(w, r, "/me", "Done. This is your profile now.")
}

// meHousehold is a household the viewer looks after, for the /me list.
type meHousehold struct {
	ID      int64
	Name    string
	Contact bool
	Managed bool // looked after as the managing person, not as a head
}

// addMeHeads adds what a head sees on /me: the households they look after,
// join requests to answer, requests they sent, and the contact household form.
func (s *Server) addMeHeads(data map[string]any, d *access.Directory, u *store.User) error {
	v := d.Viewer
	var looked []meHousehold
	for _, h := range d.EditableHouseholds() {
		if v.Staff && !v.HeadOf[h.Household.ID] && !(h.ManagedActive && h.Household.ManagedBy == v.PersonID) {
			continue // staff have /admin; /me lists only their own
		}
		looked = append(looked, meHousehold{ID: h.Household.ID, Name: h.Household.Name,
			Contact: h.Household.Kind == store.KindContact, Managed: !v.HeadOf[h.Household.ID]})
	}
	data["Looked"] = looked
	data["CanContact"] = v.PersonID != 0 && len(v.HeadOf) > 0
	open, err := s.store.OpenJoinRequests()
	if err != nil {
		return err
	}
	var answer []store.JoinRequest
	for _, rq := range open {
		if h, ok := d.Household(rq.HouseholdID); ok && access.CanHeadEditHousehold(v, h) && !v.Staff {
			answer = append(answer, rq)
		}
	}
	data["ToAnswer"] = answer
	sent, err := s.store.JoinRequestsAskedBy(u.ID)
	data["Sent"] = sent
	return err
}
