package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// postgresDSN is a PostgreSQL server for the tests, in CI a service
// container. Without it, only SQLite is tested.
func postgresDSN() string { return os.Getenv("EYRIE_TEST_POSTGRES_DSN") }

func openSQLite(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// openPostgres opens a store in a new, empty schema of the test server, so
// tests do not see each other's data. The schema is dropped afterwards.
func openPostgres(t *testing.T) *Store {
	t.Helper()
	dsn := postgresDSN()
	if dsn == "" {
		t.Skip("EYRIE_TEST_POSTGRES_DSN is not set")
	}
	b := make([]byte, 6)
	rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	// PostgreSQL takes no placeholder for a schema name; it is the random
	// hex made above.
	// nosemgrep
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The same schema name, made by this test.
		// nosemgrep
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	s, err := Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// eachDB runs f against SQLite and, when a test server is set, PostgreSQL.
func eachDB(t *testing.T, f func(t *testing.T, s *Store)) {
	t.Run("sqlite", func(t *testing.T) { f(t, openSQLite(t)) })
	t.Run("postgres", func(t *testing.T) { f(t, openPostgres(t)) })
}

func TestOpen(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		if err := s.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	s := openSQLite(t)
	if s.Dialect() != SQLite {
		t.Errorf("dialect = %s", s.Dialect())
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eyrie.db")
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSetting(ctx, "k", 1, time.Now())
	s.Close()
	s, err = Open(ctx, path) // migrations already applied
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if ok, err := s.GetSetting(ctx, "k", &n); !ok || err != nil || n != 1 {
		t.Errorf("after reopening: %v %v %d", ok, err, n)
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "missing", "eyrie.db")); err == nil {
		t.Error("opened a database in a missing folder")
	}
	// A file that is not a database fails the migrations.
	path := filepath.Join(t.TempDir(), "eyrie.db")
	os.WriteFile(path, []byte(strings.Repeat("not a database ", 100)), 0o600)
	if _, err := Open(ctx, path); err == nil {
		t.Error("opened a file that is not a database")
	}
	// A URL that pgx cannot parse fails before anything is dialled.
	if _, err := Open(ctx, "postgres://%zz"); err == nil {
		t.Error("opened a malformed PostgreSQL URL")
	}
}

func TestRebind(t *testing.T) {
	s := &Store{dialect: Postgres}
	if got := s.q(`SELECT a FROM t WHERE b = ? AND c IN (?, ?)`); got != `SELECT a FROM t WHERE b = $1 AND c IN ($2, $3)` {
		t.Errorf("postgres: %s", got)
	}
	s.dialect = SQLite
	if got := s.q(`WHERE b = ?`); got != `WHERE b = ?` {
		t.Errorf("sqlite: %s", got)
	}
}

func TestSettings(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		type value struct{ A string }
		var v value
		if ok, err := s.GetSetting(ctx, "x", &v); ok || err != nil {
			t.Fatalf("unset: %v %v", ok, err)
		}
		now := time.Now()
		if err := s.SetSetting(ctx, "x", value{"one"}, now); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSetting(ctx, "x", value{"two"}, now); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.GetSetting(ctx, "x", &v); !ok || err != nil || v.A != "two" {
			t.Errorf("get: %v %v %+v", ok, err, v)
		}
		if err := s.SetSetting(ctx, "bad", func() {}, now); err == nil {
			t.Error("saved a value that is not JSON")
		}
		s.db.Exec(s.q(`UPDATE settings SET value = ? WHERE key = ?`), "{", "x")
		if _, err := s.GetSetting(ctx, "x", &v); err == nil {
			t.Error("read a corrupt setting")
		}
		s.Close()
		if _, err := s.GetSetting(ctx, "x", &v); err == nil {
			t.Error("read from a closed database")
		}
	})
}

// column is one column as both databases describe it, with the type reduced
// to what Eyrie relies on.
type column struct {
	Table, Name, Type string
	NotNull, Key      bool
}

var types = map[string]string{
	"integer": "int", "bigint": "int", "smallint": "int",
	"text": "text", "character varying": "text",
	"boolean": "bool", "blob": "bytes", "bytea": "bytes",
	"real": "float", "double precision": "float",
}

func schema(t *testing.T, s *Store) (cols []column, indexes []string) {
	t.Helper()
	colQuery := `
		SELECT m.name, p.name, p.type, p."notnull", p.pk > 0
		FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name <> 'goose_db_version'
		ORDER BY m.name, p.cid`
	idxQuery := `SELECT tbl_name || '.' || name FROM sqlite_master
		WHERE type = 'index' AND sql IS NOT NULL ORDER BY 1`
	if s.dialect == Postgres {
		colQuery = `
			SELECT c.table_name, c.column_name, c.data_type, c.is_nullable = 'NO',
				EXISTS (SELECT 1 FROM information_schema.table_constraints tc
					JOIN information_schema.key_column_usage k
						ON k.constraint_name = tc.constraint_name AND k.table_schema = tc.table_schema
					WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = c.table_schema
						AND tc.table_name = c.table_name AND k.column_name = c.column_name)
			FROM information_schema.columns c
			WHERE c.table_schema = current_schema() AND c.table_name <> 'goose_db_version'
			ORDER BY c.table_name, c.ordinal_position`
		idxQuery = `SELECT tablename || '.' || indexname FROM pg_indexes
			WHERE schemaname = current_schema() AND indexname NOT LIKE '%_pkey' ORDER BY 1`
	}
	rows, err := s.db.Query(colQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.Table, &c.Name, &c.Type, &c.NotNull, &c.Key); err != nil {
			t.Fatal(err)
		}
		norm, ok := types[strings.ToLower(c.Type)]
		if !ok {
			t.Errorf("%s.%s: type %q is not one both databases share", c.Table, c.Name, c.Type)
		}
		c.Type = norm
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	irows, err := s.db.Query(idxQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer irows.Close()
	for irows.Next() {
		var name string
		irows.Scan(&name)
		indexes = append(indexes, name)
	}
	if err := irows.Err(); err != nil {
		t.Fatal(err)
	}
	return cols, indexes
}

// TestSchemasMatch checks that the two migration trees build the same
// schema. Without a PostgreSQL server it checks only that SQLite's column
// types are ones PostgreSQL has too.
func TestSchemasMatch(t *testing.T) {
	lite, liteIdx := schema(t, openSQLite(t))
	if len(lite) == 0 {
		t.Fatal("no tables")
	}
	pg, pgIdx := schema(t, openPostgres(t))
	if !reflect.DeepEqual(lite, pg) {
		t.Errorf("columns differ:\nsqlite   %+v\npostgres %+v", lite, pg)
	}
	if !reflect.DeepEqual(liteIdx, pgIdx) {
		t.Errorf("indexes differ:\nsqlite   %v\npostgres %v", liteIdx, pgIdx)
	}
}

func TestMigrationsInStep(t *testing.T) {
	list := func(dir string) []string {
		entries, err := migrations.ReadDir("migrations/" + dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	if a, b := list("sqlite"), list("postgres"); !reflect.DeepEqual(a, b) {
		t.Errorf("migration files differ:\nsqlite   %v\npostgres %v", a, b)
	}
}
