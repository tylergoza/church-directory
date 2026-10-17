package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Groups are labels, not permissions: being in "Deacons" gives no rights
// anywhere. visible_to says who sees the group and its page.

const (
	GroupMembers = "members" // listed in the directory and printed
	GroupStaff   = "staff"   // staff only
)

var (
	// ErrGroupContact: contacts never go in groups.
	ErrGroupContact = errors.New("contacts can't be added to groups")
	// ErrGroupArchived: archived people and archived groups take no new members.
	ErrGroupArchived = errors.New("archived people and groups can't be changed")
)

// Group is a row of groups.
type Group struct {
	ID          int64
	Name        string
	Description string
	VisibleTo   string
	Position    int
	ArchivedAt  string
}

func (g Group) Archived() bool { return g.ArchivedAt != "" }

// StaffOnly reports whether only staff may see the group.
func (g Group) StaffOnly() bool { return g.VisibleTo == GroupStaff }

// GroupRow is a group with how many people are in it (for staff's list).
type GroupRow struct {
	Group
	Count int
}

// GroupMember is a person in a group, with their label and place.
type GroupMember struct {
	Person   Person
	Label    string
	Position int
}

// GroupInput is what staff type into the group form.
type GroupInput struct {
	Name, Description, VisibleTo string
}

// Validate cleans the input up and lists what is wrong with it.
func (in *GroupInput) Validate() []string {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.VisibleTo == "" {
		in.VisibleTo = GroupMembers
	}
	var errs []string
	if in.Name == "" {
		errs = append(errs, "A group needs a name.")
	}
	if len(in.Name) > 100 {
		errs = append(errs, "The name is too long.")
	}
	if len(in.Description) > 1000 {
		errs = append(errs, "The description is too long.")
	}
	if in.VisibleTo != GroupMembers && in.VisibleTo != GroupStaff {
		errs = append(errs, "Visible to must be members or staff.")
	}
	return errs
}

// CleanGroupLabel trims a member label and checks its length.
func CleanGroupLabel(label string) (string, bool) {
	label = strings.TrimSpace(label)
	return label, len(label) <= 100
}

const groupCols = `id, name, description, visible_to, position, COALESCE(archived_at, '')`

func scanGroup(row scanner) (Group, error) {
	var g Group
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.VisibleTo, &g.Position, &g.ArchivedAt)
	return g, err
}

func groupNameTaken(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("There is already a group with that name.")
	}
	return err
}

// GetGroup reads one group (archived or not).
func (s *Store) GetGroup(id int64) (Group, error) {
	g, err := scanGroup(s.DB.QueryRow(`SELECT `+groupCols+` FROM groups WHERE id = ?`, id))
	return g, notFound(err)
}

// CreateGroup adds a group at the end of the list.
func (s *Store) CreateGroup(in GroupInput, by int64) (id int64, err error) {
	if errs := in.Validate(); len(errs) > 0 {
		return 0, errors.New(errs[0])
	}
	err = s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO groups (name, description, visible_to, position)
			VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM groups))`,
			in.Name, in.Description, in.VisibleTo)
		if err != nil {
			return groupNameTaken(err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logChange(tx, by, 0, 0, "added group")
	})
	return id, err
}

// UpdateGroup changes a group's name, description and who sees it.
func (s *Store) UpdateGroup(id int64, in GroupInput, by int64) error {
	if errs := in.Validate(); len(errs) > 0 {
		return errors.New(errs[0])
	}
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE groups SET name = ?, description = ?, visible_to = ? WHERE id = ?`,
			in.Name, in.Description, in.VisibleTo, id)
		if err != nil {
			return groupNameTaken(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, 0, 0, "edited group")
	})
}

// SetGroupPosition puts a group at a place in the list.
func (s *Store) SetGroupPosition(id int64, position int, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE groups SET position = ? WHERE id = ?`, position, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, 0, 0, "reordered group")
	})
}

// ArchiveGroup hides a group (archived true) or brings it back. Its
// members stay in it.
func (s *Store) ArchiveGroup(id int64, archived bool, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		q, what := `UPDATE groups SET archived_at = NULL WHERE id = ?`, "unarchived group"
		if archived {
			q, what = `UPDATE groups SET archived_at = datetime('now') WHERE id = ?`, "archived group"
		}
		res, err := tx.Exec(q, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, 0, 0, what)
	})
}

// ListGroups lists groups in order (position, then name) with their member
// counts; archived ones only when asked.
func (s *Store) ListGroups(archived bool) ([]GroupRow, error) {
	where := `g.archived_at IS NULL`
	if archived {
		where = `g.archived_at IS NOT NULL`
	}
	rows, err := s.DB.Query(`SELECT g.id, g.name, g.description, g.visible_to, g.position, COALESCE(g.archived_at, ''),
		(SELECT COUNT(*) FROM group_members gm WHERE gm.group_id = g.id)
		FROM groups g WHERE ` + where + ` ORDER BY g.position, g.name COLLATE NOCASE, g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.VisibleTo, &g.Position, &g.ArchivedAt, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AllGroups reads every group, archived or not, in order.
func (s *Store) AllGroups() ([]Group, error) {
	rows, err := s.DB.Query(`SELECT ` + groupCols + ` FROM groups ORDER BY position, name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GroupMembership is one row of group_members.
type GroupMembership struct {
	GroupID, PersonID int64
	Label             string
	Position          int
}

// AllGroupMemberships reads every membership, in order (the access package
// loads these once per request).
func (s *Store) AllGroupMemberships() ([]GroupMembership, error) {
	rows, err := s.DB.Query(`SELECT group_id, person_id, label, position FROM group_members
		ORDER BY group_id, position, person_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupMembership
	for rows.Next() {
		var m GroupMembership
		if err := rows.Scan(&m.GroupID, &m.PersonID, &m.Label, &m.Position); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GroupMembersOf lists the people in a group in order, for staff.
func (s *Store) GroupMembersOf(groupID int64) ([]GroupMember, error) {
	all, err := s.AllGroupMemberships()
	if err != nil {
		return nil, err
	}
	var out []GroupMember
	for _, m := range all {
		if m.GroupID != groupID {
			continue
		}
		p, err := s.GetPerson(m.PersonID)
		if err != nil {
			return nil, err
		}
		out = append(out, GroupMember{Person: p, Label: m.Label, Position: m.Position})
	}
	return out, nil
}

// AddGroupMember puts a person in a group at the end. Contacts and
// archived people or groups are refused; adding someone already in the
// group just updates their label.
func (s *Store) AddGroupMember(groupID, personID int64, label string, by int64) error {
	label, ok := CleanGroupLabel(label)
	if !ok {
		return errors.New("The label is too long.")
	}
	return s.inTx(func(tx *sql.Tx) error {
		var archived string
		if err := tx.QueryRow(`SELECT COALESCE(archived_at, '') FROM groups WHERE id = ?`, groupID).Scan(&archived); err != nil {
			return notFound(err)
		}
		var kind, parchived string
		if err := tx.QueryRow(`SELECT kind, COALESCE(archived_at, '') FROM people WHERE id = ?`, personID).Scan(&kind, &parchived); err != nil {
			return notFound(err)
		}
		if kind == KindContact {
			return ErrGroupContact
		}
		if archived != "" || parchived != "" {
			return ErrGroupArchived
		}
		if _, err := tx.Exec(`INSERT INTO group_members (group_id, person_id, label, position)
			VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM group_members WHERE group_id = ?))
			ON CONFLICT (group_id, person_id) DO UPDATE SET label = excluded.label`,
			groupID, personID, label, groupID); err != nil {
			return err
		}
		return logChange(tx, by, personID, 0, "added to a group")
	})
}

// SetGroupMemberLabel changes a member's label.
func (s *Store) SetGroupMemberLabel(groupID, personID int64, label string, by int64) error {
	label, ok := CleanGroupLabel(label)
	if !ok {
		return errors.New("The label is too long.")
	}
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE group_members SET label = ? WHERE group_id = ? AND person_id = ?`, label, groupID, personID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, personID, 0, "changed a group label")
	})
}

// MoveGroupMember moves a member up (dir < 0) or down (dir > 0) one place.
func (s *Store) MoveGroupMember(groupID, personID int64, dir int, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		ids, err := orderedGroupPeople(tx, groupID)
		if err != nil {
			return err
		}
		i := -1
		for k, id := range ids {
			if id == personID {
				i = k
			}
		}
		if i < 0 {
			return ErrNotFound
		}
		j := i + 1
		if dir < 0 {
			j = i - 1
		}
		if j >= 0 && j < len(ids) {
			ids[i], ids[j] = ids[j], ids[i]
		}
		return renumberGroup(tx, groupID, ids, by, personID, "reordered a group")
	})
}

// SetGroupMemberPosition puts a member at place n (1 is first) among the
// others.
func (s *Store) SetGroupMemberPosition(groupID, personID int64, n int, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		ids, err := orderedGroupPeople(tx, groupID)
		if err != nil {
			return err
		}
		rest := make([]int64, 0, len(ids))
		found := false
		for _, id := range ids {
			if id == personID {
				found = true
			} else {
				rest = append(rest, id)
			}
		}
		if !found {
			return ErrNotFound
		}
		n--
		if n < 0 {
			n = 0
		}
		if n > len(rest) {
			n = len(rest)
		}
		rest = append(rest[:n], append([]int64{personID}, rest[n:]...)...)
		return renumberGroup(tx, groupID, rest, by, personID, "reordered a group")
	})
}

func orderedGroupPeople(tx dbtx, groupID int64) ([]int64, error) {
	rows, err := tx.Query(`SELECT person_id FROM group_members WHERE group_id = ? ORDER BY position, person_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func renumberGroup(tx dbtx, groupID int64, ids []int64, by, personID int64, what string) error {
	for k, id := range ids {
		if _, err := tx.Exec(`UPDATE group_members SET position = ? WHERE group_id = ? AND person_id = ?`, k+1, groupID, id); err != nil {
			return err
		}
	}
	return logChange(tx, by, personID, 0, what)
}

// RemoveGroupMember takes a person out of a group.
func (s *Store) RemoveGroupMember(groupID, personID int64, by int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM group_members WHERE group_id = ? AND person_id = ?`, groupID, personID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return logChange(tx, by, personID, 0, "removed from a group")
	})
}

// PersonGroups lists the groups a person is in (archived ones too), in
// the groups' order. The access package filters by who is looking.
func (s *Store) PersonGroups(personID int64) ([]Group, error) {
	rows, err := s.DB.Query(`SELECT g.id, g.name, g.description, g.visible_to, g.position, COALESCE(g.archived_at, '')
		FROM groups g JOIN group_members gm ON gm.group_id = g.id WHERE gm.person_id = ?
		ORDER BY g.position, g.name COLLATE NOCASE, g.id`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
