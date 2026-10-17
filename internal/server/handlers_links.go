package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/store"
)

// Linking profiles to users and logins (staff): the sections on the person
// page. The rules live in store/links.go; these handlers only decide who
// may call them (staff here; heads of household in step 5, which can reuse
// store.SetLoginAllowed and store.CreateClaim with their own check).

// friendlyLinkError turns the store's expected refusals into a sentence
// for the person; ok is false for anything unexpected.
func friendlyLinkError(err error) (string, bool) {
	for _, known := range []error{
		store.ErrUnder13, store.ErrNeedsAttest, store.ErrUserHasProfile, store.ErrAlreadyLinked,
		store.ErrNoLogin, store.ErrClaimInvalid, store.ErrNotPending, store.ErrNotYours, store.ErrHasHousehold,
	} {
		if errors.Is(err, known) {
			msg := err.Error()
			return strings.ToUpper(msg[:1]) + msg[1:] + ".", true
		}
	}
	return "", false
}

// addLinkData adds what the person page's login section needs.
func (s *Server) addLinkData(data map[string]any, p store.Person) error {
	data["Under13Warning"] = store.Under13Warning
	data["AgeKnown"] = store.AgeKnown(p.Birth)
	data["KnownUnder13"] = store.KnownUnder13(p.Birth, time.Now())
	if p.UserID != 0 {
		u, err := s.store.GetUser(p.UserID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		data["LinkedUser"] = u
		return nil
	}
	if s.SSO().Enabled() {
		data["SSOLink"] = true
		picks, err := s.linkablePeople(context.Background())
		if err != nil {
			s.log.Warn("link picker: User Management list failed", "err", err)
			data["LinkError"] = ssoWords(err)
		}
		data["LinkableSSO"] = picks
		return nil
	}
	users, err := s.store.UsersWithoutProfile()
	if err != nil {
		return err
	}
	data["LinkableUsers"] = users
	return nil
}

func (s *Server) personLinkRedirect(w http.ResponseWriter, r *http.Request, id int64, okMsg string, err error) {
	to := "/admin/people/" + itoa(id)
	if err == nil {
		s.redirect(w, r, to, okMsg)
		return
	}
	if msg, ok := friendlyLinkError(err); ok {
		s.redirectError(w, r, to, msg)
		return
	}
	s.serverError(w, r, err)
}

func (s *Server) handlePersonLink(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	userID := formInt(r, "user_id")
	if sub := formStr(r, "sub"); sub != "" && s.SSO().Enabled() {
		// Picked from User Management's list: link by sso_subject.
		u, err := s.userForSubject(r.Context(), sub)
		if errors.Is(err, store.ErrNotFound) {
			s.redirectError(w, r, "/admin/people/"+itoa(id), "That person doesn't have access to the directory in User Management (any more).")
			return
		}
		if err != nil {
			s.redirectError(w, r, "/admin/people/"+itoa(id), ssoWords(err))
			return
		}
		userID = u.ID
	}
	err := s.store.LinkUser(id, userID, currentUser(r).ID, r.PostFormValue("attest13") == "1")
	s.personLinkRedirect(w, r, id, "Linked to the user.", err)
}

func (s *Server) handlePersonUnlink(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	err := s.store.UnlinkUser(id, currentUser(r).ID)
	s.personLinkRedirect(w, r, id, "Unlinked. The profile and the user both stay.", err)
}

// handlePersonLogin is "Can have their own login": action allow, disallow
// or claim (make the one-time link, shown once).
func (s *Server) handlePersonLogin(w http.ResponseWriter, r *http.Request) {
	id, by := pathID(r), currentUser(r).ID
	switch r.PostFormValue("action") {
	case "allow":
		err := s.store.SetLoginAllowed(id, true, r.PostFormValue("attest13") == "1", by)
		s.personLinkRedirect(w, r, id, "They can have their own login now. Make a claim link for them below.", err)
	case "disallow":
		err := s.store.SetLoginAllowed(id, false, false, by)
		s.personLinkRedirect(w, r, id, "Turned off. Any unused claim link stopped working.", err)
	case "claim":
		code, err := s.store.CreateClaim(id, by)
		if err != nil {
			s.personLinkRedirect(w, r, id, "", err)
			return
		}
		p, err := s.store.GetPerson(id)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		// Rendered straight away, never redirected to: the code is shown
		// once and exists nowhere else in the clear.
		s.render(w, r, http.StatusOK, "people/claim", map[string]any{
			"Title": "Claim link", "Person": p, "ClaimURL": s.baseURL(r) + "/me/claim/" + code,
		})
	default:
		s.redirectError(w, r, "/admin/people/"+itoa(id), "Choose what to do.")
	}
}
