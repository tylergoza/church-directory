package store

import (
	"errors"
	"testing"
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
)

func TestHeadsMayEditLock(t *testing.T) {
	st := openTest(t)
	p := mustPerson(t, st, "Amy", "Lee", "", "")
	if err := st.SetHeadsMayEditSelf(p, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.StaffSetHeadsMayEdit(p, false, true, "", 0); !errors.Is(err, ErrNeedReason) {
		t.Errorf("lock without a reason = %v", err)
	}
	if err := st.StaffSetHeadsMayEdit(p, false, true, "safety", 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHeadsMayEditSelf(p, true, 0); !errors.Is(err, ErrHeadsLocked) {
		t.Errorf("person turned on a locked-off setting = %v", err)
	}
	if got, _ := st.GetPerson(p); got.HeadsMayEdit || !got.HeadsMayEditLocked {
		t.Errorf("person = %+v", got)
	}
	if err := st.StaffSetHeadsMayEdit(p, false, false, "", 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetPerson(p); got.HeadsMayEditLocked || got.HeadsMayEditReason != "" {
		t.Errorf("unlocked = %+v", got)
	}
}

func TestMultiHouseholdAndJoinRequests(t *testing.T) {
	st := openTest(t)
	kid, _ := st.CreatePerson(PersonInput{FirstName: "Kid", LastName: "Lee", Birth: dates.Partial{Year: time.Now().Year() - 6}}, 0)
	adult := mustPerson(t, st, "Gran", "Lee", "", "")
	a, _ := st.CreateHousehold("A", "", 0)
	b, _ := st.CreateHousehold("B", "", 0)
	st.PlaceMember(a, kid, RoleChild, 0, 0)
	st.PlaceMember(a, adult, RoleAdult, 0, 0)

	if err := st.AddSecondHousehold(b, kid, 0, 0); !errors.Is(err, ErrNotMulti) {
		t.Errorf("without the tick = %v", err)
	}
	if err := st.SetMultiHousehold(adult, true, MultiOther, 0); !errors.Is(err, ErrChildOnly) {
		t.Errorf("adult tick = %v", err)
	}
	if err := st.SetMultiHousehold(kid, true, "bogus", 0); !errors.Is(err, ErrBadReason) {
		t.Errorf("bad reason = %v", err)
	}
	if err := st.SetMultiHousehold(kid, true, MultiDivorced, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSecondHousehold(b, adult, 0, 0); err == nil {
		t.Error("an adult was added to a second household")
	}
	id, err := st.CreateJoinRequest(kid, b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateJoinRequest(kid, b, 0); !errors.Is(err, ErrRequestOpen) {
		t.Errorf("duplicate request = %v", err)
	}
	if err := st.AnswerJoinRequest(id, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AnswerJoinRequest(id, false, 0); !errors.Is(err, ErrRequestClosed) {
		t.Errorf("answered twice = %v", err)
	}
	if hs, _ := st.PersonHouseholds(kid); len(hs) != 2 {
		t.Fatalf("households = %+v", hs)
	}
	if err := st.SetMultiHousehold(kid, false, "", 0); !errors.Is(err, ErrStillMulti) {
		t.Errorf("untick while in two = %v", err)
	}
	if err := st.SetPrimaryHousehold(kid, b, 0); err != nil {
		t.Fatal(err)
	}
	primary := 0
	for _, h := range mustHH(t, st, kid) {
		if h.Primary {
			primary++
			if h.HouseholdID != b {
				t.Error("wrong primary")
			}
		}
	}
	if primary != 1 {
		t.Errorf("%d primaries", primary)
	}
}

func mustHH(t *testing.T, st *Store, p int64) []PersonHousehold {
	t.Helper()
	hs, err := st.PersonHouseholds(p)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestConvertContact(t *testing.T) {
	st := openTest(t)
	hank := mustPerson(t, st, "Hank", "Lee", "", "")
	pat, _ := st.CreatePerson(PersonInput{FirstName: "Pat", LastName: "Other", Kind: KindContact}, 0)
	h, err := st.CreateManagedHousehold("Pat's", KindContact, hank, 0)
	if err != nil {
		t.Fatal(err)
	}
	st.PlaceMember(h, pat, RoleAdult, 0, 0)
	if err := st.ConvertContact(h, pat, 0); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetHousehold(h)
	if got.Kind != KindMember || got.ManagedBy != 0 {
		t.Errorf("household = %+v", got)
	}
	if p, _ := st.GetPerson(pat); p.Kind != KindMember {
		t.Errorf("person = %+v", p)
	}
	if hs := mustHH(t, st, pat); len(hs) != 1 || hs[0].Role != RoleHead {
		t.Errorf("memberships = %+v", hs)
	}
}
