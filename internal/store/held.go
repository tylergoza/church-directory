package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Self sign-up, held name matches and Needs action (PLAN.md "Same name as
// someone already here" and "Linking profiles to users").
//
// For step 5 (heads of household adding and renaming people) the pieces to
// call are:
//
//	HoldIfMatches  the yes/no: would this name be held?
//	CreateByMember add a person for a user who isn't staff (held when the
//	               name matches), optionally always pending (self sign-up)
//	HoldRename     a rename by someone who isn't staff (held when it matches)
//
// Staff approve everything on /admin/needs-action with the Approve*,
// ResolveSamePerson, ApproveDifferent and TurnDown* functions below.

var (
	// ErrNotPending: the person or household isn't waiting for staff.
	ErrNotPending = errors.New("that isn't waiting for the office")
	// ErrNotYours: a household the user didn't make (or that isn't pending).
	ErrNotYours = errors.New("that isn't your household")
	// ErrHasHousehold: a self sign-up household needs a profile that isn't in one yet.
	ErrHasHousehold = errors.New("you're in a household already")
)

// Pending reasons.
const (
	PendingSelfSignup = "self_signup"
	PendingNameMatch  = "name_match"
)

// HoldIfMatches says whether a name added or changed to by someone should
// be held for staff: only when they aren't staff and the name matches
// someone already in the directory (the one match rule, NameMatches).
// excludeID is the person being renamed, 0 for a new one. The matches are
// returned for the caller's own use but must never be shown to the person.
func (s *Store) HoldIfMatches(first, last, goesBy, suffix string, excludeID int64, staff bool) (held bool, matches []Person, err error) {
	if staff {
		return false, nil, nil
	}
	matches, err = s.NameMatches(first, last, goesBy, suffix, excludeID)
	return len(matches) > 0, matches, err
}

func insertPerson(tx dbtx, in PersonInput, by, userID int64, reason string, attest bool) (int64, error) {
	res, err := tx.Exec(`
		INSERT INTO people (first_name, last_name, goes_by, suffix,
			birth_month, birth_day, birth_year, anniv_month, anniv_day, anniv_year,
			email, phone, kind, created_by, user_id, login_allowed, pending, pending_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.FirstName, in.LastName, in.GoesBy, in.Suffix,
		dateArg(in.Birth.Month), dateArg(in.Birth.Day), dateArg(in.Birth.Year),
		dateArg(in.Anniversary.Month), dateArg(in.Anniversary.Day), dateArg(in.Anniversary.Year),
		in.Email, in.Phone, in.Kind, nullInt(by), nullInt(userID), userID != 0, reason != "", reason)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if attest {
		if err := recordAttest(tx, id, by); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// CreateByMember adds a person for a user who isn't staff (a head adding
// someone, or self sign-up). The input must already pass Validate. The
// person is held with reason name_match when the name matches someone here;
// with alwaysPending (self sign-up) they're pending either way
// (self_signup when nothing matches). reason is "" when they were listed
// at once. The caller shows the person only "Waiting for the office to
// check this.", never the match.
func (s *Store) CreateByMember(in PersonInput, by int64, alwaysPending bool) (id int64, reason string, err error) {
	in.clean()
	held, _, err := s.HoldIfMatches(in.FirstName, in.LastName, in.GoesBy, in.Suffix, 0, false)
	if err != nil {
		return 0, "", err
	}
	switch {
	case held:
		reason = PendingNameMatch
	case alwaysPending:
		reason = PendingSelfSignup
	}
	err = s.inTx(func(tx *sql.Tx) error {
		if id, err = insertPerson(tx, in, by, 0, reason, false); err != nil {
			return err
		}
		what := "added person"
		if reason != "" {
			what += " (waiting for the office)"
		}
		return logChange(tx, by, id, 0, what)
	})
	return id, reason, err
}

// MakeMyProfile is self sign-up: a signed-in user with no profile makes
// theirs. It is always pending (name_match when the name matches someone
// here, else self_signup) and linked to the user at once, so they see it on
// /me. Refused for a known age under 13; with an unknown age attest13 is
// required.
func (s *Store) MakeMyProfile(userID int64, in PersonInput, attest13 bool) (id int64, reason string, err error) {
	in.clean()
	u, err := s.GetUser(userID)
	if err != nil {
		return 0, "", err
	}
	held, _, err := s.HoldIfMatches(in.FirstName, in.LastName, in.GoesBy, in.Suffix, 0, u.IsStaff())
	if err != nil {
		return 0, "", err
	}
	reason = PendingSelfSignup
	if held {
		reason = PendingNameMatch
	}
	p := Person{Birth: in.Birth}
	record, err := checkLoginAge(p, attest13)
	if err != nil {
		return 0, "", err
	}
	err = s.inTx(func(tx *sql.Tx) error {
		var other int64
		switch err := tx.QueryRow(`SELECT id FROM people WHERE user_id = ?`, userID).Scan(&other); {
		case err == nil:
			return ErrUserHasProfile
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if id, err = insertPerson(tx, in, userID, userID, reason, record); err != nil {
			return err
		}
		return logChange(tx, userID, id, 0, "made own profile (waiting for the office)")
	})
	return id, reason, err
}

// CreateSelfHousehold makes a pending household for a user with a profile
// who isn't in a household yet, with their profile as its head.
func (s *Store) CreateSelfHousehold(userID int64, name string) (int64, error) {
	name, _, errs := ValidateHousehold(name, KindMember)
	if errs != nil {
		return 0, errors.New(errs[0])
	}
	me, err := s.PersonByUser(userID)
	if err != nil {
		return 0, err
	}
	hh, err := s.PersonHouseholds(me.ID)
	if err != nil {
		return 0, err
	}
	if len(hh) > 0 {
		return 0, ErrHasHousehold
	}
	var id int64
	err = s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO households (name, pending, created_by) VALUES (?, 1, ?)`, name, userID)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO household_members (household_id, person_id, role, position) VALUES (?, ?, 'head', 1)`, id, me.ID); err != nil {
			return err
		}
		return logChange(tx, userID, me.ID, id, "made own household (waiting for the office)")
	})
	return id, err
}

// AddToSelfHousehold adds a person to a pending household the user made.
// They're pending too (name_match if the name matches, else self_signup).
func (s *Store) AddToSelfHousehold(userID, householdID int64, in PersonInput, role string) (int64, string, error) {
	if !ValidHouseholdRole(role) {
		return 0, "", ErrBadRole
	}
	h, err := s.GetHousehold(householdID)
	if err != nil {
		return 0, "", err
	}
	if !h.Pending || h.CreatedBy != userID {
		return 0, "", ErrNotYours
	}
	id, reason, err := s.CreateByMember(in, userID, true)
	if err != nil {
		return 0, "", err
	}
	if err := s.PlaceMember(householdID, id, role, 0, userID); err != nil {
		return 0, "", err
	}
	return id, reason, nil
}

// HoldRename is for a rename by someone who isn't staff. If the new name
// matches someone else here, it is stored as pending_name (the old name
// stays until staff approve), held is true, and the caller must leave the
// name out of its update. If the name is unchanged or doesn't match, it
// returns false and the caller applies the rename as usual.
func (s *Store) HoldRename(personID int64, first, last, goesBy, suffix string, by int64) (held bool, err error) {
	p, err := s.GetPerson(personID)
	if err != nil {
		return false, err
	}
	first, last, goesBy, suffix = strings.TrimSpace(first), strings.TrimSpace(last), strings.TrimSpace(goesBy), strings.TrimSpace(suffix)
	if p.FirstName == first && p.LastName == last && p.GoesBy == goesBy && p.Suffix == suffix {
		return false, nil
	}
	held, _, err = s.HoldIfMatches(first, last, goesBy, suffix, personID, false)
	if err != nil || !held {
		return false, err
	}
	b, _ := json.Marshal(pendingName{First: first, Last: last, GoesBy: goesBy, Suffix: suffix, By: by})
	return true, s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE people SET pending_name = ? WHERE id = ?`, string(b), personID); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "asked to change name (waiting for the office)")
	})
}

type pendingName struct {
	First  string `json:"first"`
	Last   string `json:"last"`
	GoesBy string `json:"goes_by"`
	Suffix string `json:"suffix"`
	By     int64  `json:"by,omitempty"` // the user who asked, for the turned-down note
}

func (n pendingName) display() string { return DisplayName(n.First, n.Last, n.GoesBy, n.Suffix) }

// Notices ------------------------------------------------------------------

func addNotice(tx dbtx, userID int64, text string) error {
	if userID == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO held_notices (user_id, text) VALUES (?, ?)`, userID, text)
	return err
}

// Notice is a note for a user on their /me page.
type Notice struct {
	ID        int64
	Text      string
	CreatedAt string
}

// UnseenNotices lists a user's notes they haven't read, oldest first.
func (s *Store) UnseenNotices(userID int64) ([]Notice, error) {
	rows, err := s.DB.Query(`SELECT id, text, created_at FROM held_notices WHERE user_id = ? AND seen_at IS NULL ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notice
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.Text, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNoticesSeen marks all of a user's notes read.
func (s *Store) MarkNoticesSeen(userID int64) error {
	_, err := s.DB.Exec(`UPDATE held_notices SET seen_at = datetime('now') WHERE user_id = ? AND seen_at IS NULL`, userID)
	return err
}

// Staff decisions -------------------------------------------------------------

// ApprovePerson lets a pending person in (self sign-up, or a held match
// staff don't need to look at).
func (s *Store) ApprovePerson(id, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE people SET pending = 0, pending_reason = '', updated_at = datetime('now') WHERE id = ? AND pending = 1`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotPending
		}
		return logChange(tx, by, id, 0, "approved")
	})
}

// TurnDownPerson deletes a pending person and leaves a note for the user
// who made them. It returns the photo file name to remove ("" for none).
func (s *Store) TurnDownPerson(id, by int64) (photo string, err error) {
	err = s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, id)
		if err != nil {
			return err
		}
		if !p.Pending {
			return ErrNotPending
		}
		photo = p.Photo
		if err := addNotice(tx, p.CreatedBy, "The office didn't accept the entry for "+p.Name()+"."); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM people WHERE id = ?`, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, 0, "turned down a pending person")
	})
	if err != nil {
		photo = ""
	}
	return photo, err
}

// ApproveHousehold lets a pending household in, with the members who were
// waiting only because of self sign-up. Members held for a name match stay
// held.
func (s *Store) ApproveHousehold(id, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE households SET pending = 0, updated_at = datetime('now') WHERE id = ? AND pending = 1`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotPending
		}
		if _, err := tx.Exec(`UPDATE people SET pending = 0, pending_reason = '', updated_at = datetime('now')
			WHERE pending_reason = 'self_signup' AND id IN (SELECT person_id FROM household_members WHERE household_id = ?)`, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, id, "approved household")
	})
}

// TurnDownHousehold deletes a pending household and its pending members,
// leaving a note for the user who made it. It returns photo file names to
// remove.
func (s *Store) TurnDownHousehold(id, by int64) (photos []string, err error) {
	err = s.inTx(func(tx *sql.Tx) error {
		var name string
		var creator int64
		var pending bool
		if err := tx.QueryRow(`SELECT name, COALESCE(created_by, 0), pending FROM households WHERE id = ?`, id).Scan(&name, &creator, &pending); err != nil {
			return notFound(err)
		}
		if !pending {
			return ErrNotPending
		}
		// Pending members go with it, except the maker's own profile.
		rows, err := tx.Query(`SELECT p.id, p.photo FROM people p JOIN household_members m ON m.person_id = p.id
			WHERE m.household_id = ? AND p.pending = 1 AND (p.user_id IS NULL OR p.user_id <> ?)`, id, creator)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var pid int64
			var photo string
			if err := rows.Scan(&pid, &photo); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, pid)
			if photo != "" {
				photos = append(photos, photo)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, pid := range ids {
			if _, err := tx.Exec(`DELETE FROM people WHERE id = ?`, pid); err != nil {
				return err
			}
		}
		if err := addNotice(tx, creator, "The office didn't accept the household "+name+"."); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM households WHERE id = ?`, id); err != nil {
			return err
		}
		return logChange(tx, by, 0, 0, "turned down a pending household")
	})
	if err != nil {
		photos = nil
	}
	return photos, err
}
