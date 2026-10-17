package store

import (
	"database/sql"
	"errors"
	"strings"
)

// ErrOwnHousehold: a "start their own household" request that can't work.
var ErrOwnHousehold = errors.New("the person, spouse and children must be different people")

// StartOwnHousehold makes a new household named name and moves the person
// into it as head, with the picked spouse (also head, 0 for none) and
// children (role child). Family links are untouched. They leave whatever
// household they were in (PlaceMember's rules).
//
// managedBy is the person profile of whoever started it when that is a head
// of the household being left (steps for heads call it with their own
// profile); 0 for staff. Until someone in the new household has a login and
// is its head, that person keeps editing it (households.managed_by); see
// ManagedByActive and SettleManagedBy.
//
// Who may do this is the caller's check: staff, or a head of the household
// the person is leaving. Returns the new household's ID.
func (s *Store) StartOwnHousehold(personID int64, name string, spouseID int64, childIDs []int64, by, managedBy int64) (int64, error) {
	name, _, errs := ValidateHousehold(name, KindMember)
	if errs != nil {
		return 0, errors.New(errs[0])
	}
	if spouseID == personID {
		return 0, ErrOwnHousehold
	}
	seen := map[int64]bool{personID: true}
	if spouseID != 0 {
		seen[spouseID] = true
	}
	var kids []int64
	for _, c := range childIDs {
		if c == 0 {
			continue
		}
		if seen[c] {
			if c == spouseID || c == personID {
				return 0, ErrOwnHousehold
			}
			continue // listed twice
		}
		seen[c] = true
		kids = append(kids, c)
	}
	var hid int64
	err := s.inTx(func(tx *sql.Tx) error {
		for id := range seen {
			if err := tx.QueryRow(`SELECT id FROM people WHERE id = ?`, id).Scan(&id); err != nil {
				return notFound(err)
			}
		}
		res, err := tx.Exec(`INSERT INTO households (name, kind, managed_by) VALUES (?, ?, ?)`, name, KindMember, nullInt(managedBy))
		if err != nil {
			return err
		}
		if hid, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := logChange(tx, by, personID, hid, "started own household"); err != nil {
			return err
		}
		if err := placeMemberTx(tx, hid, personID, RoleHead, 0, by); err != nil {
			return err
		}
		if spouseID != 0 {
			if err := placeMemberTx(tx, hid, spouseID, RoleHead, 0, by); err != nil {
				return err
			}
		}
		for _, c := range kids {
			if err := placeMemberTx(tx, hid, c, RoleChild, 0, by); err != nil {
				return err
			}
		}
		return settleManagedBy(tx, hid, by)
	})
	if err != nil {
		return 0, err
	}
	return hid, nil
}

// settleManagedBy ends a household's managed_by when someone in it has a
// login and is its head.
func settleManagedBy(tx dbtx, householdID, by int64) error {
	res, err := tx.Exec(`UPDATE households SET managed_by = NULL, updated_at = datetime('now')
		WHERE id = ? AND managed_by IS NOT NULL
		AND EXISTS (SELECT 1 FROM household_members m JOIN people p ON p.id = m.person_id
			WHERE m.household_id = households.id AND m.role = 'head' AND p.user_id IS NOT NULL)`, householdID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return logChange(tx, by, 0, householdID, "ended managed_by: a head has a login")
	}
	return nil
}

// SettleManagedBy ends managed_by for one household if it has a linked head.
// PlaceMember and StartOwnHousehold do this themselves; call it after
// anything else that makes it true.
func (s *Store) SettleManagedBy(householdID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error { return settleManagedBy(tx, householdID, by) })
}

// SettleManagedByPerson does SettleManagedBy for every household the person
// heads: call it right after linking a person to a login (people.user_id).
func (s *Store) SettleManagedByPerson(personID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error { return settleManagedByPerson(tx, personID, by) })
}

// settleManagedByPerson is SettleManagedByPerson inside a transaction; every
// store path that sets people.user_id calls it.
func settleManagedByPerson(tx dbtx, personID, by int64) error {
	rows, err := tx.Query(`SELECT household_id FROM household_members WHERE person_id = ? AND role = 'head'`, personID)
	if err != nil {
		return err
	}
	var hs []int64
	for rows.Next() {
		var h int64
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		hs = append(hs, h)
	}
	rows.Close()
	for _, h := range hs {
		if err := settleManagedBy(tx, h, by); err != nil {
			return err
		}
	}
	return nil
}

// ManagedByActive returns the person who manages the household, and true,
// while their rights last: managed_by is set and no head has a login.
func (s *Store) ManagedByActive(householdID int64) (int64, bool, error) {
	var by, linked int64
	err := s.DB.QueryRow(`SELECT COALESCE(h.managed_by, 0),
			(SELECT COUNT(*) FROM household_members m JOIN people p ON p.id = m.person_id
			 WHERE m.household_id = h.id AND m.role = 'head' AND p.user_id IS NOT NULL)
		FROM households h WHERE h.id = ?`, householdID).Scan(&by, &linked)
	if err != nil {
		return 0, false, notFound(err)
	}
	return by, by != 0 && linked == 0, nil
}

// OwnHouseholdName is the suggested name for a person's new household.
func OwnHouseholdName(p Person) string {
	n := strings.TrimSpace(p.LastName)
	if n == "" {
		return "New household"
	}
	return "The " + n + " Family"
}
