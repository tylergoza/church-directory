package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tylergoza/church-directory/internal/store"
)

// Groups are labels, not permissions. Members see groups visible to
// members; staff see all of them (and manage them under /admin/groups).

// Member pages -----------------------------------------------------------

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "groups/index", map[string]any{"Title": "Groups", "Groups": d.Groups()})
}

func (s *Server) handleGroupPage(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	gp, ok := d.Group(pathID(r))
	if !ok {
		s.notFound(w, r) // a staff group isn't confirmed to exist
		return
	}
	s.render(w, r, http.StatusOK, "groups/show", map[string]any{"Title": gp.Name, "Group": gp})
}

// Staff: /admin/groups ---------------------------------------------------

func (s *Server) handleAdminGroups(w http.ResponseWriter, r *http.Request) {
	archived := r.URL.Query().Get("show") == "archived"
	rows, err := s.store.ListGroups(archived)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	show := ""
	if archived {
		show = "archived"
	}
	s.render(w, r, http.StatusOK, "groups/admin-index", map[string]any{
		"Title": "Groups", "Groups": rows, "Show": show,
		"Filters": []struct{ Value, Label string }{{"", "Active"}, {"archived", "Archived"}},
	})
}

func (s *Server) handleAdminGroupNew(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "groups/new", map[string]any{"Title": "Add group", "Form": store.GroupInput{VisibleTo: store.GroupMembers}})
}

func groupInputFrom(r *http.Request) store.GroupInput {
	return store.GroupInput{Name: formStr(r, "name"), Description: formStr(r, "description"), VisibleTo: formStr(r, "visible_to")}
}

func (s *Server) handleAdminGroupCreate(w http.ResponseWriter, r *http.Request) {
	in := groupInputFrom(r)
	errs := in.Validate()
	if len(errs) == 0 {
		id, err := s.store.CreateGroup(in, currentUser(r).ID)
		if err == nil {
			s.redirect(w, r, "/admin/groups/"+itoa(id), "Group added. Add its people below.")
			return
		}
		errs = []string{err.Error()}
	}
	s.render(w, r, http.StatusUnprocessableEntity, "groups/new", map[string]any{"Title": "Add group", "Form": in, "Errors": errs})
}

// addGroupMatch is a person who could be added to the group, from the search.
type addGroupMatch struct {
	Person     store.Person
	Households []string
}

func (s *Server) renderAdminGroup(w http.ResponseWriter, r *http.Request, status int, g store.Group, errs []string) {
	members, err := s.store.GroupMembersOf(g.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{"Title": g.Name, "Group": g, "Members": members, "Errors": errs}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		found, err := s.store.ListPeople(store.PeopleAll, q)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		in := map[int64]bool{}
		for _, m := range members {
			in[m.Person.ID] = true
		}
		var matches []addGroupMatch
		for _, p := range found {
			if !in[p.ID] && p.Kind == store.KindMember && len(matches) < 15 {
				matches = append(matches, addGroupMatch{Person: p.Person, Households: p.Households})
			}
		}
		data["Q"], data["Found"] = q, matches
	}
	s.render(w, r, status, "groups/edit", data)
}

func (s *Server) handleAdminGroupShow(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GetGroup(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdminGroup(w, r, http.StatusOK, g, nil)
}

func (s *Server) handleAdminGroupUpdate(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GetGroup(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	in := groupInputFrom(r)
	errs := in.Validate()
	if len(errs) == 0 {
		if err := s.store.UpdateGroup(g.ID, in, currentUser(r).ID); err == nil {
			s.redirect(w, r, "/admin/groups/"+itoa(g.ID), "Saved.")
			return
		} else if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		} else {
			errs = []string{err.Error()}
		}
	}
	g.Name, g.Description, g.VisibleTo = in.Name, in.Description, in.VisibleTo
	s.renderAdminGroup(w, r, http.StatusUnprocessableEntity, g, errs)
}

func (s *Server) handleAdminGroupArchive(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	archive := r.PostFormValue("archive") == "1"
	if err := s.store.ArchiveGroup(id, archive, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := "Moved back out of the archive."
	if archive {
		msg = "Archived. Members and the directory no longer see it."
	}
	s.redirect(w, r, "/admin/groups/"+itoa(id), msg)
}

// groupBack is where the member actions return to.
func groupBack(r *http.Request) string { return "/admin/groups/" + itoa(pathID(r)) }

func (s *Server) handleAdminGroupMemberAdd(w http.ResponseWriter, r *http.Request) {
	err := s.store.AddGroupMember(pathID(r), formInt(r, "person_id"), formStr(r, "label"), currentUser(r).ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, store.ErrGroupContact), errors.Is(err, store.ErrGroupArchived):
		s.redirectError(w, r, groupBack(r), "That person or group can't take new members: "+err.Error()+".")
	case err != nil:
		s.redirectError(w, r, groupBack(r), err.Error())
	default:
		s.redirect(w, r, groupBack(r), "Added.")
	}
}

// handleAdminGroupMemberUpdate saves a member's label and, when given,
// their place (1 is first).
func (s *Server) handleAdminGroupMemberUpdate(w http.ResponseWriter, r *http.Request) {
	gid, pid, by := pathID(r), pathValueID(r, "pid"), currentUser(r).ID
	err := s.store.SetGroupMemberLabel(gid, pid, formStr(r, "label"), by)
	if err == nil && formStr(r, "position") != "" {
		err = s.store.SetGroupMemberPosition(gid, pid, int(formInt(r, "position")), by)
	}
	s.groupMemberResult(w, r, err, "Saved.")
}

func (s *Server) handleAdminGroupMemberMove(w http.ResponseWriter, r *http.Request) {
	dir := 1
	if r.PostFormValue("dir") == "up" {
		dir = -1
	}
	s.groupMemberResult(w, r, s.store.MoveGroupMember(pathID(r), pathValueID(r, "pid"), dir, currentUser(r).ID), "")
}

func (s *Server) handleAdminGroupMemberRemove(w http.ResponseWriter, r *http.Request) {
	s.groupMemberResult(w, r, s.store.RemoveGroupMember(pathID(r), pathValueID(r, "pid"), currentUser(r).ID), "Removed from the group.")
}

func (s *Server) groupMemberResult(w http.ResponseWriter, r *http.Request, err error, msg string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.redirectError(w, r, groupBack(r), err.Error())
	default:
		s.redirect(w, r, groupBack(r), msg)
	}
}
