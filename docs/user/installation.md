# Installation

## Requirements

- Docker with Compose, on amd64 or arm64.

## The image

Each release is published at `ghcr.io/zjcochran/eyrie`, for amd64 and
arm64, with the tags of its version (`0.10.0`), its minor line (`0.10`,
which follows patch releases) and `latest`; from 1.0.0 on, also its major
line (`1`). Pin a version or a minor line if you do not want upgrades to
arrive on their own; see
[Which image tag to use](upgrading.md#which-image-tag-to-use).

To build it yourself instead (Go modules, npm packages and base images are
downloaded during the build):

```sh
docker build -t eyrie --build-arg VERSION=v0.10.0 --build-arg SOURCE_URL=https://github.com/zjcochran/eyrie .
```

and use `image: eyrie` below. Without `VERSION`, Eyrie reports `dev`.
`SOURCE_URL` is the source code link in the footer, which the AGPL asks
for; point it at your own copy if you change the code.

## With SQLite (the default)

`compose.yaml` in the repository:

```yaml
services:
  eyrie:
    image: ghcr.io/zjcochran/eyrie:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      TZ: Etc/UTC
    volumes:
      - eyrie-data:/data

volumes:
  eyrie-data:
```

```sh
docker compose up -d
```

Open `http://<host>:8080` and create the first account, an admin (see
[Using the web app](web-ui.md#first-start-setup)). Do it right away: until
then anyone who can reach Eyrie can.

## With PostgreSQL

For large installations. Set `EYRIE_DATABASE_URL` to a `postgres://` or
`postgresql://` URL; Eyrie creates its tables at start. PostgreSQL 17 is the
version tested.

```yaml
services:
  eyrie:
    image: ghcr.io/zjcochran/eyrie:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      TZ: Etc/UTC
      EYRIE_DATABASE_URL: postgres://eyrie@db:5432/eyrie
      PGPASSWORD: a-long-password
    volumes:
      - eyrie-data:/data # still holds the secret key
    depends_on:
      - db

  db:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: eyrie
      POSTGRES_PASSWORD: a-long-password
      POSTGRES_DB: eyrie
    volumes:
      - eyrie-db:/var/lib/postgresql/data

volumes:
  eyrie-data:
  eyrie-db:
```

Choose your own password. Keep it out of the URL: Eyrie's PostgreSQL
driver reads it from `PGPASSWORD`, like the other
[libpq environment variables](https://www.postgresql.org/docs/current/libpq-envars.html)
(`PGSSLMODE`, `PGSSLROOTCERT`, …) for anything the URL leaves out. The URL accepts the
[usual PostgreSQL parameters](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING-URIS),
for example `?sslmode=require`.

## The data folder

`/data` (`EYRIE_DATA_DIR`) holds:

| File | What it is |
| --- | --- |
| `eyrie.db`, `eyrie.db-wal`, `eyrie.db-shm` | The SQLite database, in WAL mode. Not used with PostgreSQL. |
| `secret.key` | The key that encrypts the secrets Eyrie stores (AES-256-GCM), such as two-factor secrets, notification tokens and monitors' passwords; see [Stored secrets](../technical/security.md#stored-secrets). Made at first start. Not used when `EYRIE_SECRET_KEY` is set. |
| `backups/` | [Scheduled backup files](backup-and-restore.md#scheduled-backups) (`EYRIE_BACKUP_DIR`) |
| `claude-code/` | Claude Code, only with `EYRIE_CLAUDE_CODE=true`; see [Alert analysis](alert-analysis.md#claude-with-your-subscription-claude-code) |

To back it up, see [Backup and restore](backup-and-restore.md). Without
`secret.key` (or the same `EYRIE_SECRET_KEY`), stored secrets cannot be
decrypted and must be entered again; backup files do not need it.

The container runs as the non-root user 65532, which owns `/data` in the
image, so a new volume belongs to it too. A bind-mounted folder must be
writable by that user: `sudo chown 65532:65532 ./data`.

## Behind a reverse proxy

Serve Eyrie over HTTPS through a reverse proxy whenever people you do not
trust can reach it. Tell Eyrie about the proxy:

```yaml
    environment:
      EYRIE_BASE_URL: https://eyrie.example.com
      EYRIE_TRUSTED_PROXIES: 192.0.2.10
```

`EYRIE_BASE_URL` is the public address: it makes the single sign-on
callback URL and marks the session cookie `Secure`. `EYRIE_TRUSTED_PROXIES`
is the proxy's address as Eyrie sees it (an IP or a CIDR range), so the
client address in the log and the sign-in rate limit is the visitor's, from
`X-Forwarded-For`, and not the proxy's. Headers from any other address are
ignored.

Eyrie must have its own host name: it does not work under a path. For
nginx, Caddy, Traefik, Apache httpd and HAProxy configurations, and how to
find the proxy's address, see [Reverse proxy examples](reverse-proxy.md).

## Health check

The image's `HEALTHCHECK` runs `/eyrie healthcheck` every 30 seconds (5 s
timeout, 20 s start period, 3 retries). It asks the running server for
`/healthz`, which answers 200 while the database answers.

## Upgrading

Make a backup, then pull the new image and recreate the container:

```sh
docker compose pull && docker compose up -d
```

Database migrations run by themselves at start. Versions can be skipped,
and downgrades are not supported; see
[Upgrades and compatibility](upgrading.md).

## Configuration

Every environment variable is in the
[configuration reference](../technical/configuration-reference.md).
