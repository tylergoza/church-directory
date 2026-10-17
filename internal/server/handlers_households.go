package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tylergoza/church-directory/internal/store"
)

// Households (staff): /admin/households.

func (s *Server) handleHouseholds(w http.ResponseWriter, r *http.Request) {
	filter, q := r.URL.Query().Get("show"), r.URL.Query().Get("q")
	switch filter {
	case store.HouseholdsContacts, store.HouseholdsArchived:
	default:
		filter = store.HouseholdsAll
	}
	rows, err := s.store.ListHouseholds(filter, q)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "households/index", map[string]any{
		"Title": "Households", "Households": rows, "Show": filter, "Q": q,
		"Filters": []struct{ Value, Label string }{
			{store.HouseholdsAll, "All"}, {store.HouseholdsContacts, "Contact households"}, {store.HouseholdsArchived, "Archived"},
		},
	})
}

func (s *Server) handleHouseholdNew(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "households/new", map[string]any{"Title": "Add household", "Name": "", "Kind": store.KindMember})
}

func (s *Server) handleHouseholdCreate(w http.ResponseWriter, r *http.Request) {
	name, kind, errs := store.ValidateHousehold(formStr(r, "name"), formStr(r, "kind"))
	if len(errs) > 0 {
		s.render(w, r, http.StatusUnprocessableEntity, "households/new", map[string]any{"Title": "Add household", "Name": name, "Kind": kind, "Errors": errs})
		return
	}
	id, err := s.store.CreateHousehold(name, kind, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(id), "Household added. Add its address and people below.")
}

// addMatch is a person who could be added to the household, from the search.
type addMatch struct {
	Person     store.Person
	Households []string
}

func (s *Server) renderHousehold(w http.ResponseWriter, r *http.Request, status int, h store.Household, errs []string) {
	addrs, err := s.store.ListAddresses(h.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	members, err := s.store.HouseholdMembers(h.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	changes, err := s.store.RecentChanges(0, h.ID, 15)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defaults, err := s.store.LoadDefaults()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hset, err := s.store.AllHouseholdSettings()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"PhotoVis": visRow{Field: store.FieldHouseholdPhoto, Label: "Household photo", State: visState(hset[h.ID][store.FieldHouseholdPhoto].Shown),
			Force: hset[h.ID][store.FieldHouseholdPhoto].ForceHidden, Default: defaults.Shown(store.AudienceAdult, store.FieldHouseholdPhoto)},
		"AddressDefault": defaults.Shown(store.AudienceAdult, store.FieldAddress),
		"Title":          h.Name, "Household": h, "Addresses": addrs, "BlankAddress": store.Address{}, "Members": members, "Changes": changes, "Errors": errs,
	}
	// Adding someone who is already in the directory: search by name.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		found, err := s.store.ListPeople(store.PeopleAll, q)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		in := map[int64]bool{}
		for _, m := range members {
			in[m.ID] = true
		}
		var matches []addMatch
		for _, p := range found {
			if !in[p.ID] && len(matches) < 15 {
				matches = append(matches, addMatch{Person: p.Person, Households: p.Households})
			}
		}
		data["Q"], data["Found"] = q, matches
	}
	sg, err := s.familySuggestions(h.ID, queryInt(r, "suggest"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Suggest"] = sg
	s.render(w, r, status, "households/show", data)
}

func (s *Server) handleHouseholdShow(w http.ResponseWriter, r *http.Request) {
	h, err := s.store.GetHousehold(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderHousehold(w, r, http.StatusOK, h, nil)
}

func (s *Server) handleHouseholdUpdate(w http.ResponseWriter, r *http.Request) {
	h, err := s.store.GetHousehold(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	name, kind, errs := store.ValidateHousehold(formStr(r, "name"), formStr(r, "kind"))
	if len(errs) > 0 {
		h.Name, h.Kind = name, kind
		s.renderHousehold(w, r, http.StatusUnprocessableEntity, h, errs)
		return
	}
	if err := s.store.UpdateHousehold(h.ID, name, kind, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(h.ID), "Saved.")
}

func (s *Server) handleHouseholdArchive(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	archive := r.PostFormValue("archive") == "1"
	if err := s.store.ArchiveHousehold(id, archive, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := "Moved back out of the archive."
	if archive {
		msg = "Archived."
	}
	s.redirect(w, r, "/admin/households/"+itoa(id), msg)
}

func (s *Server) handleHouseholdDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	h, err := s.store.GetHousehold(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	members, err := s.store.HouseholdMembers(h.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "households/delete", map[string]any{"Title": "Delete " + h.Name, "Household": h, "Members": members})
}

func (s *Server) handleHouseholdDelete(w http.ResponseWriter, r *http.Request) {
	photo, err := s.store.DeleteHousehold(pathID(r), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.removePhoto(photo)
	s.redirect(w, r, "/admin/households", "Household deleted. The people in it are still in the directory.")
}

// Addresses

func addressInputFrom(r *http.Request) store.AddressInput {
	return store.AddressInput{
		Label: formStr(r, "label"), Address1: formStr(r, "address1"), Address2: formStr(r, "address2"),
		City: formStr(r, "city"), State: formStr(r, "state"), Postal: formStr(r, "postal"),
	}
}

func (s *Server) handleAddressAdd(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	in := addressInputFrom(r)
	if errs := in.Validate(); len(errs) > 0 {
		s.redirectError(w, r, "/admin/households/"+itoa(id), strings.Join(errs, " "))
		return
	}
	if _, err := s.store.AddAddress(id, in, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(id), "Address added.")
}

// addressOf loads an address that must belong to the household in the path.
func (s *Server) addressOf(r *http.Request) (store.Address, error) {
	a, err := s.store.GetAddress(pathValueID(r, "aid"))
	if err == nil && a.HouseholdID != pathID(r) {
		return a, store.ErrNotFound
	}
	return a, err
}

func (s *Server) handleAddressUpdate(w http.ResponseWriter, r *http.Request) {
	a, err := s.addressOf(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	in := addressInputFrom(r)
	if errs := in.Validate(); len(errs) > 0 {
		s.redirectError(w, r, "/admin/households/"+itoa(a.HouseholdID), strings.Join(errs, " "))
		return
	}
	if err := s.store.UpdateAddress(a.ID, in, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(a.HouseholdID), "Address saved.")
}

func (s *Server) handleAddressDelete(w http.ResponseWriter, r *http.Request) {
	a, err := s.addressOf(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	back := "/admin/households/" + itoa(a.HouseholdID)
	reassign := formInt(r, "reassign")
	if r.PostFormValue("reassign") == "none" {
		reassign = store.NoAddress
	}
	switch err := s.store.DeleteAddress(a.ID, reassign, currentUser(r).ID); {
	case errors.Is(err, store.ErrNeedsReassign):
		s.redirectError(w, r, back, "Choose where the people at that address go before removing it.")
	case errors.Is(err, store.ErrWrongHousehold):
		s.redirectError(w, r, back, "Choose one of this household's other addresses.")
	case err != nil:
		s.serverError(w, r, err)
	default:
		s.redirect(w, r, back, "Address removed.")
	}
}

// Members

// memberError says why a member change was refused, for the flash.
func memberError(err error) (string, bool) {
	switch {
	case errors.Is(err, store.ErrWrongHousehold):
		return "That address belongs to another household.", true
	case errors.Is(err, store.ErrBadRole):
		return "Pick head, adult or child.", true
	}
	return "", false
}

// handleMemberPlace adds an existing person to the household, or changes
// the role and address of someone already in it ("Use that one" on the add
// form comes here too). An adult already in another household is moved.
func (s *Server) handleMemberPlace(w http.ResponseWriter, r *http.Request) {
	hid := pathID(r)
	pid := formInt(r, "person_id")
	if v := r.PathValue("pid"); v != "" {
		pid = pathValueID(r, "pid")
	}
	back := "/admin/households/" + itoa(hid)
	p, err := s.store.GetPerson(pid)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	err = s.store.PlaceMember(hid, pid, formStr(r, "role"), formInt(r, "address_id"), currentUser(r).ID)
	if msg, ok := memberError(err); ok {
		s.redirectError(w, r, back, msg)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, back+suggestQuery(pid, formStr(r, "role")), p.Name()+" is in the household.")
}

func (s *Server) handleMemberRemove(w http.ResponseWriter, r *http.Request) {
	hid := pathID(r)
	if err := s.store.RemoveMember(hid, pathValueID(r, "pid"), currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(hid), "Removed from the household. They're still in the directory.")
}
