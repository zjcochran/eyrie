// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Monitor is a monitor's saved settings. Config holds the settings of its
// type, as JSON; package check reads and checks them.
type Monitor struct {
	ID            int64
	Name          string
	Type          string
	GroupID       int64 // the group it is in; 0 for none
	AgentID       int64 // the agent it is checked on; 0 for Eyrie
	Position      int   // its place among the groups and monitors in its group
	Active        bool  // false: paused
	Interval      time.Duration
	RetryInterval time.Duration
	Retries       int
	ResendEvery   int
	Timeout       time.Duration
	UpsideDown    bool
	Description   string
	Config        json.RawMessage
	PushToken     string // push monitors only
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const monitorCols = `id, name, type, COALESCE(group_id, 0), COALESCE(agent_id, 0), position, active, interval_s, retry_interval_s, retries,
	resend_every, timeout_ms, upside_down, description, config, COALESCE(push_token, ''), created_at, updated_at`

func scanMonitor(sc interface{ Scan(...any) error }) (Monitor, error) {
	var m Monitor
	var interval, retry, timeout int64
	var config string
	var created, updated int64
	err := sc.Scan(&m.ID, &m.Name, &m.Type, &m.GroupID, &m.AgentID, &m.Position, &m.Active, &interval, &retry, &m.Retries,
		&m.ResendEvery, &timeout, &m.UpsideDown, &m.Description, &config, &m.PushToken, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, err
	}
	m.Interval, m.RetryInterval = time.Duration(interval)*time.Second, time.Duration(retry)*time.Second
	m.Timeout = time.Duration(timeout) * time.Millisecond
	m.Config = json.RawMessage(config)
	m.CreatedAt, m.UpdatedAt = fromMillis(created), fromMillis(updated)
	return m, nil
}

// nullable is NULL for the zero value, so optional references and unique
// columns hold no value rather than 0 or "".
func nullable[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

// checkGroup refuses a group that does not exist.
func (s *Store) checkGroup(ctx context.Context, tx *sql.Tx, group int64) error {
	if group == 0 {
		return nil
	}
	var ok bool
	if err := tx.QueryRowContext(ctx, s.q(`SELECT EXISTS (SELECT 1 FROM monitor_groups WHERE id = ?)`), group).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalidf("there is no group %d", group)
	}
	return nil
}

// CreateMonitor saves a new monitor, after everything in its group, and
// returns it with its ID.
func (s *Store) CreateMonitor(ctx context.Context, m Monitor, now time.Time) (Monitor, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Monitor{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if err := s.checkGroup(ctx, tx, m.GroupID); err != nil {
		return Monitor{}, err
	}
	if m.Position, err = s.endPosition(ctx, tx, m.GroupID); err != nil {
		return Monitor{}, err
	}
	var id int64
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	err = tx.QueryRowContext(ctx, s.q(`
		INSERT INTO monitors (name, type, group_id, agent_id, position, active, interval_s, retry_interval_s, retries, resend_every,
			timeout_ms, upside_down, description, config, push_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		m.Name, m.Type, nullable(m.GroupID), nullable(m.AgentID), m.Position, m.Active, int64(m.Interval/time.Second), int64(m.RetryInterval/time.Second),
		m.Retries, m.ResendEvery, m.Timeout.Milliseconds(), m.UpsideDown, m.Description, string(m.Config),
		nullable(m.PushToken), now.UnixMilli(), now.UnixMilli()).Scan(&id)
	if isUnique(err) {
		return Monitor{}, invalidf("another monitor has this push token")
	}
	if err != nil {
		return Monitor{}, err
	}
	if err := tx.Commit(); err != nil {
		return Monitor{}, err
	}
	return s.MonitorByID(ctx, id)
}

// UpdateMonitor saves a monitor's settings. Its type cannot change. A
// monitor moved to another group goes after everything there.
func (s *Store) UpdateMonitor(ctx context.Context, m Monitor, now time.Time) (Monitor, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Monitor{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var typ string
	var group int64
	if err := tx.QueryRowContext(ctx, s.q(`SELECT type, COALESCE(group_id, 0), position FROM monitors WHERE id = ?`), m.ID).Scan(&typ, &group, &m.Position); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Monitor{}, ErrNotFound
		}
		return Monitor{}, err
	}
	if typ != m.Type {
		return Monitor{}, invalidf("the type of a monitor cannot change: make a new one")
	}
	if err := s.checkGroup(ctx, tx, m.GroupID); err != nil {
		return Monitor{}, err
	}
	if group != m.GroupID {
		if m.Position, err = s.endPosition(ctx, tx, m.GroupID); err != nil {
			return Monitor{}, err
		}
	}
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	_, err = tx.ExecContext(ctx, s.q(`
		UPDATE monitors SET name = ?, group_id = ?, agent_id = ?, position = ?, active = ?, interval_s = ?, retry_interval_s = ?, retries = ?,
			resend_every = ?, timeout_ms = ?, upside_down = ?, description = ?, config = ?, push_token = ?, updated_at = ?
		WHERE id = ?`),
		m.Name, nullable(m.GroupID), nullable(m.AgentID), m.Position, m.Active, int64(m.Interval/time.Second), int64(m.RetryInterval/time.Second),
		m.Retries, m.ResendEvery, m.Timeout.Milliseconds(), m.UpsideDown, m.Description, string(m.Config),
		nullable(m.PushToken), now.UnixMilli(), m.ID)
	if isUnique(err) {
		return Monitor{}, invalidf("another monitor has this push token")
	}
	if err != nil {
		return Monitor{}, err
	}
	if err := tx.Commit(); err != nil {
		return Monitor{}, err
	}
	return s.MonitorByID(ctx, m.ID)
}

// SetMonitorActive pauses (false) or resumes (true) a monitor.
func (s *Store) SetMonitorActive(ctx context.Context, id int64, active bool, now time.Time) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE monitors SET active = ?, updated_at = ? WHERE id = ?`), active, now.UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMonitor deletes a monitor with its state, heartbeats and stats.
func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM monitors WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MonitorByID returns the monitor with this ID, or ErrNotFound.
func (s *Store) MonitorByID(ctx context.Context, id int64) (Monitor, error) {
	return scanMonitor(s.db.QueryRowContext(ctx, s.q(`SELECT `+monitorCols+` FROM monitors WHERE id = ?`), id))
}

// MonitorByPushToken returns the push monitor with this token, or ErrNotFound.
func (s *Store) MonitorByPushToken(ctx context.Context, token string) (Monitor, error) {
	if token == "" {
		return Monitor{}, ErrNotFound
	}
	return scanMonitor(s.db.QueryRowContext(ctx, s.q(`SELECT `+monitorCols+` FROM monitors WHERE push_token = ?`), token))
}

// Monitors returns every monitor, in order within each group.
func (s *Store) Monitors(ctx context.Context) ([]Monitor, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+monitorCols+` FROM monitors ORDER BY position, LOWER(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
