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
| `EYRIE_BASE_URL` | empty | How browsers reach Eyrie, such as `https://eyrie.example.com` (a trailing `/` is removed; no query). Used for the single sign-on callback URL, marks the session cookie `Secure` when it is `https://`, and is a trusted origin for cross-origin protection. When empty, each request's own address is used (`X-Forwarded-Proto` and `X-Forwarded-Host` count only from trusted proxies). |
| `EYRIE_TRUSTED_PROXIES` | empty | Reverse proxies whose `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto` and `X-Forwarded-Host` headers are believed: IP addresses and CIDR ranges, separated by commas or spaces, such as `192.0.2.10, 198.51.100.0/24`. See [Security](security.md#trusted-proxies). |
| `EYRIE_LOCAL_LOGIN` | empty | `force` (any case): password sign-in works even when single sign-on is set to be the only way in, for when the provider cannot be reached. Eyrie logs a warning at start while it is set. Any other value is refused. |
| `EYRIE_SOURCE_URL` | the URL set at build time (published images: `https://github.com/zjcochran/eyrie`) | Where the source code of the running version is: an `http(s)` URL, linked from the footer of every page and given in `/api/v1/info` (AGPL section 13). Set it when you run a modified version. Without either, the footer links only to the licence. |
| `EYRIE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, in any case. The log is plain text (`key=value`) on standard error. |
| `TZ` | `UTC` | Time zone for the log, as an IANA name such as `Etc/UTC`. The zone database is built into the binary. |

## Command line

| Command | What it does | Exit code |
| --- | --- | --- |
| `eyrie`, `eyrie serve` | Runs the server until SIGINT or SIGTERM, then stops it within 10 seconds. | 0 after a clean stop, 1 on an error |
| `eyrie healthcheck` | `GET /healthz` on the running server, with a 3-second timeout. The address comes from `EYRIE_LISTEN`; an empty host, `0.0.0.0` or `::` becomes `127.0.0.1`. | 0 if it answered 200, else 1 |
| `eyrie reset-password <user>` | Sets the user's password to the first line of standard input (at least 12 characters) and ends their sessions. The username is matched ignoring case. Also gives an account that signs in only with single sign-on a password. Uses the same database settings as the server, which may be running. | 0 when changed, else 1 |
| `eyrie reset-2fa <user>` | Turns off the user's two-factor sign-in and deletes their recovery codes, for a lost phone. | 0, also when it was off; 1 for an unknown user |
| `eyrie version` | Prints the version set at build time (`dev` otherwise). | 0 |

Any other command, or the wrong number of arguments, prints the usage and
exits 2. In the container:

```sh
echo 'a new long password' | docker compose exec -T eyrie /eyrie reset-password alice
docker compose exec eyrie /eyrie reset-2fa alice
```

## Build settings

The Dockerfile takes two build arguments, passed to the Go linker:

| Build argument | Linker flag | Default | Meaning |
| --- | --- | --- | --- |
| `VERSION` | `main.version` | `dev` | The version Eyrie reports |
| `SOURCE_URL` | `main.sourceURL` | empty | Default for `EYRIE_SOURCE_URL`; also the image's `org.opencontainers.image.source` label |

The image is labelled `org.opencontainers.image.licenses=AGPL-3.0-or-later`.
