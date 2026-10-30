# Configuration reference

Eyrie reads its configuration from environment variables at start
(`internal/config`). They keep working across upgrades within a major
version (see [Upgrades and compatibility](../user/upgrading.md)). Leading and trailing spaces are ignored, except in
`EYRIE_SECRET_KEY`. An invalid value stops Eyrie with an error naming the
variable.

| Variable | Default | Meaning |
| --- | --- | --- |
| `EYRIE_LISTEN` | `:8080` | Address the web server listens on: `host:port` or `:port`. It listens with plain TCP, never MPTCP ([Security](security.md#listening-socket)). |
| `EYRIE_DATA_DIR` | `/data` | Folder for the SQLite database (`eyrie.db`) and the secret key file (`secret.key`). Created (mode 0700) when SQLite is used and it is missing. |
| `EYRIE_DATABASE_URL` | empty | Empty: SQLite in `EYRIE_DATA_DIR`. A URL starting with `postgres://` or `postgresql://`: that PostgreSQL database. Anything else is refused. What the URL leaves out, such as the password, is read from the libpq variables (`PGPASSWORD`, `PGSSLMODE`, …), as pgx does. |
| `EYRIE_BACKUP_DIR` | `<EYRIE_DATA_DIR>/backups` | Folder for [scheduled backup files](../user/backup-and-restore.md#scheduled-backups), created (mode 0750) at the first scheduled backup. |
| `EYRIE_SECRET_KEY` | empty | Key for stored secrets. Any string; it is hashed with SHA-256 into the AES-256 key. When empty, the key is read from `EYRIE_DATA_DIR/secret.key`, which is created (mode 0600, 32 random bytes in base64) if missing. |
| `EYRIE_BASE_URL` | empty | How browsers reach Eyrie, such as `https://eyrie.example.com` (a trailing `/` is removed; no query). Used for the single sign-on callback URL, marks the session cookie `Secure` when it is `https://`, and is a trusted origin for cross-origin protection. When empty, each request's own address is used (`X-Forwarded-Proto` and `X-Forwarded-Host` count only from trusted proxies). |
| `EYRIE_TRUSTED_PROXIES` | empty | Reverse proxies whose `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto` and `X-Forwarded-Host` headers are believed: IP addresses and CIDR ranges, separated by commas or spaces, such as `192.0.2.10, 198.51.100.0/24`. See [Security](security.md#trusted-proxies). |
| `EYRIE_LOCAL_LOGIN` | empty | `force` (any case): password sign-in works even when single sign-on is set to be the only way in, for when the provider cannot be reached. Eyrie logs a warning at start while it is set. Any other value is refused. |
| `EYRIE_SOURCE_URL` | the URL set at build time (published images: `https://github.com/zjcochran/eyrie`) | Where the source code of the running version is: an `http(s)` URL, linked from the footer of every page and given in `/api/v1/info` (AGPL section 13). Set it when you run a modified version. Without either, the footer links only to the licence. |
| `EYRIE_METRICS_TOKEN` | empty | Turns on `/metrics` (Prometheus) behind `Authorization: Bearer <token>`. Empty: `/metrics` answers 404. See [HTTP interface](http-interface.md#metrics). |
| `EYRIE_CLAUDE_CODE` | `false` | `true` (any case): Eyrie downloads Claude Code from Anthropic into `<data folder>/claude-code` at start and keeps it at the stable release, so [alert analysis](../user/alert-analysis.md#claude-with-your-subscription-claude-code) can use a Claude subscription. Sign it in with `CLAUDE_CODE_OAUTH_TOKEN`. Any value but `true` or `false` is refused. |
| `CLAUDE_CODE_OAUTH_TOKEN` | empty | Read by Claude Code, never by Eyrie: the token from `claude setup-token` that signs Claude Code in with your subscription. |
| `EYRIE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, in any case. The log is plain text (`key=value`) on standard error. `debug` adds a line per check and per HTTP request; [what is logged](security.md#logging). |
| `TZ` | `UTC` | Time zone for the log and the time of day of [scheduled backups](../user/backup-and-restore.md#scheduled-backups), as an IANA name such as `Etc/UTC`. The zone database is built into the binary. Maintenance windows have their own. |

## Agent settings

Read by `eyrie agent` only; see [Agents](../user/agents.md#settings).

| Variable | Default | Meaning |
| --- | --- | --- |
| `EYRIE_AGENT_URL` | (required) | Eyrie's address, `http://` or `https://`, without a query; a trailing `/` is removed. |
| `EYRIE_AGENT_TOKEN` | empty | The one-time enrollment token from Settings → Agents. Needed to enroll; a token other than the one the agent enrolled with enrolls it again with a new key. |
| `EYRIE_AGENT_PLATFORM` | `docker` | `docker`, `swarm`, `kubernetes` or `proxmox`. Anything else is refused. |
| `EYRIE_AGENT_DIR` | `/data/agent` | The agent's folder (mode 0700): `key`, `state.json`, `heartbeat`, and `key.next` during a rotation. |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | `docker` and `swarm`: the Docker engine, `unix:///path` or `tcp://host:port`. |
| `EYRIE_AGENT_NAMESPACES` | empty: all | `kubernetes`: the namespaces to list, separated by commas. |
| `PROXMOX_URL` | empty | `proxmox`: the API's address, `https://<host>:8006`. |
| `PROXMOX_TOKEN_ID`, `PROXMOX_TOKEN_SECRET` | empty | `proxmox`: the API token, `user@realm!name`, and its secret. |
| `PROXMOX_FINGERPRINT` | empty | `proxmox`: the SHA-256 fingerprint of the server's certificate, with or without colons, in any case. |
| `EYRIE_LOG_LEVEL` | `info` | As for the server. |

## Command line

| Command | What it does | Exit code |
| --- | --- | --- |
| `eyrie`, `eyrie serve` | Runs the server until SIGINT or SIGTERM, then stops it within 10 seconds. | 0 after a clean stop, 1 on an error |
| `eyrie agent` | Runs as an [agent](../user/agents.md) until SIGINT or SIGTERM: no database, web app or port; it runs the checks of the monitors on it and reports the workloads of its platform to Eyrie. Its settings are below. | 0 after a clean stop, 1 on an error (a bad setting, a refused enrollment, a Docker engine that is not a swarm manager for `swarm`) |
| `eyrie healthcheck` | `GET /healthz` on the running server, with a 3-second timeout. The address comes from `EYRIE_LISTEN`; an empty host, `0.0.0.0` or `::` becomes `127.0.0.1`. When `EYRIE_AGENT_URL` is set, it checks the agent instead: that it reported to Eyrie within 90 seconds (the modification time of `heartbeat` in `EYRIE_AGENT_DIR`). | 0 if it answered 200 (the agent reported), else 1 |
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
