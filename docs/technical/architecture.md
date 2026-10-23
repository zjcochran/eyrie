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
| `internal/check` | The monitor types: their settings, defaults and validation, and the checks themselves. Every check reaches the network through an `Env` (dialer, HTTP transport, resolver, pinger) that tests replace. Includes Eyrie's own DNS client, ICMP echo and JSON path. |
| `internal/engine` | Runs the monitors, below. |
| `internal/notify` | Notification channels (ntfy, Gotify, Pushover, Telegram, Discord, email, signed webhooks), the messages for status changes and certificate warnings, and a background queue with retries. See [Notifications](../user/notifications.md). |
| `internal/analysis` | Alert analysis: builds the prompt from a monitor, its safe settings (`check.Describe`) and recent checks, and asks Claude through the Messages API with an API key, Claude through Claude Code (which `ClaudeCode` downloads from Anthropic and keeps current when `EYRIE_CLAUDE_CODE` is true), or a model on an OpenAI-compatible server through `/chat/completions`. See [Alert analysis](../user/alert-analysis.md). |
| `internal/totp` | Time-based one-time passwords (RFC 6238) for two-factor sign-in. |
| `internal/sso` | OpenID Connect sign-in: discovery, the authorization code flow with PKCE, ID-token checks, group rules. `internal/sso/ssotest` is a fake provider for tests. |
| `internal/api` | HTTP routes: setup, sign-in, sessions and CSRF, accounts, users, single sign-on, API tokens, the health check, security headers. See the [HTTP interface](http-interface.md) and [Security](security.md). |
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
  only the binary, on `debian:13-slim` (with every published Debian fix
  and the CA certificates; Debian's C library is for the Claude Code
  binary that `EYRIE_CLAUDE_CODE=true` downloads), running as UID 65532.

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
  down alert, when alert analysis is on, first waits in its own goroutine
  for `internal/analysis` (at most the timeout); the monitor's later
  messages wait behind it.
- Push monitors have a deadline instead of a check; manual monitors only
  change when set; groups are evaluated at their interval and when a
  member's status changes.
- The current statuses are kept in a copy that the API reads without
  waiting for the engine.

## Startup and shutdown

`eyrie serve`:

1. Reads the configuration; stops on an invalid value.
2. With SQLite, creates `EYRIE_DATA_DIR` if missing.
3. Loads the secret key (`EYRIE_SECRET_KEY`, or `secret.key`, made if
   missing), so it exists from the first start.
4. Opens the database and applies pending migrations.
5. With `EYRIE_LOCAL_LOGIN=force`, logs a warning.
6. Deletes expired sessions and old heartbeats and stats, and again every
   24 hours.
7. Starts the monitor engine, which loads every monitor and its last state.
8. Listens on `EYRIE_LISTEN` and logs `eyrie started` with the version,
   address and database.

On SIGINT or SIGTERM it stops accepting connections, waits up to 10 seconds
for requests in progress, stops the engine (which saves the heartbeats still
queued; checks in progress are dropped), closes the database and logs
`eyrie stopped`.
