package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/access"
)

// The member pages: everything here is built from the access package's
// view models, which only hold what the viewer may see. Templates for these
// pages never get a store.Person.

// viewer works out who is looking, once per request. Handlers call it (or
// directory) once and pass the result on.
func (s *Server) viewer(r *http.Request) (*access.Viewer, error) {
	return access.NewViewer(s.store, currentUser(r))
}

// directory loads the people, households and settings for the viewer.
func (s *Server) directory(r *http.Request) (*access.Directory, error) {
	v, err := s.viewer(r)
	if err != nil {
		return nil, err
	}
	return access.Load(s.store, v)
}

func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	listing, groupID := d.Listing(q), int64(0)
	if gid := queryInt(r, "group"); gid != 0 { // ?group=ID: only people in that group
		l, ok := d.ListingGroup(q, gid)
		if !ok {
			s.notFound(w, r) // a group the viewer can't see isn't confirmed to exist
			return
		}
		listing, groupID = l, gid
	}
	s.render(w, r, http.StatusOK, "directory/index", map[string]any{
		"Title": "", "Listing": listing, "Q": q, "Groups": d.Groups(), "GroupID": groupID,
	})
}

func (s *Server) handleFamily(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	fv, ok := d.Family(pathID(r), false)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "directory/household", map[string]any{"Title": fv.Name, "Family": fv})
}

func (s *Server) handlePersonPage(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, ok := d.Page(pathID(r))
	if !ok {
		s.notFound(w, r) // never 403: a person you can't see isn't confirmed to exist
		return
	}
	notes, err := s.personNotes(r, d, pathID(r), "/people/"+itoa(pathID(r)))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "directory/person", map[string]any{"Title": page.Person.Name, "Page": page, "Notes": notes})
}

func (s *Server) handleBirthdays(w http.ResponseWriter, r *http.Request) {
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "directory/birthdays", map[string]any{
		"Title": "Birthdays", "Months": d.Birthdays(time.Now()),
	})
}
