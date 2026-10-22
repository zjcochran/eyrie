# Installation

## Requirements

- Docker with Compose, on amd64 or arm64.
- To build the image: an internet connection (Go modules, npm packages and
  base images are downloaded during the build).

There is no published image yet: build it from the repository.

```sh
docker build -t eyrie .
```

Pass `--build-arg VERSION=v0.1.0` to set the version Eyrie reports; it is
`dev` otherwise.

## With SQLite (the default)

`compose.yaml` in the repository:

```yaml
services:
  eyrie:
    image: eyrie
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

Open `http://<host>:8080`.

## With PostgreSQL

For large installations. Set `EYRIE_DATABASE_URL` to a `postgres://` or
`postgresql://` URL; Eyrie creates its tables at start. PostgreSQL 17 is the
version tested.

```yaml
services:
  eyrie:
    image: eyrie
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
| `secret.key` | The key that encrypts passwords and tokens Eyrie stores (AES-256-GCM), made at first start. Not used when `EYRIE_SECRET_KEY` is set. |

Back up the whole folder, and with PostgreSQL the database as well. Without
`secret.key` (or the same `EYRIE_SECRET_KEY`), stored secrets cannot be
decrypted and must be entered again.

The container runs as the non-root user 65532, which owns `/data` in the
image, so a new volume belongs to it too. A bind-mounted folder must be
writable by that user: `sudo chown 65532:65532 ./data`.

## Health check

The image's `HEALTHCHECK` runs `/eyrie healthcheck` every 30 seconds (5 s
timeout, 20 s start period, 3 retries). It asks the running server for
`/healthz`, which answers 200 while the database answers.

## Upgrading

Rebuild the image and recreate the container:

```sh
git pull && docker build -t eyrie . && docker compose up -d
```

Database migrations run by themselves at start.

## Configuration

Every environment variable is in the
[configuration reference](../technical/configuration-reference.md).
