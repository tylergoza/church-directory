package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/secrets"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrationsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSetting("site_name", "Grace")
	st.Close()
	// Opening again runs nothing twice and keeps the data and the key.
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Setting("site_name", "") != "Grace" {
		t.Error("setting lost on reopen")
	}
}

func TestRoles(t *testing.T) {
	cases := map[string]string{"admin": RoleAdmin, "editor": RoleEditor, "member": RoleMember, "": RoleMember, "user": RoleMember, "planner": RoleMember}
	for in, want := range cases {
		if got := NormalizeRole(in); got != want {
			t.Errorf("NormalizeRole(%q) = %q, want %q", in, got, want)
		}
	}
	if RoleFromSSO(" editor ") != RoleEditor {
		t.Error("RoleFromSSO should trim")
	}
	member, editor, admin := &User{Role: RoleMember}, &User{Role: RoleEditor}, &User{Role: RoleAdmin}
	if member.IsStaff() || !editor.IsStaff() || !admin.IsStaff() {
		t.Error("IsStaff")
	}
	if member.IsAdmin() || editor.IsAdmin() || !admin.IsAdmin() {
		t.Error("IsAdmin")
	}
	var nobody *User
	if nobody.IsStaff() || nobody.IsAdmin() {
		t.Error("nil user has no access")
	}

	st := openTest(t)
	id, err := st.CreateUser("sam", "Sam", "a long password", RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := st.GetUser(id); u.Role != RoleEditor {
		t.Errorf("role = %q", u.Role)
	}
	if _, err := st.DB.Exec(`UPDATE users SET role = 'planner' WHERE id = ?`, id); err == nil {
		t.Error("the database should refuse an unknown role")
	}
}

func TestSecretSettings(t *testing.T) {
	st := openTest(t)
	if err := st.SetSecretSetting("tracker_token", "mt_abc"); err != nil {
		t.Fatal(err)
	}
	if raw := st.Setting("tracker_token", ""); !secrets.IsSealed(raw) {
		t.Errorf("stored %q, want sealed", raw)
	}
	if v, err := st.SecretSetting("tracker_token"); err != nil || v != "mt_abc" {
		t.Errorf("SecretSetting = %q, %v", v, err)
	}
	st.SetSecretSetting("tracker_token", "")
	if v, _ := st.SecretSetting("tracker_token"); v != "" {
		t.Errorf("cleared secret = %q", v)
	}
	st.SetSetting("other_token", "plain")
	st.SealPlainSecrets("other_token", "missing")
	if raw := st.Setting("other_token", ""); !secrets.IsSealed(raw) {
		t.Error("plain secret not sealed")
	}
	if st.Setting("missing", "x") != "x" {
		t.Error("sealing should skip unset keys")
	}
}

// The schema's checks keep the directory's rules where the app can't forget them.
func TestSchemaConstraints(t *testing.T) {
	st := openTest(t)
	exec := func(q string, args ...any) error { _, err := st.DB.Exec(q, args...); return err }
	count := func(q string, args ...any) int {
		var n int
		if err := st.DB.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	person := func(first, last string) int64 {
		t.Helper()
		res, err := st.DB.Exec(`INSERT INTO people (first_name, last_name) VALUES (?, ?)`, first, last)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}

	// Defaults are seeded for both audiences: 11 fields each.
	if n := count(`SELECT COUNT(*) FROM defaults WHERE audience = 'adult'`); n != 11 {
		t.Errorf("adult defaults = %d, want 11", n)
	}
	if n := count(`SELECT COUNT(*) FROM defaults WHERE audience = 'child'`); n != 11 {
		t.Errorf("child defaults = %d, want 11", n)
	}
	if n := count(`SELECT COUNT(*) FROM defaults WHERE audience = 'adult' AND shown = 1 AND field = 'birthday.year'`); n != 0 {
		t.Error("an adult's birth year should start hidden")
	}

	// Partial dates: a day needs a month.
	if err := exec(`INSERT INTO people (first_name, last_name, birth_month, birth_day) VALUES ('A', 'B', 2, 29)`); err != nil {
		t.Errorf("month and day: %v", err)
	}
	if exec(`INSERT INTO people (first_name, last_name, birth_day) VALUES ('A', 'B', 14)`) == nil {
		t.Error("a birth day with no month should be refused")
	}
	if exec(`INSERT INTO people (first_name, last_name, anniv_day) VALUES ('A', 'B', 14)`) == nil {
		t.Error("an anniversary day with no month should be refused")
	}
	if exec(`INSERT INTO people (first_name, last_name, kind) VALUES ('A', 'B', 'guest')`) == nil {
		t.Error("unknown kind should be refused")
	}
	if exec(`INSERT INTO people (first_name, last_name, pending) VALUES ('A', 'B', 1)`) == nil {
		t.Error("pending needs a reason")
	}
	if exec(`INSERT INTO people (first_name, last_name, multi_reason) VALUES ('A', 'B', 'other')`) == nil {
		t.Error("a multi-household reason needs the tick")
	}

	// One household per adult; a child may have two when marked.
	dad, kid := person("Dad", "Smith"), person("Kid", "Smith")
	exec(`INSERT INTO households (id, name) VALUES (1, 'Smiths'), (2, 'Joneses')`)
	exec(`INSERT INTO household_addresses (id, household_id, label) VALUES (1, 1, 'Main'), (2, 2, 'Other')`)
	if err := exec(`INSERT INTO household_members (household_id, person_id, role, address_id) VALUES (1, ?, 'head', 1)`, dad); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO household_members (household_id, person_id, role) VALUES (2, ?, 'adult')`, dad) == nil {
		t.Error("an adult in two households should be refused")
	}
	if exec(`INSERT INTO household_members (household_id, person_id, role, address_id) VALUES (1, ?, 'child', 2)`, kid) == nil {
		t.Error("another household's address should be refused")
	}
	if err := exec(`INSERT INTO household_members (household_id, person_id, role, address_id, is_primary) VALUES (1, ?, 'child', 1, 1), (2, ?, 'child', 2, 0)`, kid, kid); err != nil {
		t.Errorf("a child in two households: %v", err)
	}
	if exec(`UPDATE household_members SET is_primary = 1 WHERE household_id = 2 AND person_id = ?`, kid) == nil {
		t.Error("two primary households should be refused")
	}

	// Relationships: one row per couple (low id first), no self links.
	lo, hi := min(dad, kid), max(dad, kid)
	if exec(`INSERT INTO relationships (person_id, related_id, kind) VALUES (?, ?, 'spouse')`, hi, lo) == nil {
		t.Error("spouse rows are stored low id first")
	}
	if err := exec(`INSERT INTO relationships (person_id, related_id, kind) VALUES (?, ?, 'spouse')`, lo, hi); err != nil {
		t.Errorf("spouse: %v", err)
	}
	exec(`DELETE FROM relationships`)
	if exec(`INSERT INTO relationships (person_id, related_id, kind) VALUES (?, ?, 'parent')`, dad, dad) == nil {
		t.Error("a person can't be their own parent")
	}
	if err := exec(`INSERT INTO relationships (person_id, related_id, kind) VALUES (?, ?, 'parent')`, dad, kid); err != nil {
		t.Errorf("parent: %v", err)
	}
	if exec(`INSERT INTO not_same_people (person_a, person_b) VALUES (?, ?)`, hi, lo) == nil {
		t.Error("not_same_people is stored low id first")
	}

	// Deleting a person takes their notes, settings and memberships with
	// them, and blanks the audit trail instead of deleting it.
	exec(`INSERT INTO notes (person_id, kind, body) VALUES (?, 'allergy', 'peanuts')`, kid)
	exec(`INSERT INTO field_settings (person_id, field, shown) VALUES (?, 'email', 1)`, kid)
	exec(`INSERT INTO changes (person_id, what) VALUES (?, 'edited profile')`, kid)
	if err := exec(`DELETE FROM people WHERE id = ?`, kid); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM notes`,
		`SELECT COUNT(*) FROM field_settings`,
		`SELECT COUNT(*) FROM household_members WHERE person_id = ` + itoa(kid),
		`SELECT COUNT(*) FROM relationships WHERE related_id = ` + itoa(kid),
	} {
		if n := count(q); n != 0 {
			t.Errorf("%s = %d after the person was deleted, want 0", q, n)
		}
	}
	if n := count(`SELECT COUNT(*) FROM changes WHERE person_id IS NULL AND what = 'edited profile'`); n != 1 {
		t.Error("the audit trail should keep its row with the person blanked")
	}

	// Deleting a user keeps the profile linked to them.
	uid, _ := st.CreateUser("pat", "", "a long password", RoleMember)
	exec(`UPDATE people SET user_id = ? WHERE id = ?`, uid, dad)
	st.DeleteUser(uid)
	if n := count(`SELECT COUNT(*) FROM people WHERE id = ? AND user_id IS NULL`, dad); n != 1 {
		t.Error("deleting a user should unlink, not delete, the profile")
	}
}

func TestTokens(t *testing.T) {
	st := openTest(t)
	tok, err := st.CreateToken("Event planner", TokenApp, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 20 || tok[:3] != "md_" {
		t.Errorf("token = %q, want md_ prefix", tok)
	}
	got, err := st.AuthenticateToken(tok)
	if err != nil || got.Name != "Event planner" || got.Kind != TokenApp {
		t.Errorf("AuthenticateToken = %+v, %v", got, err)
	}
	if _, err := st.AuthenticateToken("md_nope"); err != ErrNotFound {
		t.Errorf("unknown token: %v", err)
	}
	if _, err := st.AuthenticateToken("xx_" + tok[3:]); err != ErrNotFound {
		t.Errorf("wrong prefix: %v", err)
	}
	list, _ := st.ListTokens(TokenApp)
	if len(list) != 1 || list[0].Prefix != tok[:9] {
		t.Errorf("ListTokens = %+v", list)
	}
	st.DeleteToken(list[0].ID)
	if _, err := st.AuthenticateToken(tok); err != ErrNotFound {
		t.Error("a revoked token should stop working")
	}
}

func TestLoginAndSessions(t *testing.T) {
	st := openTest(t)
	id, _ := st.CreateUser("pat", "Pat", "a long password", RoleMember)
	if _, ok := st.Authenticate("pat", "wrong password"); ok {
		t.Error("wrong password accepted")
	}
	if u, ok := st.Authenticate("PAT", "a long password"); !ok || u.ID != id {
		t.Error("sign-in should ignore the username's case")
	}
	sess, err := st.CreateSession(id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.GetSession(sess.Token); err != nil || got.User.ID != id {
		t.Errorf("GetSession = %+v, %v", got, err)
	}
	st.SetActive(id, false)
	if _, err := st.GetSession(sess.Token); err != ErrNotFound {
		t.Error("a deactivated user's session should be gone")
	}
	if _, ok := st.Authenticate("pat", "a long password"); ok {
		t.Error("a deactivated user can't sign in")
	}
}
