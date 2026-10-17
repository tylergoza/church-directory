package server

import (
	"net/http"
	"strconv"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

// People (staff): /admin/people.

// personForm is what was typed into the person form, kept as text so a
// form that comes back with errors shows exactly what was typed.
type personForm struct {
	First, Last, GoesBy, Suffix string
	Email, Phone, Kind          string
	BirthMonth, BirthDay        string
	BirthYear                   string
	AnnivMonth, AnnivDay        string
	AnnivYear                   string
}

func personFormFrom(r *http.Request) personForm {
	return personForm{
		First: formStr(r, "first_name"), Last: formStr(r, "last_name"),
		GoesBy: formStr(r, "goes_by"), Suffix: formStr(r, "suffix"),
		Email: formStr(r, "email"), Phone: formStr(r, "phone"), Kind: formStr(r, "kind"),
		BirthMonth: formStr(r, "birth_month"), BirthDay: formStr(r, "birth_day"), BirthYear: formStr(r, "birth_year"),
		AnnivMonth: formStr(r, "anniv_month"), AnnivDay: formStr(r, "anniv_day"), AnnivYear: formStr(r, "anniv_year"),
	}
}

func formFromPerson(p store.Person) personForm {
	n := func(v int) string {
		if v == 0 {
			return ""
		}
		return strconv.Itoa(v)
	}
	return personForm{
		First: p.FirstName, Last: p.LastName, GoesBy: p.GoesBy, Suffix: p.Suffix,
		Email: p.Email, Phone: p.Phone, Kind: p.Kind,
		BirthMonth: n(p.Birth.Month), BirthDay: n(p.Birth.Day), BirthYear: n(p.Birth.Year),
		AnnivMonth: n(p.Anniversary.Month), AnnivDay: n(p.Anniversary.Day), AnnivYear: n(p.Anniversary.Year),
	}
}

// input turns the form into a validated PersonInput, with the problems in
// words.
func (f personForm) input() (store.PersonInput, []string) {
	in := store.PersonInput{
		FirstName: f.First, LastName: f.Last, GoesBy: f.GoesBy, Suffix: f.Suffix,
		Email: f.Email, Phone: f.Phone, Kind: f.Kind,
	}
	var errs []string
	var err error
	if in.Birth, err = dates.Parse(f.BirthMonth, f.BirthDay, f.BirthYear); err != nil {
		errs = append(errs, "Birthday: "+err.Error()+".")
	}
	if in.Anniversary, err = dates.Parse(f.AnnivMonth, f.AnnivDay, f.AnnivYear); err != nil {
		errs = append(errs, "Anniversary: "+err.Error()+".")
	}
	// Validate checks dates too, but not the ones that didn't parse.
	if len(errs) > 0 {
		in.Birth, in.Anniversary = dates.Partial{}, dates.Partial{}
	}
	return in, append(errs, in.Validate()...)
}

type monthOption struct{ Value, Name string }

// monthOptions is the month select: a blank, then January to December.
func monthOptions() []monthOption {
	out := []monthOption{{"", ""}}
	for i, name := range dates.MonthNames() {
		out = append(out, monthOption{strconv.Itoa(i + 1), name})
	}
	return out
}

// nameMatch is an existing person a new entry's name matches.
type nameMatch struct {
	Person     store.Person
	Households []store.PersonHousehold
}

func (s *Server) nameMatches(in store.PersonInput, excludeID int64) ([]nameMatch, error) {
	people, err := s.store.NameMatches(in.FirstName, in.LastName, in.GoesBy, in.Suffix, excludeID)
	if err != nil {
		return nil, err
	}
	out := make([]nameMatch, len(people))
	for i, p := range people {
		hh, err := s.store.PersonHouseholds(p.ID)
		if err != nil {
			return nil, err
		}
		out[i] = nameMatch{Person: p, Households: hh}
	}
	return out, nil
}

func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	filter, q := r.URL.Query().Get("show"), r.URL.Query().Get("q")
	switch filter {
	case store.PeopleUnlinked, store.PeopleContacts, store.PeoplePending, store.PeopleArchived:
	default:
		filter = store.PeopleAll
	}
	people, err := s.store.ListPeople(filter, q)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "people/index", map[string]any{
		"Title": "People", "People": people, "Show": filter, "Q": q,
		"Filters": []struct{ Value, Label string }{
			{store.PeopleAll, "Everyone"}, {store.PeopleUnlinked, "No login"}, {store.PeopleContacts, "Contacts"},
			{store.PeoplePending, "Pending"}, {store.PeopleArchived, "Archived"},
		},
	})
}

// personNewData is what the add form needs. householdID (from ?household=
// or the form) adds the new person to that household.
func (s *Server) personNewData(householdID int64, form personForm, role string, addressID int64) (map[string]any, error) {
	data := map[string]any{"Title": "Add person", "Form": form, "Role": role, "AddressID": addressID}
	if form.Kind == "" {
		form.Kind = store.KindMember
		data["Form"] = form
	}
	if householdID != 0 {
		h, err := s.store.GetHousehold(householdID)
		if err != nil {
			return nil, err
		}
		addrs, err := s.store.ListAddresses(householdID)
		if err != nil {
			return nil, err
		}
		data["Household"], data["Addresses"] = h, addrs
	}
	return data, nil
}

func (s *Server) handlePersonNew(w http.ResponseWriter, r *http.Request) {
	data, err := s.personNewData(queryInt(r, "household"), personForm{}, store.RoleAdult, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "people/form", data)
}

// handlePersonCreate adds a person. Staff aren't held for a name match, but
// they're shown the matches first: the form comes back with them and an
// "Add anyway" button, which resubmits with add_anyway set.
func (s *Server) handlePersonCreate(w http.ResponseWriter, r *http.Request) {
	form := personFormFrom(r)
	householdID, role, addressID := formInt(r, "household_id"), formStr(r, "role"), formInt(r, "address_id")
	if householdID != 0 && !store.ValidHouseholdRole(role) {
		role = store.RoleAdult
	}
	in, errs := form.input()
	data, err := s.personNewData(householdID, form, role, addressID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(errs) > 0 {
		data["Errors"] = errs
		s.render(w, r, http.StatusUnprocessableEntity, "people/form", data)
		return
	}
	if r.PostFormValue("add_anyway") == "" {
		matches, err := s.nameMatches(in, 0)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(matches) > 0 {
			data["Matches"] = matches
			s.render(w, r, http.StatusOK, "people/form", data)
			return
		}
	}
	by := currentUser(r).ID
	id, err := s.store.CreatePerson(in, by)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if householdID != 0 {
		if err := s.store.PlaceMember(householdID, id, role, addressID, by); err != nil {
			s.redirectError(w, r, "/admin/people/"+itoa(id), "Added "+in.FirstName+", but couldn't add them to the household: "+err.Error())
			return
		}
		s.redirect(w, r, "/admin/households/"+itoa(householdID)+suggestQuery(id, role), "Added "+store.DisplayName(in.FirstName, in.LastName, in.GoesBy, in.Suffix)+" to the household.")
		return
	}
	s.redirect(w, r, "/admin/people/"+itoa(id), "Added "+store.DisplayName(in.FirstName, in.LastName, in.GoesBy, in.Suffix)+".")
}

func (s *Server) renderPerson(w http.ResponseWriter, r *http.Request, status int, p store.Person, form personForm, errs []string) {
	hh, err := s.store.PersonHouseholds(p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	changes, err := s.store.RecentChanges(p.ID, 0, 15)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vis, err := s.visRows(p.ID, hh)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	notes, err := s.personNotes(r, d, p.ID, "/admin/people/"+itoa(p.ID))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"Title": p.Name(), "Person": p, "Form": form, "Errors": errs,
		"PersonHouseholds": hh, "Changes": changes, "Vis": vis, "Notes": notes,
	}
	if err := s.addLinkData(data, p); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "people/form", data)
}

func (s *Server) handlePersonShow(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPerson(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPerson(w, r, http.StatusOK, p, formFromPerson(p), nil)
}

func (s *Server) handlePersonUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPerson(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	form := personFormFrom(r)
	in, errs := form.input()
	if len(errs) > 0 {
		s.renderPerson(w, r, http.StatusUnprocessableEntity, p, form, errs)
		return
	}
	if err := s.store.UpdatePerson(p.ID, in, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if after, err := s.store.GetPerson(p.ID); err == nil && p.UserID != 0 && after.UserID == 0 {
		to := "/admin/people/" + itoa(p.ID)
		if !s.SSO().Enabled() {
			s.redirectError(w, r, to, "Saved. They are under 13, so their login was unlinked and turned off.")
			return
		}
		// An SSO login can't be switched off here; staff's own grant
		// suspends it in User Management. A failure leaves the item on
		// Needs action with a Suspend in User Management button.
		done, err := s.suspendUnder13For(r, p.ID)
		switch {
		case err != nil:
			s.redirectError(w, r, to, "Saved. They are under 13, so their login was unlinked, but their access in User Management is still on: "+under13Failure(err)+" See Needs action.")
		case done > 0:
			s.redirect(w, r, to, "Saved. They are under 13, so their login was unlinked and their access was suspended in User Management.")
		default:
			s.redirectError(w, r, to, "Saved. They are under 13, so their login was unlinked and turned off. See Needs action if they used single sign-on.")
		}
		return
	}
	s.redirect(w, r, "/admin/people/"+itoa(p.ID), "Saved.")
}

func (s *Server) handlePersonArchive(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	archive := r.PostFormValue("archive") == "1"
	if err := s.store.ArchivePerson(id, archive, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := "Moved back out of the archive."
	if archive {
		msg = "Archived. They're hidden everywhere except the archive list here."
	}
	s.redirect(w, r, "/admin/people/"+itoa(id), msg)
}

// handlePersonDeleteConfirm is the confirmation step before a delete.
func (s *Server) handlePersonDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPerson(pathID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hh, err := s.store.PersonHouseholds(p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "people/delete", map[string]any{"Title": "Delete " + p.Name(), "Person": p, "PersonHouseholds": hh})
}

func (s *Server) handlePersonDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	photo, err := s.store.DeletePerson(id, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.removePhoto(photo)
	s.redirect(w, r, "/admin/people", "Deleted, with their notes and photo.")
}
