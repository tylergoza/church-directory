package server

import (
	"net/http"

	"github.com/tylergoza/church-directory/internal/store"
)

// Visibility settings (staff and admins). Everything saved here goes
// through the change log in the store.

// parseShown reads a Default / Shown / Hidden select: "" is nil (use the
// default).
func parseShown(v string) *bool {
	switch v {
	case "1":
		t := true
		return &t
	case "0":
		f := false
		return &f
	}
	return nil
}

// visState is the select value for a setting, the other way round.
func visState(b *bool) string {
	switch {
	case b == nil:
		return ""
	case *b:
		return "1"
	}
	return "0"
}

// Defaults (admin): /admin/defaults.

type defaultRow struct {
	Field        store.Field
	Label        string
	Adult, Child bool
	ChildApplies bool
}

func (s *Server) defaultRows() ([]defaultRow, error) {
	d, err := s.store.LoadDefaults()
	if err != nil {
		return nil, err
	}
	var rows []defaultRow
	for _, f := range store.DefaultFields {
		row := defaultRow{Field: f, Label: f.Label(), Adult: d.Shown(store.AudienceAdult, f)}
		for _, c := range store.ChildFields {
			if c == f {
				row.ChildApplies, row.Child = true, d.Shown(store.AudienceChild, f)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Server) handleDefaults(w http.ResponseWriter, r *http.Request) {
	rows, err := s.defaultRows()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "defaults", map[string]any{"Title": "Default visibility", "Rows": rows})
}

func (s *Server) handleDefaultsUpdate(w http.ResponseWriter, r *http.Request) {
	var changes []store.DefaultChange
	for _, f := range store.DefaultFields {
		if v := r.PostFormValue("adult:" + string(f)); v == "0" || v == "1" {
			changes = append(changes, store.DefaultChange{Audience: store.AudienceAdult, Field: f, Shown: v == "1"})
		}
	}
	for _, f := range store.ChildFields {
		if v := r.PostFormValue("child:" + string(f)); v == "0" || v == "1" {
			changes = append(changes, store.DefaultChange{Audience: store.AudienceChild, Field: f, Shown: v == "1"})
		}
	}
	if err := s.store.SetDefaults(changes, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/defaults", "Defaults saved.")
}

// Per-profile settings (staff): on the person's page.

type visRow struct {
	Field   store.Field
	Label   string
	State   string // "", "1" or "0"
	Force   bool
	Default bool // what the default would do
}

// visRows lists a person's fields with their own settings and what the
// default would do for them.
func (s *Server) visRows(personID int64, households []store.PersonHousehold) ([]visRow, error) {
	d, err := s.store.LoadDefaults()
	if err != nil {
		return nil, err
	}
	set, err := s.store.PersonFieldSettings(personID)
	if err != nil {
		return nil, err
	}
	audience := store.AudienceAdult
	for _, h := range households {
		if h.Role == store.RoleChild {
			audience = store.AudienceChild
		}
	}
	rows := make([]visRow, 0, len(store.PersonFields))
	for _, f := range store.PersonFields {
		rows = append(rows, visRow{Field: f, Label: f.Label(), State: visState(set[f].Shown),
			Force: set[f].ForceHidden, Default: d.Shown(audience, f)})
	}
	return rows, nil
}

func (s *Server) handlePersonVisibility(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, err := s.store.GetPerson(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	settings := map[store.Field]store.FieldSetting{}
	for _, f := range store.PersonFields {
		settings[f] = store.FieldSetting{
			Shown:       parseShown(r.PostFormValue("shown:" + string(f))),
			ForceHidden: r.PostFormValue("force:"+string(f)) == "1",
		}
	}
	if err := s.store.SetPersonFieldSettings(id, settings, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/people/"+itoa(id), "Visibility saved.")
}

// Per-address and household photo settings (staff): on the household page.

func (s *Server) handleAddressVisibility(w http.ResponseWriter, r *http.Request) {
	a, err := s.addressOf(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.SetAddressVisibility(a.ID, parseShown(r.PostFormValue("shown")), r.PostFormValue("force") == "1", currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(a.HouseholdID), "Address visibility saved.")
}

func (s *Server) handleHouseholdPhotoVisibility(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	set := store.FieldSetting{Shown: parseShown(r.PostFormValue("shown")), ForceHidden: r.PostFormValue("force") == "1"}
	if err := s.store.SetHouseholdPhotoSetting(id, set, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(id), "Photo visibility saved.")
}
