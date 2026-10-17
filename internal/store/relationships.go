package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Family links. Only two kinds are stored, between people: parent (person
// is a parent of related) and spouse (one row per couple, low id first).
// Children, siblings, grandparents and the rest are worked out from them
// (internal/access/family.go) and never stored. Links survive people moving
// between households.

// Relationship kinds.
const (
	RelParent = "parent"
	RelSpouse = "spouse"
)

var (
	// ErrSelfLink: a person linked to themselves.
	ErrSelfLink = errors.New("a person can't be linked to themselves")
	// ErrCycle: a parent link that would make someone their own ancestor.
	ErrCycle = errors.New("that would make someone their own ancestor")
	// ErrBadKind: a relationship kind other than parent or spouse.
	ErrBadKind = errors.New("kind must be parent or spouse")
)

// Relationship is a row of relationships.
type Relationship struct {
	PersonID  int64
	RelatedID int64
	Kind      string
}

// AddRelationship links two people. For RelParent, a is a parent of b; for
// RelSpouse the order doesn't matter. Adding a link that exists is a no-op.
// A parent link that would make someone their own ancestor is refused
// (ErrCycle).
func (s *Store) AddRelationship(kind string, a, b, by int64) error {
	return s.inTx(func(tx *sql.Tx) error { return addRelationshipTx(tx, kind, a, b, by) })
}

func addRelationshipTx(tx *sql.Tx, kind string, a, b, by int64) error {
	if kind != RelParent && kind != RelSpouse {
		return ErrBadKind
	}
	if a == b {
		return ErrSelfLink
	}
	for _, id := range []int64{a, b} {
		if err := tx.QueryRow(`SELECT id FROM people WHERE id = ?`, id).Scan(&id); err != nil {
			return notFound(err)
		}
	}
	if kind == RelSpouse && a > b {
		a, b = b, a
	}
	if kind == RelParent {
		// a becomes b's parent; refuse if a is already b's descendant, that
		// is, if b is one of a's ancestors.
		var n int
		err := tx.QueryRow(`
			WITH RECURSIVE up(id) AS (
				SELECT person_id FROM relationships WHERE kind = 'parent' AND related_id = ?
				UNION
				SELECT r.person_id FROM relationships r JOIN up ON r.related_id = up.id WHERE r.kind = 'parent'
			) SELECT COUNT(*) FROM up WHERE id = ?`, a, b).Scan(&n)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrCycle
		}
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO relationships (person_id, related_id, kind) VALUES (?, ?, ?)`, a, b, kind)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	what := "added " + kind + " link"
	if err := logChange(tx, by, a, 0, what); err != nil {
		return err
	}
	return logChange(tx, by, b, 0, what)
}

// RemoveRelationship removes a link (same argument order as
// AddRelationship). ErrNotFound when there isn't one.
func (s *Store) RemoveRelationship(kind string, a, b, by int64) error {
	if kind == RelSpouse && a > b {
		a, b = b, a
	}
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM relationships WHERE kind = ? AND person_id = ? AND related_id = ?`, kind, a, b)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		what := "removed " + kind + " link"
		if err := logChange(tx, by, a, 0, what); err != nil {
			return err
		}
		return logChange(tx, by, b, 0, what)
	})
}

// AllRelationships lists every link (the access layer derives the rest).
func (s *Store) AllRelationships() ([]Relationship, error) {
	return s.relationships(`1 = 1`)
}

func (s *Store) relationships(where string, args ...any) ([]Relationship, error) {
	rows, err := s.DB.Query(`SELECT person_id, related_id, kind FROM relationships WHERE `+where+` ORDER BY kind, person_id, related_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relationship
	for rows.Next() {
		var r Relationship
		if err := rows.Scan(&r.PersonID, &r.RelatedID, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PersonLinks is a person's own stored links: parents, children and
// spouses, as people.
type PersonLinks struct {
	Parents, Children, Spouses []Person
}

// ListPersonLinks lists the links a person is in.
func (s *Store) ListPersonLinks(personID int64) (PersonLinks, error) {
	var out PersonLinks
	get := func(q string, dst *[]Person) error {
		args := make([]any, strings.Count(q, "?"))
		for i := range args {
			args[i] = personID
		}
		rows, err := s.DB.Query(`SELECT `+personCols+` FROM people WHERE id IN (`+q+`) ORDER BY last_name COLLATE NOCASE, first_name COLLATE NOCASE, id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPerson(rows)
			if err != nil {
				return err
			}
			*dst = append(*dst, p)
		}
		return rows.Err()
	}
	if err := get(`SELECT person_id FROM relationships WHERE kind = 'parent' AND related_id = ?`, &out.Parents); err != nil {
		return out, err
	}
	if err := get(`SELECT related_id FROM relationships WHERE kind = 'parent' AND person_id = ?`, &out.Children); err != nil {
		return out, err
	}
	err := get(`SELECT related_id FROM relationships WHERE kind = 'spouse' AND person_id = ?
		UNION SELECT person_id FROM relationships WHERE kind = 'spouse' AND related_id = ?`, &out.Spouses)
	return out, err
}
