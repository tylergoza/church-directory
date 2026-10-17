package store

import (
	"strings"
	"testing"
)

func parseCSV(t *testing.T, s string) *ImportFile {
	t.Helper()
	f, err := ParseImport(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func counts(t *testing.T, st *Store) (people, households, addrs, changes int) {
	t.Helper()
	for q, p := range map[string]*int{
		`SELECT COUNT(*) FROM people`: &people, `SELECT COUNT(*) FROM households`: &households,
		`SELECT COUNT(*) FROM household_addresses`: &addrs, `SELECT COUNT(*) FROM changes`: &changes,
	} {
		if err := st.DB.QueryRow(q).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestImportHeaderMapping(t *testing.T) {
	f := parseCSV(t, "\xef\xbb\xbf First Name ,LAST,Goes-By,Zip,Favourite Colour,E-mail,Role\nTyler,Goza,Ty,62701,blue,t@x.org,HEAD\n")
	if len(f.Ignored) != 1 || f.Ignored[0] != "Favourite Colour" {
		t.Errorf("ignored = %v", f.Ignored)
	}
	r := f.Rows[0]
	if r.Person.FirstName != "Tyler" || r.Person.GoesBy != "Ty" || r.Addr.Postal != "62701" || r.Person.Email != "t@x.org" || r.Role != RoleHead {
		t.Errorf("row = %+v", r)
	}
	if _, err := ParseImport(strings.NewReader("first,email\nA,b@c.d\n")); err == nil {
		t.Error("a file with no last column should be refused")
	}
	if _, err := ParseImport(strings.NewReader("")); err == nil {
		t.Error("an empty file should be refused")
	}
}

func TestImportRowErrorsAndDates(t *testing.T) {
	f := parseCSV(t, "first,last,birthday,anniversary,role,kind,email\n"+
		"A,One,3/14/1980,June 5,child,contact,\n"+
		"B,Two,31/31/1980,,boss,pet,nope\n"+
		",Three,,,,,\n")
	a := f.Rows[0]
	if len(a.Errors) != 0 || a.Person.Birth.Year != 1980 || a.Person.Anniversary.Month != 6 || a.Person.Kind != KindContact {
		t.Errorf("a = %+v", a)
	}
	if len(f.Rows[1].Errors) < 4 || len(f.Rows[2].Errors) != 1 {
		t.Errorf("errors: %v / %v", f.Rows[1].Errors, f.Rows[2].Errors)
	}
}

func TestImportMatching(t *testing.T) {
	st := openTest(t)
	sr, _ := st.CreatePerson(PersonInput{FirstName: "Tyler", LastName: "Goza", Suffix: "Sr"}, 0)
	st.CreatePerson(PersonInput{FirstName: "Tyler", LastName: "Goza", Suffix: "Jr."}, 0)
	f := parseCSV(t, "first,last,suffix,goes_by\n"+
		"Tyler,Goza,Sr.,\n"+ // matches existing Sr
		"Tyler,Goza,,\n"+ // no suffix: matches neither
		"Ty,Goza,,Tyler\n"+ // matches row 2 in the file
		"Tyler,Goza,III,\n")
	if err := st.MatchImport(f); err != nil {
		t.Fatal(err)
	}
	if m := f.Rows[0].ExistingMatches; len(m) != 1 || m[0].ID != sr {
		t.Errorf("row 0 matches %v", m)
	}
	if len(f.Rows[1].ExistingMatches) != 0 || len(f.Rows[1].FileMatches) != 0 {
		t.Errorf("no suffix should match nobody: %+v", f.Rows[1])
	}
	if m := f.Rows[2].FileMatches; len(m) != 1 || m[0] != 1 || len(f.Rows[2].ExistingMatches) != 0 {
		t.Errorf("row 2 should match row 1 only: %+v", f.Rows[2])
	}
	if len(f.Rows[3].ExistingMatches)+len(f.Rows[3].FileMatches) != 0 {
		t.Error("III matches nobody")
	}
	// Defaults.
	if c := DefaultImportChoice(f.Rows[0]); c.Action != ImportExisting || c.PersonID != sr {
		t.Errorf("default 0 = %v", c)
	}
	if DefaultImportChoice(f.Rows[1]).Action != ImportAdd || DefaultImportChoice(f.Rows[2]).Action != ImportSkip {
		t.Error("defaults for 1 and 2")
	}
}

const familyCSV = `first,last,household,role,address1,city,state,postal
Ann,Lee,,,1 Oak St,Springfield,IL,62701
Bob,Lee,,,1 oak st.,springfield,il,62701
Cy,Lee,,child,1 Oak St,Springfield,IL,62701
Dee,Moss,The Mosses,,5 Elm,Town,IL,60000
Eve,Moss,The Mosses,,9 Pine,Town,IL,60001
Fay,Moss,The Mosses,,,,,
Gus,Park,,,,,,
`

func TestImportGrouping(t *testing.T) {
	f := parseCSV(t, familyCSV)
	plan := BuildPlan(f, nil)
	if !plan.OK() || plan.Created != 7 || len(plan.Households) != 3 {
		t.Fatalf("plan: %+v", plan)
	}
	// "1 Oak St" and "1 oak st." differ by a dot only: Fold ignores dots, so one household.
	lee := plan.Households[0]
	if len(lee.Members) != 3 || len(lee.Addresses) != 1 || lee.Name != "The Lee family" {
		t.Errorf("lee = %+v", lee)
	}
	if lee.Members[0].Role != RoleHead || lee.Members[1].Role != RoleAdult || lee.Members[2].Role != RoleChild {
		t.Errorf("lee roles = %v %v %v", lee.Members[0].Role, lee.Members[1].Role, lee.Members[2].Role)
	}
	moss := plan.Households[1]
	if moss.Name != "The Mosses" || len(moss.Addresses) != 2 || moss.Members[0].Addr != 0 || moss.Members[1].Addr != 1 || moss.Members[2].Addr != -1 {
		t.Errorf("moss = %+v", moss)
	}
	if park := plan.Households[2]; park.Name != "The Park family" || len(park.Members) != 1 {
		t.Errorf("park = %+v", park)
	}
}

func TestImportPreviewWritesNothing(t *testing.T) {
	st := openTest(t)
	f := parseCSV(t, familyCSV)
	if err := st.MatchImport(f); err != nil {
		t.Fatal(err)
	}
	BuildPlan(f, nil)
	if p, h, a, c := counts(t, st); p+h+a+c != 0 {
		t.Errorf("preview wrote %d %d %d %d", p, h, a, c)
	}
}

func TestImportWritesEverything(t *testing.T) {
	st := openTest(t)
	f := parseCSV(t, familyCSV)
	st.MatchImport(f)
	res, err := st.ApplyImport(BuildPlan(f, nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.People != 7 || res.Households != 3 || res.Addresses != 3 {
		// 3 households: Lee, Mosses, Park; 1+2 addresses
		t.Logf("result %+v", res)
	}
	p, h, a, _ := counts(t, st)
	if p != 7 || h != 3 || a != 3 {
		t.Errorf("counts people %d households %d addresses %d", p, h, a)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM changes WHERE what = 'imported person'`).Scan(&n)
	if n != 7 {
		t.Errorf("imported person rows = %d", n)
	}
	var withAddr int
	st.DB.QueryRow(`SELECT COUNT(*) FROM household_members WHERE address_id IS NOT NULL`).Scan(&withAddr)
	if withAddr != 5 {
		t.Errorf("members with an address = %d", withAddr)
	}
}

func TestImportUseExistingAndSkip(t *testing.T) {
	st := openTest(t)
	old, _ := st.CreatePerson(PersonInput{FirstName: "Ann", LastName: "Lee"}, 0)
	oldHome, _ := st.CreateHousehold("Old home", "", 0)
	st.PlaceMember(oldHome, old, RoleAdult, NoAddress, 0)
	f := parseCSV(t, familyCSV)
	st.MatchImport(f)
	choices := make([]ImportChoice, len(f.Rows))
	for i, r := range f.Rows {
		choices[i] = DefaultImportChoice(r)
	}
	choices[6] = ImportChoice{Action: ImportSkip} // Gus
	if choices[0].Action != ImportExisting {
		t.Fatal("Ann should default to the existing Ann")
	}
	res, err := st.ApplyImport(BuildPlan(f, choices), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.People != 5 || res.Linked != 1 {
		t.Errorf("result %+v", res)
	}
	hs, _ := st.PersonHouseholds(old)
	if len(hs) != 1 || hs[0].HouseholdID == oldHome {
		t.Errorf("Ann should have moved: %+v", hs)
	}
	if p, _, _, _ := counts(t, st); p != 6 {
		t.Errorf("people = %d", p)
	}
}

func TestImportBadRowBlocksOrIsSkipped(t *testing.T) {
	st := openTest(t)
	f := parseCSV(t, "first,last,birthday\nGood,One,\nBad,Two,Smarch 3\n")
	st.MatchImport(f)
	// Chosen to add: blocks the whole import, nothing written.
	plan := BuildPlan(f, []ImportChoice{{Action: ImportAdd}, {Action: ImportAdd}})
	if plan.OK() {
		t.Fatal("a bad row chosen for adding should block")
	}
	if _, err := st.ApplyImport(plan, 0); err == nil {
		t.Fatal("import should refuse")
	}
	if p, h, _, c := counts(t, st); p+h+c != 0 {
		t.Error("something was written")
	}
	// Default skips the bad row.
	plan = BuildPlan(f, nil)
	if !plan.OK() || plan.Skipped != 1 || plan.Created != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := st.ApplyImport(plan, 0); err != nil {
		t.Fatal(err)
	}
	if p, _, _, _ := counts(t, st); p != 1 {
		t.Errorf("people = %d", p)
	}
}

func TestImportIsOneTransaction(t *testing.T) {
	st := openTest(t)
	f := parseCSV(t, "first,last\nA,One\nB,Two\n")
	st.MatchImport(f)
	plan := BuildPlan(f, nil)
	// Make the second household insert fail: a member row clashes later.
	plan.Households[1].Kind = "bogus" // violates the kind check, after person rows were written
	if _, err := st.ApplyImport(plan, 0); err == nil {
		t.Fatal("expected a failure")
	}
	if p, h, _, c := counts(t, st); p+h+c != 0 {
		t.Errorf("rollback left %d %d %d", p, h, c)
	}
}

func TestImportRefusesBadChoice(t *testing.T) {
	st := openTest(t)
	other, _ := st.CreatePerson(PersonInput{FirstName: "Zed", LastName: "Nobody"}, 0)
	f := parseCSV(t, "first,last\nA,One\n")
	st.MatchImport(f)
	plan := BuildPlan(f, []ImportChoice{{Action: ImportExisting, PersonID: other}})
	if plan.OK() {
		t.Error("an existing person who doesn't match the name must be refused")
	}
}
