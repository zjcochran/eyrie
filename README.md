# Eyrie

Eyrie is a self-hosted uptime and infrastructure monitor: it checks your
websites, servers, databases and services, tells you when one goes down,
and publishes status pages. It is a single Go binary with its web app built
in, and keeps its data in SQLite or, for large installations, PostgreSQL.

**Status: early development.** This version is the skeleton: the server,
the database, the health check and an empty web app. Monitors, users,
notifications, tags, maintenance windows and status pages follow; see
[the plan](#roadmap).

## Quick start

There is no published image yet. Build one and start it with Docker
Compose:

```sh
docker build -t eyrie .
docker compose up -d
```

Eyrie listens on port 8080. Its data (the SQLite database and the key that
encrypts stored secrets) is in the `eyrie-data` volume, mounted at `/data`.
See [Installation](docs/user/installation.md), including PostgreSQL.

## Documentation

Everything is in [docs/](docs/README.md): installation, configuration, the
HTTP interface, the data model, testing and CI, development.

## Roadmap

1. Scaffold and CI (this version)
2. Users and sign-in: passwords, two-factor sign-in, single sign-on, API tokens
3. The monitor engine, the first monitor types, Prometheus metrics
4. Notifications: ntfy, Gotify, Pushover, Telegram, Discord, email, signed webhooks
5. Tags and maintenance windows
6. Status pages, incidents and status badges
7. and 8. The remaining monitor types (every type Uptime Kuma and Peekaping have)
9. Backup and restore
10. Documentation pass and compatibility promises

Eyrie is written from scratch; it is not a fork of Uptime Kuma, Peekaping
or any other monitor.
