# Architecture

Eyrie is one Go binary (`cmd/eyrie`). It serves the JSON API and the web
app, and will run the monitor engine. It runs as a single instance.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/eyrie` | Entry point: the `serve`, `healthcheck` and `version` commands, wiring, graceful shutdown. |
| `internal/config` | Environment variables, defaults, validation. See the [configuration reference](configuration-reference.md). |
| `internal/store` | All saved data, in SQLite or PostgreSQL, with migrations for each. See the [data model](data-model.md). |
| `internal/secrets` | AES-256-GCM sealing of secrets the store keeps (sealed values start with `v1:`). |
| `internal/api` | HTTP routes, the health check, security headers. See the [HTTP interface](http-interface.md). |
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
  only the binary, on `gcr.io/distroless/static-debian13:nonroot`.

## Startup and shutdown

`eyrie serve`:

1. Reads the configuration; stops on an invalid value.
2. With SQLite, creates `EYRIE_DATA_DIR` if missing.
3. Loads the secret key (`EYRIE_SECRET_KEY`, or `secret.key`, made if
   missing), so it exists from the first start.
4. Opens the database and applies pending migrations.
5. Listens on `EYRIE_LISTEN` and logs `eyrie started` with the version,
   address and database.

On SIGINT or SIGTERM it stops accepting connections, waits up to 10 seconds
for requests in progress, closes the database and logs `eyrie stopped`.
