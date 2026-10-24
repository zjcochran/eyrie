// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"time"
)

// Beat is one check of a monitor: a heartbeat.
type Beat struct {
	MonitorID int64
	At        time.Time
	Status    string
	Latency   time.Duration // negative: none
	Message   string
	Important bool // the status changed
}

// MonitorState is a monitor's current status, as package state keeps it.
type MonitorState struct {
	MonitorID   int64
	Status      string
	Since       time.Time
	Message     string
	LastCheck   time.Time
	Latency     time.Duration // of the last check; negative: none
	CertExpires time.Time     // zero: no certificate seen
	Failures    int
	DownChecks  int
	// CertNotified is the certificate expiry warning last sent for
	// CertExpires, as its threshold in days; 0 for none.
	CertNotified int
	// BeforeStatus and BeforeSince are, during maintenance, the status the
	// monitor had before it began; "" outside maintenance.
	BeforeStatus string
	BeforeSince  time.Time
}

// Record is a heartbeat with the state it left the monitor in.
type Record struct {
	Beat  Beat
	State MonitorState
}

// Bucket sizes of the stats tables.
const (
	hour = int64(time.Hour / time.Millisecond)
	day  = int64(24 * time.Hour / time.Millisecond)
)

// millisOrZero is t in milliseconds, or 0 for the zero time.
func millisOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func latencyMS(d time.Duration) int64 {
	if d < 0 {
		return -1
	}
	return d.Milliseconds()
}

func latencyUS(d time.Duration) int64 {
	if d < 0 {
		return -1
	}
	return d.Microseconds()
}

func fromLatency(us int64) time.Duration {
	if us < 0 {
		return -1
	}
	return time.Duration(us) * time.Microsecond
}

// Save writes heartbeats and states in one transaction, and adds each beat
// to the hourly and daily stats. Pending checks count as up in the stats:
// a monitor is not down until its retries run out. Checks during
// maintenance are left out of the stats. Beats of monitors that were
// deleted meanwhile are skipped.
func (s *Store) Save(ctx context.Context, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	for _, r := range recs {
		var exists bool
		if err := tx.QueryRowContext(ctx, s.q(`SELECT EXISTS (SELECT 1 FROM monitors WHERE id = ?)`), r.Beat.MonitorID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := s.saveOne(ctx, tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) saveOne(ctx context.Context, tx *sql.Tx, r Record) error {
	b, st := r.Beat, r.State
	at := b.At.UnixMilli()
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	if _, err := tx.ExecContext(ctx, s.q(`
		INSERT INTO heartbeats (monitor_id, at, status, latency_ms, latency_us, message, important) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		b.MonitorID, at, b.Status, latencyMS(b.Latency), latencyUS(b.Latency), b.Message, b.Important); err != nil {
		return err
	}
	var cert int64
	if !st.CertExpires.IsZero() {
		cert = st.CertExpires.UnixMilli()
	}
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	if _, err := tx.ExecContext(ctx, s.q(`
		INSERT INTO monitor_state (monitor_id, status, since, message, last_check, latency_ms, latency_us, cert_expires, failures,
			down_checks, cert_notified, before_status, before_since)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (monitor_id) DO UPDATE SET status = excluded.status, since = excluded.since, message = excluded.message,
			last_check = excluded.last_check, latency_ms = excluded.latency_ms, latency_us = excluded.latency_us, cert_expires = excluded.cert_expires,
			failures = excluded.failures, down_checks = excluded.down_checks, cert_notified = excluded.cert_notified,
			before_status = excluded.before_status, before_since = excluded.before_since`),
		st.MonitorID, st.Status, st.Since.UnixMilli(), st.Message, st.LastCheck.UnixMilli(), latencyMS(st.Latency), latencyUS(st.Latency), cert,
		st.Failures, st.DownChecks, st.CertNotified, st.BeforeStatus, millisOrZero(st.BeforeSince)); err != nil {
		return err
	}
	if b.Status == "maintenance" {
		return nil
	}
	up, down := 1, 0
	if b.Status == "down" {
		up, down = 0, 1
	}
	var sum, us, count int64
	if b.Latency >= 0 {
		sum, us, count = b.Latency.Milliseconds(), b.Latency.Microseconds(), 1
	}
	for _, t := range []struct {
		table string
		size  int64
	}{{"stats_hourly", hour}, {"stats_daily", day}} {
		// The table name is one of the two constants above; the values are
		// bound arguments.
		// nosemgrep
		if _, err := tx.ExecContext(ctx, s.q(`
			INSERT INTO `+t.table+` (monitor_id, bucket, up, down, latency_sum, latency_us_sum, latency_count)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (monitor_id, bucket) DO UPDATE SET up = `+t.table+`.up + excluded.up,
				down = `+t.table+`.down + excluded.down, latency_sum = `+t.table+`.latency_sum + excluded.latency_sum,
				latency_us_sum = `+t.table+`.latency_us_sum + excluded.latency_us_sum,
				latency_count = `+t.table+`.latency_count + excluded.latency_count`),
			b.MonitorID, at-at%t.size, up, down, sum, us, count); err != nil {
			return err
		}
	}
	return nil
}

// States returns every monitor's saved state, by monitor ID.
func (s *Store) States(ctx context.Context) (map[int64]MonitorState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT monitor_id, status, since, message, last_check, latency_us, cert_expires,
		failures, down_checks, cert_notified, before_status, before_since FROM monitor_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]MonitorState{}
	for rows.Next() {
		var st MonitorState
		var since, last, latency, cert, before int64
		if err := rows.Scan(&st.MonitorID, &st.Status, &since, &st.Message, &last, &latency, &cert, &st.Failures, &st.DownChecks,
			&st.CertNotified, &st.BeforeStatus, &before); err != nil {
			return nil, err
		}
		st.Since, st.LastCheck, st.Latency, st.CertExpires = fromMillis(since), fromMillis(last), fromLatency(latency), fromMillis(cert)
		st.BeforeSince = fromMillis(before)
		out[st.MonitorID] = st
	}
	return out, rows.Err()
}

func scanBeats(rows *sql.Rows) ([]Beat, error) {
	defer rows.Close()
	var out []Beat
	for rows.Next() {
		var b Beat
		var at, latency int64
		if err := rows.Scan(&b.MonitorID, &at, &b.Status, &latency, &b.Message, &b.Important); err != nil {
			return nil, err
		}
		b.At, b.Latency = fromMillis(at), fromLatency(latency)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Heartbeats returns a monitor's heartbeats from since on, newest first, at
// most limit of them.
func (s *Store) Heartbeats(ctx context.Context, id int64, since time.Time, limit int) ([]Beat, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`
		SELECT monitor_id, at, status, latency_us, message, important FROM heartbeats
		WHERE monitor_id = ? AND at >= ? ORDER BY at DESC, id DESC LIMIT ?`), id, since.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	return scanBeats(rows)
}

// RecentBeats returns the last n heartbeats of every monitor, oldest first.
func (s *Store) RecentBeats(ctx context.Context, n int) (map[int64][]Beat, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`
		SELECT monitor_id, at, status, latency_us, message, important FROM (
			SELECT monitor_id, at, status, latency_us, message, important, id,
				ROW_NUMBER() OVER (PARTITION BY monitor_id ORDER BY at DESC, id DESC) AS n
			FROM heartbeats) recent
		WHERE n <= ? ORDER BY monitor_id, at, id`), n)
	if err != nil {
		return nil, err
	}
	beats, err := scanBeats(rows)
	out := map[int64][]Beat{}
	for _, b := range beats {
		out[b.MonitorID] = append(out[b.MonitorID], b)
	}
	return out, err
}

// Totals are check counts over a period.
type Totals struct {
	Up, Down     int64
	LatencySum   int64 // microseconds
	LatencyCount int64
}

// Uptime is the share of checks that were up, from 0 to 1; -1 without checks.
func (t Totals) Uptime() float64 {
	if t.Up+t.Down == 0 {
		return -1
	}
	return float64(t.Up) / float64(t.Up+t.Down)
}

// AvgLatency is the mean response time; -1 without any.
func (t Totals) AvgLatency() time.Duration {
	if t.LatencyCount == 0 {
		return -1
	}
	return time.Duration(t.LatencySum/t.LatencyCount) * time.Microsecond
}

// HourlyTotals adds up every monitor's hourly stats from the hour that
// contains since.
func (s *Store) HourlyTotals(ctx context.Context, since time.Time) (map[int64]Totals, error) {
	return s.totals(ctx, "stats_hourly", since.UnixMilli()-since.UnixMilli()%hour)
}

// DailyTotals adds up every monitor's daily stats from the day that
// contains since.
func (s *Store) DailyTotals(ctx context.Context, since time.Time) (map[int64]Totals, error) {
	return s.totals(ctx, "stats_daily", since.UnixMilli()-since.UnixMilli()%day)
}

func (s *Store) totals(ctx context.Context, table string, from int64) (map[int64]Totals, error) {
	// The table name is one of two constants; from is a bound argument.
	// nosemgrep
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT monitor_id, CAST(SUM(up) AS BIGINT), CAST(SUM(down) AS BIGINT),
		CAST(SUM(latency_us_sum) AS BIGINT), CAST(SUM(latency_count) AS BIGINT)
		FROM `+table+` WHERE bucket >= ? GROUP BY monitor_id`), from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Totals{}
	for rows.Next() {
		var id int64
		var t Totals
		if err := rows.Scan(&id, &t.Up, &t.Down, &t.LatencySum, &t.LatencyCount); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// HourBucket is one monitor's counts for one hour, or one day from Days.
type HourBucket struct {
	Start time.Time
	Totals
}

// Hours returns a monitor's hourly stats from the hour that contains since,
// oldest first.
func (s *Store) Hours(ctx context.Context, id int64, since time.Time) ([]HourBucket, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT bucket, up, down, latency_us_sum, latency_count FROM stats_hourly
		WHERE monitor_id = ? AND bucket >= ? ORDER BY bucket`), id, since.UnixMilli()-since.UnixMilli()%hour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourBucket
	for rows.Next() {
		var b HourBucket
		var start int64
		if err := rows.Scan(&start, &b.Up, &b.Down, &b.LatencySum, &b.LatencyCount); err != nil {
			return nil, err
		}
		b.Start = fromMillis(start)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Days returns every monitor's daily stats from the day that contains
// since (days in UTC), oldest first, by monitor ID.
func (s *Store) Days(ctx context.Context, since time.Time) (map[int64][]HourBucket, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT monitor_id, bucket, up, down, latency_us_sum, latency_count FROM stats_daily
		WHERE bucket >= ? ORDER BY monitor_id, bucket`), since.UnixMilli()-since.UnixMilli()%day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]HourBucket{}
	for rows.Next() {
		var b HourBucket
		var id, start int64
		if err := rows.Scan(&id, &start, &b.Up, &b.Down, &b.LatencySum, &b.LatencyCount); err != nil {
			return nil, err
		}
		b.Start = fromMillis(start)
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}

// Prune deletes heartbeats older than beats, hourly stats older than hours
// and daily stats and audit log entries older than days.
func (s *Store) Prune(ctx context.Context, beats, hours, days time.Time) error {
	for _, p := range []struct {
		query  string
		before time.Time
	}{
		{`DELETE FROM heartbeats WHERE at < ?`, beats},
		{`DELETE FROM stats_hourly WHERE bucket < ?`, hours},
		{`DELETE FROM stats_daily WHERE bucket < ?`, days},
		{`DELETE FROM audit_log WHERE at < ?`, days},
	} {
		if _, err := s.db.ExecContext(ctx, s.q(p.query), p.before.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}
