package server

import (
	"net/url"
	"strings"
	"testing"
)

func TestFamilySuggestionsAndOwnHousehold(t *testing.T) {
	c, st := staffClient(t, Config{})
	hid, _ := st.CreateHousehold("The Lees", "", 0)
	page := "/admin/households/" + itoa(hid)
	dad, mom, kid := mustPersonID(t, st, "Dad", "Lee"), mustPersonID(t, st, "Mom", "Lee"), mustPersonID(t, st, "Kid", "Lee")
	c.post(page, page+"/members", url.Values{"person_id": {itoa(dad)}, "role": {"head"}}, 200)
	body := c.post(page, page+"/members", url.Values{"person_id": {itoa(mom)}, "role": {"head"}}, 200)
	if !strings.Contains(body, "are spouses") {
		t.Errorf("a second head should suggest a spouse link:\n%s", body)
	}
	body = c.post(page, page+"/members", url.Values{"person_id": {itoa(kid)}, "role": {"child"}}, 200)
	if !strings.Contains(body, "is a parent of Kid Lee") || strings.Count(body, `name="link"`) != 2 || !strings.Contains(body, "checked") {
		t.Errorf("a child should suggest both heads as parents:\n%s", body)
	}
	// Save the ticked ones, then no suggestion is left for the child.
	c.post(page, page+"/family-links", url.Values{"link": {"parent:" + itoa(dad) + ":" + itoa(kid), "parent:" + itoa(mom) + ":" + itoa(kid), "spouse:" + itoa(dad) + ":" + itoa(mom)}}, 200)
	if rels, _ := st.AllRelationships(); len(rels) != 3 {
		t.Fatalf("relationships = %+v", rels)
	}
	if strings.Contains(c.get(page+"?suggest="+itoa(kid), 200), `name="link"`) {
		t.Error("nothing left to suggest")
	}
	// A link for someone outside the household is ignored.
	out := mustPersonID(t, st, "Out", "Side")
	c.post(page, page+"/family-links", url.Values{"link": {"parent:" + itoa(out) + ":" + itoa(kid)}}, 200)
	if rels, _ := st.AllRelationships(); len(rels) != 3 {
		t.Errorf("a link outside the household was saved: %+v", rels)
	}
	// Cycle refused with a message.
	fam := "/admin/people/" + itoa(dad) + "/family"
	body = c.post(fam, "/admin/people/"+itoa(kid)+"/relationships", url.Values{"rel": {"child"}, "person_id": {itoa(dad)}}, 200)
	if !strings.Contains(body, "own ancestor") {
		t.Errorf("cycle should be refused:\n%s", body)
	}
	// The family page, then the kid starts a household with Dad's help: staff
	// start Dad's own household with Mom and Kid.
	body = c.get(fam, 200)
	if !strings.Contains(body, "Start their own household") || !strings.Contains(body, `name="child_id"`) {
		t.Errorf("family page:\n%s", body)
	}
	c.post(fam, "/admin/people/"+itoa(dad)+"/own-household", url.Values{"name": {"The Dad Lees"}, "spouse_id": {itoa(mom)}, "child_id": {itoa(kid)}}, 200)
	rows, _ := st.ListHouseholds("", "Dad Lees")
	if len(rows) != 1 || len(rows[0].Members) != 3 {
		t.Fatalf("new household = %+v", rows)
	}
	// Someone unrelated can't be moved this way.
	c.post(fam, "/admin/people/"+itoa(dad)+"/own-household", url.Values{"name": {"X"}, "child_id": {itoa(out)}}, 200)
	if ms, _ := st.PersonHouseholds(out); len(ms) != 0 {
		t.Error("an unrelated person was moved")
	}
	// A member sees the Family section on the person page.
	m := c.as("mel")
	if body := m.get("/people/"+itoa(kid), 200); !strings.Contains(body, "Parents") || !strings.Contains(body, "Dad Lee") {
		t.Errorf("person page lacks Family:\n%s", body)
	}
}
