# Upgrades and compatibility

**Eyrie is not at 1.0.0 yet.** Until then (0.x), any release may change
or remove something below; its release notes say what. The promises on
this page apply from 1.0.0 on, when Eyrie's versions follow
[semantic versioning](https://semver.org): `MAJOR.MINOR.PATCH`.

| Release | What changes |
| --- | --- |
| Patch (1.4.**2**) | Fixes only |
| Minor (1.**5**.0) | New features. Everything below keeps working. |
| Major (**2**.0.0) | May change or remove something below; its release notes say what and how to move |

Upgrading from a 0.x version works all the same (see
[Upgrading](#upgrading)).

## Upgrading

1. Make a backup: a [backup file](backup-and-restore.md#making-one), or a
   copy of the data folder (and of the PostgreSQL database), so you can go
   back.
2. Pull the new image and recreate the container:

   ```sh
   docker compose pull && docker compose up -d
   ```

3. At start, Eyrie applies the database migrations of every version in
   between, then logs `eyrie started` with the new version.

Versions can be skipped: any earlier version, 0.1.0 included, upgrades
straight to the newest. Across a major version, read its release notes
first.

### Which image tag to use

| Tag | Follows |
| --- | --- |
| `1.4.2` | Nothing: exactly that version |
| `1.4` | Its patch releases |
| `1` | From 1.0.0: every compatible release, the minor and patch releases of 1.x |
| `latest` | Every release, new major versions included |

From 1.0.0, `1` gets new features and fixes without breaking changes.
Pin a version to upgrade only by hand. There is no major tag for 0.x: in
0.x, a minor release can break things, so pin `0.10` to get only its
fixes.

## Downgrading

Going back to an older version is not supported. Once a newer version has
migrated the database, an older one refuses to start:

```text
eyrie: migrate the sqlite database: the database was used by a newer version of
Eyrie (schema 7; this version knows up to 6): run that version, or restore a
backup made before the upgrade
```

To go back, stop Eyrie and put back the data folder or database copied
before the upgrade, then start the older version. Or start the older
version on an empty database and [restore at setup](backup-and-restore.md#restoring-at-setup)
a backup file made before the upgrade: a backup file of a newer database is
refused.

## What stays compatible

From 1.0.0, within a major version, upgrading never needs you to change your setup,
scripts or integrations:

| | Promise |
| --- | --- |
| Your data | The database and data folder of any earlier version are upgraded at start, with every monitor, setting, account, status page and its history kept and meaning the same. |
| Backup files | A [backup file](backup-and-restore.md) restores into the same or any newer version (backup files exist since 0.9.0), on SQLite or PostgreSQL. |
| Configuration | Every [environment variable](../technical/configuration-reference.md) and value keeps working, with the same meaning and default. New ones are optional. |
| Command line | The [commands](../technical/configuration-reference.md#command-line), their arguments and exit codes. |
| HTTP API | Every [route under `/api/v1`](../technical/http-interface.md): its method, path, access, request fields, answer fields, their meaning, and status codes. Each monitor type's settings. |
| Event stream | The events of `/api/v1/events` and their data. |
| Push URLs | `/api/push/<token>` with `status`, `msg` and `ping`, as Uptime Kuma has them. |
| Public pages | Status pages at `/status/<address>`, and badges at `/api/badge/<id>/<kind>` with their parameters. |
| Links | The addresses of monitors' pages (`/monitors/<id>`), which notifications link to. |
| Notifications | The webhook's JSON fields and its `X-Hub-Signature-256` signature, and the event names (`monitor_down`, …). |
| Metrics | The names, types and labels of the [Prometheus metrics](../technical/http-interface.md#metrics). |
| Health check | `/healthz` and `eyrie healthcheck`. |
| Monitor types | No type is removed, and saved monitors keep checking the same way. |

New things can appear in a minor release: routes, optional request fields,
fields in answers and webhooks, events, metrics and labels' values (a new
status, a new monitor type). Programs that read Eyrie should ignore what
they do not know.

## What can change in any release

- **The web app**: its looks, pages and words. Use the API to automate.
- **Text meant for people**: the wording of check messages, notifications,
  error messages (`{"error": "…"}`; the status code stays) and the log.
- **The database's tables**, which are Eyrie's own: read data through the
  API, and copy it with backup files. Something reading the tables directly
  can break at any upgrade.
- **Alert analysis**: the instructions given to the model and its answers;
  the Claude models, Claude Code and other services follow their makers.
- **Bugs and security problems**: their fixes ship in a patch release
  even when something relied on the old behaviour. The release notes say
  so.

## Removing things

What a later major version will remove or change is announced first:
marked deprecated in the docs and the release notes of a minor release,
while it keeps working until that major version.

## Supported versions

Fixes, security fixes included, go into the newest release only. Upgrade
to get them; within a major version, nothing stops you.

## Platforms

| | |
| --- | --- |
| Image | `linux/amd64` and `linux/arm64`, on Docker with Compose |
| PostgreSQL | 17 is the version tested |
| Web app | Current versions of the major browsers |
