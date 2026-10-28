// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// When a group alerts its own channels.
const (
	AlertNone     = "none"     // never
	AlertAny      = "any"      // when its first member goes down
	AlertSelected = "selected" // when one of AlertMonitors goes down
)

// Group is a folder of monitors and other groups. It is not checked.
type Group struct {
	ID       int64
	Name     string
	ParentID int64 // the group it is in; 0 for none
	Position int   // its place among the groups and monitors in its parent
	Alert    string
	// Channels are the channels its alerts go to; AlertMonitors the
	// monitors that count with AlertSelected.
	Channels      []int64
	AlertMonitors []int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// maxDepth bounds the walks up a chain of groups.
const maxDepth = 20

// Groups returns every group, in order within each parent.
func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, COALESCE(parent_id, 0), position, alert, created_at, updated_at
		FROM monitor_groups ORDER BY position, LOWER(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var created, updated int64
		if err := rows.Scan(&g.ID, &g.Name, &g.ParentID, &g.Position, &g.Alert, &created, &updated); err != nil {
			return nil, err
		}
		g.CreatedAt, g.UpdatedAt = fromMillis(created), fromMillis(updated)
		g.Channels, g.AlertMonitors = []int64{}, []int64{}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := map[int64]*Group{}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	for _, c := range []struct {
		query string
		add   func(g *Group, id int64)
	}{
		{`SELECT group_id, channel_id FROM group_channels ORDER BY channel_id`,
			func(g *Group, id int64) { g.Channels = append(g.Channels, id) }},
		{`SELECT group_id, monitor_id FROM group_alert_monitors ORDER BY monitor_id`,
			func(g *Group, id int64) { g.AlertMonitors = append(g.AlertMonitors, id) }},
	} {
		if err := s.eachPair(ctx, c.query, func(gid, id int64) {
			if g := byID[gid]; g != nil {
				c.add(g, id)
			}
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GroupByID returns the group with this ID, or ErrNotFound.
func (s *Store) GroupByID(ctx context.Context, id int64) (Group, error) {
	gs, err := s.Groups(ctx)
	if err != nil {
		return Group{}, err
	}
	for _, g := range gs {
		if g.ID == id {
			return g, nil
		}
	}
	return Group{}, ErrNotFound
}

// checkGroupParent refuses a parent that is missing, the group itself or
// one of the groups in it.
func (s *Store) checkGroupParent(ctx context.Context, tx *sql.Tx, id, parent int64) error {
	for p, depth := parent, 0; p != 0; depth++ {
		if p == id {
			return invalidf("a group cannot be in itself or in a group inside it")
		}
		if depth >= maxDepth {
			return invalidf("groups can be at most %d deep", maxDepth)
		}
		var up int64
		err := tx.QueryRowContext(ctx, s.q(`SELECT COALESCE(parent_id, 0) FROM monitor_groups WHERE id = ?`), p).Scan(&up)
		if errors.Is(err, sql.ErrNoRows) {
			return invalidf("there is no group %d", p)
		}
		if err != nil {
			return err
		}
		p = up
	}
	return nil
}

// endPosition is the position after every group and monitor in a parent.
func (s *Store) endPosition(ctx context.Context, tx *sql.Tx, parent int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, s.q(`SELECT COALESCE(MAX(p), -1) + 1 FROM (
		SELECT position AS p FROM monitor_groups WHERE COALESCE(parent_id, 0) = ?
		UNION ALL SELECT position FROM monitors WHERE COALESCE(group_id, 0) = ?) AS places`), parent, parent).Scan(&n)
	return n, err
}

func validAlert(a string) error {
	switch a {
	case AlertNone, AlertAny, AlertSelected:
		return nil
	}
	return invalidf("unknown alert %q: none, any or selected", a)
}

// CreateGroup saves a new group, after everything in its parent.
func (s *Store) CreateGroup(ctx context.Context, g Group, now time.Time) (Group, error) {
	if err := validAlert(g.Alert); err != nil {
		return Group{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if err := s.checkGroupParent(ctx, tx, 0, g.ParentID); err != nil {
		return Group{}, err
	}
	if g.Position, err = s.endPosition(ctx, tx, g.ParentID); err != nil {
		return Group{}, err
	}
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	err = tx.QueryRowContext(ctx, s.q(`INSERT INTO monitor_groups (name, parent_id, position, alert, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`),
		g.Name, nullable(g.ParentID), g.Position, g.Alert, now.UnixMilli(), now.UnixMilli()).Scan(&g.ID)
	if err != nil {
		return Group{}, err
	}
	if err := s.setGroupLinks(ctx, tx, g); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GroupByID(ctx, g.ID)
}

// UpdateGroup saves a group's name, parent, alert, channels and alert
// monitors. A group moved to another parent goes after everything there.
func (s *Store) UpdateGroup(ctx context.Context, g Group, now time.Time) (Group, error) {
	if err := validAlert(g.Alert); err != nil {
		return Group{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var parent int64
	err = tx.QueryRowContext(ctx, s.q(`SELECT COALESCE(parent_id, 0), position FROM monitor_groups WHERE id = ?`), g.ID).Scan(&parent, &g.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, err
	}
	if err := s.checkGroupParent(ctx, tx, g.ID, g.ParentID); err != nil {
		return Group{}, err
	}
	if parent != g.ParentID {
		if g.Position, err = s.endPosition(ctx, tx, g.ParentID); err != nil {
			return Group{}, err
		}
	}
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE monitor_groups SET name = ?, parent_id = ?, position = ?, alert = ?, updated_at = ?
		WHERE id = ?`), g.Name, nullable(g.ParentID), g.Position, g.Alert, now.UnixMilli(), g.ID); err != nil {
		return Group{}, err
	}
	for _, q := range []string{`DELETE FROM group_channels WHERE group_id = ?`, `DELETE FROM group_alert_monitors WHERE group_id = ?`} {
		if _, err := tx.ExecContext(ctx, s.q(q), g.ID); err != nil {
			return Group{}, err
		}
	}
	if err := s.setGroupLinks(ctx, tx, g); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GroupByID(ctx, g.ID)
}

// setGroupLinks saves a group's channels and alert monitors.
func (s *Store) setGroupLinks(ctx context.Context, tx *sql.Tx, g Group) error {
	for _, c := range []struct {
		ids                []int64
		table, ref, column string
		what               string
	}{
		{g.Channels, "group_channels", "notification_channels", "channel_id", "notification channel"},
		{g.AlertMonitors, "group_alert_monitors", "monitors", "monitor_id", "monitor"},
	} {
		seen := map[int64]bool{}
		for _, id := range c.ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			var exists bool
			// The table and column names are the constants above; the
			// values are bound arguments.
			// nosemgrep
			if err := tx.QueryRowContext(ctx, s.q(`SELECT EXISTS (SELECT 1 FROM `+c.ref+` WHERE id = ?)`), id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return invalidf("there is no %s %d", c.what, id)
			}
			// nosemgrep
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO `+c.table+` (group_id, `+c.column+`) VALUES (?, ?)`), g.ID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteGroup deletes a group. What was in it moves to the group's parent,
// after what is already there.
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var parent int64
	err = tx.QueryRowContext(ctx, s.q(`SELECT COALESCE(parent_id, 0) FROM monitor_groups WHERE id = ?`), id).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	end, err := s.endPosition(ctx, tx, parent)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`UPDATE monitor_groups SET parent_id = ?, position = position + ? WHERE parent_id = ?`,
		`UPDATE monitors SET group_id = ?, position = position + ? WHERE group_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, s.q(q), nullable(parent), end, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM monitor_groups WHERE id = ?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ChannelsForGroup returns the enabled channels a group alerts.
func (s *Store) ChannelsForGroup(ctx context.Context, groupID int64) ([]Channel, error) {
	return s.channels(ctx, `WHERE enabled = TRUE AND id IN (SELECT channel_id FROM group_channels WHERE group_id = ?)`, groupID)
}

// Place is where a group or a monitor goes in Arrange.
type Place struct {
	Group   bool  // a group, else a monitor
	ID      int64 // the group's or monitor's ID
	GroupID int64 // the group it goes in; 0 for none
}

// Arrange moves groups and monitors: each into its place's group, in the
// order given among the places with the same group. It refuses a place
// for something missing, and groups in themselves.
func (s *Store) Arrange(ctx context.Context, places []Place, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()                   //nolint:errcheck // a no-op after Commit
	parents, err := groupParents(ctx, tx) // as they will be
	if err != nil {
		return err
	}
	for _, p := range places {
		if _, ok := parents[p.GroupID]; p.GroupID != 0 && !ok {
			return invalidf("there is no group %d", p.GroupID)
		}
		if p.Group {
			if _, ok := parents[p.ID]; !ok {
				return invalidf("there is no group %d", p.ID)
			}
			parents[p.ID] = p.GroupID
		}
	}
	for id := range parents {
		for p, depth := parents[id], 0; p != 0; p, depth = parents[p], depth+1 {
			if p == id {
				return invalidf("a group cannot be in itself or in a group inside it")
			}
			if depth >= maxDepth {
				return invalidf("groups can be at most %d deep", maxDepth)
			}
		}
	}
	next := map[int64]int{}
	for _, p := range places {
		pos := next[p.GroupID]
		next[p.GroupID]++
		q := `UPDATE monitors SET group_id = ?, position = ?, updated_at = ? WHERE id = ?`
		if p.Group {
			q = `UPDATE monitor_groups SET parent_id = ?, position = ?, updated_at = ? WHERE id = ?`
		}
		res, err := tx.ExecContext(ctx, s.q(q), nullable(p.GroupID), pos, now.UnixMilli(), p.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return invalidf("there is no monitor %d", p.ID)
		}
	}
	return tx.Commit()
}

// groupParents reads every group's parent.
func groupParents(ctx context.Context, tx *sql.Tx) (map[int64]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(parent_id, 0) FROM monitor_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parents := map[int64]int64{}
	for rows.Next() {
		var id, p int64
		if err := rows.Scan(&id, &p); err != nil {
			return nil, err
		}
		parents[id] = p
	}
	return parents, rows.Err()
}

// convertGroupMonitors turns the group monitors of earlier versions (type
// 'group', their members' parent_id) into groups with the same IDs: their
// channels, maintenance windows and members carry over, a group with
// channels alerts when its first member goes down, as a group monitor
// did, and on status pages a group's monitors take its place. It runs when the store opens and after a restore, and does nothing
// once there are none.
func (s *Store) convertGroupMonitors(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE type = 'group'`).Scan(&n); err != nil || n == 0 {
		return err
	}
	over := ""
	if s.dialect == Postgres {
		over = ` OVERRIDING SYSTEM VALUE`
	}
	const groups = `(SELECT id FROM monitors WHERE type = 'group')`
	stmts := []string{
		`INSERT INTO monitor_groups (id, name, parent_id, position, alert, created_at, updated_at)` + over + `
			SELECT id, name, NULL, 0, CASE WHEN EXISTS (SELECT 1 FROM monitor_channels c WHERE c.monitor_id = monitors.id)
				THEN 'any' ELSE 'none' END, created_at, updated_at
			FROM monitors WHERE type = 'group'`,
		`UPDATE monitor_groups SET parent_id = (SELECT m.parent_id FROM monitors m
			WHERE m.id = monitor_groups.id AND m.parent_id IN ` + groups + `)`,
		`INSERT INTO group_channels (group_id, channel_id)
			SELECT monitor_id, channel_id FROM monitor_channels WHERE monitor_id IN ` + groups,
		`INSERT INTO maintenance_groups (window_id, group_id)
			SELECT window_id, monitor_id FROM maintenance_monitors WHERE monitor_id IN ` + groups,
		// A group on a status page: its monitors, and those of the groups
		// in it, take its place.
		`WITH RECURSIVE inside (group_id, monitor_id) AS (
			SELECT parent_id, id FROM monitors WHERE parent_id IN ` + groups + `
			UNION SELECT i.group_id, m.id FROM inside i JOIN monitors m ON m.parent_id = i.monitor_id)
		INSERT INTO status_page_monitors (section_id, monitor_id)
			SELECT DISTINCT spm.section_id, i.monitor_id FROM status_page_monitors spm
			JOIN inside i ON i.group_id = spm.monitor_id
			JOIN monitors m ON m.id = i.monitor_id AND m.type <> 'group'
		ON CONFLICT (section_id, monitor_id) DO NOTHING`,
		`UPDATE monitors SET group_id = parent_id WHERE parent_id IN ` + groups,
		`UPDATE monitors SET parent_id = NULL WHERE parent_id IS NOT NULL`,
		`DELETE FROM monitors WHERE type = 'group'`,
	}
	if s.dialect == Postgres {
		stmts = append(stmts, `SELECT setval(pg_get_serial_sequence('monitor_groups', 'id'),
			(SELECT COALESCE(MAX(id), 0) + 1 FROM monitor_groups), false)`)
	}
	for _, q := range stmts {
		// Fixed statements, without values.
		// nosemgrep
		if _, err := tx.ExecContext(ctx, q); err != nil { //nolint:gosec // G202: fixed SQL
			return err
		}
	}
	return nil
}

// convertOld turns what earlier versions saved into what this one uses.
func (s *Store) convertOld(ctx context.Context, tx *sql.Tx) error {
	if err := s.convertGroupMonitors(ctx, tx); err != nil {
		return err
	}
	return s.convertAgentMonitors(ctx, tx)
}

// convertOnOpen runs convertOld in a transaction of its own.
func (s *Store) convertOnOpen(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if err := s.convertOld(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
