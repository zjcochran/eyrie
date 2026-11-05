// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"math"
	"time"
)

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	ID      int64
	At      time.Time
	Actor   string // the username; "" when unknown
	IP      string
	Action  string // such as "monitor created"
	Details string // key=value pairs
}

// AuditFilter selects audit entries, newest first. BeforeID and Offset
// page backwards; Action and Actor, when set, must match.
type AuditFilter struct {
	Action   string
	Actor    string
	BeforeID int64
	Offset   int
	Limit    int // default 100, at most 1000; All for every entry
}

// All as a limit returns everything.
const All = -1

// AddAudit appends to the audit log.
func (s *Store) AddAudit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO audit_log (at, actor, ip, action, details) VALUES (?, ?, ?, ?, ?)`),
		e.At.UnixMilli(), e.Actor, e.IP, e.Action, e.Details)
	return err
}

// Audit returns the audit entries matching f, newest first.
func (s *Store) Audit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	where, args := f.where()
	q := `SELECT id, at, actor, ip, action, details FROM audit_log` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit(f.Limit), max(f.Offset, 0))
	// Only fixed SQL is added above; the values are bound arguments.
	// nosemgrep
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.IP, &e.Action, &e.Details); err != nil {
			return nil, err
		}
		e.At = fromMillis(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountAudit returns how many audit entries match f's action and actor,
// for every page.
func (s *Store) CountAudit(ctx context.Context, f AuditFilter) (int, error) {
	f.BeforeID = 0
	where, args := f.where()
	var n int
	// Only fixed SQL is added by where; the values are bound arguments.
	// nosemgrep
	err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM audit_log`+where), args...).Scan(&n)
	return n, err
}

func (f AuditFilter) where() (string, []any) {
	q := ` WHERE 1 = 1`
	var args []any
	if f.Action != "" {
		q += ` AND action = ?`
		args = append(args, f.Action)
	}
	if f.Actor != "" {
		q += ` AND actor = ?`
		args = append(args, f.Actor)
	}
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	return q, args
}

// limit is a page size for SQL: 100 by default, at most 1000, or every row
// for All.
func limit(n int) int64 {
	switch {
	case n == All:
		return math.MaxInt64
	case n <= 0 || n > 1000:
		return 100
	}
	return int64(n)
}

// AuditNames returns the actions and the actors in the audit log, each
// sorted, for filters.
func (s *Store) AuditNames(ctx context.Context) (actions, actors []string, err error) {
	actions, err = s.column(ctx, `SELECT DISTINCT action FROM audit_log ORDER BY action`)
	if err != nil {
		return nil, nil, err
	}
	actors, err = s.column(ctx, `SELECT DISTINCT actor FROM audit_log WHERE actor <> '' ORDER BY actor`)
	return actions, actors, err
}

// column reads a query's one text column.
func (s *Store) column(ctx context.Context, query string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
