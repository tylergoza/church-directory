package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Household is a row of households.
type Household struct {
	ID         int64
	Name       string
	Photo      string
	Kind       string // KindMember or KindContact
	ManagedBy  int64  // a person, for contact households; 0 for none
	Pending    bool   // made through self sign-up, waiting for staff
	CreatedBy  int64  // the user who made a pending household; 0 for none
	ArchivedAt string
	CreatedAt  string
	UpdatedAt  string
}

func (h Household) Archived() bool { return h.ArchivedAt != "" }

// Household roles.
const (
	RoleHead  = "head"
	RoleAdult = "adult"
	RoleChild = "child"
)

// ValidHouseholdRole reports whether r is head, adult or child.
func ValidHouseholdRole(r string) bool { return r == RoleHead || r == RoleAdult || r == RoleChild }

var (
	// ErrNeedsReassign: an address with people at it can only be deleted
	// when they're given another address (or none).
	ErrNeedsReassign = errors.New("people live at this address: choose where they go")
	// ErrWrongHousehold: an address that isn't one of the household's.
	ErrWrongHousehold = errors.New("that address belongs to another household")
	// ErrBadRole: a household role other than head, adult or child.
	ErrBadRole = errors.New("role must be head, adult or child")
)

// NoAddress, as an address ID, means "no address".
const NoAddress int64 = -1

const householdCols = `id, name, photo, kind, COALESCE(managed_by, 0), pending, COALESCE(created_by, 0), COALESCE(archived_at, ''), created_at, updated_at`

func scanHousehold(row scanner) (Household, error) {
	var h Household
	err := row.Scan(&h.ID, &h.Name, &h.Photo, &h.Kind, &h.ManagedBy, &h.Pending, &h.CreatedBy, &h.ArchivedAt, &h.CreatedAt, &h.UpdatedAt)
	return h, err
}

// ValidateHousehold cleans up a household's name and kind and lists what's
// wrong with them.
func ValidateHousehold(name, kind string) (string, string, []string) {
	name, kind = strings.TrimSpace(name), strings.TrimSpace(kind)
	if kind == "" {
		kind = KindMember
	}
	var errs []string
	if name == "" {
		errs = append(errs, "Household name is required.")
	}
	if len(name) > 200 {
		errs = append(errs, "Household name is too long.")
	}
	if kind != KindMember && kind != KindContact {
		errs = append(errs, "Kind must be member or contact.")
	}
	return name, kind, errs
}

func (s *Store) GetHousehold(id int64) (Household, error) {
	h, err := scanHousehold(s.DB.QueryRow(`SELECT `+householdCols+` FROM households WHERE id = ?`, id))
	return h, notFound(err)
}

func (s *Store) CreateHousehold(name, kind string, by int64) (int64, error) {
	name, kind, errs := ValidateHousehold(name, kind)
	if errs != nil {
		return 0, errors.New(errs[0])
	}
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO households (name, kind) VALUES (?, ?)`, name, kind)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, 0, id, "added household")
	})
	return id, err
}

func (s *Store) UpdateHousehold(id int64, name, kind string, by int64) error {
	name, kind, errs := ValidateHousehold(name, kind)
	if errs != nil {
		return errors.New(errs[0])
	}
	return s.inTx(func(tx *sql.Tx) error {
		var oldName, oldKind string
		if err := tx.QueryRow(`SELECT name, kind FROM households WHERE id = ?`, id).Scan(&oldName, &oldKind); err != nil {
			return notFound(err)
		}
		var what []string
		if oldName != name {
			what = append(what, "name")
		}
		if oldKind != kind {
			what = append(what, "kind")
		}
		if len(what) == 0 {
			return nil
		}
		if _, err := tx.Exec(`UPDATE households SET name = ?, kind = ?, updated_at = datetime('now') WHERE id = ?`, name, kind, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, id, "edited household "+strings.Join(what, ", "))
	})
}

// ArchiveHousehold hides a household (archived true) or brings it back.
// Its members stay in it.
func (s *Store) ArchiveHousehold(id int64, archived bool, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		q, what := `UPDATE households SET archived_at = NULL, updated_at = datetime('now') WHERE id = ?`, "unarchived household"
		if archived {
			q, what = `UPDATE households SET archived_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`, "archived household"
		}
		res, err := tx.Exec(q, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, 0, id, what)
	})
}

// DeleteHousehold removes a household with its addresses and memberships.
// The people in it stay. It returns the household's photo file name ("" for
// none) for the caller to remove.
func (s *Store) DeleteHousehold(id int64, by int64) (photo string, err error) {
	err = s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT photo FROM households WHERE id = ?`, id).Scan(&photo); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`DELETE FROM households WHERE id = ?`, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, 0, "deleted household")
	})
	if err != nil {
		photo = ""
	}
	return photo, err
}

// HouseholdRow is a household with who's in it, for lists.
type HouseholdRow struct {
	Household
	Members []string // display names, heads first
}

// The filters of /admin/households.
const (
	HouseholdsAll      = ""
	HouseholdsContacts = "contacts"
	HouseholdsArchived = "archived"
)

// ListHouseholds lists households by name. q matches the household's name
// or a member's name, ignoring case and accents.
func (s *Store) ListHouseholds(filter, q string) ([]HouseholdRow, error) {
	where := `archived_at IS NULL`
	switch filter {
	case HouseholdsContacts:
		where += ` AND kind = 'contact'`
	case HouseholdsArchived:
		where = `archived_at IS NOT NULL`
	}
	rows, err := s.DB.Query(`SELECT ` + householdCols + ` FROM households WHERE ` + where + ` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	var hs []Household
	for rows.Next() {
		h, err := scanHousehold(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		hs = append(hs, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := s.memberNamesByHousehold()
	if err != nil {
		return nil, err
	}
	words := strings.Fields(Fold(q))
	var out []HouseholdRow
	for _, h := range hs {
		row := HouseholdRow{Household: h, Members: members[h.ID]}
		hay := Fold(h.Name + " " + strings.Join(row.Members, " "))
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *Store) memberNamesByHousehold() (map[int64][]string, error) {
	rows, err := s.DB.Query(`
		SELECT m.household_id, p.first_name, p.last_name, p.goes_by, p.suffix
		FROM household_members m JOIN people p ON p.id = m.person_id
		ORDER BY m.household_id, CASE m.role WHEN 'head' THEN 0 WHEN 'adult' THEN 1 ELSE 2 END, m.position, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64][]string{}
	for rows.Next() {
		var hid int64
		var f, l, g, sfx string
		if err := rows.Scan(&hid, &f, &l, &g, &sfx); err != nil {
			return nil, err
		}
		m[hid] = append(m[hid], DisplayName(f, l, g, sfx))
	}
	return m, rows.Err()
}

func (s *Store) householdNamesByPerson() (map[int64][]string, error) {
	rows, err := s.DB.Query(`SELECT m.person_id, h.name FROM household_members m JOIN households h ON h.id = m.household_id ORDER BY m.person_id, h.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64][]string{}
	for rows.Next() {
		var pid int64
		var name string
		if err := rows.Scan(&pid, &name); err != nil {
			return nil, err
		}
		m[pid] = append(m[pid], name)
	}
	return m, rows.Err()
}

// Addresses ------------------------------------------------------------------

// Address is one of a household's addresses.
type Address struct {
	ID          int64
	HouseholdID int64
	Label       string
	Address1    string
	Address2    string
	City        string
	State       string
	Postal      string
	Shown       *bool // the heads' choice; nil means use the default (step 3)
	ForceHidden bool  // staff's override (step 3)
	Position    int
	Members     int // how many people are assigned to it
}

// OneLine is the address on one line, "123 Oak St, Springfield, IL 62701".
func (a Address) OneLine() string {
	var parts []string
	for _, p := range []string{a.Address1, a.Address2, a.City} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if sp := strings.TrimSpace(a.State + " " + a.Postal); sp != "" {
		parts = append(parts, sp)
	}
	return strings.Join(parts, ", ")
}

// Title is the label, or the address when it has none.
func (a Address) Title() string {
	if a.Label != "" {
		return a.Label
	}
	if l := a.OneLine(); l != "" {
		return l
	}
	return "Address"
}

// AddressInput is what the address form holds.
type AddressInput struct {
	Label, Address1, Address2, City, State, Postal string
}

// Validate trims the fields and lists what's wrong.
func (in *AddressInput) Validate() []string {
	in.Label, in.Address1, in.Address2 = strings.TrimSpace(in.Label), strings.TrimSpace(in.Address1), strings.TrimSpace(in.Address2)
	in.City, in.State, in.Postal = strings.TrimSpace(in.City), strings.TrimSpace(in.State), strings.TrimSpace(in.Postal)
	var errs []string
	if in.Address1 == "" && in.City == "" && in.Postal == "" {
		errs = append(errs, "Enter a street, city or postal code for the address.")
	}
	if len(in.Label+in.Address1+in.Address2+in.City+in.State+in.Postal) > 600 {
		errs = append(errs, "The address is too long.")
	}
	return errs
}

func (s *Store) ListAddresses(householdID int64) ([]Address, error) {
	return s.listAddresses(`a.household_id = ?`, householdID)
}

// listAddresses lists addresses matching a where clause, by household then
// position.
func (s *Store) listAddresses(where string, args ...any) ([]Address, error) {
	rows, err := s.DB.Query(`
		SELECT a.id, a.household_id, a.label, a.address1, a.address2, a.city, a.state, a.postal,
		       a.shown, a.force_hidden, a.position,
		       (SELECT COUNT(*) FROM household_members m WHERE m.address_id = a.id)
		FROM household_addresses a WHERE `+where+` ORDER BY a.household_id, a.position, a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Address
	for rows.Next() {
		var a Address
		var shown sql.NullBool
		if err := rows.Scan(&a.ID, &a.HouseholdID, &a.Label, &a.Address1, &a.Address2, &a.City, &a.State, &a.Postal,
			&shown, &a.ForceHidden, &a.Position, &a.Members); err != nil {
			return nil, err
		}
		if shown.Valid {
			v := shown.Bool
			a.Shown = &v
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAddress loads one address (with who lives at it).
func (s *Store) GetAddress(id int64) (Address, error) {
	var hid int64
	if err := s.DB.QueryRow(`SELECT household_id FROM household_addresses WHERE id = ?`, id).Scan(&hid); err != nil {
		return Address{}, notFound(err)
	}
	all, err := s.ListAddresses(hid)
	if err != nil {
		return Address{}, err
	}
	for _, a := range all {
		if a.ID == id {
			return a, nil
		}
	}
	return Address{}, ErrNotFound
}

// AddAddress adds an address after the household's others. The input must
// already pass Validate.
func (s *Store) AddAddress(householdID int64, in AddressInput, by int64) (int64, error) {
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id FROM households WHERE id = ?`, householdID).Scan(&householdID); err != nil {
			return notFound(err)
		}
		res, err := tx.Exec(`
			INSERT INTO household_addresses (household_id, label, address1, address2, city, state, postal, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM household_addresses WHERE household_id = ?))`,
			householdID, in.Label, in.Address1, in.Address2, in.City, in.State, in.Postal, householdID)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, 0, householdID, "added address")
	})
	return id, err
}

func (s *Store) UpdateAddress(id int64, in AddressInput, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		var hid int64
		if err := tx.QueryRow(`SELECT household_id FROM household_addresses WHERE id = ?`, id).Scan(&hid); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE household_addresses SET label = ?, address1 = ?, address2 = ?, city = ?, state = ?, postal = ? WHERE id = ?`,
			in.Label, in.Address1, in.Address2, in.City, in.State, in.Postal, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, hid, "edited address")
	})
}

// DeleteAddress removes an address. When people are assigned to it,
// reassignTo says where they go: another of the household's addresses, or
// NoAddress; 0 gives ErrNeedsReassign.
func (s *Store) DeleteAddress(id, reassignTo, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		var hid int64
		if err := tx.QueryRow(`SELECT household_id FROM household_addresses WHERE id = ?`, id).Scan(&hid); err != nil {
			return notFound(err)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM household_members WHERE address_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			switch {
			case reassignTo == 0 || reassignTo == id:
				return ErrNeedsReassign
			case reassignTo == NoAddress:
				// the foreign key clears them
			default:
				var other int64
				err := tx.QueryRow(`SELECT household_id FROM household_addresses WHERE id = ?`, reassignTo).Scan(&other)
				if err != nil || other != hid {
					return ErrWrongHousehold
				}
				if _, err := tx.Exec(`UPDATE household_members SET address_id = ? WHERE address_id = ?`, reassignTo, id); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM household_addresses WHERE id = ?`, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, hid, "removed address")
	})
}

// Members --------------------------------------------------------------------

// Member is a person's place in a household.
type Member struct {
	Person
	HouseholdID int64
	Role        string
	AddressID   int64 // 0 for none
	IsPrimary   bool
	Position    int
}

// HouseholdMembers lists a household's people: heads, then adults, then
// children, each in the order they were added.
func (s *Store) HouseholdMembers(householdID int64) ([]Member, error) {
	// None of people's column names clash with household_members', so
	// personCols works unqualified.
	rows, err := s.DB.Query(`SELECT `+personCols+`, m.household_id, m.role, COALESCE(m.address_id, 0), m.is_primary, m.position
		FROM household_members m JOIN people p ON p.id = m.person_id WHERE m.household_id = ?
		ORDER BY CASE m.role WHEN 'head' THEN 0 WHEN 'adult' THEN 1 ELSE 2 END, m.position, p.id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		err := rows.Scan(append(personDests(&m.Person), &m.HouseholdID, &m.Role, &m.AddressID, &m.IsPrimary, &m.Position)...)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PlaceMember puts a person in a household with a role and an address
// (0 or NoAddress for none): it adds them, or changes their role or
// address when they're already in it. An adult has one home, so a head or
// adult (and a child who isn't marked as living in more than one household)
// is taken out of their other households first: this is how people move.
// The one exception is a child marked "lives in more than one household",
// who keeps their other households as a child.
func (s *Store) PlaceMember(householdID, personID int64, role string, addressID, by int64) error {
	if !ValidHouseholdRole(role) {
		return ErrBadRole
	}
	return s.inTx(func(tx *sql.Tx) error {
		if err := placeMemberTx(tx, householdID, personID, role, addressID, by); err != nil {
			return err
		}
		return settleManagedBy(tx, householdID, by)
	})
}

func placeMemberTx(tx *sql.Tx, householdID, personID int64, role string, addressID, by int64) error {
	{
		var multi bool
		if err := tx.QueryRow(`SELECT multi_household FROM people WHERE id = ?`, personID).Scan(&multi); err != nil {
			return notFound(err)
		}
		if err := tx.QueryRow(`SELECT id FROM households WHERE id = ?`, householdID).Scan(&householdID); err != nil {
			return notFound(err)
		}
		if addressID == NoAddress {
			addressID = 0
		}
		if addressID != 0 {
			var hid int64
			if err := tx.QueryRow(`SELECT household_id FROM household_addresses WHERE id = ?`, addressID).Scan(&hid); err != nil || hid != householdID {
				return ErrWrongHousehold
			}
		}
		// Households they leave, for the audit trail.
		rows, err := tx.Query(`SELECT household_id, role FROM household_members WHERE person_id = ? AND household_id <> ?`, personID, householdID)
		if err != nil {
			return err
		}
		var leaving []int64
		for rows.Next() {
			var hid int64
			var r string
			if err := rows.Scan(&hid, &r); err != nil {
				rows.Close()
				return err
			}
			if role == RoleChild && multi && r == RoleChild {
				continue // stays in their other households
			}
			leaving = append(leaving, hid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, hid := range leaving {
			if _, err := tx.Exec(`DELETE FROM household_members WHERE household_id = ? AND person_id = ?`, hid, personID); err != nil {
				return err
			}
			if err := logChange(tx, by, personID, hid, "moved out of household"); err != nil {
				return err
			}
		}
		var existing string
		err = tx.QueryRow(`SELECT role FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.Exec(`
				INSERT INTO household_members (household_id, person_id, role, address_id, position)
				VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM household_members WHERE household_id = ?))`,
				householdID, personID, role, nullInt(addressID), householdID)
			if err != nil {
				return err
			}
			return logChange(tx, by, personID, householdID, "added to household as "+role)
		case err != nil:
			return err
		}
		if _, err := tx.Exec(`UPDATE household_members SET role = ?, address_id = ? WHERE household_id = ? AND person_id = ?`,
			role, nullInt(addressID), householdID, personID); err != nil {
			return err
		}
		return logChange(tx, by, personID, householdID, "changed role or address in household")
	}
}

// RemoveMember takes a person out of a household. They stay in the
// directory, in no household.
func (s *Store) RemoveMember(householdID, personID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, personID, householdID, "removed from household")
	})
}

// PersonHousehold is one household a person is in.
type PersonHousehold struct {
	HouseholdID int64
	Name        string
	Role        string
	Primary     bool // the child's primary household (is_primary)
}

// PersonHouseholds lists the households a person is in (almost always
// one), by name.
func (s *Store) PersonHouseholds(personID int64) ([]PersonHousehold, error) {
	rows, err := s.DB.Query(`SELECT h.id, h.name, m.role, m.is_primary FROM household_members m JOIN households h ON h.id = m.household_id
		WHERE m.person_id = ? ORDER BY h.name COLLATE NOCASE, h.id`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonHousehold
	for rows.Next() {
		var h PersonHousehold
		if err := rows.Scan(&h.HouseholdID, &h.Name, &h.Role, &h.Primary); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
