package store

import (
	"database/sql"
)

// Field is something about a person (or a household) that can be shown or
// hidden. The values are the ones the field_settings, household_settings
// and defaults tables allow.
type Field string

const (
	FieldEmail           Field = "email"
	FieldPhone           Field = "phone"
	FieldPhoto           Field = "photo"
	FieldBirthMonth      Field = "birthday.month"
	FieldBirthDay        Field = "birthday.day"
	FieldBirthYear       Field = "birthday.year"
	FieldAnniversaryMon  Field = "anniversary.month"
	FieldAnniversaryDay  Field = "anniversary.day"
	FieldAnniversaryYear Field = "anniversary.year"
	// FieldAddress is only a row of defaults: each address carries its own
	// setting on household_addresses.
	FieldAddress Field = "address"
	// FieldHouseholdPhoto is on households (household_settings).
	FieldHouseholdPhoto Field = "household.photo"
)

// PersonFields are the fields a person's profile can show or hide, in the
// order the forms list them.
var PersonFields = []Field{
	FieldEmail, FieldPhone, FieldPhoto,
	FieldBirthMonth, FieldBirthDay, FieldBirthYear,
	FieldAnniversaryMon, FieldAnniversaryDay, FieldAnniversaryYear,
}

// ChildFields are the PersonFields that apply to children (PLAN.md,
// "Starting defaults": anniversaries are "not applicable").
var ChildFields = []Field{
	FieldEmail, FieldPhone, FieldPhoto, FieldBirthMonth, FieldBirthDay, FieldBirthYear,
}

// DefaultFields are the fields /admin/defaults sets for adults.
var DefaultFields = append(append([]Field{}, PersonFields...), FieldAddress, FieldHouseholdPhoto)

var fieldLabels = map[Field]string{
	FieldEmail: "Email", FieldPhone: "Phone", FieldPhoto: "Photo",
	FieldBirthMonth: "Birthday: month", FieldBirthDay: "Birthday: day", FieldBirthYear: "Birthday: year",
	FieldAnniversaryMon: "Anniversary: month", FieldAnniversaryDay: "Anniversary: day", FieldAnniversaryYear: "Anniversary: year",
	FieldAddress: "Household addresses", FieldHouseholdPhoto: "Household photo",
}

// Label is the field's name for people, "Birthday: month".
func (f Field) Label() string { return fieldLabels[f] }

// IsPersonField reports whether f is one of PersonFields.
func IsPersonField(f Field) bool {
	for _, p := range PersonFields {
		if p == f {
			return true
		}
	}
	return false
}

// Audiences for the defaults.
const (
	AudienceAdult = "adult"
	AudienceChild = "child"
)

// FieldSetting is one field's own setting: Shown nil means "use the
// default"; ForceHidden is staff's override.
type FieldSetting struct {
	Shown       *bool
	ForceHidden bool
}

// IsZero is a setting that says nothing (no row is kept for it).
func (s FieldSetting) IsZero() bool { return s.Shown == nil && !s.ForceHidden }

// Defaults is what shows when nobody chose, by audience and field.
type Defaults map[string]bool

func defaultKey(audience string, f Field) string { return audience + "|" + string(f) }

// Shown looks a default up. A missing row hides (the migration seeds them
// all, so this is only a safe fallback).
func (d Defaults) Shown(audience string, f Field) bool { return d[defaultKey(audience, f)] }

// LoadDefaults reads the defaults the admin set.
func (s *Store) LoadDefaults() (Defaults, error) {
	rows, err := s.DB.Query(`SELECT audience, field, shown FROM defaults`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := Defaults{}
	for rows.Next() {
		var a, f string
		var shown bool
		if err := rows.Scan(&a, &f, &shown); err != nil {
			return nil, err
		}
		d[defaultKey(a, Field(f))] = shown
	}
	return d, rows.Err()
}

// DefaultChange is one default to set.
type DefaultChange struct {
	Audience string
	Field    Field
	Shown    bool
}

// SetDefaults saves defaults, logging each one that actually changed
// ("changed default for children: email").
func (s *Store) SetDefaults(changes []DefaultChange, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		for _, c := range changes {
			var old bool
			err := tx.QueryRow(`SELECT shown FROM defaults WHERE audience = ? AND field = ?`, c.Audience, string(c.Field)).Scan(&old)
			if err != nil {
				return notFound(err)
			}
			if old == c.Shown {
				continue
			}
			if _, err := tx.Exec(`UPDATE defaults SET shown = ? WHERE audience = ? AND field = ?`, c.Shown, c.Audience, string(c.Field)); err != nil {
				return err
			}
			who := "adults"
			if c.Audience == AudienceChild {
				who = "children"
			}
			if err := logChange(tx, by, 0, 0, "changed default for "+who+": "+string(c.Field)); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanSetting(shown sql.NullBool, force bool) FieldSetting {
	st := FieldSetting{ForceHidden: force}
	if shown.Valid {
		v := shown.Bool
		st.Shown = &v
	}
	return st
}

// AllFieldSettings reads every profile's own settings: person -> field.
func (s *Store) AllFieldSettings() (map[int64]map[Field]FieldSetting, error) {
	rows, err := s.DB.Query(`SELECT person_id, field, shown, force_hidden FROM field_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[Field]FieldSetting{}
	for rows.Next() {
		var pid int64
		var f string
		var shown sql.NullBool
		var force bool
		if err := rows.Scan(&pid, &f, &shown, &force); err != nil {
			return nil, err
		}
		if out[pid] == nil {
			out[pid] = map[Field]FieldSetting{}
		}
		out[pid][Field(f)] = scanSetting(shown, force)
	}
	return out, rows.Err()
}

// PersonFieldSettings reads one profile's own settings.
func (s *Store) PersonFieldSettings(personID int64) (map[Field]FieldSetting, error) {
	all, err := s.AllFieldSettings()
	if err != nil {
		return nil, err
	}
	if m := all[personID]; m != nil {
		return m, nil
	}
	return map[Field]FieldSetting{}, nil
}

// SetPersonFieldSettings saves a profile's own settings for the fields in
// settings (a field with a zero setting loses its row). Each field that
// changed is logged ("changed email visibility", "force-hid email").
func (s *Store) SetPersonFieldSettings(personID int64, settings map[Field]FieldSetting, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id FROM people WHERE id = ?`, personID).Scan(&personID); err != nil {
			return notFound(err)
		}
		for _, f := range PersonFields {
			want, ok := settings[f]
			if !ok {
				continue
			}
			var shown sql.NullBool
			var force bool
			err := tx.QueryRow(`SELECT shown, force_hidden FROM field_settings WHERE person_id = ? AND field = ?`, personID, string(f)).Scan(&shown, &force)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			old := scanSetting(shown, force)
			if sameSetting(old, want) {
				continue
			}
			if want.IsZero() {
				_, err = tx.Exec(`DELETE FROM field_settings WHERE person_id = ? AND field = ?`, personID, string(f))
			} else {
				_, err = tx.Exec(`INSERT INTO field_settings (person_id, field, shown, force_hidden, set_by, set_at)
					VALUES (?, ?, ?, ?, ?, datetime('now'))
					ON CONFLICT (person_id, field) DO UPDATE SET shown = excluded.shown,
						force_hidden = excluded.force_hidden, set_by = excluded.set_by, set_at = excluded.set_at`,
					personID, string(f), nullBool(want.Shown), want.ForceHidden, nullInt(by))
			}
			if err != nil {
				return err
			}
			if want.ForceHidden != old.ForceHidden {
				what := "force-hid " + string(f)
				if !want.ForceHidden {
					what = "lifted force hide on " + string(f)
				}
				if err := logChange(tx, by, personID, 0, what); err != nil {
					return err
				}
			}
			if !sameShown(old.Shown, want.Shown) {
				if err := logChange(tx, by, personID, 0, "changed "+string(f)+" visibility"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func sameShown(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameSetting(a, b FieldSetting) bool {
	return a.ForceHidden == b.ForceHidden && sameShown(a.Shown, b.Shown)
}

// AllHouseholdSettings reads every household's own photo setting.
func (s *Store) AllHouseholdSettings() (map[int64]map[Field]FieldSetting, error) {
	rows, err := s.DB.Query(`SELECT household_id, field, shown, force_hidden FROM household_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[Field]FieldSetting{}
	for rows.Next() {
		var hid int64
		var f string
		var shown sql.NullBool
		var force bool
		if err := rows.Scan(&hid, &f, &shown, &force); err != nil {
			return nil, err
		}
		if out[hid] == nil {
			out[hid] = map[Field]FieldSetting{}
		}
		out[hid][Field(f)] = scanSetting(shown, force)
	}
	return out, rows.Err()
}

// SetHouseholdPhotoSetting saves whether the household's photo shows.
func (s *Store) SetHouseholdPhotoSetting(householdID int64, set FieldSetting, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id FROM households WHERE id = ?`, householdID).Scan(&householdID); err != nil {
			return notFound(err)
		}
		var shown sql.NullBool
		var force bool
		err := tx.QueryRow(`SELECT shown, force_hidden FROM household_settings WHERE household_id = ? AND field = ?`,
			householdID, string(FieldHouseholdPhoto)).Scan(&shown, &force)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		old := scanSetting(shown, force)
		if sameSetting(old, set) {
			return nil
		}
		if set.IsZero() {
			_, err = tx.Exec(`DELETE FROM household_settings WHERE household_id = ? AND field = ?`, householdID, string(FieldHouseholdPhoto))
		} else {
			_, err = tx.Exec(`INSERT INTO household_settings (household_id, field, shown, force_hidden, set_by, set_at)
				VALUES (?, ?, ?, ?, ?, datetime('now'))
				ON CONFLICT (household_id, field) DO UPDATE SET shown = excluded.shown,
					force_hidden = excluded.force_hidden, set_by = excluded.set_by, set_at = excluded.set_at`,
				householdID, string(FieldHouseholdPhoto), nullBool(set.Shown), set.ForceHidden, nullInt(by))
		}
		if err != nil {
			return err
		}
		return logChange(tx, by, 0, householdID, "changed household photo visibility")
	})
}

// SetAddressVisibility saves whether an address shows (nil: use the
// default) and staff's force hide.
func (s *Store) SetAddressVisibility(addressID int64, shown *bool, forceHidden bool, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		var hid int64
		if err := tx.QueryRow(`SELECT household_id FROM household_addresses WHERE id = ?`, addressID).Scan(&hid); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE household_addresses SET shown = ?, force_hidden = ? WHERE id = ?`,
			nullBool(shown), forceHidden, addressID); err != nil {
			return err
		}
		return logChange(tx, by, 0, hid, "changed address visibility")
	})
}

// Membership is a row of household_members, for working out who is a head
// or a child and who lives at which address.
type Membership struct {
	HouseholdID int64
	PersonID    int64
	Role        string
	AddressID   int64 // 0 for none
	IsPrimary   bool
	Position    int
}

// AllMemberships lists every household membership.
func (s *Store) AllMemberships() ([]Membership, error) {
	rows, err := s.DB.Query(`SELECT household_id, person_id, role, COALESCE(address_id, 0), is_primary, position
		FROM household_members ORDER BY household_id, CASE role WHEN 'head' THEN 0 WHEN 'adult' THEN 1 ELSE 2 END, position, person_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.HouseholdID, &m.PersonID, &m.Role, &m.AddressID, &m.IsPrimary, &m.Position); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllPeople loads every person (the access layer filters them).
func (s *Store) AllPeople() ([]Person, error) {
	rows, err := s.DB.Query(`SELECT ` + personCols + ` FROM people ORDER BY last_name COLLATE NOCASE, first_name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllHouseholds loads every household.
func (s *Store) AllHouseholds() ([]Household, error) {
	rows, err := s.DB.Query(`SELECT ` + householdCols + ` FROM households ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Household
	for rows.Next() {
		h, err := scanHousehold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AllAddresses loads every household's addresses, in order.
func (s *Store) AllAddresses() ([]Address, error) {
	return s.listAddresses(`1 = 1`)
}

// PersonByUser finds the profile linked to a user; ErrNotFound if none.
func (s *Store) PersonByUser(userID int64) (Person, error) {
	p, err := scanPerson(s.DB.QueryRow(`SELECT `+personCols+` FROM people WHERE user_id = ?`, userID))
	return p, notFound(err)
}

// HeadHouseholds lists the households a person heads.
func (s *Store) HeadHouseholds(personID int64) ([]int64, error) {
	rows, err := s.DB.Query(`SELECT household_id FROM household_members WHERE person_id = ? AND role = 'head'`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
