package server

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
	"github.com/tylergoza/church-directory/internal/store"
)

// seeded is a household and a few people with an email address each that
// is meant to be shown or hidden, so a page can be searched for the ones
// that must not be there.
type seeded struct {
	st                                               *store.Store
	household                                        int64
	ann, bob, cy, dee, contact, archie, penny, loner int64
	pennyCreator                                     int64
}

const (
	shownEmail  = "ann-shown@example.org"
	bobEmail    = "bob-own-hidden@example.org"
	cyEmail     = "cy-child-default@example.org"
	deeEmail    = "dee-forced@example.org"
	contactMail = "contact-never@example.org"
	archieMail  = "archie-archived@example.org"
	pennyMail   = "penny-pending@example.org"
	secretAddr  = "9 Secret Lane"
)

func seedDirectory(t *testing.T, st *store.Store) seeded {
	t.Helper()
	sd := seeded{st: st}
	person := func(first, email string, kind string) int64 {
		id, err := st.CreatePerson(store.PersonInput{FirstName: first, LastName: "Lee", Email: email, Phone: "", Kind: kind,
			Anniversary: dates.Partial{}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sd.ann = person("Ann", shownEmail, store.KindMember)
	sd.bob = person("Bob", bobEmail, store.KindMember)
	sd.cy = person("Cy", cyEmail, store.KindMember)
	sd.dee = person("Dee", deeEmail, store.KindMember)
	sd.contact = person("Conny", contactMail, store.KindContact)
	sd.archie = person("Archie", archieMail, store.KindMember)
	sd.penny = person("Penny", pennyMail, store.KindMember)
	sd.loner = person("Loner", "", store.KindMember)
	var err error
	if sd.household, err = st.CreateHousehold("The Lee Family", "", 0); err != nil {
		t.Fatal(err)
	}
	main, _ := st.AddAddress(sd.household, store.AddressInput{Label: "Main", Address1: "1 Oak St", City: "Springfield"}, 0)
	secret, _ := st.AddAddress(sd.household, store.AddressInput{Label: "Other", Address1: secretAddr, City: "Springfield"}, 0)
	f := false
	if err := st.SetAddressVisibility(secret, &f, false, 0); err != nil {
		t.Fatal(err)
	}
	place := func(id int64, role string, addr int64) {
		if err := st.PlaceMember(sd.household, id, role, addr, 0); err != nil {
			t.Fatal(err)
		}
	}
	place(sd.ann, store.RoleHead, main)
	place(sd.bob, store.RoleHead, main)
	place(sd.cy, store.RoleChild, main)
	place(sd.dee, store.RoleAdult, secret)
	place(sd.contact, store.RoleAdult, 0)
	place(sd.archie, store.RoleAdult, 0)
	place(sd.penny, store.RoleAdult, 0)
	if err := st.SetPersonFieldSettings(sd.bob, map[store.Field]store.FieldSetting{store.FieldEmail: {Shown: &f}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPersonFieldSettings(sd.dee, map[store.Field]store.FieldSetting{store.FieldEmail: {ForceHidden: true}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchivePerson(sd.archie, true, 0); err != nil {
		t.Fatal(err)
	}
	// Penny is waiting for staff; nobody who made her is signed in here.
	if _, err := st.DB.Exec(`UPDATE people SET pending = 1, pending_reason = 'name_match' WHERE id = ?`, sd.penny); err != nil {
		t.Fatal(err)
	}
	// Both heads share an anniversary; Ann's birthday is this month.
	anniv := dates.Partial{Month: 6, Day: 5, Year: 2001}
	for _, id := range []int64{sd.ann, sd.bob} {
		p, _ := st.GetPerson(id)
		in := store.PersonInput{FirstName: p.FirstName, LastName: p.LastName, Email: p.Email, Kind: p.Kind, Anniversary: anniv}
		if id == sd.ann {
			in.Birth = dates.Partial{Month: int(time.Now().Month()), Day: 15}
		}
		if err := st.UpdatePerson(id, in, 0); err != nil {
			t.Fatal(err)
		}
	}
	return sd
}

// hiddenStrings must never appear for an ordinary member.
var hiddenStrings = []string{bobEmail, cyEmail, deeEmail, contactMail, archieMail, pennyMail, secretAddr, "Conny", "Archie", "Penny"}

func (sd seeded) pages() []string {
	return []string{"/", "/people/" + itoa(sd.ann), "/people/" + itoa(sd.bob), "/people/" + itoa(sd.cy), "/people/" + itoa(sd.dee),
		"/households/" + itoa(sd.household), "/birthdays", "/?q=lee"}
}

func TestHiddenFieldsNeverReachMemberPages(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	mel := c.as("mel")
	for _, page := range sd.pages() {
		body := mel.get(page, 200)
		for _, secret := range hiddenStrings {
			if strings.Contains(body, secret) {
				t.Errorf("%s shows %q to a member", page, secret)
			}
		}
	}
	// What is shown does show, so the checks above mean something.
	if body := mel.get("/households/"+itoa(sd.household), 200); !strings.Contains(body, shownEmail) || !strings.Contains(body, "1 Oak St") {
		t.Error("the household page should show Ann's email and the shown address")
	}
	if body := mel.get("/people/"+itoa(sd.ann), 200); !strings.Contains(body, shownEmail) {
		t.Error("Ann's page should show her email")
	}
	// Not visible at all: 404, never 403.
	for _, id := range []int64{sd.contact, sd.archie, sd.penny, 9999} {
		mel.get("/people/"+itoa(id), 404)
	}
	mel.get("/households/9999", 404)
	// Staff see everything the person pages hold.
	sam := c.as("sam")
	for _, id := range []int64{sd.contact, sd.archie, sd.penny} {
		sam.get("/people/"+itoa(id), 200)
	}
	if body := sam.get("/people/"+itoa(sd.dee), 200); !strings.Contains(body, deeEmail) {
		t.Error("staff should see a force-hidden email")
	}
	if body := sam.get("/households/"+itoa(sd.household), 200); !strings.Contains(body, secretAddr) || !strings.Contains(body, cyEmail) {
		t.Error("staff should see hidden addresses and a child's hidden email")
	}
}

func TestDirectoryPageListsNoContactsOrArchived(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	for _, who := range []*client{c, c.as("mel")} {
		body := who.get("/", 200)
		for _, name := range []string{"Conny Lee", "Archie Lee"} {
			if strings.Contains(body, name) {
				t.Errorf("the directory page lists %q", name)
			}
		}
		for _, name := range []string{"The Lee Family", "Ann Lee", "Loner Lee"} {
			if !strings.Contains(body, name) {
				t.Errorf("the directory page is missing %q", name)
			}
		}
	}
	_ = sd
}

func TestPendingPersonVisibleOnlyToCreatorAndStaff(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	nan, _ := st.CreateUser("nan", "Nan", "a long password", store.RoleMember)
	mel, _ := st.CreateUser("mel2", "Mel2", "a long password", store.RoleMember)
	if _, err := st.DB.Exec(`UPDATE people SET created_by = ? WHERE id = ?`, nan, sd.penny); err != nil {
		t.Fatal(err)
	}
	_ = mel
	c.as("nan").get("/people/"+itoa(sd.penny), 200)
	c.as("sam").get("/people/"+itoa(sd.penny), 200)
	c.as("mel").get("/people/"+itoa(sd.penny), 404)
	if body := c.as("mel").get("/", 200); strings.Contains(body, "Penny") {
		t.Error("someone else's pending person is listed")
	}
}

func TestHeadsAndTheLinkedPersonSeeEverythingAboutThem(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	hank, _ := st.CreateUser("hank", "Hank", "a long password", store.RoleMember)
	if _, err := st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, hank, sd.ann); err != nil {
		t.Fatal(err)
	}
	body := c.as("hank").get("/households/"+itoa(sd.household), 200)
	for _, want := range []string{bobEmail, cyEmail, deeEmail, secretAddr} {
		if !strings.Contains(body, want) {
			t.Errorf("a head of the household should see %q", want)
		}
	}
	// The contact is in the head's household, so the head may open them too.
	c.as("hank").get("/people/"+itoa(sd.contact), 200)
	// A head of another household doesn't.
	other, _ := st.CreateHousehold("The Other Family", "", 0)
	zed := mustPersonID(t, st, "Zed", "Other")
	st.PlaceMember(other, zed, store.RoleHead, 0, 0)
	zuid, _ := st.CreateUser("zed", "Zed", "a long password", store.RoleMember)
	st.DB.Exec(`UPDATE people SET user_id = ? WHERE id = ?`, zuid, zed)
	if body := c.as("zed").get("/households/"+itoa(sd.household), 200); strings.Contains(body, deeEmail) || strings.Contains(body, bobEmail) {
		t.Error("a head of another household sees hidden fields")
	}
	c.as("zed").get("/people/"+itoa(sd.contact), 404)
}

func TestSharedAnniversaryShowsOnceOnTheCard(t *testing.T) {
	c, st := staffClient(t, Config{})
	seedDirectory(t, st)
	body := c.as("mel").get("/", 200)
	if n := strings.Count(body, "June 5, 2001"); n != 1 {
		t.Errorf("shared anniversary shown %d times on the card", n)
	}
}

func TestBirthdaysPage(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	body := c.as("mel").get("/birthdays", 200)
	month := dates.MonthName(int(time.Now().Month()))
	if !strings.Contains(body, "Ann Lee") || !strings.Contains(body, month) {
		t.Errorf("Ann's birthday should be listed in %s", month)
	}
	// Hide her month: she drops off the page.
	f := false
	st.SetPersonFieldSettings(sd.ann, map[store.Field]store.FieldSetting{store.FieldBirthMonth: {Shown: &f}}, 0)
	if body := c.as("mel").get("/birthdays", 200); strings.Contains(body, "day-list__day\">15") {
		t.Error("a hidden birthday is listed")
	}
}

func TestDefaultsPageAdminOnlyAndChangesWhatShows(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	st.CreateUser("ada", "Ada", "a long password", store.RoleAdmin)
	c.as("sam").get("/admin/defaults", 403)
	c.as("mel").get("/admin/defaults", 403)
	ada := c.as("ada")
	body := ada.get("/admin/defaults", 200)
	if !strings.Contains(body, `name="child:email"`) || strings.Contains(body, `name="child:anniversary.day"`) {
		t.Error("children's column only for the fields that apply")
	}
	mel := c.as("mel")
	if strings.Contains(mel.get("/households/"+itoa(sd.household), 200), cyEmail) {
		t.Fatal("child email shows before the default changes")
	}
	form := url.Values{"child:email": {"1"}}
	ada.post("/admin/defaults", "/admin/defaults", form, 200)
	if !strings.Contains(mel.get("/households/"+itoa(sd.household), 200), cyEmail) {
		t.Error("the child default didn't take")
	}
	d, _ := st.LoadDefaults()
	if !d.Shown(store.AudienceChild, store.FieldEmail) || !d.Shown(store.AudienceAdult, store.FieldEmail) {
		t.Errorf("defaults = %v", d)
	}
	changes, _ := st.RecentChanges(0, 0, 5)
	_ = changes
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM changes WHERE what LIKE 'changed default for children%'`).Scan(&n)
	if n != 1 {
		t.Errorf("the change log has %d default rows", n)
	}
	// A member can't post it.
	mel.post("/account", "/admin/defaults", url.Values{}, 403)
}

func TestStaffForceHideAndAddressVisibility(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	sam, mel := c.as("sam"), c.as("mel")
	// The edit page lists the fields.
	if body := sam.get("/admin/people/"+itoa(sd.ann), 200); !strings.Contains(body, `name="force:email"`) || !strings.Contains(body, `name="shown:birthday.month"`) {
		t.Error("the person page should have the visibility form")
	}
	form := url.Values{"force:email": {"1"}}
	sam.post("/admin/people/"+itoa(sd.ann), "/admin/people/"+itoa(sd.ann)+"/visibility", form, 200)
	if strings.Contains(mel.get("/people/"+itoa(sd.ann), 200), shownEmail) {
		t.Error("a force-hidden email shows to a member")
	}
	set, _ := st.PersonFieldSettings(sd.ann)
	if !set[store.FieldEmail].ForceHidden {
		t.Error("force hide not saved")
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM changes WHERE person_id = ? AND what = 'force-hid email'`, sd.ann).Scan(&n)
	if n != 1 {
		t.Errorf("force hide logged %d times", n)
	}
	// An address: hide it from members.
	addrs, _ := st.ListAddresses(sd.household)
	main := addrs[0]
	sam.post("/admin/households/"+itoa(sd.household), "/admin/households/"+itoa(sd.household)+"/addresses/"+itoa(main.ID)+"/visibility",
		url.Values{"shown": {"0"}}, 200)
	if strings.Contains(mel.get("/households/"+itoa(sd.household), 200), "1 Oak St") {
		t.Error("a hidden address shows")
	}
	// The household photo setting saves.
	sam.post("/admin/households/"+itoa(sd.household), "/admin/households/"+itoa(sd.household)+"/photo-visibility",
		url.Values{"force": {"1"}}, 200)
	hs, _ := st.AllHouseholdSettings()
	if !hs[sd.household][store.FieldHouseholdPhoto].ForceHidden {
		t.Error("household photo force hide not saved")
	}
	// Members can't.
	mel.post("/account", "/admin/people/"+itoa(sd.ann)+"/visibility", url.Values{}, 403)
}

func TestSearchAndAZ(t *testing.T) {
	c, st := staffClient(t, Config{})
	seedDirectory(t, st)
	mel := c.as("mel")
	body := mel.get("/?q=loner", 200)
	if !strings.Contains(body, "Loner Lee") || strings.Contains(body, "The Lee Family") {
		t.Error("search should narrow to Loner")
	}
	if body := mel.get("/", 200); !strings.Contains(body, `href="#letter-L"`) {
		t.Error("the A-Z bar should link to L")
	}
}
