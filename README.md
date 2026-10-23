# Eyrie

Eyrie is a self-hosted uptime and infrastructure monitor: it checks your
websites, servers, databases and services, tells you when one goes down,
and publishes status pages. It is a single Go binary with its web app built
in, and keeps its data in SQLite or, for large installations, PostgreSQL.

**Status: early development.** This version monitors websites (HTTP(S),
keywords, JSON, or in a real browser), TCP ports, ping, DNS, WebSockets,
mail (SMTP), time (NTP), RADIUS, SNMP, SIP, SFTP, gRPC, Docker containers,
game servers, Tailscale nodes, system services, PM2 processes, probes
around the world (Globalping), databases (PostgreSQL, MySQL and MariaDB,
SQL Server, Oracle, MongoDB, Redis) and message brokers (MQTT, RabbitMQ,
Kafka), takes pushes from jobs, and has
manual monitors and groups, a live dashboard, uptime history and Prometheus
metrics, and notifies ntfy, Gotify, Pushover, Telegram, Discord, email and
signed webhooks, with accounts, two-factor sign-in and single sign-on.
Down alerts can carry an analysis of the failure by Claude (an API key or
your Claude subscription) or a model on your own server, such as Ollama
(off by default). Monitors have tags, and maintenance windows (once, weekly,
monthly, every N days or by cron) silence the monitors they cover. Public
status pages, built from tags and monitors, show uptime, incidents with a
timeline and maintenance, and their monitors have status badges.
Backup and restore follow; see [the plan](#roadmap).

## Quick start

Each release is published as a multi-arch image (amd64, arm64) at
`ghcr.io/zjcochran/eyrie`. Start it with the `compose.yaml` in this
repository:

```sh
docker compose up -d
```

Eyrie listens on port 8080; open it and create the first account, an
admin. Its data (the SQLite database and the key that
encrypts stored secrets) is in the `eyrie-data` volume, mounted at `/data`.
See [Installation](docs/user/installation.md), including PostgreSQL.

## Documentation

Everything is in [docs/](docs/README.md): installation, monitors, maintenance windows, status pages, notifications, alert analysis, using the web app,
single sign-on, configuration, the HTTP interface, security, the data
model, testing and CI, development.

## Roadmap

1. Scaffold and CI
2. Users and sign-in: passwords, two-factor sign-in, single sign-on, API tokens
3. The monitor engine, the first monitor types, Prometheus metrics
4. Notifications: ntfy, Gotify, Pushover, Telegram, Discord, email, signed webhooks
5. Alert analysis: down alerts carry an analysis by Claude (an Anthropic API key, or a Claude subscription through Claude Code) or a model on your own server (Ollama, LM Studio, llama.cpp, vLLM)
6. Tags and maintenance windows
7. Status pages, incidents and status badges
8. Network and system monitor types: WebSocket, SMTP, NTP, RADIUS, SNMP, Docker, SIP, Tailscale, system services, PM2, Globalping, SFTP, game servers, gRPC, a real browser
9. Database and message broker monitor types: PostgreSQL, MySQL, SQL Server, Oracle, MongoDB, Redis, MQTT, RabbitMQ, Kafka (this version, with 8)
10. Backup and restore
11. Documentation pass and compatibility promises

Eyrie is written from scratch; it is not a fork of Uptime Kuma, Peekaping
or any other monitor.

## License

Copyright the Eyrie contributors. Licensed under the
[GNU Affero General Public License v3.0 or later](LICENSE)
(`AGPL-3.0-or-later`). Every page links to the source of the running
version, as the AGPL requires; set `EYRIE_SOURCE_URL` if you run a modified
version.
