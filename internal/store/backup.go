// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Backups carry whole tables, row by row, with values in a form both
// databases read: so a backup of SQLite restores into PostgreSQL and back.

// Row is one table row, column name to value: numbers, booleans, text, null,
// and bytes as base64 text.
type Row map[string]any

// Snapshot is the data a backup carries.
type Snapshot struct {
	Config  map[string][]Row `json:"config"`
	History map[string][]Row `json:"history,omitempty"` // nil: no history
}

// ConfigTables are copied whole, in an order that satisfies the foreign
// keys. Sessions are never copied: they belong to the running instance.
var ConfigTables = []string{
	"settings", "users", "oidc_identities", "recovery_codes", "user_pictures", "api_tokens",
	"notification_channels", "tags", "monitors", "monitor_channels", "monitor_tags",
	"maintenance_windows", "maintenance_monitors", "maintenance_tags",
	"status_pages", "status_page_logos", "status_page_sections", "status_page_monitors", "incidents", "incident_updates",
}

// historyTables are a monitor's state and history; column, when set, is
// the time in milliseconds a period is chosen by.
var historyTables = []struct{ name, column string }{
	{"monitor_state", ""},
	{"heartbeats", "at"},
	{"stats_hourly", "bucket"},
	{"stats_daily", "bucket"},
}

// loadHistory inserts the history tables' rows.
func (s *Store) loadHistory(ctx context.Context, tx *sql.Tx, history map[string][]Row) error {
	for _, h := range historyTables {
		if err := s.load(ctx, tx, h.name, history[h.name]); err != nil {
			return err
		}
	}
	return fillMicros(ctx, tx)
}

// fillMicros gives the response times of a backup from before they were
// kept in microseconds their value from the milliseconds, as migration
// 00007 does.
func fillMicros(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`UPDATE monitor_state SET latency_us = latency_ms * 1000 WHERE latency_us < 0 AND latency_ms >= 0`,
		`UPDATE heartbeats SET latency_us = latency_ms * 1000 WHERE latency_us < 0 AND latency_ms >= 0`,
		`UPDATE stats_hourly SET latency_us_sum = latency_sum * 1000 WHERE latency_us_sum < 0`,
		`UPDATE stats_daily SET latency_us_sum = latency_sum * 1000 WHERE latency_us_sum < 0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// selfRefs are columns that name a row of their own table: they are set
// once every row is in.
var selfRefs = map[string]string{"monitors": "parent_id"}

// Errors Import returns.
var (
	ErrNoAdmin = errors.New("the backup has no enabled admin account")
)

// SchemaVersion is the latest migration applied.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&v)
	return v, err
}

// querier is what reads rows: a transaction or a connection.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Export reads the configuration and, with history, the monitors' state
// and history from since onwards (all of it when since is zero), in one
// consistent read.
func (s *Store) Export(ctx context.Context, history bool, since time.Time) (Snapshot, error) {
	q, done, err := s.readTx(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer done()
	snap := Snapshot{Config: map[string][]Row{}}
	for _, t := range ConfigTables {
		if snap.Config[t], err = s.dump(ctx, q, t, "", 0); err != nil {
			return Snapshot{}, fmt.Errorf("export %s: %w", t, err)
		}
	}
	if !history {
		return snap, nil
	}
	snap.History = map[string][]Row{}
	for _, h := range historyTables {
		if snap.History[h.name], err = s.dump(ctx, q, h.name, h.column, millisOrZero(since)); err != nil {
			return Snapshot{}, fmt.Errorf("export %s: %w", h.name, err)
		}
	}
	return snap, nil
}

// readTx starts a read that sees one moment of the database. On SQLite it
// is a deferred transaction, which takes no write lock, so checks go on
// saving while a backup is read.
func (s *Store) readTx(ctx context.Context) (querier, func(), error) {
	if s.dialect == Postgres {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return nil, nil, err
		}
		return tx, func() { tx.Rollback() }, nil //nolint:errcheck,gosec // read-only
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.ExecContext(ctx, `BEGIN DEFERRED`); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, func() {
		conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`) //nolint:errcheck,gosec // read-only
		conn.Close()
	}, nil
}

// column kinds, the same in both databases.
const (
	kindInt   = "int"
	kindBool  = "bool"
	kindBytes = "bytes"
	kindFloat = "float"
	kindText  = "text"
)

type colInfo struct {
	kind     string
	identity bool // PostgreSQL makes its values
}

// columns lists a table's columns and their kinds, so only real column
// names reach the SQL.
func (s *Store) columns(ctx context.Context, q querier, table string) (map[string]colInfo, error) {
	query := `SELECT name, type, 0 FROM pragma_table_info(?)`
	if s.dialect == Postgres {
		query = `SELECT column_name, data_type, CASE WHEN is_identity = 'YES' THEN 1 ELSE 0 END
			FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1`
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]colInfo{}
	for rows.Next() {
		var name, typ string
		var identity int
		if err := rows.Scan(&name, &typ, &identity); err != nil {
			return nil, err
		}
		out[name] = colInfo{kind: kindOf(typ), identity: identity == 1}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no table %s", table)
	}
	return out, nil
}

func kindOf(typ string) string {
	t := strings.ToUpper(typ)
	switch {
	case strings.Contains(t, "INT"):
		return kindInt
	case strings.Contains(t, "BOOL"):
		return kindBool
	case strings.Contains(t, "BLOB"), strings.Contains(t, "BYTEA"):
		return kindBytes
	case strings.Contains(t, "REAL"), strings.Contains(t, "DOUBLE"), strings.Contains(t, "FLOAT"), strings.Contains(t, "NUMERIC"):
		return kindFloat
	}
	return kindText
}

// dump reads a table's rows; with a time column, only those from since on.
func (s *Store) dump(ctx context.Context, q querier, table, timeColumn string, since int64) ([]Row, error) {
	cols, err := s.columns(ctx, q, table)
	if err != nil {
		return nil, err
	}
	query := `SELECT * FROM ` + table
	var args []any
	if timeColumn != "" {
		query += ` WHERE ` + timeColumn + ` >= ?`
		args = append(args, since)
	}
	// In one order whatever the database: by every column, the key first.
	order := make([]string, len(cols))
	for i := range order {
		order[i] = strconv.Itoa(i + 1)
	}
	query += ` ORDER BY ` + strings.Join(order, ", ")
	// The table and column names are from the fixed lists above.
	// nosemgrep
	rows, err := q.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []Row{}
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, n := range names {
			r[n] = exportValue(cols[n].kind, vals[i])
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// exportValue writes a value the same way whichever database it came from.
func exportValue(kind string, v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if kind == kindBytes {
			return base64.StdEncoding.EncodeToString(x)
		}
		return string(x)
	case int64:
		if kind == kindBool {
			return x != 0
		}
	case int32:
		return int64(x)
	}
	return v
}

// importValue turns a value from a backup into one for a column.
func (s *Store) importValue(kind string, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch kind {
	case kindInt:
		switch n := v.(type) {
		case json.Number:
			return n.Int64()
		case float64:
			return int64(n), nil
		}
	case kindFloat:
		if n, ok := v.(json.Number); ok {
			return n.Float64()
		}
		if f, ok := v.(float64); ok {
			return f, nil
		}
	case kindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%v is not true or false", v)
		}
		if s.dialect == Postgres {
			return b, nil
		}
		if b {
			return int64(1), nil
		}
		return int64(0), nil
	case kindBytes:
		if t, ok := v.(string); ok {
			return base64.StdEncoding.DecodeString(t)
		}
	default:
		if t, ok := v.(string); ok {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%v is not a %s value", v, kind)
}

// Import replaces the configuration with snap's, and the monitors' state and
// history with snap's history (none when it has none: the history belongs to
// the monitors replaced), in one transaction: on any error nothing changes.
// Every session ends, since the users are replaced. With empty, it refuses
// (ErrSetupDone) when an account exists: a restore in place of setup.
func (s *Store) Import(ctx context.Context, snap Snapshot, empty bool) error {
	for t := range snap.Config {
		if !slices.Contains(ConfigTables, t) {
			return fmt.Errorf("unknown table %q in the backup", t)
		}
	}
	for t := range snap.History {
		if !isHistoryTable(t) {
			return fmt.Errorf("unknown table %q in the backup", t)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if empty {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupDone
		}
	}
	// Children first, so the foreign keys hold at every step.
	order := []string{"sessions"}
	for i := len(historyTables) - 1; i >= 0; i-- {
		order = append(order, historyTables[i].name)
	}
	for i := len(ConfigTables) - 1; i >= 0; i-- {
		order = append(order, ConfigTables[i])
	}
	for _, t := range order {
		// Table names come from the fixed lists above, never from the backup.
		// nosemgrep
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil { //nolint:gosec // G202: fixed table names
			return fmt.Errorf("clear %s: %w", t, err)
		}
	}
	for _, t := range ConfigTables {
		if err := s.load(ctx, tx, t, snap.Config[t]); err != nil {
			return err
		}
	}
	if err := s.loadHistory(ctx, tx, snap.History); err != nil {
		return err
	}
	if err := s.resetIdentities(ctx, tx); err != nil {
		return err
	}
	var admins int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM users WHERE role = ? AND disabled = ?`), RoleAdmin, false).Scan(&admins); err != nil {
		return err
	}
	if admins == 0 {
		return ErrNoAdmin
	}
	return tx.Commit()
}

// load inserts a table's rows.
func (s *Store) load(ctx context.Context, tx *sql.Tx, table string, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	cols, err := s.columns(ctx, tx, table)
	if err != nil {
		return err
	}
	self := selfRefs[table]
	var later [][2]any // id, the self reference
	for i, r := range rows {
		names := make([]string, 0, len(r))
		for c := range r {
			if _, ok := cols[c]; !ok {
				return fmt.Errorf("%s row %d: unknown column %q", table, i+1, c)
			}
			names = append(names, c)
		}
		slices.Sort(names)
		args := make([]any, len(names))
		identity := false
		for j, c := range names {
			v, err := s.importValue(cols[c].kind, r[c])
			if err != nil {
				return fmt.Errorf("%s row %d, %s: %w", table, i+1, c, err)
			}
			if c == self && v != nil {
				later = append(later, [2]any{r["id"], v})
				v = nil
			}
			args[j] = v
			identity = identity || cols[c].identity
		}
		over := ""
		if identity {
			over = ` OVERRIDING SYSTEM VALUE`
		}
		// The table is from the fixed lists, every column was checked
		// against the schema above, and values are bound arguments.
		q := `INSERT INTO ` + table + ` (` + strings.Join(names, ", ") + `)` + over + ` VALUES (?` + strings.Repeat(", ?", len(names)-1) + `)` //nolint:gosec // G202: names checked against the schema
		// nosemgrep
		if _, err := tx.ExecContext(ctx, s.q(q), args...); err != nil {
			return fmt.Errorf("%s row %d: %w", table, i+1, err)
		}
	}
	for _, l := range later {
		id, err := s.importValue(kindInt, l[0])
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		// The same fixed table and column.
		// nosemgrep
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE `+table+` SET `+self+` = ? WHERE id = ?`), l[1], id); err != nil { //nolint:gosec // G202: fixed names
			return fmt.Errorf("%s %v: %w", table, l[0], err)
		}
	}
	return nil
}

// resetIdentities moves PostgreSQL's identity sequences past the restored
// ids, so new rows do not collide with them. SQLite needs nothing.
func (s *Store) resetIdentities(ctx context.Context, tx *sql.Tx) error {
	if s.dialect != Postgres {
		return nil
	}
	for _, t := range append(slices.Clone(ConfigTables), "heartbeats") {
		cols, err := s.columns(ctx, tx, t)
		if err != nil {
			return err
		}
		if c, ok := cols["id"]; !ok || !c.identity {
			continue
		}
		// The table is from the fixed lists.
		// nosemgrep
		_, err = tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence($1, 'id'), COALESCE((SELECT MAX(id) FROM `+t+`), 0) + 1, false)`, t) //nolint:gosec // G202: fixed table names
		if err != nil {
			return fmt.Errorf("reset %s ids: %w", t, err)
		}
	}
	return nil
}

func isHistoryTable(t string) bool {
	for _, h := range historyTables {
		if h.name == t {
			return true
		}
	}
	return false
}
