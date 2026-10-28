# Architecture

Eyrie is one Go binary (`cmd/eyrie`). It serves the JSON API and the web
app, and runs the monitor engine. It runs as a single instance.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/eyrie` | Entry point: the `serve`, `healthcheck`, `reset-password`, `reset-2fa` and `version` commands, wiring, graceful shutdown, the daily session prune. |
| `internal/config` | Environment variables, defaults, validation. See the [configuration reference](configuration-reference.md). |
| `internal/store` | All saved data, in SQLite or PostgreSQL, with migrations for each. See the [data model](data-model.md). |
| `internal/secrets` | AES-256-GCM sealing of secrets the store keeps (sealed values start with `v1:`). |
| `internal/state` | The status of a monitor from its check results: a pure state machine (up, pending, down; retries, notifications, upside-down). |
| `internal/maintenance` | When maintenance windows are on: a pure function of a schedule (manual, once, every N days, weekdays, days of the month, cron) and the time, in the schedule's time zone, with Eyrie's own cron parser. See [Maintenance windows](../user/maintenance.md). |
| `internal/check` | The monitor types: their settings, defaults and validation, and the checks themselves. Every check reaches the network through an `Env` (dialer, HTTP transport, resolver, pinger, program runner, the box that opens secret settings) that tests replace. Includes Eyrie's own clients for DNS, ICMP echo, NTP, RADIUS, SNMP (with BER and SNMPv3's security), SIP, SFTP (over `golang.org/x/crypto/ssh`), WebSocket, the Chrome DevTools Protocol, gRPC (with a `.proto` reader and the protobuf wire format), game server queries, Redis's RESP, MQTT 3.1.1 and a JSON path. Databases and Kafka go through their drivers (pgx, go-sql-driver/mysql, go-mssqldb, go-ora, mongo-driver, franz-go), each given the `Env`'s dialer. |
| `internal/workload` | What runs on a platform and whether it is up, for agents: read-only clients for Docker and Docker Swarm (the Engine API), Kubernetes (the API with the in-cluster ServiceAccount) and Proxmox VE (the API with a token, the certificate pinned by fingerprint). |
| `internal/agentauth` | The agent protocol: Ed25519 keys, signed requests, nonces, the messages. See [Agent protocol](agent-protocol.md). |
| `internal/agent` | `eyrie agent`: enrolls, then reports its platform's workloads every 30 seconds and on Docker events, and rotates its key. |
| `internal/agents` | Eyrie's side of its agents: adding and enrolling them, checking their signatures, keeping their reports in memory and the store, noticing silent ones, and answering agent monitors' checks (`check.Agents`). |
| `internal/engine` | Runs the monitors, below. |
| `internal/notify` | Notification channels (ntfy, Gotify, Pushover, Telegram, Discord, email, signed webhooks), the messages for status changes and certificate warnings, and a background queue with retries. See [Notifications](../user/notifications.md). |
| `internal/analysis` | Alert analysis: builds the prompt from a monitor, its safe settings (`check.Describe`) and recent checks, and asks Claude through the Messages API with an API key, Claude through Claude Code (which `ClaudeCode` downloads from Anthropic and keeps current when `EYRIE_CLAUDE_CODE` is true), or a model on an OpenAI-compatible server through `/chat/completions`. See [Alert analysis](../user/alert-analysis.md). |
| `internal/backup` | Backup files: the store's snapshot with its secrets opened, gzipped and encrypted with a passphrase (argon2id, AES-256-GCM); reading them back with the secrets sealed with this instance's key; the daily scheduled backups and their folder. A restore runs inside `Engine.Replace`, which stops the checks, saves the heartbeats already made, and loads every monitor again afterwards. |
| `internal/badge` | Draws status badges as SVG, with text widths estimated from Verdana 11 px, so no font is needed. |
| `internal/totp` | Time-based one-time passwords (RFC 6238) for two-factor sign-in. |
| `internal/sso` | OpenID Connect sign-in: discovery, the authorization code flow with PKCE, ID-token checks, group rules. `internal/sso/ssotest` is a fake provider for tests. |
| `internal/api` | HTTP routes: setup, sign-in, sessions and CSRF, accounts, users, single sign-on, API tokens, monitors and push URLs, the event stream, tags, maintenance windows, status pages, incidents and badges, notification and alert analysis settings, backups and restores, metrics, the health check, security headers. See the [HTTP interface](http-interface.md) and [Security](security.md). |
| `internal/ui` | Serves the web app embedded from `internal/ui/dist`. |
| `web/` | The web app: Svelte 5, Vite, TypeScript. Not a Go package. |

## The web app

`web/` is built by Vite into `internal/ui/dist`, which is not committed.
The Go build embeds that folder only with the build tag `ui`
(`internal/ui/embed.go`). Without the tag, `internal/ui/stub.go` serves a
page saying there is no web interface, so `go build`, `go test`,
`go vet` and golangci-lint work without Node.js.

Node.js is never installed on a developer's machine. It runs only:

- in the CI jobs `web` (type check, build, tests) and `web-lock`, and
- in the first stage of the Dockerfile, which builds the app; the second
  stage builds the Go binary with `-tags ui`, and the image that ships has
  only the binary, on `alpine:3`, the newest Alpine release (with every
  published Alpine fix; its C library, musl, is for the Claude Code
  binary that `EYRIE_CLAUDE_CODE=true` downloads), running as root, as WAN
  Drawbridge does, so it can use a mounted Docker socket whatever group
  owns it on the host.

## The monitor engine

`internal/engine`:

- One goroutine owns every monitor's state; the API talks to it through
  commands (add or change, remove, check now, push, set a manual status).
- It wakes when the earliest check is due, at most a minute apart, and
  starts every due check on a worker; at most 64 checks run at once, and a
  check never overlaps itself. Each check gets the monitor's timeout.
- The next check starts one interval (the retry interval while pending)
  after the last one started. After a restart, a monitor's first check comes
  at a fixed offset into its interval, derived from its ID, so they do not
  all start at once; a push monitor gets one interval for its first push.
- Results go through `internal/state`; each one is a heartbeat, queued for
  a writer goroutine that saves them in batches about once a second, sent
  to the event stream, and logged when the status changed. Outcomes worth a
  notification go to `internal/notify`'s queue, as do certificate warnings,
  which the engine decides from each HTTPS result and the warning days. A
  down alert is queued at once; when alert analysis is on, a goroutine then
  asks `internal/analysis` (at most the timeout) and queues the answer as a
  second message.
- Push monitors have a deadline instead of a check; manual monitors only
  change when set.
- Groups (`monitor_groups`) are not checked. The engine reads them at start
  and whenever the API changes a group or where monitors are; after a
  monitor's status change that notifies, it works out which groups are
  down (one of their counted monitors down), and a group with alerts that
  went down or came back up goes to `internal/notify`, to the group's own
  channels. Maintenance starting or ending, and changes to groups, update
  that silently.
- Maintenance: the engine keeps the windows (their schedules compiled by
  `internal/maintenance`) and every monitor's tags, read at start and again
  when the API changes a window, a tag or a monitor's tags. It also wakes
  when a window next starts or ends. A monitor covered by a window that is
  on (directly, by a tag, or through a group it is in) enters maintenance:
  its state is kept aside (and saved, in `monitor_state.before_status`),
  and its checks are recorded as `maintenance` without going through
  `internal/state`, so they change nothing and notify nobody. When no
  window covers it any more, the kept state comes back as a heartbeat
  (`Maintenance ended`) and it is checked at once.
- The current statuses are kept in a copy that the API reads without
  waiting for the engine.

## Startup and shutdown

`eyrie serve`:

1. Reads the configuration; stops on an invalid value.
2. With SQLite, creates `EYRIE_DATA_DIR` if missing.
3. Loads the secret key (`EYRIE_SECRET_KEY`, or `secret.key`, made if
   missing), so it exists from the first start.
4. Opens the database and applies pending migrations.
   A database that a newer version migrated stops it (see
   [Downgrading](../user/upgrading.md#downgrading)).
5. With `EYRIE_LOCAL_LOGIN=force`, logs a warning.
6. Deletes expired sessions and old heartbeats and stats, and again every
   24 hours.
7. With `EYRIE_CLAUDE_CODE=true`, starts getting Claude Code in the
   background; reads the alert analysis settings; starts the notification
   queue and the scheduled backups (which look every minute).
8. Listens on `EYRIE_LISTEN`, starts the monitor engine, which loads every
   monitor and its last state, and logs `eyrie started` with the version,
   address and database.

On SIGINT or SIGTERM it stops accepting connections, waits up to 10 seconds
for requests in progress, stops the engine (which saves the heartbeats still
queued; checks in progress are dropped), closes the database and logs
`eyrie stopped`.
