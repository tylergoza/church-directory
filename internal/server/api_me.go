package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/store"
	"github.com/tylergoza/church-directory/internal/umtoken"
)

// The per-person read API, /api/v1/me/…, for the church MCP server (and
// later the website's members area, MEMBERS_PLAN.md). The gate (token
// check, local user) is me_auth.go; the shapes are APP_CONTRACTS.md in
// ../mcp_server, section 3.2.
//
// Everything here is what the person sees signed in to the web pages,
// built from the same access code: the viewer is access.NewViewer for the
// token's user, people and households come from d.Listing, d.Page and
// d.Family (CanSeePerson, CanSeeHousehold), and every field from the view
// models (Visible). A field the viewer may not see is omitted. Never here:
// notes, photos, logins, user IDs, anything not on the web pages (the one
// addition, age, is worked out only from the birthday parts the viewer
// can already see: access.VisibleAge).

// meMaxItems bounds every list (the contract's cap).
const meMaxItems = 100

func (s *Server) meRoutes(mux *http.ServeMux) {
	s.meRoute(mux, "GET /api/v1/me", s.meSelf)
	s.meRoute(mux, "GET /api/v1/me/people", s.mePeople)
	s.meRoute(mux, "GET /api/v1/me/people/{id}", s.mePerson)
	s.meRoute(mux, "GET /api/v1/me/households/{id}", s.meHousehold)
	s.meCatchAll(mux)
}

// meUMConfig is the live single sign-on connection; the /me API is off
// without it.
func (s *Server) meUMConfig() (umtoken.Config, bool) {
	c := s.SSO()
	if !c.Enabled() || c.ClientID == "" {
		return umtoken.Config{}, false
	}
	return umtoken.Config{URL: c.InternalURL, ClientID: c.ClientID, ClientSecret: c.ClientSecret}, true
}

// meLocalUser is what the SSO callback and grant check do (sso.go). A role
// that isn't a directory role, and a login linked to a profile known to be
// under 13, get no user (403): under-13s never use the directory through a
// login, and an AI tool is no exception.
func (s *Server) meLocalUser(u umtoken.User) (*store.User, error) {
	user, err := s.store.FindOrCreateSSOUser(u.Sub, u.Username, u.Name, store.RoleFromSSO(u.Role))
	if errors.Is(err, store.ErrNoAccess) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p, err := s.store.PersonByUser(user.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return user, nil
	case err != nil:
		return nil, err
	case store.KnownUnder13(p.Birth, time.Now()):
		return nil, nil
	}
	return user, nil
}

// meJSON writes an answer with the /me headers (no-store, private).
func meJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// meError is the gate's {"error": msg} (me_auth.go calls it).
func meError(w http.ResponseWriter, status int, msg string) {
	meJSON(w, status, map[string]string{"error": msg})
}

// meFail is an error with a code, {"error": msg, "code": code}.
func meFail(w http.ResponseWriter, status int, code, msg string) {
	meJSON(w, status, apiError{Error: msg, Code: code})
}

func (s *Server) meServerError(w http.ResponseWriter, err error) {
	s.log.Error("me api", "err", err)
	meFail(w, http.StatusInternalServerError, "server_error", "Something went wrong.")
}

// meDirectory loads the directory exactly as the web pages do for the
// token's user.
func (s *Server) meDirectory(r *http.Request) (*access.Directory, error) {
	v, err := access.NewViewer(s.store, meUser(r))
	if err != nil {
		return nil, err
	}
	return access.Load(s.store, v)
}

// meLimit is ?limit= within 1..meMaxItems, meMaxItems when not given.
func meLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 || n > meMaxItems {
		return meMaxItems
	}
	return n
}

// mePathID is the {id}, or false (answered 404) when it isn't a number.
func mePathID(w http.ResponseWriter, r *http.Request, what string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		meFail(w, http.StatusNotFound, "not_found", "No such "+what+".")
		return 0, false
	}
	return id, true
}

type meRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// meHouseholdRef is a household a person is in, with their role there.
type meHouseholdRef struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	CanEdit *bool  `json:"can_edit,omitempty"` // only on GET /api/v1/me
}

// mePersonJSON is a person as the viewer may see them: built only from an
// access.PersonView (and VisibleAge), so every field went through Visible.
// Empty means hidden or not entered, and is omitted.
type mePersonJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"` // head / adult / child, in the household shown
	Age         int    `json:"age,omitempty"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Birthday    string `json:"birthday,omitempty"`
	Anniversary string `json:"anniversary,omitempty"`
	Household   *meRef `json:"household,omitempty"`  // search results
	AddressID   int64  `json:"address_id,omitempty"` // household members
}

func mePerson(d *access.Directory, pv access.PersonView, now time.Time) mePersonJSON {
	j := mePersonJSON{ID: pv.ID, Name: pv.Name, Role: pv.Role, Email: pv.Email, Phone: pv.Phone,
		Birthday: pv.Birthday.Text, Anniversary: pv.Anniversary.Text}
	if a, ok := d.Person(pv.ID); ok {
		if age, ok := access.VisibleAge(d.Viewer, a, now); ok {
			j.Age = age
		}
	}
	return j
}

// meSelf: GET /api/v1/me, the caller's own entry: their profile's name and
// id, their role here, and the households their page lists. A login with
// no profile (or one they can't see yet) has no households.
func (s *Server) meSelf(w http.ResponseWriter, r *http.Request) {
	d, err := s.meDirectory(r)
	if err != nil {
		s.meServerError(w, err)
		return
	}
	u := meUser(r)
	out := struct {
		ID         int64            `json:"id,omitempty"`
		Name       string           `json:"name"`
		AppRole    string           `json:"app_role"`
		Households []meHouseholdRef `json:"households"`
	}{Name: u.Name(), AppRole: u.Role, Households: []meHouseholdRef{}}
	if pid := d.Viewer.PersonID; pid != 0 {
		if page, ok := d.Page(pid); ok {
			out.ID, out.Name = page.Person.ID, page.Person.Name
			for _, h := range page.Households {
				ha, _ := d.Household(h.ID)
				can := access.CanHeadEditHousehold(d.Viewer, ha)
				out.Households = append(out.Households, meHouseholdRef{ID: h.ID, Name: h.Name, Role: h.Role, CanEdit: &can})
			}
		}
	}
	meJSON(w, http.StatusOK, out)
}

// mePeople: GET /api/v1/me/people?q=&limit=. The people the directory page
// lists for this viewer (d.Listing: members only, no contacts or archived
// people, whoever is looking), narrowed to those whose shown name has every
// word of q (ignoring case and accents), by last name.
func (s *Server) mePeople(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	words := strings.Fields(store.Fold(q))
	if len(words) == 0 {
		meFail(w, http.StatusBadRequest, "missing_q", "q is needed: part of a name.")
		return
	}
	d, err := s.meDirectory(r)
	if err != nil {
		s.meServerError(w, err)
		return
	}
	type found struct {
		pv    access.PersonView
		homes []meRef
		roles map[int64]string
		key   string
	}
	byID := map[int64]*found{}
	var order []int64
	add := func(pv access.PersonView, home *meRef) {
		f := byID[pv.ID]
		if f == nil {
			a, ok := d.Person(pv.ID)
			if !ok {
				return
			}
			// Only the name as shown (goes-by or first, last, suffix), as
			// the directory page searches: someone who goes by another
			// name isn't found by a first name the web never shows.
			if !hasAllWords(store.Fold(pv.Name), words) {
				return
			}
			f = &found{pv: pv, roles: map[int64]string{}, key: store.Fold(a.Person.LastName + " " + pv.Name)}
			byID[pv.ID] = f
			order = append(order, pv.ID)
		}
		if home != nil {
			f.homes = append(f.homes, *home)
			f.roles[home.ID] = pv.Role
		}
	}
	for _, e := range d.Listing("").Entries {
		if e.Person != nil {
			add(*e.Person, nil)
			continue
		}
		home := &meRef{ID: e.Family.ID, Name: e.Family.Name}
		for _, g := range e.Family.Groups {
			for _, pv := range g.People {
				add(pv, home)
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byID[order[i]], byID[order[j]]
		if a.key != b.key {
			return a.key < b.key
		}
		return order[i] < order[j]
	})
	now := time.Now()
	people := []mePersonJSON{}
	for _, id := range order {
		if len(people) == meLimit(r) {
			break
		}
		f := byID[id]
		pv := f.pv
		pv.Role = ""
		j := mePerson(d, pv, now)
		if len(f.homes) > 0 {
			home := f.homes[0]
			if a, ok := d.Person(id); ok {
				for _, m := range a.Memberships {
					for _, h := range f.homes {
						if m.IsPrimary && m.HouseholdID == h.ID {
							home = h
						}
					}
				}
			}
			j.Household, j.Role = &home, f.roles[home.ID]
		}
		people = append(people, j)
	}
	meJSON(w, http.StatusOK, map[string]any{"people": people})
}

func hasAllWords(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// mePersonDetail is GET /api/v1/me/people/{id}: the person page.
type mePersonDetail struct {
	mePersonJSON
	Households []meHouseholdRef `json:"households"`
	Family     []meFamilyJSON   `json:"family"`
	Groups     []meGroupJSON    `json:"groups"`
}

type meFamilyJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Relation string `json:"relation"`
}

type meGroupJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"` // the person's label in it
}

// meRelation turns the person page's Family headings into one word each.
var meRelation = map[string]string{
	"Parents": "parent", "Spouse": "spouse", "Siblings": "sibling", "Children": "child",
	"Grandparents": "grandparent", "Grandchildren": "grandchild",
}

// mePerson: GET /api/v1/me/people/{id}, built from d.Page as /people/{id}
// is (no notes).
func (s *Server) mePerson(w http.ResponseWriter, r *http.Request) {
	id, ok := mePathID(w, r, "person")
	if !ok {
		return
	}
	d, err := s.meDirectory(r)
	if err != nil {
		s.meServerError(w, err)
		return
	}
	page, ok := d.Page(id)
	if !ok {
		meFail(w, http.StatusNotFound, "not_found", "No such person.")
		return
	}
	out := mePersonDetail{mePersonJSON: mePerson(d, page.Person, time.Now()),
		Households: []meHouseholdRef{}, Family: []meFamilyJSON{}, Groups: []meGroupJSON{}}
	for _, h := range page.Households {
		out.Households = append(out.Households, meHouseholdRef{ID: h.ID, Name: h.Name, Role: h.Role})
	}
	// role: in their primary household when the viewer may see it, else
	// the first one listed.
	if len(page.Households) > 0 {
		out.Role = page.Households[0].Role
		if a, ok := d.Person(id); ok {
			for _, m := range a.Memberships {
				for _, h := range page.Households {
					if m.IsPrimary && m.HouseholdID == h.ID {
						out.Role = h.Role
					}
				}
			}
		}
	}
	for _, g := range page.Family.Groups {
		rel := meRelation[g.Label]
		if rel == "" {
			rel = strings.ToLower(g.Label)
		}
		for _, p := range g.People {
			out.Family = append(out.Family, meFamilyJSON{ID: p.ID, Name: p.Name, Relation: rel})
		}
	}
	for _, g := range page.Groups {
		out.Groups = append(out.Groups, meGroupJSON{ID: g.ID, Name: g.Name, Label: g.Label})
	}
	meJSON(w, http.StatusOK, out)
}

type meAddressJSON struct {
	ID    int64    `json:"id"`
	Label string   `json:"label,omitempty"`
	Lines []string `json:"lines"`
}

func meAddressLines(a store.Address) []string {
	var lines []string
	for _, l := range []string{a.Address1, a.Address2} {
		if l != "" {
			lines = append(lines, l)
		}
	}
	last := a.City
	if sp := strings.TrimSpace(a.State + " " + a.Postal); sp != "" {
		if last != "" {
			last += ", "
		}
		last += sp
	}
	if last != "" {
		lines = append(lines, last)
	}
	return lines
}

// meHousehold: GET /api/v1/me/households/{id}, the household card
// /households/{id} shows (d.Family): its people the viewer may see, heads
// first, and only the addresses the card shows (VisibleAddress, and with
// someone shown living there).
func (s *Server) meHousehold(w http.ResponseWriter, r *http.Request) {
	id, ok := mePathID(w, r, "household")
	if !ok {
		return
	}
	d, err := s.meDirectory(r)
	if err != nil {
		s.meServerError(w, err)
		return
	}
	fv, ok := d.Family(id, false)
	h, hok := d.Household(id)
	if !ok || !hok {
		meFail(w, http.StatusNotFound, "not_found", "No such household.")
		return
	}
	shown := map[int64]access.PersonView{}
	for _, g := range fv.Groups {
		for _, pv := range g.People {
			shown[pv.ID] = pv
		}
	}
	visibleAddr := map[int64]store.Address{}
	for _, a := range h.Addresses {
		if access.VisibleAddress(d.Viewer, h, a) {
			visibleAddr[a.ID] = a
		}
	}
	now := time.Now()
	members := []mePersonJSON{}
	used := map[int64]bool{}
	for _, m := range h.Members { // heads first
		pv, ok := shown[m.PersonID]
		if !ok {
			continue
		}
		pv.Role = m.Role
		j := mePerson(d, pv, now)
		if _, ok := visibleAddr[m.AddressID]; ok {
			j.AddressID = m.AddressID
			used[m.AddressID] = true
		}
		members = append(members, j)
	}
	addrs := []meAddressJSON{}
	for _, a := range h.Addresses {
		if used[a.ID] {
			addrs = append(addrs, meAddressJSON{ID: a.ID, Label: a.Label, Lines: meAddressLines(a)})
		}
	}
	out := map[string]any{"id": fv.ID, "name": fv.Name, "addresses": addrs, "members": members}
	if fv.Anniversary != "" { // the heads' shared anniversary, shown once on the card
		out["anniversary"] = fv.Anniversary
	}
	meJSON(w, http.StatusOK, out)
}
