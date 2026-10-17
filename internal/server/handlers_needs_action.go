package server

import (
	"net/http"
)

// Needs action (staff): /admin/needs-action. Pending self sign-ups and
// households, held name matches shown next to the entries they match,
// held renames, and under-13 logins still to switch off.

func (s *Server) handleNeedsAction(w http.ResponseWriter, r *http.Request) {
	na, err := s.store.ListNeedsAction()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "needs_action/index", map[string]any{
		"Title": "Needs action", "NA": na,
		// The Suspend in User Management button needs SSO on and the
		// staff member's own grant.
		"CanSuspend": s.SSO().Enabled() && actingGrant(r) != "",
	})
}

// needsActionDone redirects back to the list after a decision.
func (s *Server) needsActionDone(w http.ResponseWriter, r *http.Request, okMsg string, err error) {
	if err == nil {
		s.redirect(w, r, "/admin/needs-action", okMsg)
		return
	}
	if msg, ok := friendlyError(err); ok {
		s.redirectError(w, r, "/admin/needs-action", msg)
		return
	}
	s.serverError(w, r, err)
}

func (s *Server) handleNAApprove(w http.ResponseWriter, r *http.Request) {
	err := s.store.ApprovePerson(pathID(r), currentUser(r).ID)
	s.needsActionDone(w, r, "Approved.", err)
}

func (s *Server) handleNATurnDown(w http.ResponseWriter, r *http.Request) {
	photo, err := s.store.TurnDownPerson(pathID(r), currentUser(r).ID)
	if err == nil {
		s.removePhoto(photo)
	}
	s.needsActionDone(w, r, "Turned down. The person who made it will see a note.", err)
}

func (s *Server) handleNASame(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.serverError(w, r, err)
		return
	}
	photo, err := s.store.ResolveSamePerson(pathID(r), formInt(r, "existing_id"), r.PostForm["copy"],
		r.PostFormValue("attest13") == "1", currentUser(r).ID)
	if err == nil {
		s.removePhoto(photo)
	}
	s.needsActionDone(w, r, "Same person: the held entry is gone and the existing person is used.", err)
}

func (s *Server) handleNADifferent(w http.ResponseWriter, r *http.Request) {
	err := s.store.ApproveDifferent(pathID(r), formStr(r, "goes_by"), formStr(r, "suffix"), currentUser(r).ID)
	s.needsActionDone(w, r, "Approved as a different person.", err)
}

func (s *Server) handleNAHouseholdApprove(w http.ResponseWriter, r *http.Request) {
	err := s.store.ApproveHousehold(pathID(r), currentUser(r).ID)
	s.needsActionDone(w, r, "Household approved.", err)
}

func (s *Server) handleNAHouseholdTurnDown(w http.ResponseWriter, r *http.Request) {
	photos, err := s.store.TurnDownHousehold(pathID(r), currentUser(r).ID)
	for _, p := range photos {
		s.removePhoto(p)
	}
	s.needsActionDone(w, r, "Household turned down. The person who made it will see a note.", err)
}

func (s *Server) handleNARenameApprove(w http.ResponseWriter, r *http.Request) {
	err := s.store.ApproveRename(pathID(r), currentUser(r).ID)
	s.needsActionDone(w, r, "Name change approved.", err)
}

func (s *Server) handleNARenameTurnDown(w http.ResponseWriter, r *http.Request) {
	err := s.store.RejectRename(pathID(r), currentUser(r).ID)
	s.needsActionDone(w, r, "Name change turned down.", err)
}

// handleNAUnder13Suspend is the Suspend in User Management button: the
// PATCH with this staff member's grant, then the item is done. On failure
// it stays.
func (s *Server) handleNAUnder13Suspend(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.Under13ItemFor(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.suspendUnder13(r.Context(), actingGrant(r), it, currentUser(r).ID); err != nil {
		s.log.Warn("under-13: suspend in User Management failed", "item", it.ID, "by", currentUser(r).Username, "err", err)
		s.redirectError(w, r, "/admin/needs-action", "Not suspended: "+under13Failure(err))
		return
	}
	s.redirect(w, r, "/admin/needs-action", "Suspended in User Management and signed out here.")
}

func (s *Server) handleNAUnder13Done(w http.ResponseWriter, r *http.Request) {
	err := s.store.ResolveUnder13(pathID(r), currentUser(r).ID)
	s.needsActionDone(w, r, "Marked done.", err)
}
