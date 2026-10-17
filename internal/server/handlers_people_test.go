package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/photos"
	"github.com/tylergoza/church-directory/internal/store"
)

// staffClient is a server with a staff member signed in.
func staffClient(t *testing.T, cfg Config) (*client, *store.Store) {
	t.Helper()
	c, st := newTestServerWith(t, cfg)
	st.CreateUser("sam", "Sam", "a long password", store.RoleEditor)
	st.CreateUser("mel", "Mel", "a long password", store.RoleMember)
	return c.as("sam"), st
}

func countPeople(t *testing.T, st *store.Store) int {
	t.Helper()
	rows, err := st.ListPeople(store.PeopleAll, "")
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func TestPeoplePagesAreStaffOnly(t *testing.T) {
	c, st := newTestServer(t)
	st.CreateUser("sam", "", "a long password", store.RoleEditor)
	st.CreateUser("mel", "", "a long password", store.RoleMember)
	pid := mustPersonID(t, st, "Tyler", "Goza")
	hid, _ := st.CreateHousehold("The Gozas", "", 0)
	pages := []string{"/admin/people", "/admin/people/new", "/admin/people/" + itoa(pid), "/admin/people/" + itoa(pid) + "/delete",
		"/admin/households", "/admin/households/new", "/admin/households/" + itoa(hid)}
	mel, sam := c.as("mel"), c.as("sam")
	for _, p := range pages {
		mel.get(p, 403)
		sam.get(p, 200)
	}
	// Members can't write either.
	mel.post("/account", "/admin/people", url.Values{"first_name": {"Eve"}, "last_name": {"Evil"}}, 403)
	mel.post("/account", "/admin/people/"+itoa(pid)+"/delete", url.Values{}, 403)
	mel.post("/account", "/admin/households", url.Values{"name": {"x"}}, 403)
	if countPeople(t, st) != 1 {
		t.Error("a member changed the directory")
	}
	// Signed out goes to sign in.
	if body := (&client{t: t, base: c.base, http: &http.Client{}}).get("/admin/people", 200); !strings.Contains(body, `action="/login"`) {
		t.Error("expected the sign-in form")
	}
	if body := sam.get("/admin", 200); !strings.Contains(body, "/admin/people") || !strings.Contains(body, "/admin/households") {
		t.Error("the admin page should link to people and households")
	}
}

func mustPersonID(t *testing.T, st *store.Store, first, last string) int64 {
	t.Helper()
	id, err := st.CreatePerson(store.PersonInput{FirstName: first, LastName: last}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStaffAddFormShowsMatchesFirst(t *testing.T) {
	c, st := staffClient(t, Config{})
	hid, _ := st.CreateHousehold("The Goza Family", "", 0)
	tyler := mustPersonID(t, st, "Tyler", "Goza")
	st.PlaceMember(hid, tyler, store.RoleHead, 0, 0)
	add := url.Values{"first_name": {"tyler"}, "last_name": {"GOZA"}, "kind": {"member"}}

	// A match: nothing is added, and the matches come first.
	body := c.post("/admin/people/new", "/admin/people", add, 200)
	for _, want := range []string{"Already in the directory", "Tyler Goza", "The Goza Family", "Use that one", "Add anyway"} {
		if !strings.Contains(body, want) {
			t.Errorf("match page missing %q", want)
		}
	}
	if countPeople(t, st) != 1 {
		t.Fatal("added without confirming")
	}
	if !strings.Contains(body, `href="/admin/people/`+itoa(tyler)+`"`) {
		t.Error("Use that one should go to the existing person")
	}
	// Add anyway goes through.
	add.Set("add_anyway", "1")
	body = c.post("/admin/people/new", "/admin/people", add, 200)
	if countPeople(t, st) != 2 || !strings.Contains(body, "Added tyler GOZA") {
		t.Errorf("Add anyway didn't add: %d people", countPeople(t, st))
	}
	// A suffix tells them apart: no match, so it's added straight away.
	c.post("/admin/people/new", "/admin/people", url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "suffix": {"Jr."}}, 200)
	if countPeople(t, st) != 3 {
		t.Error("a suffixed name shouldn't match")
	}
	// Errors come back with what was typed.
	body = c.post("/admin/people/new", "/admin/people", url.Values{"first_name": {"Ann"}, "last_name": {"Lee"}, "birth_month": {"2"}, "birth_day": {"30"}}, 422)
	if !strings.Contains(body, "February doesn&#39;t have 30 days") || !strings.Contains(body, `value="30"`) {
		t.Errorf("date error page:\n%s", body)
	}
	body = c.post("/admin/people/new", "/admin/people", url.Values{"first_name": {"Ann"}, "last_name": {"Lee"}, "birth_day": {"4"}}, 422)
	if !strings.Contains(body, "pick a month") {
		t.Error("a day without a month should be refused")
	}
	if countPeople(t, st) != 3 {
		t.Error("an invalid form added someone")
	}
}

func TestAddFromHouseholdPageUsesThatOne(t *testing.T) {
	c, st := staffClient(t, Config{})
	home, _ := st.CreateHousehold("The Smiths", "", 0)
	addr, _ := st.AddAddress(home, store.AddressInput{Label: "Main", Address1: "1 Oak St"}, 0)
	gozas, _ := st.CreateHousehold("The Gozas", "", 0)
	tyler := mustPersonID(t, st, "Tyler", "Goza")
	st.PlaceMember(gozas, tyler, store.RoleAdult, 0, 0)

	page := "/admin/people/new?household=" + itoa(home)
	body := c.get(page, 200)
	if !strings.Contains(body, "The Smiths") || !strings.Contains(body, "1 Oak St") && !strings.Contains(body, "Main") {
		t.Error("household context missing from the add form")
	}
	form := url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "household_id": {itoa(home)}, "role": {"head"}, "address_id": {itoa(addr)}}
	body = c.post(page, "/admin/people", form, 200)
	if !strings.Contains(body, "Use that one") || !strings.Contains(body, "/admin/households/"+itoa(home)+"/members") {
		t.Error("expected Use that one to add the existing person to this household")
	}
	// Use that one: the existing person is added here, as an adult they move.
	c.post(page, "/admin/households/"+itoa(home)+"/members", url.Values{"person_id": {itoa(tyler)}, "role": {"head"}, "address_id": {itoa(addr)}}, 200)
	ms, _ := st.HouseholdMembers(home)
	if len(ms) != 1 || ms[0].ID != tyler || ms[0].Role != "head" || ms[0].AddressID != addr {
		t.Errorf("members = %+v", ms)
	}
	if old, _ := st.HouseholdMembers(gozas); len(old) != 0 {
		t.Error("the adult should have moved out of the old household")
	}
	if countPeople(t, st) != 1 {
		t.Error("Use that one must not add a second person")
	}
	// Add anyway from the household page: new person, in the household with the role and address.
	form.Set("add_anyway", "1")
	form.Set("role", "child")
	c.post(page, "/admin/people", form, 200)
	ms, _ = st.HouseholdMembers(home)
	if len(ms) != 2 || ms[1].Role != "child" || ms[1].AddressID != addr {
		t.Errorf("members after Add anyway = %+v", ms)
	}
}

func TestEditPerson(t *testing.T) {
	c, st := staffClient(t, Config{})
	id := mustPersonID(t, st, "Tyler", "Goza")
	page := "/admin/people/" + itoa(id)
	body := c.post(page, page, url.Values{
		"first_name": {"Tyler"}, "last_name": {"Goza"}, "goes_by": {"Ty"}, "suffix": {"Jr."},
		"email": {"ty@example.org"}, "birth_month": {"3"}, "birth_day": {"14"}, "birth_year": {"1985"},
		"anniv_month": {"2"}, "anniv_day": {"29"}, "kind": {"contact"},
	}, 200)
	if !strings.Contains(body, "Saved.") || !strings.Contains(body, "Ty Goza Jr.") || !strings.Contains(body, "Legal name: Tyler Goza Jr.") {
		t.Errorf("edit page:\n%s", body)
	}
	p, _ := st.GetPerson(id)
	if p.Birth.String() != "March 14, 1985" || p.Anniversary.String() != "February 29" || p.Kind != "contact" {
		t.Errorf("saved person = %+v", p)
	}
	if !strings.Contains(body, "edited name") || strings.Contains(body, "ty@example.org</li>") {
		t.Error("history should say what changed, never the values")
	}
	// Bad dates are refused and nothing changes.
	c.post(page, page, url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "anniv_month": {"2"}, "anniv_day": {"29"}, "anniv_year": {"2001"}}, 422)
	c.post(page, page, url.Values{"first_name": {"Tyler"}, "last_name": {"Goza"}, "birth_year": {"abc"}}, 422)
	if q, _ := st.GetPerson(id); q.Birth != p.Birth {
		t.Error("an invalid edit changed the person")
	}
	// Archive and bring back.
	c.post(page, page+"/archive", url.Values{"archive": {"1"}}, 200)
	if q, _ := st.GetPerson(id); !q.Archived() {
		t.Error("not archived")
	}
	if body := c.get("/admin/people?show=archived", 200); !strings.Contains(body, "Goza") {
		t.Error("archive list missing the person")
	}
	if body := c.get("/admin/people", 200); strings.Contains(body, "Goza, ") {
		t.Error("archived person in the main list")
	}
	c.post(page, page+"/archive", url.Values{"archive": {"0"}}, 200)
	if q, _ := st.GetPerson(id); q.Archived() {
		t.Error("still archived")
	}
	c.get("/admin/people?show=contacts&q=ty", 200)
	c.get("/admin/people?show=pending", 200)
	c.get("/admin/people?show=unlinked", 200)
	c.get("/admin/people/999999", 404)
}

func TestDeletePersonRemovesNotesAndPhotoFiles(t *testing.T) {
	dir := photos.Dir{Path: filepath.Join(t.TempDir(), "photos")}
	c, st := staffClient(t, Config{Photos: dir})
	id := mustPersonID(t, st, "Tyler", "Goza")
	other := mustPersonID(t, st, "Jane", "Goza")
	name, err := dir.Save([]byte("full"), []byte("thumb"))
	if err != nil {
		t.Fatal(err)
	}
	keep, _ := dir.Save([]byte("full"), []byte("thumb"))
	st.DB.Exec(`UPDATE people SET photo = ? WHERE id = ?`, name, id)
	st.DB.Exec(`UPDATE people SET photo = ? WHERE id = ?`, keep, other)
	st.DB.Exec(`INSERT INTO notes (person_id, body) VALUES (?, 'peanut allergy')`, id)
	exists := func(n string) bool {
		_, err := os.Stat(filepath.Join(dir.Path, n+".jpg"))
		return err == nil
	}
	if !exists(name) {
		t.Fatal("test photo missing")
	}

	page := "/admin/people/" + itoa(id)
	if body := c.get(page+"/delete", 200); !strings.Contains(body, "This can't be undone") || !strings.Contains(body, "Yes, delete") {
		t.Error("expected a confirmation page")
	}
	if !exists(name) {
		t.Fatal("the confirmation page deleted the photo")
	}
	body := c.post(page+"/delete", page+"/delete", url.Values{}, 200)
	if !strings.Contains(body, "Deleted") {
		t.Error("no confirmation message")
	}
	if exists(name) {
		t.Error("the photo file is still there")
	}
	if _, err := os.Stat(filepath.Join(dir.Path, name+"_t.jpg")); err == nil {
		t.Error("the thumbnail is still there")
	}
	if !exists(keep) {
		t.Error("someone else's photo was removed")
	}
	var notes int
	st.DB.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&notes)
	if notes != 0 {
		t.Error("the notes are still there")
	}
	c.get(page, 404)
}

func TestHouseholdPages(t *testing.T) {
	c, st := staffClient(t, Config{})
	body := c.post("/admin/households/new", "/admin/households", url.Values{"name": {"The Gozas"}, "kind": {"member"}}, 200)
	if !strings.Contains(body, "Household added") {
		t.Errorf("household page:\n%s", body)
	}
	c.post("/admin/households/new", "/admin/households", url.Values{"name": {" "}}, 422)
	rows, _ := st.ListHouseholds("", "")
	if len(rows) != 1 {
		t.Fatalf("households = %+v", rows)
	}
	hid := rows[0].ID
	page := "/admin/households/" + itoa(hid)
	other, _ := st.CreateHousehold("The Smiths", "", 0)

	// Rename, two addresses.
	c.post(page, page, url.Values{"name": {"The Goza Family"}, "kind": {"member"}}, 200)
	c.post(page, page+"/addresses", url.Values{"label": {"Main"}, "address1": {"123 Oak St"}, "city": {"Springfield"}}, 200)
	c.post(page, page+"/addresses", url.Values{"label": {"Grandparents"}, "address1": {"45 Elm Rd"}}, 200)
	body = c.post(page, page+"/addresses", url.Values{"label": {"Empty"}}, 200)
	if !strings.Contains(body, "Enter a street, city or postal code") {
		t.Error("an empty address should be refused")
	}
	addrs, _ := st.ListAddresses(hid)
	if len(addrs) != 2 {
		t.Fatalf("addresses = %+v", addrs)
	}
	main, grand := addrs[0].ID, addrs[1].ID
	c.post(page, page+"/addresses/"+itoa(main), url.Values{"label": {"Home"}, "address1": {"123 Oak Street"}}, 200)
	if a, _ := st.GetAddress(main); a.Label != "Home" || a.Address1 != "123 Oak Street" {
		t.Errorf("address = %+v", a)
	}

	// People: add new ones from the household page, change roles and addresses.
	tyler, jane, gran := mustPersonID(t, st, "Tyler", "Goza"), mustPersonID(t, st, "Jane", "Goza"), mustPersonID(t, st, "Bob", "Goza")
	c.post(page, page+"/members", url.Values{"person_id": {itoa(tyler)}, "role": {"head"}, "address_id": {itoa(main)}}, 200)
	c.post(page, page+"/members", url.Values{"person_id": {itoa(jane)}, "role": {"head"}, "address_id": {itoa(main)}}, 200)
	c.post(page, page+"/members", url.Values{"person_id": {itoa(gran)}, "role": {"adult"}, "address_id": {itoa(grand)}}, 200)
	body = c.get(page+"?q=bob", 200)
	if strings.Contains(body, `value="`+itoa(gran)+`"`) {
		t.Error("someone already in the household shouldn't be offered")
	}
	if body = c.get(page+"?q=zzz", 200); !strings.Contains(body, "No one matching") {
		t.Error("expected no-results text")
	}
	if ms, _ := st.HouseholdMembers(hid); len(ms) != 3 || ms[0].Role != "head" || ms[1].Role != "head" {
		t.Errorf("members = %+v", ms)
	}
	// A foreign address is refused with a message, nothing changes.
	foreign, _ := st.AddAddress(other, store.AddressInput{Address1: "9 Pine"}, 0)
	body = c.post(page, page+"/members/"+itoa(gran), url.Values{"role": {"adult"}, "address_id": {itoa(foreign)}}, 200)
	if !strings.Contains(body, "belongs to another household") {
		t.Error("expected the wrong-household message")
	}

	// Removing an address with people at it needs a reassign target.
	body = c.post(page, page+"/addresses/"+itoa(grand)+"/delete", url.Values{}, 200)
	if !strings.Contains(body, "Choose where the people at that address go") {
		t.Error("expected the reassign message")
	}
	if _, err := st.GetAddress(grand); err != nil {
		t.Error("address removed without a reassign target")
	}
	c.post(page, page+"/addresses/"+itoa(grand)+"/delete", url.Values{"reassign": {itoa(main)}}, 200)
	if _, err := st.GetAddress(grand); err != store.ErrNotFound {
		t.Error("address not removed")
	}
	ms, _ := st.HouseholdMembers(hid)
	for _, m := range ms {
		if m.AddressID != main {
			t.Errorf("%s at %d, want %d", m.Name(), m.AddressID, main)
		}
	}
	// An address of another household can't be reached through this one.
	c.post(page, page+"/addresses/"+itoa(foreign)+"/delete", url.Values{}, 404)

	// Moving a person to the other household takes them out of this one.
	otherPage := "/admin/households/" + itoa(other)
	c.post(otherPage, otherPage+"/members", url.Values{"person_id": {itoa(gran)}, "role": {"adult"}}, 200)
	if ms, _ := st.HouseholdMembers(hid); len(ms) != 2 {
		t.Errorf("after the move: %+v", ms)
	}
	if ms, _ := st.HouseholdMembers(other); len(ms) != 1 || ms[0].ID != gran {
		t.Errorf("after the move, other: %+v", ms)
	}
	// Remove: the person stays.
	c.post(page, page+"/members/"+itoa(jane)+"/remove", url.Values{}, 200)
	if _, err := st.GetPerson(jane); err != nil {
		t.Error("removing from a household deleted the person")
	}
	if body := c.get(page, 200); !strings.Contains(body, "History") || !strings.Contains(body, "moved out of household") && !strings.Contains(body, "removed from household") {
		t.Error("history missing")
	}

	// Archive and delete (with the confirmation page); people stay.
	c.post(page, page+"/archive", url.Values{"archive": {"1"}}, 200)
	if h, _ := st.GetHousehold(hid); !h.Archived() {
		t.Error("not archived")
	}
	c.post(page, page+"/archive", url.Values{"archive": {"0"}}, 200)
	if body := c.get(page+"/delete", 200); !strings.Contains(body, "Tyler Goza") {
		t.Error("delete page should name who's in it")
	}
	c.post(page+"/delete", page+"/delete", url.Values{}, 200)
	if _, err := st.GetHousehold(hid); err != store.ErrNotFound {
		t.Error("household not deleted")
	}
	if _, err := st.GetPerson(tyler); err != nil {
		t.Error("deleting the household deleted its people")
	}
	c.get("/admin/households?show=contacts&q=smith", 200)
	c.get("/admin/households?show=archived", 200)
}
