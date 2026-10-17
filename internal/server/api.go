package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
)

// The read-only API for the other church apps (PLAN.md "Sign-ups and the
// API"). App tokens only: no cookies, no sessions, no CSRF. Everything that
// leaves here is built field by field from the small structs below, so a
// birthday, email, phone, photo or address can't slip out by accident.

// maxAPIPeople is how many IDs one GET /api/v1/people call may ask for.
const maxAPIPeople = 500

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func apiFail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

// requireToken lets a request through only with a valid app token
// (Authorization: Bearer md_…). The lookup is by the token's hash, so a
// timing difference reveals nothing about a real token; the final compare
// is constant-time as well.
func (s *Server) requireToken(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		auth := r.Header.Get("Authorization")
		raw, ok := strings.CutPrefix(auth, "Bearer ")
		raw = strings.TrimSpace(raw)
		if !ok || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="directory"`)
			apiFail(w, http.StatusUnauthorized, "unauthorized", "Send an app token as Authorization: Bearer md_…")
			return
		}
		t, err := s.store.AuthenticateToken(raw)
		if errors.Is(err, store.ErrNotFound) ||
			(err == nil && subtle.ConstantTimeCompare([]byte(t.Kind), []byte(store.TokenApp)) != 1) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="directory", error="invalid_token"`)
			apiFail(w, http.StatusUnauthorized, "unauthorized", "That token isn't valid.")
			return
		}
		if err != nil {
			s.log.Error("api token", "err", err)
			apiFail(w, http.StatusInternalServerError, "server_error", "Something went wrong.")
			return
		}
		next(w, r)
	})
}

func (s *Server) apiNotFound(w http.ResponseWriter, r *http.Request) {
	apiFail(w, http.StatusNotFound, "not_found", "No such API route.")
}

func (s *Server) apiServerError(w http.ResponseWriter, err error) {
	s.log.Error("api", "err", err)
	apiFail(w, http.StatusInternalServerError, "server_error", "Something went wrong.")
}

// apiDirectory loads everything as staff would see it. The API does its
// own leaving-out (members only, no archived, no pending) and builds its
// output from tiny structs that hold no private field.
func (s *Server) apiDirectory() (*access.Directory, error) {
	return access.Load(s.store, &access.Viewer{Staff: true})
}

// apiPerson is a person as the API shows them: name, household role and
// household, age. Nothing else.
type apiPerson struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role,omitempty"` // head / adult / child, in the household
	Age       *int   `json:"age,omitempty"`
	Household string `json:"household,omitempty"`
}

// apiListable: a member who isn't archived or waiting for staff.
func apiListable(a *access.PersonAccess) bool {
	p := a.Person
	return p.Kind == store.KindMember && !p.Archived() && !p.Pending
}

func ageOf(p store.Person) *int {
	if n, ok := store.AgeOn(p.Birth, time.Now()); ok {
		return &n
	}
	return nil
}

// household picks the membership shown: the primary one, else the first,
// skipping archived households. Returns the zero Membership when none.
func apiMembership(d *access.Directory, a *access.PersonAccess, prefer map[int64]bool) (store.Membership, string) {
	var first *store.Membership
	for i := range a.Memberships {
		m := a.Memberships[i]
		if h, ok := d.Household(m.HouseholdID); !ok || h.Household.Archived() {
			continue
		}
		if prefer != nil && !prefer[m.HouseholdID] {
			continue
		}
		if m.IsPrimary {
			return m, householdName(d, m.HouseholdID)
		}
		if first == nil {
			first = &m
		}
	}
	if first != nil {
		return *first, householdName(d, first.HouseholdID)
	}
	return store.Membership{}, ""
}

func householdName(d *access.Directory, id int64) string {
	if h, ok := d.Household(id); ok {
		return h.Household.Name
	}
	return ""
}

// handleAPIByUser: GET /api/v1/people/by-user/{sub}. The user's profile and
// the people they may sign up: everyone in the households they head, else
// only themselves. {sub} is the user's SSO subject (the User Management
// user ID).
func (s *Server) handleAPIByUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserBySubject(r.PathValue("sub"))
	if errors.Is(err, store.ErrNotFound) {
		apiFail(w, http.StatusNotFound, "unknown_user", "No user with that subject.")
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	out := struct {
		Profile *apiPerson  `json:"profile"`
		People  []apiPerson `json:"people"`
	}{People: []apiPerson{}}

	prof, err := s.store.PersonByUser(u.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !u.Active) {
		writeJSON(w, http.StatusOK, out) // a login with no profile (or no access) signs nobody up
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	d, err := s.apiDirectory()
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	self, ok := d.Person(prof.ID)
	if !ok || !apiListable(self) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	v, err := access.NewViewer(s.store, u)
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	build := func(a *access.PersonAccess, prefer map[int64]bool) apiPerson {
		m, hn := apiMembership(d, a, prefer)
		return apiPerson{ID: a.Person.ID, Name: a.Person.Name(), Role: m.Role, Age: ageOf(a.Person), Household: hn}
	}
	me := build(self, v.HeadOf)
	out.Profile = &me
	seen := map[int64]bool{}
	add := func(p apiPerson) {
		if !seen[p.ID] {
			seen[p.ID] = true
			out.People = append(out.People, p)
		}
	}
	add(me)
	heads := make([]int64, 0, len(v.HeadOf))
	for hid, isHead := range v.HeadOf {
		if isHead {
			heads = append(heads, hid)
		}
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i] < heads[j] })
	for _, hid := range heads {
		h, ok := d.Household(hid)
		if !ok || h.Household.Archived() {
			continue
		}
		for _, m := range h.Members { // heads first
			a, ok := d.Person(m.PersonID)
			if !ok || !apiListable(a) {
				continue
			}
			add(apiPerson{ID: a.Person.ID, Name: a.Person.Name(), Role: m.Role, Age: ageOf(a.Person),
				Household: h.Household.Name})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type apiNote struct {
	Kind string `json:"kind"`
	Body string `json:"body"`
}

// apiTeamPerson is a person on an event team's page. Gone people have only
// ID and Gone.
type apiTeamPerson struct {
	ID        int64     `json:"id"`
	Gone      bool      `json:"gone,omitempty"`
	Name      string    `json:"name,omitempty"`
	Age       *int      `json:"age,omitempty"`
	Household string    `json:"household,omitempty"`
	Notes     []apiNote `json:"notes,omitempty"`
}

// handleAPIPeople: GET /api/v1/people?ids=1,2,3&notes=allergy,dietary.
func (s *Server) handleAPIPeople(w http.ResponseWriter, r *http.Request) {
	var ids []int64
	seen := map[int64]bool{}
	for _, f := range strings.Split(r.URL.Query().Get("ids"), ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil || id <= 0 {
			apiFail(w, http.StatusBadRequest, "bad_ids", "ids must be a comma-separated list of numbers.")
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
		if len(ids) > maxAPIPeople {
			apiFail(w, http.StatusBadRequest, "too_many_ids", "Ask for at most "+strconv.Itoa(maxAPIPeople)+" ids at a time.")
			return
		}
	}
	if len(ids) == 0 {
		apiFail(w, http.StatusBadRequest, "missing_ids", "Give ids=1,2,3.")
		return
	}
	var kinds []string
	for _, k := range strings.Split(r.URL.Query().Get("notes"), ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		valid := false
		for _, nk := range store.NoteKinds {
			valid = valid || nk == k
		}
		if !valid {
			apiFail(w, http.StatusBadRequest, "bad_notes", "Unknown note kind "+strconv.Quote(k)+".")
			return
		}
		kinds = append(kinds, k)
	}
	d, err := s.apiDirectory()
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	var live []int64
	for _, id := range ids {
		if a, ok := d.Person(id); ok && apiListable(a) {
			live = append(live, id)
		}
	}
	notes := map[int64][]apiNote{}
	shared, err := s.store.SharedNotes(live, kinds)
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	for _, n := range shared {
		notes[n.PersonID] = append(notes[n.PersonID], apiNote{Kind: n.Kind, Body: n.Body})
	}
	people := make([]apiTeamPerson, 0, len(ids))
	for _, id := range ids {
		a, ok := d.Person(id)
		if !ok || !apiListable(a) {
			people = append(people, apiTeamPerson{ID: id, Gone: true})
			continue
		}
		_, hn := apiMembership(d, a, nil)
		people = append(people, apiTeamPerson{ID: id, Name: a.Person.Name(), Age: ageOf(a.Person),
			Household: hn, Notes: notes[id]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": people})
}

type apiGroupMember struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

type apiGroup struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	People      []apiGroupMember `json:"people,omitempty"`
}

// groupsDirectory is the directory as an ordinary signed-in member sees
// it: staff-only and archived groups, and people a member can't see, are
// left out by the same code the pages use.
func (s *Server) groupsDirectory() (*access.Directory, error) {
	return access.Load(s.store, &access.Viewer{})
}

func (s *Server) handleAPIGroups(w http.ResponseWriter, r *http.Request) {
	d, err := s.groupsDirectory()
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	out := []apiGroup{}
	for _, g := range d.Groups() {
		out = append(out, apiGroup{ID: g.ID, Name: g.Name, Description: g.Description})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (s *Server) handleAPIGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiFail(w, http.StatusNotFound, "not_found", "No such group.")
		return
	}
	d, err := s.groupsDirectory()
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	gp, ok := d.Group(id)
	if !ok {
		apiFail(w, http.StatusNotFound, "not_found", "No such group.")
		return
	}
	g := apiGroup{ID: gp.ID, Name: gp.Name, Description: gp.Description, People: []apiGroupMember{}}
	for _, p := range gp.People {
		g.People = append(g.People, apiGroupMember{ID: p.Person.ID, Name: p.Person.Name, Label: p.Label})
	}
	writeJSON(w, http.StatusOK, g)
}
