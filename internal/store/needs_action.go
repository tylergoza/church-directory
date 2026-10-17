package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// What staff see on /admin/needs-action, and the decisions they make.

// CopyField is something a held entry filled in that the existing person
// has empty, offered to copy over.
type CopyField struct {
	Key, Label, Value string
}

// The keys ResolveSamePerson accepts in its copy list.
const (
	CopyGoesBy      = "goes_by"
	CopyEmail       = "email"
	CopyPhone       = "phone"
	CopyBirthday    = "birthday"
	CopyAnniversary = "anniversary"
)

// CopyableFields lists what the held entry has that the existing person
// doesn't.
func CopyableFields(held, existing Person) []CopyField {
	var out []CopyField
	add := func(key, label, v string) { out = append(out, CopyField{key, label, v}) }
	if held.GoesBy != "" && existing.GoesBy == "" {
		add(CopyGoesBy, "Goes by", held.GoesBy)
	}
	if held.Email != "" && existing.Email == "" {
		add(CopyEmail, "Email", held.Email)
	}
	if held.Phone != "" && existing.Phone == "" {
		add(CopyPhone, "Phone", held.Phone)
	}
	if !held.Birth.IsZero() && existing.Birth.IsZero() {
		add(CopyBirthday, "Birthday", held.Birth.String())
	}
	if !held.Anniversary.IsZero() && existing.Anniversary.IsZero() {
		add(CopyAnniversary, "Anniversary", held.Anniversary.String())
	}
	return out
}

// MatchInfo is an existing person next to a held entry. Only staff ever
// see it.
type MatchInfo struct {
	Person      Person
	Households  []PersonHousehold
	LinkedUser  string // display name of the linked user, "" if none
	Copyable    []CopyField
	NeedsAttest bool // linking the held entry's user needs the "13 or older" tick
}

// HeldItem is a person held for staff: a self sign-up or a new person that
// matched an existing name.
type HeldItem struct {
	Person     Person
	Households []PersonHousehold // where it was added, if a household add
	CreatedBy  string
	Matches    []MatchInfo
}

// RenameItem is a held rename.
type RenameItem struct {
	Person  Person
	NewName string
	Matches []MatchInfo
}

// HouseholdItem is a pending self sign-up household.
type HouseholdItem struct {
	Household Household
	CreatedBy string
	Members   []string
}

// NeedsAction is the whole page.
type NeedsAction struct {
	SelfSignup []HeldItem // pending, reason self_signup
	Households []HouseholdItem
	Held       []HeldItem // pending, reason name_match
	Renames    []RenameItem
	Under13    []Under13Item
	Requests   []JoinRequest // heads asking to add a child to another household
}

// Count is how many things are waiting.
func (n NeedsAction) Count() int {
	return len(n.SelfSignup) + len(n.Households) + len(n.Held) + len(n.Renames) + len(n.Under13) + len(n.Requests)
}

func (s *Store) userName(id int64) string {
	if id == 0 {
		return ""
	}
	u, err := s.GetUser(id)
	if err != nil {
		return ""
	}
	return u.Name()
}

func (s *Store) matchInfos(held Person, matches []Person) ([]MatchInfo, error) {
	var out []MatchInfo
	for _, m := range matches {
		if m.Pending { // another held entry is not "already here"
			continue
		}
		hh, err := s.PersonHouseholds(m.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, MatchInfo{
			Person: m, Households: hh, LinkedUser: s.userName(m.UserID),
			Copyable:    CopyableFields(held, m),
			NeedsAttest: held.UserID != 0 && !AgeKnown(m.Birth) && held.AgeAttestedBy == 0 && m.AgeAttestedBy == 0,
		})
	}
	return out, nil
}

func (s *Store) heldItem(p Person) (HeldItem, error) {
	it := HeldItem{Person: p, CreatedBy: s.userName(p.CreatedBy)}
	var err error
	if it.Households, err = s.PersonHouseholds(p.ID); err != nil {
		return it, err
	}
	if p.PendingReason == PendingNameMatch {
		matches, err := s.NameMatches(p.FirstName, p.LastName, p.GoesBy, p.Suffix, p.ID)
		if err != nil {
			return it, err
		}
		if it.Matches, err = s.matchInfos(p, matches); err != nil {
			return it, err
		}
	}
	return it, nil
}

// ListNeedsAction gathers everything waiting for staff.
func (s *Store) ListNeedsAction() (NeedsAction, error) {
	var na NeedsAction
	rows, err := s.DB.Query(`SELECT ` + personCols + ` FROM people WHERE pending = 1 OR pending_name <> '' ORDER BY created_at, id`)
	if err != nil {
		return na, err
	}
	var held, renamed []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return na, err
		}
		if p.Pending {
			held = append(held, p)
		}
		if p.PendingName != "" {
			renamed = append(renamed, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return na, err
	}
	for _, p := range held {
		it, err := s.heldItem(p)
		if err != nil {
			return na, err
		}
		if p.PendingReason == PendingNameMatch {
			na.Held = append(na.Held, it)
		} else {
			na.SelfSignup = append(na.SelfSignup, it)
		}
	}
	for _, p := range renamed {
		var n pendingName
		if json.Unmarshal([]byte(p.PendingName), &n) != nil {
			continue
		}
		matches, err := s.NameMatches(n.First, n.Last, n.GoesBy, n.Suffix, p.ID)
		if err != nil {
			return na, err
		}
		mi, err := s.matchInfos(Person{}, matches)
		if err != nil {
			return na, err
		}
		na.Renames = append(na.Renames, RenameItem{Person: p, NewName: n.display(), Matches: mi})
	}
	hrows, err := s.DB.Query(`SELECT ` + householdCols + ` FROM households WHERE pending = 1 ORDER BY created_at, id`)
	if err != nil {
		return na, err
	}
	var hs []Household
	for hrows.Next() {
		h, err := scanHousehold(hrows)
		if err != nil {
			hrows.Close()
			return na, err
		}
		hs = append(hs, h)
	}
	hrows.Close()
	if err := hrows.Err(); err != nil {
		return na, err
	}
	for _, h := range hs {
		ms, err := s.HouseholdMembers(h.ID)
		if err != nil {
			return na, err
		}
		it := HouseholdItem{Household: h, CreatedBy: s.userName(h.CreatedBy)}
		for _, m := range ms {
			it.Members = append(it.Members, m.Name()+" ("+m.Role+")")
		}
		na.Households = append(na.Households, it)
	}
	if na.Under13, err = s.Under13Items(); err != nil {
		return na, err
	}
	if na.Requests, err = s.OpenJoinRequests(); err != nil {
		return na, err
	}
	return na, nil
}

// NeedsActionCount is how many things are waiting, for the menu badge. It
// counts rows only, so it is cheap enough for every staff page.
func (s *Store) NeedsActionCount() int {
	var n int
	err := s.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM people WHERE pending = 1) +
		(SELECT COUNT(*) FROM people WHERE pending_name <> '') +
		(SELECT COUNT(*) FROM households WHERE pending = 1) +
		(SELECT COUNT(*) FROM under13_items WHERE resolved_at IS NULL) +
		(SELECT COUNT(*) FROM household_requests WHERE answered_at IS NULL)`).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}

// recordPair writes a not_same_people row (low id first), once.
func recordPair(tx dbtx, a, b, by int64) error {
	if a == b {
		return nil
	}
	if a > b {
		a, b = b, a
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO not_same_people (person_a, person_b, decided_by) VALUES (?, ?, ?)`, a, b, nullInt(by))
	return err
}

// ResolveSamePerson is the staff answer "Same person" for a held name
// match: the held entry is deleted and the existing person is used instead.
//
//   - Held entry has a user (self sign-up): the existing person is linked
//     to that user, with the usual age rules (attest13 is the "13 or older"
//     tick when neither profile has an attestation and the age is unknown).
//     No second profile is made.
//   - Held entry was added to households (a head adding someone): the
//     existing person is put in those households with the same role.
//   - copy lists the CopyField keys to copy from the held entry to the
//     existing person; only fields the existing person has empty are copied.
//
// It returns the held entry's photo file name to remove ("" for none).
func (s *Store) ResolveSamePerson(heldID, existingID int64, copy []string, attest13 bool, by int64) (photo string, err error) {
	type place struct {
		household int64
		role      string
		address   int64
	}
	var places []place
	err = s.inTx(func(tx *sql.Tx) error {
		held, err := loadPerson(tx, heldID)
		if err != nil {
			return err
		}
		if !held.Pending || held.PendingReason != PendingNameMatch {
			return ErrNotPending
		}
		ex, err := loadPerson(tx, existingID)
		if err != nil {
			return err
		}
		if ex.ID == held.ID || ex.Pending {
			return errors.New("choose a person who is already in the directory")
		}
		if held.UserID != 0 {
			if ex.UserID != 0 && ex.UserID != held.UserID {
				return ErrAlreadyLinked
			}
			if KnownUnder13(ex.Birth, now()) {
				return ErrUnder13
			}
			if !AgeKnown(ex.Birth) && !attest13 && held.AgeAttestedBy == 0 && ex.AgeAttestedBy == 0 {
				return ErrNeedsAttest
			}
		}
		rows, err := tx.Query(`SELECT household_id, role, COALESCE(address_id, 0) FROM household_members WHERE person_id = ?`, heldID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pl place
			if err := rows.Scan(&pl.household, &pl.role, &pl.address); err != nil {
				rows.Close()
				return err
			}
			places = append(places, pl)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		photo = held.Photo
		if _, err := tx.Exec(`DELETE FROM people WHERE id = ?`, heldID); err != nil {
			return err
		}
		// Copy what was asked for and is really empty on the existing person.
		offered := map[string]bool{}
		for _, f := range CopyableFields(held, ex) {
			offered[f.Key] = true
		}
		var sets []string
		var args []any
		for _, key := range copy {
			if !offered[key] {
				continue
			}
			switch key {
			case CopyGoesBy:
				sets, args = append(sets, "goes_by = ?"), append(args, held.GoesBy)
			case CopyEmail:
				sets, args = append(sets, "email = ?"), append(args, held.Email)
			case CopyPhone:
				sets, args = append(sets, "phone = ?"), append(args, held.Phone)
			case CopyBirthday:
				sets = append(sets, "birth_month = ?, birth_day = ?, birth_year = ?")
				args = append(args, dateArg(held.Birth.Month), dateArg(held.Birth.Day), dateArg(held.Birth.Year))
			case CopyAnniversary:
				sets = append(sets, "anniv_month = ?, anniv_day = ?, anniv_year = ?")
				args = append(args, dateArg(held.Anniversary.Month), dateArg(held.Anniversary.Day), dateArg(held.Anniversary.Year))
			}
		}
		if len(sets) > 0 {
			args = append(args, existingID)
			if _, err := tx.Exec(`UPDATE people SET `+strings.Join(sets, ", ")+`, updated_at = datetime('now') WHERE id = ?`, args...); err != nil {
				return err
			}
		}
		if held.UserID != 0 && ex.UserID == 0 {
			if err := linkUser(tx, existingID, held.UserID, by, attest13 || held.AgeAttestedBy != 0, "linked to user (same person as a self sign-up)"); err != nil {
				return err
			}
		}
		if held.AgeAttestedBy != 0 && !AgeKnown(ex.Birth) && held.UserID != 0 {
			if err := recordAttest(tx, existingID, held.AgeAttestedBy); err != nil {
				return err
			}
		}
		if err := enforceUnder13(tx, existingID, by); err != nil {
			return err
		}
		return logChange(tx, by, existingID, 0, "same person as a held entry")
	})
	if err != nil {
		return "", err
	}
	for _, pl := range places {
		if err := s.PlaceMember(pl.household, existingID, pl.role, pl.address, by); err != nil {
			return photo, err
		}
	}
	return photo, nil
}

// ApproveDifferent is the staff answer "Different people": the held entry
// is let in and every person it matched is recorded as a different person
// (not_same_people) so they aren't flagged again. goesBy and suffix are
// optional ways to tell them apart; empty leaves the entry as it is.
func (s *Store) ApproveDifferent(heldID int64, goesBy, suffix string, by int64) error {
	p, err := s.GetPerson(heldID)
	if err != nil {
		return err
	}
	if !p.Pending {
		return ErrNotPending
	}
	matches, err := s.NameMatches(p.FirstName, p.LastName, p.GoesBy, p.Suffix, p.ID)
	if err != nil {
		return err
	}
	goesBy, suffix = strings.TrimSpace(goesBy), strings.TrimSpace(suffix)
	return s.inTx(func(tx *sql.Tx) error {
		if goesBy != "" || suffix != "" {
			if goesBy == "" {
				goesBy = p.GoesBy
			}
			if suffix == "" {
				suffix = p.Suffix
			}
			if _, err := tx.Exec(`UPDATE people SET goes_by = ?, suffix = ? WHERE id = ?`, goesBy, suffix, heldID); err != nil {
				return err
			}
		}
		for _, m := range matches {
			if err := recordPair(tx, heldID, m.ID, by); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE people SET pending = 0, pending_reason = '', updated_at = datetime('now') WHERE id = ?`, heldID); err != nil {
			return err
		}
		return logChange(tx, by, heldID, 0, "approved: different people")
	})
}

// ApproveRename applies a held rename and records the people it matched as
// different people.
func (s *Store) ApproveRename(personID, by int64) error {
	p, err := s.GetPerson(personID)
	if err != nil {
		return err
	}
	var n pendingName
	if p.PendingName == "" || json.Unmarshal([]byte(p.PendingName), &n) != nil {
		return ErrNotPending
	}
	matches, err := s.NameMatches(n.First, n.Last, n.GoesBy, n.Suffix, personID)
	if err != nil {
		return err
	}
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE people SET first_name = ?, last_name = ?, goes_by = ?, suffix = ?, pending_name = '', updated_at = datetime('now') WHERE id = ?`,
			n.First, n.Last, n.GoesBy, n.Suffix, personID); err != nil {
			return err
		}
		for _, m := range matches {
			if err := recordPair(tx, personID, m.ID, by); err != nil {
				return err
			}
		}
		return logChange(tx, by, personID, 0, "approved name change")
	})
}

// RejectRename throws a held rename away (the old name stays) and leaves a
// note for the user who asked.
func (s *Store) RejectRename(personID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, personID)
		if err != nil {
			return err
		}
		if p.PendingName == "" {
			return ErrNotPending
		}
		var n pendingName
		_ = json.Unmarshal([]byte(p.PendingName), &n)
		if _, err := tx.Exec(`UPDATE people SET pending_name = '' WHERE id = ?`, personID); err != nil {
			return err
		}
		if err := addNotice(tx, n.By, "The office didn't accept the name change for "+p.Name()+"."); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "turned down name change")
	})
}
