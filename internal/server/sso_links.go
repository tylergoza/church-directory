package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/sso"
	"github.com/tylergoza/church-directory/internal/store"
)

// Single sign-on and linking profiles ----------------------------------------
//
// With SSO on, "Link to user" lists the people User Management says have
// directory access, and links by sso_subject. The local users table is only
// a cache of them: a row is made the first time someone is picked (or signs
// in), by store.FindOrCreateSSOUser, the one function for that.

// actingGrant is the signed-in person's SSO grant, or "" when they signed
// in with the break-glass local login.
func actingGrant(r *http.Request) string {
	if sess, _ := r.Context().Value(ctxSession).(*store.Session); sess != nil {
		return sess.Grant
	}
	return ""
}

const ssoCallTimeout = 8 * time.Second

// ssoWords words a failed call to User Management for the page.
func ssoWords(err error) string {
	var ae *sso.APIError
	switch {
	case errors.As(err, &ae):
		return ae.Message()
	case errors.Is(err, sso.ErrUnreachable):
		return "User Management can't be reached right now. Nothing was changed; please try again in a few minutes."
	}
	return "Something went wrong talking to User Management."
}

// ssoPick is one person in the Link to user picker.
type ssoPick struct {
	Sub, Username, Name string
}

// linkablePeople lists User Management's active directory users who don't
// have a profile yet (the list is cached for a minute in internal/sso).
func (s *Server) linkablePeople(ctx context.Context) ([]ssoPick, error) {
	ctx, cancel := context.WithTimeout(ctx, ssoCallTimeout)
	defer cancel()
	list, err := s.SSO().AppUsers(ctx)
	if err != nil {
		return nil, err
	}
	taken, err := s.store.SubjectsWithProfile()
	if err != nil {
		return nil, err
	}
	var out []ssoPick
	for _, u := range list.Users {
		if !u.Active || u.Sub == "" || store.RoleFromSSO(u.Role) == "" || taken[u.Sub] {
			continue
		}
		name := u.Name
		if name == "" {
			name = u.Username
		}
		out = append(out, ssoPick{Sub: u.Sub, Username: u.Username, Name: name})
	}
	return out, nil
}

// userForSubject is the local user for a User Management user ID chosen in
// the picker, made through FindOrCreateSSOUser if missing. The person must
// be on User Management's current list with directory access.
func (s *Server) userForSubject(ctx context.Context, sub string) (*store.User, error) {
	ctx, cancel := context.WithTimeout(ctx, ssoCallTimeout)
	defer cancel()
	list, err := s.SSO().AppUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range list.Users {
		if u.Sub == sub && u.Active {
			return s.store.FindOrCreateSSOUser(u.Sub, u.Username, u.Name, store.RoleFromSSO(u.Role))
		}
	}
	return nil, store.ErrNotFound
}

// Found-later under-13 --------------------------------------------------------

var errNoGrant = errors.New("you signed in with the local login")

// suspendUnder13 switches off the item's user in User Management, acting
// as the signed-in staff member (their grant goes along as X-Acting-Grant;
// User Management decides whether they may), then closes the item and
// signs the user out here. On any failure the item stays open.
func (s *Server) suspendUnder13(ctx context.Context, grant string, it store.Under13Item, by int64) error {
	if !s.SSO().Enabled() || it.SSOSubject == "" {
		return errors.New("single sign-on is off")
	}
	if grant == "" {
		return errNoGrant
	}
	ctx, cancel := context.WithTimeout(ctx, ssoCallTimeout)
	defer cancel()
	t := true
	if _, err := s.SSO().UpdateAppUser(ctx, grant, it.SSOSubject, nil, &t); err != nil {
		return err
	}
	if err := s.store.SetActive(it.UserID, false); err != nil {
		return err
	}
	if err := s.store.DeleteSessionsForUser(it.UserID); err != nil {
		return err
	}
	return s.store.ResolveUnder13(it.ID, by)
}

// suspendUnder13For tries every open item for a person (called right after
// staff saved a birthday). It returns how many were suspended and the first
// failure, if any; failed items stay on Needs action.
func (s *Server) suspendUnder13For(r *http.Request, personID int64) (done int, err error) {
	items, lerr := s.store.Under13Items()
	if lerr != nil {
		return 0, lerr
	}
	for _, it := range items {
		if it.PersonID != personID {
			continue
		}
		if e := s.suspendUnder13(r.Context(), actingGrant(r), it, currentUser(r).ID); e != nil {
			s.log.Warn("under-13: suspend in User Management failed; item stays on Needs action", "item", it.ID, "err", e)
			if err == nil {
				err = e
			}
			continue
		}
		done++
	}
	return done, err
}

// under13Failure words why a suspension didn't happen.
func under13Failure(err error) string {
	if errors.Is(err, errNoGrant) {
		return "You signed in with the local login, so it can't be done from here: sign in through User Management, or suspend them in User Management yourself and press Done."
	}
	if strings.Contains(err.Error(), "single sign-on is off") {
		return "Single sign-on is off."
	}
	return ssoWords(err)
}
