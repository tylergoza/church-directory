package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Heads of household (PLAN.md step 5): the heads_may_edit opt-in and staff
// lock, "Lives in more than one household", join requests, contact
// households. None of these check who is asking; the handlers do, with
// access.CanEditPerson, access.CanHeadEditHousehold and friends.

var (
	// ErrHeadsLocked: staff set "heads may edit" and only staff can change it.
	ErrHeadsLocked = errors.New("the office has set this and only the office can change it")
	// ErrNeedReason: locking needs a short reason.
	ErrNeedReason = errors.New("write a short reason for the lock")
	// ErrNotMulti: a second household needs the "lives in more than one household" tick.
	ErrNotMulti = errors.New("tick \"Lives in more than one household\" on this child first")
	// ErrChildOnly: only children can be in more than one household.
	ErrChildOnly = errors.New("only a child can live in more than one household")
	// ErrBadReason: the reason must be divorced or other.
	ErrBadReason = errors.New("choose a reason: parents divorced or separated, or other")
	// ErrStillMulti: unticking needs the child to be in one household only.
	ErrStillMulti = errors.New("take the child out of the other households first")
	// ErrAlreadyIn: already in that household.
	ErrAlreadyIn = errors.New("they are already in that household")
	// ErrRequestClosed: the join request was answered already.
	ErrRequestClosed = errors.New("that request has been answered already")
	// ErrRequestOpen: there is a request waiting for that household already.
	ErrRequestOpen = errors.New("there is a request waiting for that household already")
)

// Reasons for living in more than one household.
const (
	MultiDivorced = "divorced"
	MultiOther    = "other"
)

// SetHeadsMayEditSelf is the person's own choice, "Let my household's heads
// edit my profile". Refused while staff have locked it (ErrHeadsLocked).
func (s *Store) SetHeadsMayEditSelf(personID int64, on bool, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, personID)
		if err != nil {
			return err
		}
		if p.HeadsMayEditLocked {
			return ErrHeadsLocked
		}
		if p.HeadsMayEdit == on {
			return nil
		}
		if _, err := tx.Exec(`UPDATE people SET heads_may_edit = ?, updated_at = datetime('now') WHERE id = ?`, on, personID); err != nil {
			return err
		}
		what := "let heads edit their profile"
		if !on {
			what = "stopped heads editing their profile"
		}
		return logChange(tx, by, personID, 0, what)
	})
}

// StaffSetHeadsMayEdit is staff's setting: on or off, and optionally locked
// so only staff can change it again. Locking needs a reason.
func (s *Store) StaffSetHeadsMayEdit(personID int64, on, lock bool, reason string, by int64) error {
	reason = strings.TrimSpace(reason)
	if lock && reason == "" {
		return ErrNeedReason
	}
	if len(reason) > 300 {
		reason = reason[:300]
	}
	if !lock {
		reason = ""
	}
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := loadPerson(tx, personID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE people SET heads_may_edit = ?, heads_may_edit_locked = ?,
			heads_may_edit_locked_by = ?, heads_may_edit_reason = ?, updated_at = datetime('now') WHERE id = ?`,
			on, lock, nullIf(lock, by), reason, personID); err != nil {
			return err
		}
		what := "staff set heads may edit " + onOff(on)
		if lock {
			what += " and locked it"
		}
		return logChange(tx, by, personID, 0, what)
	})
}

func nullIf(cond bool, v int64) any {
	if !cond || v == 0 {
		return nil
	}
	return v
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// isChildSomewhere: the person has role child in at least one household.
func isChildSomewhere(tx dbtx, personID int64) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM household_members WHERE person_id = ? AND role = 'child'`, personID).Scan(&n)
	return n > 0, err
}

// SetMultiHousehold ticks or unticks "Lives in more than one household".
// On needs a reason (divorced or other) and a child. The child's current
// household becomes primary if none is. Off is refused while the child is
// in more than one household.
func (s *Store) SetMultiHousehold(personID int64, on bool, reason string, by int64) error {
	if on && reason != MultiDivorced && reason != MultiOther {
		return ErrBadReason
	}
	return s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, personID)
		if err != nil {
			return err
		}
		if on {
			child, err := isChildSomewhere(tx, personID)
			if err != nil {
				return err
			}
			if !child {
				return ErrChildOnly
			}
			if _, err := tx.Exec(`UPDATE people SET multi_household = 1, multi_reason = ?, updated_at = datetime('now') WHERE id = ?`, reason, personID); err != nil {
				return err
			}
			var primaries int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM household_members WHERE person_id = ? AND is_primary = 1`, personID).Scan(&primaries); err != nil {
				return err
			}
			if primaries == 0 {
				if _, err := tx.Exec(`UPDATE household_members SET is_primary = 1 WHERE person_id = ? AND household_id =
					(SELECT household_id FROM household_members WHERE person_id = ? ORDER BY position, household_id LIMIT 1)`, personID, personID); err != nil {
					return err
				}
			}
			return logChange(tx, by, personID, 0, "ticked lives in more than one household")
		}
		if !p.MultiHousehold {
			return nil
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM household_members WHERE person_id = ?`, personID).Scan(&n); err != nil {
			return err
		}
		if n > 1 {
			return ErrStillMulti
		}
		if _, err := tx.Exec(`UPDATE people SET multi_household = 0, multi_reason = '', updated_at = datetime('now') WHERE id = ?`, personID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE household_members SET is_primary = 0 WHERE person_id = ?`, personID); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "unticked lives in more than one household")
	})
}

// SetPrimaryHousehold marks which of the person's households is primary:
// where they're sorted on their own and in the printed index. One per person.
func (s *Store) SetPrimaryHousehold(personID, householdID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM household_members WHERE person_id = ? AND household_id = ?`, personID, householdID).Scan(&one); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE household_members SET is_primary = 0 WHERE person_id = ?`, personID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE household_members SET is_primary = 1 WHERE person_id = ? AND household_id = ?`, personID, householdID); err != nil {
			return err
		}
		return logChange(tx, by, personID, householdID, "set primary household")
	})
}

// AddSecondHousehold adds a child who lives in more than one household to
// another household (role child only). Refused without the tick.
func (s *Store) AddSecondHousehold(householdID, personID, addressID, by int64) error {
	p, err := s.GetPerson(personID)
	if err != nil {
		return err
	}
	if !p.MultiHousehold {
		return ErrNotMulti
	}
	hh, err := s.PersonHouseholds(personID)
	if err != nil {
		return err
	}
	isChild := false
	for _, h := range hh {
		if h.HouseholdID == householdID {
			return ErrAlreadyIn
		}
		if h.Role == RoleChild {
			isChild = true
		}
	}
	if !isChild {
		return ErrChildOnly
	}
	return s.PlaceMember(householdID, personID, RoleChild, addressID, by)
}

// Join requests --------------------------------------------------------------

// JoinRequest is a head's ask to add a child to a household someone else heads.
type JoinRequest struct {
	ID            int64
	PersonID      int64
	HouseholdID   int64
	AskedBy       int64
	AskedAt       string
	PersonName    string
	HouseholdName string
	AskedByName   string
}

const joinCols = `r.id, r.person_id, r.household_id, COALESCE(r.asked_by, 0), r.asked_at,
	p.first_name, p.last_name, p.goes_by, p.suffix, h.name`

func (s *Store) joinRequests(where string, args ...any) ([]JoinRequest, error) {
	rows, err := s.DB.Query(`SELECT `+joinCols+` FROM household_requests r
		JOIN people p ON p.id = r.person_id JOIN households h ON h.id = r.household_id
		WHERE `+where+` ORDER BY r.asked_at, r.id`, args...)
	if err != nil {
		return nil, err
	}
	var out []JoinRequest
	for rows.Next() {
		var r JoinRequest
		var first, last, goes, suffix string
		if err := rows.Scan(&r.ID, &r.PersonID, &r.HouseholdID, &r.AskedBy, &r.AskedAt, &first, &last, &goes, &suffix, &r.HouseholdName); err != nil {
			rows.Close()
			return nil, err
		}
		r.PersonName = DisplayName(first, last, goes, suffix)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].AskedByName = s.userName(out[i].AskedBy)
	}
	return out, nil
}

// OpenJoinRequests lists every request waiting for an answer (staff's view).
func (s *Store) OpenJoinRequests() ([]JoinRequest, error) {
	return s.joinRequests(`r.answered_at IS NULL`)
}

// GetJoinRequest loads one request, answered or not.
func (s *Store) GetJoinRequest(id int64) (JoinRequest, error) {
	rs, err := s.joinRequests(`r.id = ?`, id)
	if err != nil {
		return JoinRequest{}, err
	}
	if len(rs) == 0 {
		return JoinRequest{}, ErrNotFound
	}
	return rs[0], nil
}

// JoinRequestsAskedBy lists the open requests a user sent.
func (s *Store) JoinRequestsAskedBy(userID int64) ([]JoinRequest, error) {
	return s.joinRequests(`r.answered_at IS NULL AND r.asked_by = ?`, userID)
}

// CreateJoinRequest asks to add a child (who lives in more than one
// household) to a household the asker doesn't head.
func (s *Store) CreateJoinRequest(personID, householdID, by int64) (int64, error) {
	p, err := s.GetPerson(personID)
	if err != nil {
		return 0, err
	}
	if _, err := s.GetHousehold(householdID); err != nil {
		return 0, err
	}
	if !p.MultiHousehold {
		return 0, ErrNotMulti
	}
	hh, err := s.PersonHouseholds(personID)
	if err != nil {
		return 0, err
	}
	child := false
	for _, h := range hh {
		if h.HouseholdID == householdID {
			return 0, ErrAlreadyIn
		}
		child = child || h.Role == RoleChild
	}
	if !child {
		return 0, ErrChildOnly
	}
	var id int64
	err = s.inTx(func(tx *sql.Tx) error {
		var open int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM household_requests WHERE person_id = ? AND household_id = ? AND answered_at IS NULL`, personID, householdID).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return ErrRequestOpen
		}
		res, err := tx.Exec(`INSERT INTO household_requests (person_id, household_id, asked_by) VALUES (?, ?, ?)`, personID, householdID, nullInt(by))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, personID, householdID, "asked to join a household")
	})
	return id, err
}

// AnswerJoinRequest accepts (the child is added to the household) or turns
// down a request. The one who asked gets a note on /me either way. The
// caller has checked that the answerer is a head of the household or staff.
func (s *Store) AnswerJoinRequest(id int64, accept bool, by int64) error {
	r, err := s.GetJoinRequest(id)
	if err != nil {
		return err
	}
	var open int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM household_requests WHERE id = ? AND answered_at IS NULL`, id).Scan(&open); err != nil {
		return err
	}
	if open == 0 {
		return ErrRequestClosed
	}
	if accept {
		if err := s.AddSecondHousehold(r.HouseholdID, r.PersonID, 0, by); err != nil && !errors.Is(err, ErrAlreadyIn) {
			return err
		}
	}
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE household_requests SET answered_by = ?, answered_at = datetime('now'), accepted = ? WHERE id = ?`,
			nullInt(by), accept, id); err != nil {
			return err
		}
		verb, text := "turned down", " was turned down."
		if accept {
			verb, text = "accepted", " was accepted."
		}
		if err := addNotice(tx, r.AskedBy, "Your request to add "+r.PersonName+" to "+r.HouseholdName+text); err != nil {
			return err
		}
		return logChange(tx, by, r.PersonID, r.HouseholdID, verb+" a join request")
	})
}

// Contact households ------------------------------------------------------------

// CreateManagedHousehold makes a household that managedBy (a person)
// edits until a head of it has a login: a contact household (kind
// KindContact, never listed or printed) for a child's other parent.
func (s *Store) CreateManagedHousehold(name, kind string, managedBy, by int64) (int64, error) {
	name, kind, errs := ValidateHousehold(name, kind)
	if errs != nil {
		return 0, errors.New(errs[0])
	}
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		if _, err := loadPerson(tx, managedBy); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO households (name, kind, managed_by) VALUES (?, ?, ?)`, name, kind, managedBy)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, managedBy, id, "made a "+kind+" household")
	})
	return id, err
}

// ConvertContact is staff turning a contact household and one of its people
// into members: the household and the person become kind member and the
// person its head. managed_by is cleared, which ends the head who made it
// editing it.
func (s *Store) ConvertContact(householdID, personID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&one); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(`UPDATE households SET kind = 'member', managed_by = NULL, updated_at = datetime('now') WHERE id = ?`, householdID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE people SET kind = 'member', updated_at = datetime('now') WHERE id = ?`, personID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE household_members SET role = 'head' WHERE household_id = ? AND person_id = ?`, householdID, personID); err != nil {
			return err
		}
		return logChange(tx, by, personID, householdID, "turned a contact household into a member household")
	})
}

// ClearHeldRename drops a held rename that has been replaced by a newer
// edit that was not held, so staff aren't asked about a name no one wants.
func (s *Store) ClearHeldRename(personID int64) error {
	_, err := s.DB.Exec(`UPDATE people SET pending_name = '' WHERE id = ? AND pending_name <> ''`, personID)
	return err
}
