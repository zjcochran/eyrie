# Configuration reference

Eyrie reads its configuration from environment variables at start
(`internal/config`). Leading and trailing spaces are ignored, except in
`EYRIE_SECRET_KEY`. An invalid value stops Eyrie with an error naming the
variable.

| Variable | Default | Meaning |
| --- | --- | --- |
| `EYRIE_LISTEN` | `:8080` | Address the web server listens on: `host:port` or `:port`. |
| `EYRIE_DATA_DIR` | `/data` | Folder for the SQLite database (`eyrie.db`) and the secret key file (`secret.key`). Created (mode 0700) when SQLite is used and it is missing. |
| `EYRIE_DATABASE_URL` | empty | Empty: SQLite in `EYRIE_DATA_DIR`. A URL starting with `postgres://` or `postgresql://`: that PostgreSQL database. Anything else is refused. What the URL leaves out, such as the password, is read from the libpq variables (`PGPASSWORD`, `PGSSLMODE`, …), as pgx does. |
| `EYRIE_SECRET_KEY` | empty | Key for stored secrets. Any string; it is hashed with SHA-256 into the AES-256 key. When empty, the key is read from `EYRIE_DATA_DIR/secret.key`, which is created (mode 0600, 32 random bytes in base64) if missing. |
| `EYRIE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, in any case. The log is plain text (`key=value`) on standard error. |
| `TZ` | `UTC` | Time zone for the log, as an IANA name such as `Etc/UTC`. The zone database is built into the binary. |

## Command line

| Command | What it does | Exit code |
| --- | --- | --- |
| `eyrie`, `eyrie serve` | Runs the server until SIGINT or SIGTERM, then stops it within 10 seconds. | 0 after a clean stop, 1 on an error |
| `eyrie healthcheck` | `GET /healthz` on the running server, with a 3-second timeout. The address comes from `EYRIE_LISTEN`; an empty host, `0.0.0.0` or `::` becomes `127.0.0.1`. | 0 if it answered 200, else 1 |
| `eyrie version` | Prints the version set at build time (`dev` otherwise). | 0 |

Any other command, or any argument after one, prints the usage and exits 2.
