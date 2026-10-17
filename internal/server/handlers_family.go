package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tylergoza/church-directory/internal/store"
)

// Family links and "start their own household" (staff pages). The logic
// that decides what may happen is in functions that take a canEdit check, so
// heads of household (step 5) can call the same ones with their own rule:
// linkPeople, applyFamilyLinks and startOwnHousehold. Staff pass
// anyoneEditable.

func anyoneEditable(int64) bool { return true }

// linkError says why a link was refused, for the flash.
func linkError(err error) string {
	switch {
	case errors.Is(err, store.ErrSelfLink):
		return "A person can't be linked to themselves."
	case errors.Is(err, store.ErrCycle):
		return "That would make someone their own ancestor."
	case errors.Is(err, store.ErrNotFound):
		return "Couldn't find that person."
	}
	return ""
}

// linkPeople adds or removes (remove true) a link between thisID and
// otherID. rel says what other is to this person: "parent" (other is their
// parent), "child" (other is their child) or "spouse". Both must pass
// canEdit. It returns a message for the flash and whether it worked.
func (s *Server) linkPeople(rel string, thisID, otherID int64, remove bool, by int64, canEdit func(int64) bool) (string, bool) {
	if !canEdit(thisID) || !canEdit(otherID) {
		return "You can't change that person's family links.", false
	}
	kind, a, b := store.RelParent, otherID, thisID
	switch rel {
	case "parent":
	case "child":
		a, b = thisID, otherID
	case "spouse":
		kind = store.RelSpouse
	default:
		return "Pick parent, child or spouse.", false
	}
	var err error
	if remove {
		err = s.store.RemoveRelationship(kind, a, b, by)
	} else {
		err = s.store.AddRelationship(kind, a, b, by)
	}
	if err != nil {
		if msg := linkError(err); msg != "" {
			return msg, false
		}
		return "Couldn't save that link.", false
	}
	if remove {
		return "Link removed.", true
	}
	return "Link added.", true
}

// parseLinkChoice reads "parent:A:B" or "spouse:A:B" (the suggestion
// checkboxes): A is a parent of B, or A and B are spouses.
func parseLinkChoice(v string) (kind string, a, b int64, ok bool) {
	parts := strings.Split(v, ":")
	if len(parts) != 3 || (parts[0] != store.RelParent && parts[0] != store.RelSpouse) {
		return "", 0, 0, false
	}
	var err1, err2 error
	a, err1 = strconv.ParseInt(parts[1], 10, 64)
	b, err2 = strconv.ParseInt(parts[2], 10, 64)
	return parts[0], a, b, err1 == nil && err2 == nil
}

// applyFamilyLinks adds the ticked suggestions (form values "link") for a
// household. Both people of each link must be in the household and pass
// canEdit. It returns what happened, for the flash.
func (s *Server) applyFamilyLinks(r *http.Request, householdID, by int64, canEdit func(int64) bool) (msg string, ok bool) {
	r.ParseForm()
	members, err := s.store.HouseholdMembers(householdID)
	if err != nil {
		return "Couldn't load the household.", false
	}
	in := map[int64]bool{}
	for _, m := range members {
		in[m.ID] = true
	}
	added := 0
	for _, v := range r.PostForm["link"] {
		kind, a, b, valid := parseLinkChoice(v)
		if !valid || !in[a] || !in[b] || !canEdit(a) || !canEdit(b) {
			continue
		}
		if err := s.store.AddRelationship(kind, a, b, by); err != nil {
			if m := linkError(err); m != "" {
				return m, false
			}
			return "Couldn't save the links.", false
		}
		added++
	}
	if added == 0 {
		return "No links added.", true
	}
	return "Family " + pluralize(added, "link") + " added.", true
}

func (s *Server) handleFamilyLinksApply(w http.ResponseWriter, r *http.Request) {
	hid := pathID(r)
	msg, ok := s.applyFamilyLinks(r, hid, currentUser(r).ID, anyoneEditable)
	back := "/admin/households/" + itoa(hid)
	if !ok {
		s.redirectError(w, r, back, msg)
		return
	}
	s.redirect(w, r, back, msg)
}

// familySuggestion is the follow-up form after adding someone to a
// household: tick (default) the links to make.
type familySuggestion struct {
	Person  store.Person
	Parents []store.Person // heads who could be this child's parents
	Spouse  *store.Person  // the other head, when there are exactly two
}

func (f *familySuggestion) empty() bool { return f == nil || (len(f.Parents) == 0 && f.Spouse == nil) }

// familySuggestions works out what to suggest after personID was added to
// the household: a child gets the household's heads as parents; a second
// head gets a spouse link with the first. Nil when there's nothing to
// suggest.
func (s *Server) familySuggestions(householdID, personID int64) (*familySuggestion, error) {
	if personID == 0 {
		return nil, nil
	}
	members, err := s.store.HouseholdMembers(householdID)
	if err != nil {
		return nil, err
	}
	var me *store.Member
	var heads []store.Member
	for i, m := range members {
		if m.ID == personID {
			me = &members[i]
		}
		if m.Role == store.RoleHead {
			heads = append(heads, m)
		}
	}
	if me == nil {
		return nil, nil
	}
	links, err := s.store.ListPersonLinks(personID)
	if err != nil {
		return nil, err
	}
	linked := func(list []store.Person, id int64) bool {
		for _, p := range list {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	sg := &familySuggestion{Person: me.Person}
	switch me.Role {
	case store.RoleChild:
		for _, h := range heads {
			if !linked(links.Parents, h.ID) {
				sg.Parents = append(sg.Parents, h.Person)
			}
		}
	case store.RoleHead:
		if len(heads) == 2 {
			for _, h := range heads {
				if h.ID != personID && !linked(links.Spouses, h.ID) {
					p := h.Person
					sg.Spouse = &p
				}
			}
		}
	}
	if sg.empty() {
		return nil, nil
	}
	return sg, nil
}

// suggestQuery is what to add to the household URL after adding someone,
// so the page offers family links.
func suggestQuery(personID int64, role string) string {
	if role == store.RoleChild || role == store.RoleHead {
		return "?suggest=" + itoa(personID)
	}
	return ""
}

// The person's family page (staff) ---------------------------------------

type familyPageMatch struct {
	Person     store.Person
	Households []string
}

func (s *Server) handlePersonFamily(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPerson(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	links, err := s.store.ListPersonLinks(p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hh, err := s.store.PersonHouseholds(p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Who shares a household with them: ticked by default when starting
	// their own household.
	together := map[int64]bool{}
	for _, h := range hh {
		ms, err := s.store.HouseholdMembers(h.HouseholdID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		for _, m := range ms {
			together[m.ID] = true
		}
	}
	data := map[string]any{
		"Title": "Family of " + p.Name(), "Person": p, "Links": links, "PersonHouseholds": hh,
		"Together": together, "OwnName": store.OwnHouseholdName(p), "From": queryInt(r, "from"),
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		found, err := s.store.ListPeople(store.PeopleAll, q)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		var matches []familyPageMatch
		for _, f := range found {
			if f.ID != p.ID && len(matches) < 15 {
				matches = append(matches, familyPageMatch{Person: f.Person, Households: f.Households})
			}
		}
		data["Q"], data["Found"] = q, matches
	}
	s.render(w, r, http.StatusOK, "people/family", data)
}

func (s *Server) handlePersonLinkAdd(w http.ResponseWriter, r *http.Request) {
	s.personLinkChange(w, r, false)
}

func (s *Server) handlePersonLinkRemove(w http.ResponseWriter, r *http.Request) {
	s.personLinkChange(w, r, true)
}

func (s *Server) personLinkChange(w http.ResponseWriter, r *http.Request, remove bool) {
	id := pathID(r)
	back := "/admin/people/" + itoa(id) + "/family"
	msg, ok := s.linkPeople(formStr(r, "rel"), id, formInt(r, "person_id"), remove, currentUser(r).ID, anyoneEditable)
	if !ok {
		s.redirectError(w, r, back, msg)
		return
	}
	s.redirect(w, r, back, msg)
}

// Start their own household ---------------------------------------------

// startOwnHousehold is the shared rule for "start their own household":
// the spouse must be one of the person's spouse links and the children
// among their child links, so nobody unrelated is moved. canEdit is asked
// about everyone moving; managedBy is the starter's profile when they are a
// head (0 for staff). Returns the new household's ID, or a message.
func (s *Server) startOwnHousehold(personID int64, name string, spouseID int64, childIDs []int64, by, managedBy int64, canEdit func(int64) bool) (int64, string) {
	links, err := s.store.ListPersonLinks(personID)
	if err != nil {
		return 0, "Couldn't load their family."
	}
	isSpouse, isChild := map[int64]bool{}, map[int64]bool{}
	for _, p := range links.Spouses {
		isSpouse[p.ID] = true
	}
	for _, p := range links.Children {
		isChild[p.ID] = true
	}
	if !canEdit(personID) || (spouseID != 0 && (!isSpouse[spouseID] || !canEdit(spouseID))) {
		return 0, "You can't move that person."
	}
	for _, c := range childIDs {
		if !isChild[c] || !canEdit(c) {
			return 0, "You can only move their own children."
		}
	}
	hid, err := s.store.StartOwnHousehold(personID, name, spouseID, childIDs, by, managedBy)
	if err != nil {
		if errors.Is(err, store.ErrOwnHousehold) || strings.Contains(err.Error(), "name") {
			return 0, err.Error()
		}
		return 0, "Couldn't start the household."
	}
	return hid, ""
}

func (s *Server) handlePersonOwnHousehold(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := "/admin/people/" + itoa(id) + "/family"
	r.ParseForm()
	var kids []int64
	for _, v := range r.PostForm["child_id"] {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			kids = append(kids, n)
		}
	}
	hid, msg := s.startOwnHousehold(id, formStr(r, "name"), formInt(r, "spouse_id"), kids, currentUser(r).ID, 0, anyoneEditable)
	if msg != "" {
		s.redirectError(w, r, back, msg)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(hid), "New household started. Add its address below.")
}
