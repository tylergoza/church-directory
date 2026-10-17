package server

import (
	"net/url"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

func TestStaffGroupsHiddenFromMembers(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	open, _ := st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	secret, _ := st.CreateGroup(store.GroupInput{Name: "Needs a ride", VisibleTo: store.GroupStaff}, 0)
	gone, _ := st.CreateGroup(store.GroupInput{Name: "Old Choir"}, 0)
	for _, g := range []int64{open, secret, gone} {
		if err := st.AddGroupMember(g, sd.ann, "Leader", 0); err != nil {
			t.Fatal(err)
		}
	}
	st.AddGroupMember(secret, sd.loner, "", 0)
	st.ArchiveGroup(gone, true, 0)

	mel, sam := c.as("mel"), c.as("sam")
	body := mel.get("/groups", 200)
	if !strings.Contains(body, "Singers") || strings.Contains(body, "Needs a ride") || strings.Contains(body, "Old Choir") {
		t.Errorf("member's group list is wrong:\n%s", body)
	}
	mel.get("/groups/"+itoa(open), 200)
	mel.get("/groups/"+itoa(secret), 404)
	mel.get("/groups/"+itoa(gone), 404)
	mel.get("/?group="+itoa(secret), 404)
	mel.get("/?group="+itoa(gone), 404)
	if body := mel.get("/people/"+itoa(sd.ann), 200); !strings.Contains(body, "Singers") || strings.Contains(body, "Needs a ride") {
		t.Error("a member sees only members' groups on a person's page")
	}
	// Staff see the staff group, but not an archived one.
	if body := sam.get("/groups", 200); !strings.Contains(body, "Needs a ride") || strings.Contains(body, "Old Choir") {
		t.Errorf("staff's group list is wrong:\n%s", body)
	}
	sam.get("/groups/"+itoa(secret), 200)
	sam.get("/groups/"+itoa(gone), 404)
	if body := sam.get("/people/"+itoa(sd.ann), 200); !strings.Contains(body, "Needs a ride") {
		t.Error("staff see staff groups on a person's page")
	}
	// Management is staff only.
	mel.get("/admin/groups", 403)
}

func TestGroupPageShowsOnlyWhatEachPersonShows(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	g, _ := st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	for _, id := range []int64{sd.bob, sd.ann, sd.dee, sd.penny} {
		if err := st.AddGroupMember(g, id, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	// Archived after joining: still hidden from the page.
	if _, err := st.DB.Exec(`INSERT INTO group_members (group_id, person_id, position) VALUES (?, ?, 9)`, g, sd.archie); err != nil {
		t.Fatal(err)
	}
	st.SetGroupMemberPosition(g, sd.ann, 1, 0)
	st.SetGroupMemberLabel(g, sd.ann, "Chair", 0)
	mel := c.as("mel")
	body := mel.get("/groups/"+itoa(g), 200)
	for _, hidden := range []string{bobEmail, deeEmail, archieMail, pennyMail, "Archie", "Penny"} {
		if strings.Contains(body, hidden) {
			t.Errorf("group page shows %q to a member", hidden)
		}
	}
	if !strings.Contains(body, shownEmail) || !strings.Contains(body, "Chair") {
		t.Error("Ann's shown email and her label should show")
	}
	if a, b := strings.Index(body, "Ann Lee"), strings.Index(body, "Bob Lee"); a < 0 || b < 0 || a > b {
		t.Error("people should show in position order (Ann moved first)")
	}
	if body := c.as("sam").get("/groups/"+itoa(g), 200); !strings.Contains(body, bobEmail) {
		t.Error("staff see hidden emails")
	}
}

func TestGroupFilterOnDirectory(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	g, _ := st.CreateGroup(store.GroupInput{Name: "Singers"}, 0)
	st.AddGroupMember(g, sd.loner, "", 0)
	mel := c.as("mel")
	body := mel.get("/?group="+itoa(g), 200)
	if !strings.Contains(body, "Loner Lee") || strings.Contains(body, "The Lee Family") {
		t.Errorf("filter should list only the group's people:\n%s", body)
	}
	st.AddGroupMember(g, sd.cy, "", 0)
	if body := mel.get("/?group="+itoa(g), 200); !strings.Contains(body, "The Lee Family") {
		t.Error("a household with someone in the group should be listed")
	}
	if strings.Contains(mel.get("/?group="+itoa(g), 200), cyEmail) {
		t.Error("filtered directory leaks a hidden email")
	}
}

func TestAdminGroupsFlow(t *testing.T) {
	c, st := staffClient(t, Config{})
	sd := seedDirectory(t, st)
	c.post("/admin/groups/new", "/admin/groups", url.Values{"name": {"Drama Team"}, "visible_to": {"members"}}, 200)
	gs, _ := st.ListGroups(false)
	if len(gs) != 1 {
		t.Fatalf("groups = %+v", gs)
	}
	g := gs[0].ID
	page := "/admin/groups/" + itoa(g)
	c.post(page, page+"/members", url.Values{"person_id": {itoa(sd.ann)}, "label": {"Leader"}}, 200)
	c.post(page, page+"/members", url.Values{"person_id": {itoa(sd.loner)}}, 200)
	// A contact can't be added: the page comes back with the reason.
	if body := c.post(page, page+"/members", url.Values{"person_id": {itoa(sd.contact)}}, 200); !strings.Contains(body, "can't") {
		t.Error("adding a contact should say why not")
	}
	ms, _ := st.GroupMembersOf(g)
	if len(ms) != 2 || ms[0].Person.ID != sd.ann {
		t.Fatalf("members = %+v", ms)
	}
	c.post(page, page+"/members/"+itoa(sd.loner)+"/move", url.Values{"dir": {"up"}}, 200)
	if ms, _ := st.GroupMembersOf(g); ms[0].Person.ID != sd.loner {
		t.Error("move up didn't move")
	}
	c.post(page, page+"/members/"+itoa(sd.ann), url.Values{"label": {"Chair"}, "position": {"1"}}, 200)
	if ms, _ := st.GroupMembersOf(g); ms[0].Person.ID != sd.ann || ms[0].Label != "Chair" {
		t.Errorf("save label/position: %+v", ms)
	}
	if body := c.get(page+"?q=loner", 200); !strings.Contains(body, "Search") {
		t.Error("edit page with search")
	}
	c.post(page, page+"/members/"+itoa(sd.loner)+"/remove", url.Values{}, 200)
	c.post(page, page+"/archive", url.Values{"archive": {"1"}}, 200)
	c.as("mel").get("/groups/"+itoa(g), 404)
}
