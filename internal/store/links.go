package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Linking profiles to users (PLAN.md "Linking profiles to users" and
// "Children under 13"). A person is a profile, a user is a login; the link
// is people.user_id, and one user has at most one profile.

// Under13Warning is shown wherever "Link to user" or "Can have their own
// login" appears.
const Under13Warning = "Children under 13 can't have their own login. If we learn that someone under 13 has one, it will be turned off and may be deleted."

var (
	// ErrUnder13: the person's known age is under 13.
	ErrUnder13 = errors.New("children under 13 can't have their own login")
	// ErrNeedsAttest: the age is unknown and "This person is 13 or older" wasn't ticked.
	ErrNeedsAttest = errors.New("tick \"This person is 13 or older\" first")
	// ErrUserHasProfile: the user already has a profile.
	ErrUserHasProfile = errors.New("that user already has a profile")
	// ErrAlreadyLinked: the person already has a user (unlink first).
	ErrAlreadyLinked = errors.New("that person is linked to a user already")
	// ErrNoLogin: a claim link needs "Can have their own login" first.
	ErrNoLogin = errors.New("turn on \"Can have their own login\" first")
	// ErrClaimInvalid: the claim link is unknown, used or expired.
	ErrClaimInvalid = errors.New("that link doesn't work: it's used, expired or wrong")
)

// now is the clock; tests may replace it.
var now = time.Now

// ClaimTTL is how long a claim link works.
const ClaimTTL = "+14 days"

// checkLoginAge applies the under-13 rules for turning a login on. It
// returns whether the tick should be recorded as the person's attestation.
func checkLoginAge(p Person, attest bool) (record bool, err error) {
	if KnownUnder13(p.Birth, now()) {
		return false, ErrUnder13
	}
	if AgeKnown(p.Birth) {
		return false, nil
	}
	if !attest && p.AgeAttestedBy == 0 {
		return false, ErrNeedsAttest
	}
	return attest, nil
}

func recordAttest(tx dbtx, personID, by int64) error {
	_, err := tx.Exec(`UPDATE people SET age_attested_by = ?, age_attested_at = datetime('now') WHERE id = ?`, nullInt(by), personID)
	return err
}

func loadPerson(tx dbtx, id int64) (Person, error) {
	p, err := scanPerson(tx.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, id))
	return p, notFound(err)
}

// LinkUser links a person to a user (staff). One profile per user; refused
// when the person's known age is under 13; with an unknown age attest13
// (the "This person is 13 or older" tick) is required, and who ticked it
// and when is recorded. A linked person can have a login, so
// login_allowed is turned on. Fields staff set (visibility overrides) stay
// as they are.
func (s *Store) LinkUser(personID, userID, by int64, attest13 bool) error {
	return s.inTx(func(tx *sql.Tx) error { return linkUser(tx, personID, userID, by, attest13, "linked to user") })
}

func linkUser(tx dbtx, personID, userID, by int64, attest13 bool, what string) error {
	p, err := loadPerson(tx, personID)
	if err != nil {
		return err
	}
	if p.UserID != 0 {
		return ErrAlreadyLinked
	}
	var one int64
	if err := tx.QueryRow(`SELECT id FROM users WHERE id = ?`, userID).Scan(&one); err != nil {
		return notFound(err)
	}
	var other int64
	switch err := tx.QueryRow(`SELECT id FROM people WHERE user_id = ?`, userID).Scan(&other); {
	case err == nil:
		return ErrUserHasProfile
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	record, err := checkLoginAge(p, attest13)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE people SET user_id = ?, login_allowed = 1, updated_at = datetime('now') WHERE id = ?`, userID, personID); err != nil {
		return err
	}
	if err := settleManagedByPerson(tx, personID, by); err != nil {
		return err
	}
	if record {
		if err := recordAttest(tx, personID, by); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE claims SET used_at = datetime('now') WHERE person_id = ? AND used_at IS NULL`, personID); err != nil {
		return err
	}
	return logChange(tx, by, personID, 0, what)
}

// UnlinkUser removes the link: the profile stays, the user stays.
func (s *Store) UnlinkUser(personID, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, personID)
		if err != nil {
			return err
		}
		if p.UserID == 0 {
			return nil
		}
		if _, err := tx.Exec(`UPDATE people SET user_id = NULL, login_allowed = 0, updated_at = datetime('now') WHERE id = ?`, personID); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "unlinked from user")
	})
}

// SetLoginAllowed sets "Can have their own login". Turning it on follows
// the same age rules as LinkUser. Turning it off revokes unused claim links
// and is refused while a user is linked (unlink first).
//
// It does no permission check: the staff handler (and, in step 5, the
// heads-of-household handler) decide who may call it.
func (s *Store) SetLoginAllowed(personID int64, allowed, attest13 bool, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, personID)
		if err != nil {
			return err
		}
		if allowed == p.LoginAllowed {
			return nil
		}
		if allowed {
			record, err := checkLoginAge(p, attest13)
			if err != nil {
				return err
			}
			if record {
				if err := recordAttest(tx, personID, by); err != nil {
					return err
				}
			}
		} else {
			if p.UserID != 0 {
				return ErrAlreadyLinked
			}
			if _, err := tx.Exec(`UPDATE claims SET used_at = datetime('now') WHERE person_id = ? AND used_at IS NULL`, personID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE people SET login_allowed = ?, updated_at = datetime('now') WHERE id = ?`, allowed, personID); err != nil {
			return err
		}
		what := "turned on can have their own login"
		if !allowed {
			what = "turned off can have their own login"
		}
		return logChange(tx, by, personID, 0, what)
	})
}

// Claim links ------------------------------------------------------------

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// CreateClaim makes a one-time link code for a person (valid 14 days) and
// returns the code. It is the only time the code exists in the clear: only
// its SHA-256 hash is stored. The person needs "Can have their own login",
// no user yet, and no known age under 13. An earlier unused code for the
// same person stops working. No permission check (see SetLoginAllowed).
func (s *Store) CreateClaim(personID, by int64) (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(buf)
	err := s.inTx(func(tx *sql.Tx) error {
		p, err := loadPerson(tx, personID)
		if err != nil {
			return err
		}
		if p.UserID != 0 {
			return ErrAlreadyLinked
		}
		if KnownUnder13(p.Birth, now()) {
			return ErrUnder13
		}
		if !p.LoginAllowed {
			return ErrNoLogin
		}
		if _, err := tx.Exec(`UPDATE claims SET used_at = datetime('now') WHERE person_id = ? AND used_at IS NULL`, personID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO claims (code_hash, person_id, made_by, expires_at) VALUES (?, ?, ?, datetime('now', ?))`,
			hashCode(code), personID, nullInt(by), ClaimTTL); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "made a claim link")
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// claimPerson finds the person a live code is for.
func claimPerson(tx dbtx, code string) (Person, error) {
	var pid int64
	err := tx.QueryRow(`SELECT person_id FROM claims WHERE code_hash = ? AND used_at IS NULL AND expires_at > datetime('now')`, hashCode(code)).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrClaimInvalid
	}
	if err != nil {
		return Person{}, err
	}
	return loadPerson(tx, pid)
}

// ClaimPreview checks a code without using it, for the confirm page. The
// user must have no profile yet. It returns only the person's name parts
// for the page; ErrClaimInvalid covers unknown, used and expired codes.
func (s *Store) ClaimPreview(code string, userID int64) (Person, error) {
	var p Person
	err := s.inTx(func(tx *sql.Tx) error {
		var err error
		if p, err = checkClaim(tx, code, userID); err != nil {
			return err
		}
		return nil
	})
	return p, err
}

func checkClaim(tx dbtx, code string, userID int64) (Person, error) {
	p, err := claimPerson(tx, code)
	if err != nil {
		return p, err
	}
	var other int64
	switch err := tx.QueryRow(`SELECT id FROM people WHERE user_id = ?`, userID).Scan(&other); {
	case err == nil:
		return p, ErrUserHasProfile
	case !errors.Is(err, sql.ErrNoRows):
		return p, err
	}
	if p.UserID != 0 {
		return p, ErrClaimInvalid
	}
	if KnownUnder13(p.Birth, now()) {
		return p, ErrUnder13
	}
	if !p.LoginAllowed {
		return p, ErrClaimInvalid
	}
	return p, nil
}

// AcceptClaim links the user to the code's profile and uses the code up.
// Refused when the code is unknown, used or expired, the profile is known
// to be under 13, or the user already has a profile.
func (s *Store) AcceptClaim(code string, userID int64) (Person, error) {
	var p Person
	err := s.inTx(func(tx *sql.Tx) error {
		var err error
		if p, err = checkClaim(tx, code, userID); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE claims SET used_at = datetime('now') WHERE code_hash = ? AND used_at IS NULL`, hashCode(code))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrClaimInvalid
		}
		if _, err := tx.Exec(`UPDATE people SET user_id = ?, updated_at = datetime('now') WHERE id = ?`, userID, p.ID); err != nil {
			return err
		}
		if err := settleManagedByPerson(tx, p.ID, userID); err != nil {
			return err
		}
		p.UserID = userID
		return logChange(tx, userID, p.ID, 0, "claimed profile")
	})
	return p, err
}

// Found-later under-13 ---------------------------------------------------

// enforceUnder13 is the check every birthday save runs (UpdatePerson, and
// any other store function that changes a birth year must call it in its
// own transaction): when the person is now known to be under 13, a linked
// user is unlinked at once and login_allowed goes off with any open claim
// link. A local user is set inactive (and signed out). An SSO user can't be
// switched off from here, so an item goes on Needs action (under13_items)
// until staff suspend their User Management access; the server turns that
// into the PATCH, then calls ResolveUnder13.
func enforceUnder13(tx dbtx, personID, by int64) error {
	p, err := loadPerson(tx, personID)
	if err != nil {
		return err
	}
	if !KnownUnder13(p.Birth, now()) || (p.UserID == 0 && !p.LoginAllowed) {
		return nil
	}
	if _, err := tx.Exec(`UPDATE people SET user_id = NULL, login_allowed = 0 WHERE id = ?`, personID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE claims SET used_at = datetime('now') WHERE person_id = ? AND used_at IS NULL`, personID); err != nil {
		return err
	}
	if p.UserID != 0 {
		var sso string
		if err := tx.QueryRow(`SELECT COALESCE(sso_subject, '') FROM users WHERE id = ?`, p.UserID).Scan(&sso); err != nil {
			return err
		}
		if sso == "" {
			if _, err := tx.Exec(`UPDATE users SET active = 0 WHERE id = ?`, p.UserID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, p.UserID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(`INSERT INTO under13_items (user_id, person_id) VALUES (?, ?)`, p.UserID, personID); err != nil {
			return err
		}
	}
	return logChange(tx, by, personID, 0, "unlinked login: known to be under 13")
}

// Under13Item is an SSO login to switch off: the profile was found to be
// under 13 and unlinked, but the person's User Management access remains.
type Under13Item struct {
	ID         int64
	UserID     int64
	SSOSubject string // the User Management user ID to suspend
	UserName   string
	PersonID   int64 // 0 if the profile was deleted
	PersonName string
	CreatedAt  string
}

// Under13Items lists the open items, oldest first.
func (s *Store) Under13Items() ([]Under13Item, error) {
	rows, err := s.DB.Query(`
		SELECT i.id, i.user_id, COALESCE(u.sso_subject, ''), COALESCE(NULLIF(u.display_name, ''), u.username),
		       COALESCE(i.person_id, 0), COALESCE(p.first_name, ''), COALESCE(p.last_name, ''), COALESCE(p.goes_by, ''), COALESCE(p.suffix, ''), i.created_at
		FROM under13_items i JOIN users u ON u.id = i.user_id LEFT JOIN people p ON p.id = i.person_id
		WHERE i.resolved_at IS NULL ORDER BY i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Under13Item
	for rows.Next() {
		var it Under13Item
		var f, l, g, sfx string
		if err := rows.Scan(&it.ID, &it.UserID, &it.SSOSubject, &it.UserName, &it.PersonID, &f, &l, &g, &sfx, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.PersonName = DisplayName(f, l, g, sfx)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ResolveUnder13 closes an item once the user's access has been suspended.
func (s *Store) ResolveUnder13(id, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE under13_items SET resolved_at = datetime('now'), resolved_by = ? WHERE id = ? AND resolved_at IS NULL`, nullInt(by), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, 0, 0, "resolved an under-13 login item")
	})
}

// Users to pick from ---------------------------------------------------------

// UsersWithoutProfile lists the active users who have no profile, for the
// Link to user picker.
func (s *Store) UsersWithoutProfile() ([]User, error) {
	return s.listUsers(`SELECT ` + userCols + ` FROM users WHERE active = 1 AND id NOT IN (SELECT user_id FROM people WHERE user_id IS NOT NULL) ORDER BY username`)
}

// SubjectsWithProfile is the set of User Management user IDs (sso_subject)
// whose cached user already has a profile, for the Link to user picker
// with single sign-on on.
func (s *Store) SubjectsWithProfile() (map[string]bool, error) {
	rows, err := s.DB.Query(`SELECT u.sso_subject FROM users u JOIN people p ON p.user_id = u.id WHERE u.sso_subject IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, err
		}
		out[sub] = true
	}
	return out, rows.Err()
}

// Under13ItemFor returns an open item by ID.
func (s *Store) Under13ItemFor(id int64) (Under13Item, error) {
	items, err := s.Under13Items()
	if err != nil {
		return Under13Item{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return Under13Item{}, ErrNotFound
}
