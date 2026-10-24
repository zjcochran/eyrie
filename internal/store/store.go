// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store keeps Eyrie's data in SQLite or PostgreSQL.
//
// Queries are written once, with ? placeholders, in SQL both databases
// accept; the store rewrites the placeholders for PostgreSQL. Each database
// has its own migrations (migrations/sqlite, migrations/postgres), which must
// produce the same tables, columns and indexes: a test compares them.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	// The PostgreSQL driver, registered as "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	// The pure-Go SQLite driver, registered as "sqlite".
	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrations embed.FS

// Dialect is the kind of database.
type Dialect string

// The databases Eyrie runs on.
const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// ErrNewerDatabase means a newer version of Eyrie has migrated the
// database: downgrades are not supported.
var ErrNewerDatabase = errors.New("the database was used by a newer version of Eyrie")

// Store holds all of Eyrie's data.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// Open connects to the database and applies migrations. dsn is a
// postgres:// or postgresql:// URL for PostgreSQL; anything else is the path
// of a SQLite file, created if missing.
func Open(ctx context.Context, dsn string) (*Store, error) {
	s := &Store{dialect: SQLite}
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		s.dialect = Postgres
		s.db, err = sql.Open("pgx", dsn)
	} else {
		// Transactions take the write lock when they begin (BEGIN IMMEDIATE),
		// so a second writer waits for the first under busy_timeout instead of
		// failing at once with "database is locked".
		s.db, err = sql.Open("sqlite", "file:"+dsn+
			"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	}
	if err != nil {
		return nil, err
	}
	if err := s.db.PingContext(ctx); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("open the %s database: %w", s.dialect, err)
	}
	if err := s.migrate(ctx); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("migrate the %s database: %w", s.dialect, err)
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	dir, err := fs.Sub(migrations, "migrations/"+string(s.dialect))
	if err != nil {
		return err
	}
	gd := goose.DialectSQLite3
	if s.dialect == Postgres {
		gd = goose.DialectPostgres
	}
	p, err := goose.NewProvider(gd, s.db, dir)
	if err != nil {
		return err
	}
	// goose would start on a database a newer Eyrie migrated, whose tables
	// this version does not know; refuse it instead.
	have, err := p.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	sources := p.ListSources()
	if known := sources[len(sources)-1].Version; have > known {
		return fmt.Errorf("%w (schema %d; this version knows up to %d): run that version, or restore a backup made before the upgrade", ErrNewerDatabase, have, known)
	}
	_, err = p.Up(ctx)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Dialect is the kind of database the store uses.
func (s *Store) Dialect() Dialect { return s.dialect }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// q returns query for the store's database: PostgreSQL numbers its
// placeholders ($1, $2, …). Queries never contain a literal ? in a string.
func (s *Store) q(query string) string {
	if s.dialect != Postgres {
		return query
	}
	return rebind(query)
}

func rebind(query string) string {
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// GetSetting reads the setting key as JSON into v. It reports false, leaving
// v alone, when the setting was never saved.
func (s *Store) GetSetting(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT value FROM settings WHERE key = ?`), key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("setting %s: %w", key, err)
	}
	return true, nil
}

// SetSetting saves v as JSON under key, replacing what was there.
func (s *Store) SetSetting(ctx context.Context, key string, v any, now time.Time) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.q(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`),
		key, string(b), now.UnixMilli())
	return err
}
