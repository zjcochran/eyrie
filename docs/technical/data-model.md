# Data model

## Two databases

Eyrie runs on SQLite (the default) or PostgreSQL (`internal/store`):

| | SQLite | PostgreSQL |
| --- | --- | --- |
| Driver | `modernc.org/sqlite`, pure Go | `github.com/jackc/pgx/v5` (`stdlib`) |
| Chosen when | `EYRIE_DATABASE_URL` is empty | `EYRIE_DATABASE_URL` starts with `postgres://` or `postgresql://` |
| Connection | `EYRIE_DATA_DIR/eyrie.db`, WAL journal, `busy_timeout` 5000 ms, foreign keys on, transactions begin immediately (they take the write lock at `BEGIN`) | The URL as given |

Queries are written once with `?` placeholders, in SQL both databases
accept (`ON CONFLICT … DO UPDATE`, `RETURNING`); for PostgreSQL the store
rewrites the placeholders to `$1`, `$2`, …. Queries never contain a literal
`?` in a string.

Column conventions, the same in both:

| Kind | Type | Notes |
| --- | --- | --- |
| Times | `BIGINT` | Unix milliseconds, UTC |
| Text, JSON | `TEXT` | JSON documents are stored as text |
| Flags | `BOOLEAN` | SQLite stores 0 and 1 |
| Binary | `BLOB` / `BYTEA` | The only type spelled differently |

## Migrations

Each database has its own [goose](https://github.com/pressly/goose)
migrations, embedded in the binary: `internal/store/migrations/sqlite` and
`internal/store/migrations/postgres`. Pending migrations run at every start,
before the server listens; goose records them in `goose_db_version`.

Rules, enforced by tests in `internal/store`:

- Both folders hold the same file names (`TestMigrationsInStep`).
- Both produce the same tables, columns (order, type as in the table above,
  `NOT NULL`, primary key) and indexes (`TestSchemasMatch`, against a real
  PostgreSQL server in CI).
- Migrations are only added, never edited.

## Tables

### settings

Settings saved from the web UI, one JSON document per key.

| Column | Type | Notes |
| --- | --- | --- |
| `key` | `TEXT` | Primary key |
| `value` | `TEXT` | JSON |
| `updated_at` | `BIGINT` | Unix milliseconds of the last save |

No setting is stored yet.
