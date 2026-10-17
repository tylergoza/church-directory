package access

import (
	"path/filepath"
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

type famEnv struct {
	st                                     *store.Store
	gpa, gma, dad, mom, kid, sib, outsider int64
	old, own                               int64
}

func famNames(fs FamilySection, label string) []string {
	for _, g := range fs.Groups {
		if g.Label == label {
			var out []string
			for _, p := range g.People {
				out = append(out, p.Name)
			}
			return out
		}
	}
	return nil
}

func newFam(t *testing.T) *famEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mk := func(first string) int64 {
		id, err := st.CreatePerson(store.PersonInput{FirstName: first, LastName: "Lee"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	e := &famEnv{st: st}
	e.gpa, e.gma, e.dad, e.mom, e.kid, e.sib, e.outsider = mk("Gpa"), mk("Gma"), mk("Dad"), mk("Mom"), mk("Kid"), mk("Sib"), mk("Outsider")
	e.old, _ = st.CreateHousehold("The Old Lees", "", 0)
	st.PlaceMember(e.old, e.gpa, store.RoleHead, 0, 0)
	st.PlaceMember(e.old, e.gma, store.RoleHead, 0, 0)
	st.PlaceMember(e.old, e.dad, store.RoleAdult, 0, 0)
	st.PlaceMember(e.old, e.mom, store.RoleAdult, 0, 0)
	st.PlaceMember(e.old, e.kid, store.RoleChild, 0, 0)
	st.PlaceMember(e.old, e.sib, store.RoleChild, 0, 0)
	for _, l := range [][2]int64{{e.gpa, e.dad}, {e.gma, e.dad}, {e.dad, e.kid}, {e.mom, e.kid}, {e.dad, e.sib}} {
		if err := st.AddRelationship(store.RelParent, l[0], l[1], 0); err != nil {
			t.Fatal(err)
		}
	}
	st.AddRelationship(store.RelSpouse, e.dad, e.mom, 0)
	return e
}

func (e *famEnv) dir(t *testing.T, v *Viewer) *Directory {
	t.Helper()
	d, err := Load(e.st, v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGrandchildrenFoundBothWaysAfterOwnHouseholdMove(t *testing.T) {
	e := newFam(t)
	member := &Viewer{UserID: 1}
	// Dad, Mom and their children start their own household.
	own, err := e.st.StartOwnHousehold(e.dad, "The Dad Lees", e.mom, []int64{e.kid, e.sib}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.own = own
	d := e.dir(t, member)
	gp, _ := d.Page(e.gpa)
	if got := famNames(gp.Family, "Grandchildren"); !eq(got, []string{"Kid Lee", "Sib Lee"}) {
		t.Errorf("grandchildren = %v", got)
	}
	if got := famNames(gp.Family, "Children"); !eq(got, []string{"Dad Lee"}) {
		t.Errorf("children = %v", got)
	}
	kp, _ := d.Page(e.kid)
	if got := famNames(kp.Family, "Grandparents"); !eq(got, []string{"Gma Lee", "Gpa Lee"}) {
		t.Errorf("grandparents = %v", got)
	}
	// Each relative links to the household they are in now.
	for _, g := range kp.Family.Groups {
		if g.Label == "Grandparents" && (len(g.People[0].Households) != 1 || g.People[0].Households[0].ID != e.old) {
			t.Errorf("grandparent households = %+v", g.People[0].Households)
		}
	}
	// Siblings share a parent; the person isn't their own sibling.
	if got := famNames(kp.Family, "Siblings"); !eq(got, []string{"Sib Lee"}) {
		t.Errorf("siblings = %v", got)
	}
	if got := famNames(kp.Family, "Parents"); !eq(got, []string{"Dad Lee", "Mom Lee"}) {
		t.Errorf("parents = %v", got)
	}
	// Related households, both ways.
	oldFV, _ := d.Family(e.old, false)
	ownFV, _ := d.Family(e.own, false)
	if len(oldFV.Related) != 1 || oldFV.Related[0].ID != e.own {
		t.Errorf("old related = %+v", oldFV.Related)
	}
	if len(ownFV.Related) != 1 || ownFV.Related[0].ID != e.old {
		t.Errorf("own related = %+v", ownFV.Related)
	}
	if len(ownFV.Related) == 1 && ownFV.Related[0].Name != "The Old Lees" {
		t.Errorf("related name = %q", ownFV.Related[0].Name)
	}
	// Someone with no links has no Family section.
	op, _ := d.Page(e.outsider)
	if !op.Family.Empty() {
		t.Errorf("outsider family = %+v", op.Family)
	}
}

func TestFamilyHidesContactsAndArchivedFromMembers(t *testing.T) {
	e := newFam(t)
	member, staff := &Viewer{UserID: 1}, &Viewer{UserID: 2, Staff: true}
	// Sib becomes a contact, Mom is archived.
	if _, err := e.st.DB.Exec(`UPDATE people SET kind = 'contact' WHERE id = ?`, e.sib); err != nil {
		t.Fatal(err)
	}
	if err := e.st.ArchivePerson(e.mom, true, 0); err != nil {
		t.Fatal(err)
	}
	kp, _ := e.dir(t, member).Page(e.kid)
	if got := famNames(kp.Family, "Siblings"); got != nil {
		t.Errorf("member sees contact sibling: %v", got)
	}
	if got := famNames(kp.Family, "Parents"); !eq(got, []string{"Dad Lee"}) {
		t.Errorf("member sees archived parent: %v", got)
	}
	// A head of the contact's household may see the contact.
	head := &Viewer{UserID: 3, PersonID: e.gpa, HeadOf: map[int64]bool{e.old: true}}
	hp, _ := e.dir(t, head).Page(e.kid)
	if got := famNames(hp.Family, "Siblings"); !eq(got, []string{"Sib Lee"}) {
		t.Errorf("head sees contact sibling in own household: %v", got)
	}
	// A link through an archived person isn't followed.
	if err := e.st.ArchivePerson(e.dad, true, 0); err != nil {
		t.Fatal(err)
	}
	gp, _ := e.dir(t, member).Page(e.gpa)
	if got := famNames(gp.Family, "Grandchildren"); got != nil {
		t.Errorf("grandchildren through an archived parent: %v", got)
	}
	// Staff see all of it.
	sp, _ := e.dir(t, staff).Page(e.kid)
	if got := famNames(sp.Family, "Siblings"); !eq(got, []string{"Sib Lee"}) {
		t.Errorf("staff siblings = %v", got)
	}
}
