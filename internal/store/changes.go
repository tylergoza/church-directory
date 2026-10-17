package store

import (
	"context"
	"database/sql"
)

// dbtx is what both *sql.DB and *sql.Tx can do.
type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// inTx runs fn in a transaction: committed if it returns nil, rolled back
// otherwise. The database has one connection, so inside fn use only tx,
// never s.DB.
func (s *Store) inTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// logChange writes a row to the audit trail: who (by, 0 for nobody), which
// person and household (0 for none) and what was done, in a few words like
// "edited name". Never values: names, notes and so on stay out of it. Every
// write calls it in the same transaction as the write.
func logChange(tx dbtx, by, personID, householdID int64, what string) error {
	_, err := tx.Exec(`INSERT INTO changes (person_id, household_id, by_user, what) VALUES (?, ?, ?, ?)`,
		nullInt(personID), nullInt(householdID), nullInt(by), what)
	return err
}

// LogChange writes an audit row on its own, for writes made outside this
// package's transactions.
func (s *Store) LogChange(by, personID, householdID int64, what string) error {
	return logChange(s.DB, by, personID, householdID, what)
}

// Change is one row of the audit trail.
type Change struct {
	ID          int64
	PersonID    int64
	HouseholdID int64
	By          string // display name or username; "" if the user is gone
	What        string
	At          string
}

// RecentChanges lists the newest changes to a person (personID) or a
// household (householdID); pass 0 for the other.
func (s *Store) RecentChanges(personID, householdID int64, limit int) ([]Change, error) {
	rows, err := s.DB.Query(`
		SELECT c.id, COALESCE(c.person_id, 0), COALESCE(c.household_id, 0),
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''), c.what, c.at
		FROM changes c LEFT JOIN users u ON u.id = c.by_user
		WHERE (? <> 0 AND c.person_id = ?) OR (? <> 0 AND c.household_id = ?)
		ORDER BY c.id DESC LIMIT ?`, personID, personID, householdID, householdID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.ID, &c.PersonID, &c.HouseholdID, &c.By, &c.What, &c.At); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
