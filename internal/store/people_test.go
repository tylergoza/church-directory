package store

import (
	"testing"

	"github.com/tylergoza/church-directory/internal/dates"
)

func mustPerson(t *testing.T, st *Store, first, last, goesBy, suffix string) int64 {
	t.Helper()
	id, err := st.CreatePerson(PersonInput{FirstName: first, LastName: last, GoesBy: goesBy, Suffix: suffix}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDisplayName(t *testing.T) {
	cases := []struct{ first, last, goesBy, suffix, want, legal string }{
		{"Tyler", "Goza", "", "", "Tyler Goza", "Tyler Goza"},
		{"Tyler", "Goza", "", "Jr.", "Tyler Goza Jr.", "Tyler Goza Jr."},
		{"Tyler", "Goza", "Ty", "", "Ty Goza", "Tyler Goza"},
		{"Tyler", "Goza", "Ty", "III", "Ty Goza III", "Tyler Goza III"},
		{" Tyler ", " Goza ", "  ", " ", "Tyler Goza", "Tyler Goza"},
	}
	for _, c := range cases {
		if got := DisplayName(c.first, c.last, c.goesBy, c.suffix); got != c.want {
			t.Errorf("DisplayName(%q,%q,%q,%q) = %q, want %q", c.first, c.last, c.goesBy, c.suffix, got, c.want)
		}
		if got := LegalName(c.first, c.last, c.suffix); got != c.legal {
			t.Errorf("LegalName = %q, want %q", got, c.legal)
		}
	}
}

func TestFold(t *testing.T) {
	cases := map[string]string{
		"Tyler":              "tyler",
		"  José   Núñez ":    "jose nunez",
		"Jr.":                "jr",
		"J.R.":               "jr",
		"O'Neil":             "oneil",
		"Smith-Jones":        "smith jones",
		"Łukasz Wójcik":      "lukasz wojcik",
		"Zoë Ångström":       "zoe angstrom",
		"Dvořák Čapek":       "dvorak capek",
		"Straße":             "strasse",
		"":                   "",
		"ÉMILE":              "emile",
		"Ünal Şahin Ğüneş":   "unal sahin gunes",
		"Ørsted Søren Œuvre": "orsted soren oeuvre",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameMatches(t *testing.T) {
	st := openTest(t)
	tyler := mustPerson(t, st, "Tyler", "Goza", "", "")
	jr := mustPerson(t, st, "Tyler", "Goza", "", "Jr.")
	ty := mustPerson(t, st, "Timothy", "Goza", "Ty", "")
	jose := mustPerson(t, st, "José", "Núñez", "", "")
	old := mustPerson(t, st, "Mary", "Smith", "", "")
	if err := st.ArchivePerson(old, true, 0); err != nil {
		t.Fatal(err)
	}
	contact, err := st.CreatePerson(PersonInput{FirstName: "Carol", LastName: "Jones", Kind: KindContact}, 0)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name                        string
		first, last, goesBy, suffix string
		exclude                     int64
		want                        []int64
	}{
		{"same name", "Tyler", "Goza", "", "", 0, []int64{tyler}},
		{"case and spaces", "  tYLER ", "GOZA", "", "", 0, []int64{tyler}},
		{"Jr with a dot", "Tyler", "Goza", "", "Jr.", 0, []int64{jr}},
		{"Jr without a dot", "Tyler", "Goza", "", "Jr", 0, []int64{jr}},
		{"Jr in capitals", "Tyler", "Goza", "", "JR", 0, []int64{jr}},
		{"no suffix does not match Jr", "Tyler", "Goza", "", "", 0, []int64{tyler}},
		{"Jr does not match no suffix", "Tyler", "Goza", "", "Jr.", 0, []int64{jr}},
		{"a different suffix", "Tyler", "Goza", "", "III", 0, nil},
		{"goes-by against first name", "Ty", "Goza", "", "", 0, []int64{ty}},
		{"first name against goes-by", "Timothy", "Goza", "", "", 0, []int64{ty}},
		{"goes-by against goes-by", "Bob", "Goza", "Ty", "", 0, []int64{ty}},
		{"new goes-by against existing first name", "Tim", "Goza", "Tyler", "", 0, []int64{tyler}},
		{"new person with both", "Tyler", "Goza", "Ty", "", 0, []int64{tyler, ty}},
		{"accents", "Jose", "Nunez", "", "", 0, []int64{jose}},
		{"accents the other way", "José", "NÚÑEZ", "", "", 0, []int64{jose}},
		{"archived people are matched", "Mary", "Smith", "", "", 0, []int64{old}},
		{"contacts are matched", "Carol", "Jones", "", "", 0, []int64{contact}},
		{"different last name", "Tyler", "Gozo", "", "", 0, nil},
		{"different first name", "Taylor", "Goza", "", "", 0, nil},
		{"a person is not their own match", "Tyler", "Goza", "", "", tyler, nil},
		{"excluding one leaves others", "Tyler", "Goza", "Ty", "", tyler, []int64{ty}},
		{"empty name", "", "", "", "", 0, nil},
	}
	for _, c := range cases {
		got, err := st.NameMatches(c.first, c.last, c.goesBy, c.suffix, c.exclude)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, p := range got {
			ids = append(ids, p.ID)
		}
		if len(ids) != len(c.want) {
			t.Errorf("%s: matched %v, want %v", c.name, ids, c.want)
			continue
		}
		for _, w := range c.want {
			found := false
			for _, id := range ids {
				found = found || id == w
			}
			if !found {
				t.Errorf("%s: matched %v, want %v", c.name, ids, c.want)
			}
		}
	}

	// A pair recorded as different people isn't flagged again, from either
	// side, but only for the person being checked.
	twin := mustPerson(t, st, "Tyler", "Goza", "", "")
	if got, _ := st.NameMatches("Tyler", "Goza", "", "", twin); len(got) != 1 || got[0].ID != tyler {
		t.Fatalf("before the decision: %v", got)
	}
	if err := st.RecordNotSamePeople(twin, tyler, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordNotSamePeople(tyler, twin, 0); err != nil { // twice, other way round
		t.Fatal(err)
	}
	if got, _ := st.NameMatches("Tyler", "Goza", "", "", twin); len(got) != 0 {
		t.Errorf("a not-same pair was flagged: %v", got)
	}
	if got, _ := st.NameMatches("Tyler", "Goza", "", "", tyler); len(got) != 0 {
		t.Errorf("a not-same pair was flagged from the other side: %v", got)
	}
	// A brand new entry (no ID yet) still sees both.
	if got, _ := st.NameMatches("Tyler", "Goza", "", "", 0); len(got) != 2 {
		t.Errorf("a new entry should match both, got %v", got)
	}
	if err := st.RecordNotSamePeople(tyler, tyler, 0); err == nil {
		t.Error("a person can't be recorded as different from themselves")
	}
}

func TestPersonCRUDAndChanges(t *testing.T) {
	st := openTest(t)
	by, _ := st.CreateUser("sam", "Sam", "a long password", RoleEditor)
	in := PersonInput{FirstName: "Tyler", LastName: "Goza", Birth: dates.Partial{Month: 3, Day: 14}, Email: "t@example.org"}
	if errs := in.Validate(); errs != nil {
		t.Fatal(errs)
	}
	id, err := st.CreatePerson(in, by)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.GetPerson(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "Tyler Goza" || p.Birth != (dates.Partial{Month: 3, Day: 14}) || p.Kind != KindMember || p.Pending || p.UserID != 0 || p.Archived() {
		t.Errorf("person = %+v", p)
	}
	in.GoesBy, in.Birth.Year, in.Email = "Ty", 1985, "" // name, birthday, email
	if err := st.UpdatePerson(id, in, by); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdatePerson(id, in, by); err != nil { // nothing changed: nothing logged
		t.Fatal(err)
	}
	if err := st.ArchivePerson(id, true, by); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPerson(id); !p.Archived() {
		t.Error("not archived")
	}
	if err := st.ArchivePerson(id, false, by); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchivePerson(999, true, by); err != ErrNotFound {
		t.Errorf("archive missing person: %v", err)
	}
	changes, err := st.RecentChanges(id, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var whats []string
	for _, c := range changes {
		whats = append(whats, c.What)
		if c.By != "Sam" {
			t.Errorf("change by %q", c.By)
		}
	}
	want := []string{"unarchived person", "archived person", "edited name, birthday, email", "added person"}
	if len(whats) != len(want) {
		t.Fatalf("changes = %v, want %v", whats, want)
	}
	for i := range want {
		if whats[i] != want[i] {
			t.Errorf("change %d = %q, want %q", i, whats[i], want[i])
		}
	}
}

func TestPersonValidate(t *testing.T) {
	bad := []PersonInput{
		{LastName: "Goza"},
		{FirstName: "Tyler"},
		{FirstName: "Tyler", LastName: "Goza", Kind: "visitor"},
		{FirstName: "Tyler", LastName: "Goza", Email: "no-at-sign"},
		{FirstName: "Tyler", LastName: "Goza", Birth: dates.Partial{Day: 4}},
		{FirstName: "Tyler", LastName: "Goza", Anniversary: dates.Partial{Month: 2, Day: 29, Year: 2001}},
	}
	for i, in := range bad {
		if len(in.Validate()) == 0 {
			t.Errorf("case %d should be invalid: %+v", i, in)
		}
	}
	ok := PersonInput{FirstName: " Tyler ", LastName: "Goza", Anniversary: dates.Partial{Month: 2, Day: 29}}
	if errs := ok.Validate(); len(errs) != 0 || ok.FirstName != "Tyler" || ok.Kind != KindMember {
		t.Errorf("valid input: %v %+v", errs, ok)
	}
}

func TestListPeopleFilters(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser("linked", "", "a long password", RoleMember)
	a := mustPerson(t, st, "Anna", "Adams", "", "")
	b := mustPerson(t, st, "Émile", "Brown", "", "")
	c, _ := st.CreatePerson(PersonInput{FirstName: "Carl", LastName: "Cole", Kind: KindContact}, 0)
	d := mustPerson(t, st, "Dina", "Dunn", "", "")
	e := mustPerson(t, st, "Eve", "Ellis", "", "")
	st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, u, a)
	st.DB.Exec(`UPDATE people SET pending = 1, pending_reason = 'name_match' WHERE id = ?`, d)
	st.ArchivePerson(e, true, 0)
	h, _ := st.CreateHousehold("The Browns", "", 0)
	st.PlaceMember(h, b, RoleHead, 0, 0)

	ids := func(filter, q string) []int64 {
		rows, err := st.ListPeople(filter, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", name, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: %v, want %v", name, got, want)
			}
		}
	}
	eq("all", ids(PeopleAll, ""), a, b, c, d)
	eq("unlinked", ids(PeopleUnlinked, ""), b, c, d)
	eq("contacts", ids(PeopleContacts, ""), c)
	eq("pending", ids(PeoplePending, ""), d)
	eq("archived", ids(PeopleArchived, ""), e)
	eq("search accents", ids(PeopleAll, "emile"), b)
	eq("search two words", ids(PeopleAll, "brown é"), b)
	eq("search none", ids(PeopleAll, "zzz"))
	if rows, _ := st.ListPeople(PeopleAll, "brown"); len(rows) != 1 || len(rows[0].Households) != 1 || rows[0].Households[0] != "The Browns" {
		t.Errorf("households in rows: %+v", rows)
	}
}

func TestDeletePersonRemovesEverything(t *testing.T) {
	st := openTest(t)
	by, _ := st.CreateUser("sam", "", "a long password", RoleEditor)
	id := mustPerson(t, st, "Tyler", "Goza", "", "")
	other := mustPerson(t, st, "Jane", "Goza", "", "")
	st.DB.Exec(`UPDATE people SET photo = 'abc123' WHERE id = ?`, id)
	h, _ := st.CreateHousehold("The Gozas", "", by)
	st.PlaceMember(h, id, RoleHead, 0, by)
	st.DB.Exec(`INSERT INTO notes (person_id, body) VALUES (?, 'peanuts')`, id)
	st.DB.Exec(`INSERT INTO field_settings (person_id, field, shown) VALUES (?, 'email', 1)`, id)
	st.DB.Exec(`INSERT INTO relationships (person_id, related_id, kind) VALUES (?, ?, 'spouse')`, id, other)
	st.DB.Exec(`INSERT INTO claims (code_hash, person_id, expires_at) VALUES ('h', ?, '2099-01-01')`, id)
	st.DB.Exec(`INSERT INTO groups (name) VALUES ('Singers')`)
	st.DB.Exec(`INSERT INTO group_members (group_id, person_id) VALUES (1, ?)`, id)
	st.RecordNotSamePeople(id, other, by)

	photo, err := st.DeletePerson(id, by)
	if err != nil {
		t.Fatal(err)
	}
	if photo != "abc123" {
		t.Errorf("photo = %q", photo)
	}
	for _, table := range []string{"notes", "field_settings", "relationships", "claims", "group_members", "household_members", "not_same_people"} {
		var n int
		st.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
	if _, err := st.GetPerson(id); err != ErrNotFound {
		t.Errorf("GetPerson after delete: %v", err)
	}
	if _, err := st.GetPerson(other); err != nil {
		t.Errorf("the other person should stay: %v", err)
	}
	// The audit trail keeps "deleted person" with no person and no values.
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM changes WHERE what = 'deleted person' AND person_id IS NULL`).Scan(&n)
	if n != 1 {
		t.Error("expected a 'deleted person' change")
	}
	if _, err := st.DeletePerson(id, by); err != ErrNotFound {
		t.Errorf("deleting twice: %v", err)
	}
}
