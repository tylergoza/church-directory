package store

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tylergoza/church-directory/internal/dates"
)

// CSV import (step 13). Three stages, and only the last writes anything:
//
//	ParseImport  reads the file into rows (no database)
//	MatchImport  finds same-name people already here and earlier in the file
//	BuildPlan    turns the rows and staff's choices into planned households
//	ApplyImport  writes the plan in ONE transaction
//
// BuildPlan is pure, so the preview and the import can't disagree.

const (
	// ImportMaxBytes is the biggest CSV accepted.
	ImportMaxBytes = 2 << 20
	// ImportMaxRows is the most data rows in one file.
	ImportMaxRows = 2000
)

// ImportFields are the columns the import knows, with the header names
// (lower case, spaces and dashes as underscores) each answers to.
var ImportFields = []struct {
	Field, Help string
	Aliases     []string
}{
	{"first", "First (legal) name. Required.", []string{"first", "first_name", "firstname", "given", "given_name"}},
	{"last", "Last name. Required.", []string{"last", "last_name", "lastname", "surname", "family_name"}},
	{"goes_by", "The name they go by, if not the first name.", []string{"goes_by", "goesby", "nickname", "preferred", "preferred_name"}},
	{"suffix", "Jr, Sr, III and so on. Counts when matching names.", []string{"suffix"}},
	{"email", "Email address.", []string{"email", "e_mail", "email_address"}},
	{"phone", "Phone number.", []string{"phone", "phone_number", "mobile", "cell"}},
	{"birthday", "2000-03-14, 3/14/2000, 3/14, March 14 or just 2000.", []string{"birthday", "birth", "birthdate", "birth_date", "dob"}},
	{"anniversary", "Same formats as birthday.", []string{"anniversary", "anniv", "wedding"}},
	{"household", "Household name. Rows with the same name share a household. Optional.", []string{"household", "household_name", "family"}},
	{"role", "head, adult or child. Blank: adult, or head for the first adult of the household.", []string{"role", "household_role"}},
	{"address1", "Street address.", []string{"address1", "address_1", "address", "street"}},
	{"address2", "Apartment, unit.", []string{"address2", "address_2", "unit"}},
	{"city", "City.", []string{"city", "town"}},
	{"state", "State.", []string{"state", "province"}},
	{"postal", "Postal or ZIP code.", []string{"postal", "postal_code", "zip", "zip_code", "postcode"}},
	{"kind", "member (default) or contact.", []string{"kind", "type"}},
}

// ImportColumn is a header that was recognised.
type ImportColumn struct{ Field, Header string }

// ImportRow is one line of the file.
type ImportRow struct {
	Index           int // position in the file, from 0
	Line            int // line number in the file, for messages
	Person          PersonInput
	Household, Role string
	Addr            AddressInput
	Errors          []string
	ExistingMatches []Person // people already in the directory with this name
	FileMatches     []int    // Index of earlier rows with this name
}

// Name is how the row's person shows in the preview.
func (r *ImportRow) Name() string {
	return DisplayName(r.Person.FirstName, r.Person.LastName, r.Person.GoesBy, r.Person.Suffix)
}

// HasAddress reports whether the row has any address part.
func (r *ImportRow) HasAddress() bool {
	a := r.Addr
	return a.Address1 != "" || a.Address2 != "" || a.City != "" || a.State != "" || a.Postal != ""
}

// ImportFile is a parsed CSV.
type ImportFile struct {
	Rows    []*ImportRow
	Mapped  []ImportColumn
	Ignored []string // headers nothing was made of
}

// ParseImport reads a CSV. Headers are matched by name without regard to
// case; unknown columns are listed in Ignored. Problems with a row go in
// that row's Errors; the error returned is for a file that can't be used
// at all.
func ParseImport(r io.Reader) (*ImportFile, error) {
	data, err := io.ReadAll(io.LimitReader(r, ImportMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > ImportMaxBytes {
		return nil, fmt.Errorf("That file is too large. The most is %d MB.", ImportMaxBytes>>20)
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("That file is empty.")
	} else if err != nil {
		return nil, fmt.Errorf("That doesn't look like a CSV file: %v", err)
	}
	f := &ImportFile{}
	col := map[string]int{} // field -> column
	for i, h := range header {
		field := importField(h)
		if field == "" || has(col, field) {
			if strings.TrimSpace(h) != "" {
				f.Ignored = append(f.Ignored, strings.TrimSpace(h))
			}
			continue
		}
		col[field] = i
		f.Mapped = append(f.Mapped, ImportColumn{Field: field, Header: strings.TrimSpace(h)})
	}
	if !has(col, "first") || !has(col, "last") {
		return nil, errors.New("The first row must have the headers, and they need at least a first and a last column.")
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Problem reading the file: %v", err)
		}
		line, _ := cr.FieldPos(0)
		get := func(field string) string {
			if i, ok := col[field]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		blank := true
		for _, v := range rec {
			if strings.TrimSpace(v) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		if len(f.Rows) >= ImportMaxRows {
			return nil, fmt.Errorf("That file has more than %d people. Split it and import it in parts.", ImportMaxRows)
		}
		row := &ImportRow{Index: len(f.Rows), Line: line}
		row.Person = PersonInput{
			FirstName: get("first"), LastName: get("last"), GoesBy: get("goes_by"), Suffix: get("suffix"),
			Email: get("email"), Phone: get("phone"), Kind: strings.ToLower(get("kind")),
		}
		var derrs []string
		var perr error
		if row.Person.Birth, perr = dates.ParseLoose(get("birthday")); perr != nil {
			derrs = append(derrs, "Birthday: "+perr.Error()+".")
		}
		if row.Person.Anniversary, perr = dates.ParseLoose(get("anniversary")); perr != nil {
			derrs = append(derrs, "Anniversary: "+perr.Error()+".")
		}
		row.Errors = append(row.Errors, derrs...)
		// Person.Validate re-checks dates, which are fine or empty here.
		row.Errors = append(row.Errors, row.Person.Validate()...)
		row.Household = get("household")
		if len(row.Household) > 200 {
			row.Errors = append(row.Errors, "Household name is too long.")
		}
		row.Role = strings.ToLower(get("role"))
		if row.Role != "" && !ValidHouseholdRole(row.Role) {
			row.Errors = append(row.Errors, "Role must be head, adult or child.")
		}
		row.Addr = AddressInput{Address1: get("address1"), Address2: get("address2"), City: get("city"), State: get("state"), Postal: get("postal")}
		if row.HasAddress() {
			row.Errors = append(row.Errors, row.Addr.Validate()...)
		}
		f.Rows = append(f.Rows, row)
	}
	if len(f.Rows) == 0 {
		return nil, errors.New("That file has headers but no people.")
	}
	return f, nil
}

func has(m map[string]int, k string) bool { _, ok := m[k]; return ok }

func importField(header string) string {
	h := strings.ToLower(strings.TrimSpace(header))
	h = strings.NewReplacer(" ", "_", "-", "_", ".", "").Replace(h)
	for _, f := range ImportFields {
		for _, a := range f.Aliases {
			if a == h {
				return f.Field
			}
		}
	}
	return ""
}

// MatchImport fills in each row's matches against the people already in
// the directory (archived and contacts too) and the rows before it in the
// file, all through NameKey.Matches. It reads only.
func (s *Store) MatchImport(f *ImportFile) error {
	rows, err := s.DB.Query(`SELECT ` + personCols + ` FROM people ORDER BY last_name COLLATE NOCASE, first_name COLLATE NOCASE, id`)
	if err != nil {
		return err
	}
	var all []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return err
		}
		all = append(all, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	keys := make([]NameKey, len(f.Rows))
	for i, r := range f.Rows {
		keys[i] = NewNameKey(r.Person.FirstName, r.Person.LastName, r.Person.GoesBy, r.Person.Suffix)
		r.ExistingMatches, r.FileMatches = nil, nil
		if r.Person.FirstName == "" || r.Person.LastName == "" {
			continue
		}
		for _, p := range all {
			if keys[i].Matches(p.NameKey()) {
				r.ExistingMatches = append(r.ExistingMatches, p)
			}
		}
		for j := 0; j < i; j++ {
			if keys[i].Matches(keys[j]) {
				r.FileMatches = append(r.FileMatches, j)
			}
		}
	}
	return nil
}

// What to do with a row.
const (
	ImportAdd      = "add"
	ImportSkip     = "skip"
	ImportExisting = "existing" // with ImportChoice.PersonID
)

// ImportChoice is staff's decision for one row.
type ImportChoice struct {
	Action   string
	PersonID int64
}

// Value is the choice as a form value: add, skip or existing:12.
func (c ImportChoice) Value() string {
	if c.Action == ImportExisting {
		return ImportExisting + ":" + strconv.FormatInt(c.PersonID, 10)
	}
	return c.Action
}

// ParseImportChoice reads Value back. Anything unreadable is skip.
func ParseImportChoice(v string) ImportChoice {
	switch a, id, _ := strings.Cut(v, ":"); a {
	case ImportAdd:
		return ImportChoice{Action: ImportAdd}
	case ImportExisting:
		if n, err := strconv.ParseInt(id, 10, 64); err == nil && n > 0 {
			return ImportChoice{Action: ImportExisting, PersonID: n}
		}
	}
	return ImportChoice{Action: ImportSkip}
}

// DefaultImportChoice: a row with errors is skipped; one that matches
// someone here is linked to the first match; one that only matches an
// earlier row is skipped as a duplicate; anything else is added.
func DefaultImportChoice(r *ImportRow) ImportChoice {
	switch {
	case len(r.Errors) > 0:
		return ImportChoice{Action: ImportSkip}
	case len(r.ExistingMatches) > 0:
		return ImportChoice{Action: ImportExisting, PersonID: r.ExistingMatches[0].ID}
	case len(r.FileMatches) > 0:
		return ImportChoice{Action: ImportSkip}
	}
	return ImportChoice{Action: ImportAdd}
}

// PlanMember is one person in a planned household.
type PlanMember struct {
	Row      *ImportRow
	Role     string
	Addr     int   // index into the household's Addresses, -1 for none
	PersonID int64 // an existing person, 0 for one to be created
}

// PlanHousehold is a household the import will make.
type PlanHousehold struct {
	Name, Kind string
	Addresses  []AddressInput
	Members    []PlanMember
}

// ImportPlan is everything an import would write.
type ImportPlan struct {
	Households []PlanHousehold
	// Created are the rows that become new people; Linked are rows that use
	// someone already here (placed in a household only when the row says
	// which household).
	Created, Linked, Skipped int
	Problems                 []string
	creates                  []*ImportRow
	linked                   []ImportLink
}

// ImportLink is a row using an existing person.
type ImportLink struct {
	Row      *ImportRow
	PersonID int64
}

// OK reports whether the plan can be imported.
func (p ImportPlan) OK() bool { return len(p.Problems) == 0 }

func addrKey(a AddressInput) string {
	return Fold(strings.Join([]string{a.Address1, a.Address2, a.City, a.State, a.Postal}, "|"))
}

// BuildPlan groups the rows into households and checks the choices. Rows
// with the same household name share a household; with no name, rows with
// the same address do; otherwise a person is a household of one. A
// household with several different addresses gets all of them and each
// member the one on their row. choices is indexed like f.Rows; a missing
// choice is the default.
func BuildPlan(f *ImportFile, choices []ImportChoice) ImportPlan {
	var plan ImportPlan
	type group struct {
		name    string
		members []*ImportRow
		ids     []int64
	}
	var groups []*group
	byKey := map[string]*group{}
	for i, r := range f.Rows {
		c := DefaultImportChoice(r)
		if i < len(choices) {
			c = choices[i]
		}
		switch c.Action {
		case ImportSkip:
			plan.Skipped++
			continue
		case ImportAdd:
		case ImportExisting:
			ok := false
			for _, m := range r.ExistingMatches {
				ok = ok || m.ID == c.PersonID
			}
			if !ok {
				plan.Problems = append(plan.Problems, fmt.Sprintf("Line %d: that isn't one of the people who match this name.", r.Line))
				continue
			}
		default:
			plan.Problems = append(plan.Problems, fmt.Sprintf("Line %d: unknown choice.", r.Line))
			continue
		}
		if len(r.Errors) > 0 {
			plan.Problems = append(plan.Problems, fmt.Sprintf("Line %d (%s): fix the file or skip this row.", r.Line, r.Name()))
			continue
		}
		var key string
		switch {
		case r.Household != "":
			key = "n:" + Fold(r.Household)
		case r.HasAddress():
			key = "a:" + addrKey(r.Addr)
		default:
			key = "r:" + strconv.Itoa(i)
		}
		if c.Action == ImportExisting {
			plan.Linked++
			plan.linked = append(plan.linked, ImportLink{Row: r, PersonID: c.PersonID})
			if key[0] == 'r' {
				continue // nothing says where they live: leave their household alone
			}
		} else {
			plan.Created++
			plan.creates = append(plan.creates, r)
		}
		g := byKey[key]
		if g == nil {
			g = &group{}
			byKey[key] = g
			groups = append(groups, g)
		}
		if g.name == "" {
			g.name = r.Household
		}
		g.members = append(g.members, r)
		if c.Action == ImportExisting {
			g.ids = append(g.ids, c.PersonID)
		} else {
			g.ids = append(g.ids, 0)
		}
	}
	for _, g := range groups {
		h := PlanHousehold{Name: g.name, Kind: KindContact}
		if h.Name == "" {
			h.Name = "The " + g.members[0].Person.LastName + " family"
		}
		hasHead := false
		for _, r := range g.members {
			hasHead = hasHead || r.Role == RoleHead
			if r.Person.Kind != KindContact {
				h.Kind = KindMember
			}
		}
		addrAt := map[string]int{}
		for i, r := range g.members {
			role := r.Role
			if role == "" {
				role = RoleAdult
				if !hasHead {
					role, hasHead = RoleHead, true
				}
			}
			m := PlanMember{Row: r, Role: role, Addr: -1, PersonID: g.ids[i]}
			if r.HasAddress() {
				k := addrKey(r.Addr)
				idx, ok := addrAt[k]
				if !ok {
					idx = len(h.Addresses)
					addrAt[k] = idx
					h.Addresses = append(h.Addresses, r.Addr)
				}
				m.Addr = idx
			}
			h.Members = append(h.Members, m)
		}
		plan.Households = append(plan.Households, h)
	}
	return plan
}

// ErrImportProblems: the plan has rows that must be fixed or skipped.
var ErrImportProblems = errors.New("some rows need fixing or skipping before the import")

// ImportResult says what an import wrote.
type ImportResult struct {
	People, Linked, Households, Addresses int
}

// ApplyImport writes the plan in one transaction: if anything fails,
// nothing is written. Every new person and household gets an audit row
// ("imported person", "imported household").
func (s *Store) ApplyImport(plan ImportPlan, by int64) (ImportResult, error) {
	var res ImportResult
	if !plan.OK() {
		return res, ErrImportProblems
	}
	err := s.inTx(func(tx *sql.Tx) error {
		res = ImportResult{}
		ids := map[*ImportRow]int64{}
		for _, r := range plan.creates {
			in := r.Person
			in.clean()
			out, err := tx.Exec(`
				INSERT INTO people (first_name, last_name, goes_by, suffix,
					birth_month, birth_day, birth_year, anniv_month, anniv_day, anniv_year,
					email, phone, kind, created_by)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				in.FirstName, in.LastName, in.GoesBy, in.Suffix,
				dateArg(in.Birth.Month), dateArg(in.Birth.Day), dateArg(in.Birth.Year),
				dateArg(in.Anniversary.Month), dateArg(in.Anniversary.Day), dateArg(in.Anniversary.Year),
				in.Email, in.Phone, in.Kind, nullInt(by))
			if err != nil {
				return err
			}
			id, err := out.LastInsertId()
			if err != nil {
				return err
			}
			ids[r] = id
			res.People++
			if err := logChange(tx, by, id, 0, "imported person"); err != nil {
				return err
			}
		}
		for _, l := range plan.linked {
			var one int64
			if err := tx.QueryRow(`SELECT id FROM people WHERE id = ?`, l.PersonID).Scan(&one); err != nil {
				return notFound(err)
			}
			ids[l.Row] = l.PersonID
			res.Linked++
		}
		for _, h := range plan.Households {
			out, err := tx.Exec(`INSERT INTO households (name, kind) VALUES (?, ?)`, h.Name, h.Kind)
			if err != nil {
				return err
			}
			hid, err := out.LastInsertId()
			if err != nil {
				return err
			}
			res.Households++
			if err := logChange(tx, by, 0, hid, "imported household"); err != nil {
				return err
			}
			addrIDs := make([]int64, len(h.Addresses))
			for i, a := range h.Addresses {
				out, err := tx.Exec(`
					INSERT INTO household_addresses (household_id, label, address1, address2, city, state, postal, position)
					VALUES (?, '', ?, ?, ?, ?, ?, ?)`, hid, a.Address1, a.Address2, a.City, a.State, a.Postal, i+1)
				if err != nil {
					return err
				}
				if addrIDs[i], err = out.LastInsertId(); err != nil {
					return err
				}
				res.Addresses++
				if err := logChange(tx, by, 0, hid, "imported address"); err != nil {
					return err
				}
			}
			for pos, m := range h.Members {
				pid := ids[m.Row]
				var addr any
				if m.Addr >= 0 {
					addr = addrIDs[m.Addr]
				}
				if m.PersonID != 0 { // an existing person moves here, as in PlaceMember
					if err := leaveOtherHouseholds(tx, pid, hid, m.Role, by); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(`
					INSERT INTO household_members (household_id, person_id, role, address_id, position)
					VALUES (?, ?, ?, ?, ?)`, hid, pid, m.Role, addr, pos+1); err != nil {
					return err
				}
				if err := logChange(tx, by, pid, hid, "added to household as "+m.Role+" by import"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// leaveOtherHouseholds takes an existing person out of their other
// households, the way PlaceMember does: a child who lives in more than one
// household keeps their other child places.
func leaveOtherHouseholds(tx *sql.Tx, personID, keep int64, role string, by int64) error {
	var multi bool
	if err := tx.QueryRow(`SELECT multi_household FROM people WHERE id = ?`, personID).Scan(&multi); err != nil {
		return notFound(err)
	}
	rows, err := tx.Query(`SELECT household_id, role FROM household_members WHERE person_id = ? AND household_id <> ?`, personID, keep)
	if err != nil {
		return err
	}
	var leaving []int64
	for rows.Next() {
		var hid int64
		var r string
		if err := rows.Scan(&hid, &r); err != nil {
			rows.Close()
			return err
		}
		if role == RoleChild && multi && r == RoleChild {
			continue
		}
		leaving = append(leaving, hid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, hid := range leaving {
		if _, err := tx.Exec(`DELETE FROM household_members WHERE household_id = ? AND person_id = ?`, hid, personID); err != nil {
			return err
		}
		if err := logChange(tx, by, personID, hid, "moved out of household"); err != nil {
			return err
		}
	}
	return nil
}

// OneLine is the planned address on one line, as Address.OneLine shows it.
func (a AddressInput) OneLine() string {
	return Address{Address1: a.Address1, Address2: a.Address2, City: a.City, State: a.State, Postal: a.Postal}.OneLine()
}
