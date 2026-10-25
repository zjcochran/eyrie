# Data model

## Two databases

Eyrie runs on SQLite (the default) or PostgreSQL (`internal/store`):

| | SQLite | PostgreSQL |
| --- | --- | --- |
| Driver | `modernc.org/sqlite`, pure Go | `github.com/jackc/pgx/v5` (`stdlib`) |
| Chosen when | `EYRIE_DATABASE_URL` is empty | `EYRIE_DATABASE_URL` starts with `postgres://` or `postgresql://` |
| Connection | `EYRIE_DATA_DIR/eyrie.db`, WAL journal, `busy_timeout` 5000 ms, foreign keys on, transactions begin immediately (they take the write lock at `BEGIN`) | The URL as given |

Queries are written once with `?` placeholders, in SQL both databases
accept (`ON CONFLICT … DO UPDATE`, `RETURNING`); for PostgreSQL the store
rewrites the placeholders to `$1`, `$2`, …. Queries never contain a literal
`?` in a string.

Column conventions, the same in both:

| Kind | Type | Notes |
| --- | --- | --- |
| Times | `BIGINT` | Unix milliseconds, UTC |
| Text, JSON | `TEXT` | JSON documents are stored as text |
| Flags | `BOOLEAN` | SQLite stores 0 and 1 |
| Binary | `BLOB` / `BYTEA` | Spelled differently |
| Generated IDs | `INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT` / `BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY` | Spelled differently; IDs are never reused. New rows get theirs from `RETURNING id`. |
| Unique columns | `CREATE UNIQUE INDEX` | Never a `UNIQUE` constraint, so both databases list the same indexes |

## Backup files

A [backup file](../user/backup-and-restore.md) carries tables row by row,
with values both databases read (numbers, booleans, text, null, bytes in
base64), so it restores into either. Every table but `sessions` is
copied; `monitor_state`, `heartbeats`, `stats_hourly` and `stats_daily` only
with history. A restore deletes the rows of every one of these tables and
`sessions`, children first, and inserts the backup's with their ids, in one
transaction; `monitors.parent_id` is set once every monitor is in, and
PostgreSQL's identity sequences are moved past the restored ids. Only
known tables and columns are accepted, and the schema version of a backup
must not be newer than the database's. A backup of an older schema
leaves out the columns added since, which take their defaults.

## Migrations

Each database has its own [goose](https://github.com/pressly/goose)
migrations, embedded in the binary: `internal/store/migrations/sqlite` and
`internal/store/migrations/postgres`. Pending migrations run at every start,
before the server listens; goose records them in `goose_db_version`.

Rules, enforced by tests in `internal/store`:

- Both folders hold the same file names (`TestMigrationsInStep`).
- Both produce the same tables, columns (order, type as in the table above,
  `NOT NULL`, primary key) and indexes (`TestSchemasMatch`, against a real
  PostgreSQL server in CI).
- Migrations are only added, never edited.
- Within a major version they only add tables, indexes, and columns that
  are nullable or have a default, so older backup files restore
  (`TestBackupOlderSchema`).
- Eyrie refuses to start on a database migrated past the newest migration
  it has, that is by a newer version (`ErrNewerDatabase`,
  `TestNewerDatabaseRefused`).

## Tables

### settings

Settings saved from the web UI, one JSON document per key.

| Column | Type | Notes |
| --- | --- | --- |
| `key` | `TEXT` | Primary key |
| `value` | `TEXT` | JSON |
| `updated_at` | `BIGINT` | Unix milliseconds of the last save |

| Key | Value |
| --- | --- |
| `backup_schedule` | Scheduled backups: `enabled`, `time` (`HH:MM`), `keep`, `history`, `passphrase` (sealed with the instance key). Missing: off, 03:00, 14, `none`. |
| `backup_schedule_last` | The last scheduled backup: `at`, `file`, `error`, `success` (times in RFC 3339). |
| `analysis` | Alert analysis settings: `enabled`, `provider` (`api`, `claude-code` or `local`), `model` (`""` for the default), `api_key` (the Anthropic API key, sealed with the instance key; `""` for none), `base_url` (the local server's API), `local_api_key` (sealed; `""` for none), `timeout_s`. Missing: off, `api`, 60 seconds. |
| `notifications` | `{"cert_expiry_days":[21,14,7]}`: the days before a certificate expires when monitors that ask for it warn. Missing: 21, 14, 7. |
| `retention` | `{"heartbeat_days":90}`: how many days heartbeats are kept, 1 to 3650. Missing: 90. |
| `sso` | Single sign-on settings: `enabled`, `issuer`, `client_id`, `client_secret` (sealed with the instance key; `""` for none), `display_name`, `auto_provision`, `allowed_group`, `admin_group`, `sso_only`. Missing: off, with `auto_provision` on. |

### users

Accounts (migration `00002_users`).

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key, assigned by the database (`AUTOINCREMENT` / identity) |
| `username` | `TEXT` | As typed, trimmed; 1 to 64 characters, no control characters |
| `username_key` | `TEXT` | The username in lower case; unique index `users_username_key`, so `Alice` and `alice` are one account |
| `password_hash` | `TEXT` | argon2id, PHC format; `NULL` for an account that signs in only with single sign-on |
| `role` | `TEXT` | `admin` or `viewer` |
| `disabled` | `BOOLEAN` | A disabled account cannot sign in and has no sessions |
| `totp_secret` | `TEXT` | Two-factor secret, sealed with the instance key; `''` when off |
| `totp_step` | `BIGINT` | The last 30-second step a code was accepted for; a code for it or an earlier step is refused |
| `sso_picture` | `BOOLEAN` | Single sign-on may set the picture; off once the user removes one it set |
| `created_at` | `BIGINT` | |
| `last_login` | `BIGINT` | `0`: never |

### sessions

Signed-in browsers. Index `sessions_user` on `user_id`.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `TEXT` | Primary key: SHA-256 (hex) of the cookie token, never the token |
| `user_id` | `BIGINT` | `users.id`, `ON DELETE CASCADE` |
| `csrf` | `TEXT` | The session's CSRF token |
| `created_at`, `expires_at` | `BIGINT` | Expiry slides to 7 days after use, at most once an hour |
| `ip` | `TEXT` | Client address at sign-in |
| `user_agent` | `TEXT` | At most 256 bytes |

Expired sessions are deleted at start and then once a day.

### oidc_identities

Single sign-on accounts linked to users. Primary key `(issuer, subject)`,
so an identity belongs to one user at most; index `oidc_identities_user`.

| Column | Type | Notes |
| --- | --- | --- |
| `issuer`, `subject` | `TEXT` | The ID token's `iss` and `sub` |
| `user_id` | `BIGINT` | `users.id`, `ON DELETE CASCADE` |
| `email` | `TEXT` | The `email` claim when linked or last updated, for display |
| `linked_at` | `BIGINT` | |

### recovery_codes

Unused two-factor recovery codes. Primary key `(user_id, hash)`.

| Column | Type | Notes |
| --- | --- | --- |
| `user_id` | `BIGINT` | `users.id`, `ON DELETE CASCADE` |
| `hash` | `TEXT` | SHA-256 (hex) of the code as `xxxx-xxxx-xxxx`; deleted when used |

### user_pictures

| Column | Type | Notes |
| --- | --- | --- |
| `user_id` | `BIGINT` | Primary key, `users.id`, `ON DELETE CASCADE` |
| `content_type` | `TEXT` | `image/png`, `image/jpeg`, `image/gif` or `image/webp` |
| `data` | `BLOB` / `BYTEA` | At most 1 MiB |
| `source` | `TEXT` | `upload` or `sso`. A picture from single sign-on never replaces an upload. |
| `updated_at` | `BIGINT` | |

### api_tokens

Unique index `api_tokens_hash` on `hash`.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `name` | `TEXT` | 1 to 64 characters |
| `hash` | `TEXT` | SHA-256 (hex) of the token; the token itself is shown once and never stored |
| `created_at` | `BIGINT` | |
| `created_by` | `TEXT` | Username of the admin who made it |
| `last_used` | `BIGINT` | `0`: never |

### agents

Eyrie's [agents](../user/agents.md). Migration `00009_agents`. `name` is
unique. Times are milliseconds since the epoch, `0` for none.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key; the agent's ID in its signatures |
| `name` | `TEXT` | 1 to 64 characters, unique |
| `platform`, `hostname`, `version` | `TEXT` | From the agent's enrollment and reports |
| `token_hash` | `TEXT` | SHA-256 (hex) of the one-time enrollment token; `''` when none waits |
| `token_expires` | `BIGINT` | When that token expires |
| `public_key` | `TEXT` | `ed25519:…`; `''` until the agent enrolls, and after a new token |
| `previous_key` | `TEXT` | The key before a rotation, accepted until the new one is used |
| `key_created` | `BIGINT` | When `public_key` was made; refused 90 days later |
| `last_seen`, `last_address` | `BIGINT`, `TEXT` | The last report's time and client address |
| `error` | `TEXT` | Why the last report had no workloads; `''` when it had |
| `workloads` | `TEXT` | The last report's workloads, as JSON (`[{"key","name","kind","group","up","status"}]`) |
| `created_at` | `BIGINT` | |

### monitors

Migration `00003_monitors`. Index `monitors_parent` on `parent_id`; unique
index `monitors_push_token` on `push_token` (`NULL` for other types, so it
does not collide).

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `name` | `TEXT` | 1 to 100 characters |
| `type` | `TEXT` | One of the [monitor types](../user/monitors.md#types), such as `http`, `docker`, `agent`, `push`, `manual` or `group`; never changes |
| `parent_id` | `BIGINT` | The group it is in, `NULL` for none; `ON DELETE SET NULL`. Must be a group, and not the monitor itself or one of its members. |
| `active` | `BOOLEAN` | False: paused |
| `interval_s`, `retry_interval_s` | `INTEGER` | Seconds |
| `retries`, `resend_every` | `INTEGER` | |
| `timeout_ms` | `INTEGER` | Milliseconds |
| `upside_down` | `BOOLEAN` | |
| `description` | `TEXT` | |
| `config` | `TEXT` | The type's settings as JSON, with defaults filled in and secret settings sealed (package `check`) |
| `push_token` | `TEXT` | Push monitors: 18 random bytes, base64url |
| `created_at`, `updated_at` | `BIGINT` | |

### monitor_state

Each monitor's current status, so a restart carries on where it stopped.
Written with every heartbeat.

| Column | Type | Notes |
| --- | --- | --- |
| `monitor_id` | `BIGINT` | Primary key, `monitors.id`, `ON DELETE CASCADE` |
| `status` | `TEXT` | `up`, `pending`, `down` or `maintenance` |
| `since` | `BIGINT` | When the status began |
| `message` | `TEXT` | Of the last check |
| `last_check` | `BIGINT` | |
| `latency_ms` | `INTEGER` | Of the last check, in whole milliseconds; `-1` for none. Still written, but read only to fill `latency_us` |
| `latency_us` | `BIGINT` | Of the last check, in microseconds; `-1` for none. Migration `00007_latency_micros`. |
| `cert_expires` | `BIGINT` | The last certificate an HTTPS check saw; `0` for none |
| `failures` | `INTEGER` | Failed checks in a row while pending |
| `down_checks` | `INTEGER` | Checks since the last down notification, for "notify again every" |
| `cert_notified` | `INTEGER` | The certificate warning last sent for `cert_expires`, as its threshold in days; `0` for none. Migration `00004_notifications`. |
| `before_status`, `before_since` | `TEXT`, `BIGINT` | In maintenance, the status the monitor had before it began and since when, which come back when it ends; `''` and `0` otherwise. Migration `00005_tags_maintenance`. |

### heartbeats

One row per check. Indexes `heartbeats_monitor_at` on `(monitor_id, at)`
and `heartbeats_at` on `at` (for pruning). Rows older than the `retention`
setting are deleted daily.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `monitor_id` | `BIGINT` | `monitors.id`, `ON DELETE CASCADE` |
| `at` | `BIGINT` | When the check ended |
| `status` | `TEXT` | `up`, `pending`, `down` or `maintenance` |
| `latency_ms` | `INTEGER` | In whole milliseconds; `-1` for none. Still written, but read only to fill `latency_us` |
| `latency_us` | `BIGINT` | In microseconds; `-1` for none. Migration `00007_latency_micros`. |
| `message` | `TEXT` | |
| `important` | `BOOLEAN` | The status changed |

Heartbeats are written in batches about once a second (at most 500 per
transaction), with the state and the stats below in the same transaction.

### stats_hourly, stats_daily

Check counts per monitor and hour (`bucket`: the hour's start) or day
(`bucket`: midnight UTC). Primary key `(monitor_id, bucket)`; `monitor_id`
`ON DELETE CASCADE`. Hourly rows are kept 35 days, daily rows 400 days.

| Column | Type | Notes |
| --- | --- | --- |
| `monitor_id` | `BIGINT` | |
| `bucket` | `BIGINT` | |
| `up` | `INTEGER` | Up and pending checks (checks in maintenance are not counted at all) |
| `down` | `INTEGER` | Down checks |
| `latency_sum` | `BIGINT` | Whole milliseconds, over checks with a response time. Still written, but read only to fill `latency_us_sum` |
| `latency_us_sum` | `BIGINT` | Microseconds, over checks with a response time; `-1` until filled from `latency_sum`. Migration `00007_latency_micros`. |
| `latency_count` | `INTEGER` | Checks with a response time |

Response times were kept in whole milliseconds until migration
`00007_latency_micros`, which made a 0.5 ms ping 0 ms. The migration fills
the microsecond columns from the millisecond ones, and a restore does the
same for a backup made before it.

Uptime is `up / (up + down)`. The 24-hour and 30-day figures add up the
hourly rows from the hour that contains "24 hours (30 days) ago", the 1-year
figure the daily rows from the day 365 days ago.

### notification_channels

Migration `00004_notifications`.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `name` | `TEXT` | 1 to 100 characters |
| `kind` | `TEXT` | `ntfy`, `gotify`, `pushover`, `telegram`, `discord`, `email` or `webhook` |
| `url`, `token`, `username` | `TEXT` | Sealed with the instance key; `''` for none |
| `recipients` | `TEXT` | Telegram chat ID, or email recipients separated by commas |
| `sender` | `TEXT` | Email sender |
| `enabled` | `BOOLEAN` | False: nothing is sent to it |
| `is_default` | `BOOLEAN` | Given to new monitors |
| `created_at`, `updated_at` | `BIGINT` | |

### monitor_channels

Which channels each monitor notifies. Primary key
`(monitor_id, channel_id)`, both `ON DELETE CASCADE`; index
`monitor_channels_channel` on `channel_id`.

### tags

Migration `00005_tags_maintenance`. Unique index `tags_name` on
`LOWER(name)`: names are unique whatever the case.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `name` | `TEXT` | 1 to 50 characters |
| `color` | `TEXT` | `#rrggbb`, lower case |
| `created_at`, `updated_at` | `BIGINT` | |

### monitor_tags

The tags of each monitor. Primary key `(monitor_id, tag_id)`, both
`ON DELETE CASCADE`; index `monitor_tags_tag` on `tag_id`.

| Column | Type | Notes |
| --- | --- | --- |
| `monitor_id`, `tag_id` | `BIGINT` | |
| `value` | `TEXT` | The monitor's value for the tag, up to 100 characters; `''` for none |

### maintenance_windows

Migration `00005_tags_maintenance`.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `title` | `TEXT` | 1 to 100 characters |
| `description` | `TEXT` | Up to 2000 characters |
| `enabled` | `BOOLEAN` | False: turned off, never on |
| `schedule` | `TEXT` | JSON, read by package `maintenance`: `strategy` (`manual`, `once`, `interval`, `weekdays`, `monthdays`, `cron`), `time_zone`, and the strategy's fields (`start`, `end`, `from`, `to`, `every_days`, `weekdays`, `month_days`, `cron`, `duration_minutes`, `first_day`, `last_day`); see [HTTP interface](http-interface.md#maintenance-windows) |
| `all_monitors` | `BOOLEAN` | It covers every monitor; the two tables below are then empty |
| `created_at`, `updated_at` | `BIGINT` | |

### maintenance_monitors, maintenance_tags

The monitors and the tags a window covers (with the members of the groups
among the monitors, and the monitors with the tags). Primary keys
`(window_id, monitor_id)` and `(window_id, tag_id)`, all `ON DELETE
CASCADE`, so deleting a monitor or a tag takes it out of windows; indexes
`maintenance_monitors_monitor` on `monitor_id` and `maintenance_tags_tag`
on `tag_id`.

### status_pages

Migration `00006_status_pages`. Unique index `status_pages_slug` on `slug`.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `slug` | `TEXT` | The address, `/status/<slug>`: 1 to 64 lower-case letters, digits and single dashes |
| `title` | `TEXT` | 1 to 100 characters |
| `description`, `footer` | `TEXT` | Up to 2000 characters each |
| `published` | `BOOLEAN` | False: only signed-in users see it, and its monitors have no badges |
| `theme` | `TEXT` | `auto`, `light` or `dark` |
| `accent` | `TEXT` | `#rrggbb`; `''` for Eyrie's |
| `show_tags`, `show_cert_expiry` | `BOOLEAN` | |
| `created_at`, `updated_at` | `BIGINT` | |

### status_page_logos

A page's logo, at most one: `page_id` (primary key, `ON DELETE CASCADE`),
`content_type`, `data` (`BLOB` / `BYTEA`, at most 1 MB), `updated_at`.

### status_page_sections, status_page_monitors

A page's sections: `id`, `page_id` (`ON DELETE CASCADE`), `position` (from
0, in order), `title` (up to 100 characters), `tag_id` (`NULL` for none;
`ON DELETE SET NULL`). Indexes `status_page_sections_page` and
`status_page_sections_tag`. `status_page_monitors` lists the monitors a
section names: primary key `(section_id, monitor_id)`, both `ON DELETE
CASCADE`, index `status_page_monitors_monitor`. A section shows those and
the monitors with its tag.

### audit_log

Who did what. Migration `00008_audit_log`. Index `audit_log_at` on `at`.
Rows older than 400 days are deleted daily, with the daily stats.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `INTEGER` / `BIGINT` | Primary key |
| `at` | `BIGINT` | |
| `actor` | `TEXT` | The username who did it; for a failed sign-in, the one tried |
| `ip` | `TEXT` | The client address |
| `action` | `TEXT` | The log message, such as `monitor created` |
| `details` | `TEXT` | The log line's other fields, `key=value`, quoted when they have spaces |

### incidents, incident_updates

`incidents`: `id`, `page_id` (`ON DELETE CASCADE`), `title` (1 to 200
characters), `status` (that of the latest update), `created_at`,
`updated_at`, `resolved_at` (`0` while open). Index `incidents_page` on
`(page_id, created_at)`.

`incident_updates`, the timeline: `id`, `incident_id` (`ON DELETE
CASCADE`), `status` (`investigating`, `identified`, `monitoring` or
`resolved`), `message` (1 to 5000 characters), `author` (the username of
who posted it), `created_at`. Index `incident_updates_incident` on
`(incident_id, created_at)`.
