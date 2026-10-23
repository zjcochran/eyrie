// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Window is a maintenance window. Schedule is the JSON that package
// maintenance reads. It covers every monitor (AllMonitors), or the
// monitors in MonitorIDs and those with a tag in TagIDs; covering a group
// covers its members.
type Window struct {
	ID          int64
	Title       string
	Description string
	Enabled     bool
	Schedule    json.RawMessage
	AllMonitors bool
	MonitorIDs  []int64
	TagIDs      []int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateWindow saves a new maintenance window.
func (s *Store) CreateWindow(ctx context.Context, w Window, now time.Time) (Window, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Window{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	err = tx.QueryRowContext(ctx, s.q(`
		INSERT INTO maintenance_windows (title, description, enabled, schedule, all_monitors, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		w.Title, w.Description, w.Enabled, string(w.Schedule), w.AllMonitors, now.UnixMilli(), now.UnixMilli()).Scan(&w.ID)
	if err != nil {
		return Window{}, err
	}
	if err := s.setCoverage(ctx, tx, w); err != nil {
		return Window{}, err
	}
	if err := tx.Commit(); err != nil {
		return Window{}, err
	}
	return s.WindowByID(ctx, w.ID)
}

// UpdateWindow saves a maintenance window's changes.
func (s *Store) UpdateWindow(ctx context.Context, w Window, now time.Time) (Window, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Window{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	res, err := tx.ExecContext(ctx, s.q(`
		UPDATE maintenance_windows SET title = ?, description = ?, enabled = ?, schedule = ?, all_monitors = ?, updated_at = ?
		WHERE id = ?`),
		w.Title, w.Description, w.Enabled, string(w.Schedule), w.AllMonitors, now.UnixMilli(), w.ID)
	if err != nil {
		return Window{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Window{}, ErrNotFound
	}
	for _, table := range []string{"maintenance_monitors", "maintenance_tags"} {
		// The table name is one of the two constants above.
		// nosemgrep
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM `+table+` WHERE window_id = ?`), w.ID); err != nil {
			return Window{}, err
		}
	}
	if err := s.setCoverage(ctx, tx, w); err != nil {
		return Window{}, err
	}
	if err := tx.Commit(); err != nil {
		return Window{}, err
	}
	return s.WindowByID(ctx, w.ID)
}

// setCoverage saves the monitors and tags a window covers.
func (s *Store) setCoverage(ctx context.Context, tx *sql.Tx, w Window) error {
	for _, c := range []struct {
		ids          []int64
		table, ref   string
		column, what string
	}{
		{w.MonitorIDs, "maintenance_monitors", "monitors", "monitor_id", "monitor"},
		{w.TagIDs, "maintenance_tags", "tags", "tag_id", "tag"},
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
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO `+c.table+` (window_id, `+c.column+`) VALUES (?, ?)`), w.ID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteWindow deletes a maintenance window.
func (s *Store) DeleteWindow(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM maintenance_windows WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// WindowByID returns the maintenance window with this ID, or ErrNotFound.
func (s *Store) WindowByID(ctx context.Context, id int64) (Window, error) {
	ws, err := s.windows(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Window{}, err
	}
	if len(ws) == 0 {
		return Window{}, ErrNotFound
	}
	return ws[0], nil
}

// Windows returns every maintenance window, by title.
func (s *Store) Windows(ctx context.Context) ([]Window, error) { return s.windows(ctx, "") }

func (s *Store) windows(ctx context.Context, where string, args ...any) ([]Window, error) {
	out, err := s.windowRows(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Window{}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	for _, c := range []struct {
		query string
		add   func(w *Window, id int64)
	}{
		{`SELECT window_id, monitor_id FROM maintenance_monitors ORDER BY monitor_id`,
			func(w *Window, id int64) { w.MonitorIDs = append(w.MonitorIDs, id) }},
		{`SELECT window_id, tag_id FROM maintenance_tags ORDER BY tag_id`,
			func(w *Window, id int64) { w.TagIDs = append(w.TagIDs, id) }},
	} {
		if err := s.eachPair(ctx, c.query, func(wid, id int64) {
			if w := byID[wid]; w != nil {
				c.add(w, id)
			}
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// windowRows reads the windows themselves, without what they cover.
func (s *Store) windowRows(ctx context.Context, where string, args ...any) ([]Window, error) {
	// where is one of the fixed clauses above; values are bound arguments.
	// nosemgrep
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id, title, description, enabled, schedule, all_monitors, created_at, updated_at
		FROM maintenance_windows `+where+` ORDER BY LOWER(title), id`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Window
	for rows.Next() {
		var w Window
		var schedule string
		var created, updated int64
		if err := rows.Scan(&w.ID, &w.Title, &w.Description, &w.Enabled, &schedule, &w.AllMonitors, &created, &updated); err != nil {
			return nil, err
		}
		w.Schedule, w.CreatedAt, w.UpdatedAt = json.RawMessage(schedule), fromMillis(created), fromMillis(updated)
		w.MonitorIDs, w.TagIDs = []int64{}, []int64{}
		out = append(out, w)
	}
	return out, rows.Err()
}

// eachPair calls f with each row of a query of two IDs.
func (s *Store) eachPair(ctx context.Context, query string, f func(a, b int64)) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b int64
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		f(a, b)
	}
	return rows.Err()
}
