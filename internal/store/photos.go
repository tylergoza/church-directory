package store

import "database/sql"

// SetPersonPhoto points the person at a new photo file name ("" removes
// it) and returns the old name so the caller can delete its files. The
// audit row says "changed photo" or "removed photo", never the name.
func (s *Store) SetPersonPhoto(id int64, name string, by int64) (old string, err error) {
	err = s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT photo FROM people WHERE id = ?`, id).Scan(&old); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE people SET photo = ?, updated_at = datetime('now') WHERE id = ?`, name, id); err != nil {
			return err
		}
		return logChange(tx, by, id, 0, photoWhat(name))
	})
	return old, err
}

// SetHouseholdPhoto is SetPersonPhoto for a household.
func (s *Store) SetHouseholdPhoto(id int64, name string, by int64) (old string, err error) {
	err = s.inTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT photo FROM households WHERE id = ?`, id).Scan(&old); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE households SET photo = ?, updated_at = datetime('now') WHERE id = ?`, name, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, id, photoWhat(name))
	})
	return old, err
}

func photoWhat(name string) string {
	if name == "" {
		return "removed photo"
	}
	return "changed photo"
}

// PhotoOwner finds whose photo a file name is: a person or a household
// (the other is 0). ErrNotFound when nothing uses the name.
func (s *Store) PhotoOwner(name string) (personID, householdID int64, err error) {
	if name == "" {
		return 0, 0, ErrNotFound
	}
	err = s.DB.QueryRow(`SELECT id FROM people WHERE photo = ?`, name).Scan(&personID)
	if err == nil {
		return personID, 0, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, err
	}
	err = s.DB.QueryRow(`SELECT id FROM households WHERE photo = ?`, name).Scan(&householdID)
	if err == sql.ErrNoRows {
		return 0, 0, ErrNotFound
	}
	return 0, householdID, err
}
