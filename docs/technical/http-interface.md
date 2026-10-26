# HTTP interface

One server (`internal/api`) answers everything on `EYRIE_LISTEN`.

## Conventions

- Request and response bodies are JSON (`application/json`), except the
  picture upload and download. Every JSON answer has
  `Cache-Control: no-store`.
- Errors answer `{"error":"<message>"}`, a sentence meant for people:
  `400` for a body that is not JSON (at most 64 KiB), `401` when not signed
  in, `403` when not allowed, `404` for something that does not exist,
  `422` for a request that was understood but refused (a short password,
  the last admin), `429` for too many failed sign-ins, `500` for a failure
  of Eyrie itself (logged as `request failed`).
- Times are RFC 3339 in UTC; `null` means never.
- **Session** routes need the session cookie (`eyrie_session`). Requests
  other than `GET` and `HEAD` on them must also send the session's CSRF
  token in the `X-CSRF-Token` header, or get `403`. **Admin** routes are
  session routes for the admin role only (`403` for viewers).
- **Read** routes take a session or an API token
  (`Authorization: Bearer eyt_…`); they are all `GET`.
- **Public** routes need nothing.

See [Security](security.md) for sessions, CSRF, rate limits and tokens.

What of this stays the same between versions is in
[Upgrades and compatibility](../user/upgrading.md#what-stays-compatible).

## Routes

### Health and version

| Method and path | Access | Answer |
| --- | --- | --- |
| `GET /healthz` | public | `200` `ok` while the database answers a ping within 2 seconds; otherwise `503` `database unavailable` and a warning in the log. `text/plain`, `Cache-Control: no-store`. |
| `GET /api/v1/info` | public | `200` `{"name":"Eyrie","version":"<version>","license":"AGPL-3.0-or-later","source":"<URL of the source code, or empty>"}` |

### Setup and sign-in

| Method and path | Access | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/session` | public | | `200` [session](#session) |
| `POST /api/v1/setup` | public, only while no account exists | `{"username","password"}` | `201` session, signed in as the new admin. `403` once an account exists; `422` for a bad username or password. |
| `POST /api/v1/setup/restore` | public, only while no account exists | a backup file, as for [`POST /api/v1/restore`](#backup-and-restore) | `200` as for that route; no one is signed in. `403` once an account exists. |
| `POST /api/v1/login` | public | `{"username","password"}` | `200` session; or `200` `{"two_factor":true}` and the `eyrie_2fa` cookie when a code must follow. `401` wrong username or password (the same for a disabled account), `403` when password sign-in is off, `429` rate limited. |
| `POST /api/v1/login/code` | the `eyrie_2fa` cookie | `{"code"}`: from the app, or a recovery code | `200` session. `401` wrong code, or the step expired or used its 5 tries; `429` rate limited. |
| `POST /api/v1/logout` | public | | `204`; ends the session if there is one and clears the cookie. |
| `GET /auth/oidc/login?next=<path>` | public, single sign-on on | | `302` to the provider. With `link=1` and a session, links the signed-in account instead (without a session: `303` to `/login`). `404` while single sign-on is off. |
| `GET /auth/oidc/callback` | the provider sends the browser here | | `303` to `next` (a local path, else `/`) signed in; to `/login?error=<message>` when refused; after linking, to `/settings/account?message=<message>` or `?error=<message>`. `404` while single sign-on is off. |

#### Session

```json
{
  "setup": false,
  "user": { "id": 1, "username": "alice", "role": "admin", "...": "see user" },
  "csrf": "<token>",
  "sso": { "enabled": true, "name": "Example ID" },
  "password_sign_in": true,
  "version": "v0.10.0",
  "source": "https://github.com/zjcochran/eyrie"
}
```

`setup` is `true` only while no account exists. `user` is `null` and
`csrf` is left out when not signed in. `password_sign_in` is `false` when
single sign-on is the only way in (and `EYRIE_LOCAL_LOGIN` is not `force`).
`source` is where the running version's source code is (`EYRIE_SOURCE_URL`,
or the URL built in), `""` when neither is set; the app's footer links to
it and to the licence on every page, signed in or not.

#### User

```json
{
  "id": 2,
  "username": "bob",
  "role": "viewer",
  "disabled": false,
  "password": true,
  "sso": false,
  "two_factor": false,
  "picture": "/api/v1/users/2/picture?v=1800000000000",
  "sso_picture": true,
  "created_at": "2027-01-15T08:00:00Z",
  "last_login": null,
  "role_from_group": false
}
```

`password`: the account has a password. `sso`: a single sign-on account is
linked. `picture` is `""` without a picture; its `v` changes with the
picture. `sso_picture`: single sign-on may set the picture.
`role_from_group`: the single sign-on admin group decides the role (an
account without a password while an admin group is set).

### Your account

All session routes.

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/account` | | `200` `{"user", "identities":[{"issuer","email","linked_at"}], "two_factor":{"available","on","codes_left"}, "sso":{"enabled","name"}}` |
| `PUT /api/v1/account/password` | `{"current","new"}` | `200` session with a new CSRF token: every session of the account ends and this browser gets a new one. `422` wrong current password, a new one too short, or an account without a password. |
| `POST /api/v1/account/two-factor/setup` | | `200` `{"secret","uri","qr","sealed"}`: the key, its `otpauth://` link, the QR code as a `data:image/png` URL, and the key sealed with the instance key. Nothing is stored. `422` when two-factor sign-in is on or not available. |
| `POST /api/v1/account/two-factor/enable` | `{"sealed","code","password"}` | `200` `{"codes":[…]}`: 10 recovery codes, shown once. `422` wrong password, wrong code, or a `sealed` value that does not open. |
| `POST /api/v1/account/two-factor/recovery-codes` | `{"password"}` | `200` `{"codes":[…]}`, replacing all earlier codes. `422` when off or wrong password. |
| `POST /api/v1/account/two-factor/disable` | `{"password"}` | `204`. `422` wrong password. |
| `PUT /api/v1/account/picture` | the image itself | `200` user. PNG, JPEG, GIF or WebP by content, at most 1 MiB; otherwise `422`. |
| `DELETE /api/v1/account/picture` | | `200` user. Removing a picture from single sign-on also stops single sign-on from setting one. |
| `POST /api/v1/account/picture/sso` | | `200` user: single sign-on may set the picture again, from the next sign-in. |
| `DELETE /api/v1/account/sso` | | `204`, unlinks single sign-on. `422` for an account without a password. |
| `GET /api/v1/users/{id}/picture` | | The image, `Cache-Control: private, max-age=86400`. Only your own, or anyone's for admins; `404` otherwise and when there is none. |

### Users

All admin routes.

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/users` | | `200` list of users, by username |
| `POST /api/v1/users` | `{"username","password","role"}` | `201` user. `422` taken name (ignoring case), short password, role not `admin` or `viewer`. |
| `PATCH /api/v1/users/{id}` | `{"role"?, "disabled"?}` | `200` user. Disabling ends the user's sessions. `422` for yourself, for the last enabled admin, and for the role of an account whose role comes from the single sign-on admin group. |
| `PUT /api/v1/users/{id}/password` | `{"password"}` | `204`; the user's sessions end. `422` for an account without a password. |
| `DELETE /api/v1/users/{id}/two-factor` | | `204`, turns off the user's two-factor sign-in and deletes their recovery codes. |
| `DELETE /api/v1/users/{id}` | | `204`, with the user's sessions, single sign-on links, recovery codes and picture. `422` for yourself and the last enabled admin. |

### Single sign-on settings

All admin routes.

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/settings/sso` | | `200` `{"enabled","issuer","client_id","has_secret","display_name","auto_provision","allowed_group","admin_group","sso_only","callback_url"}`. The client secret is never sent. |
| `PUT /api/v1/settings/sso` | the same fields, plus `client_secret` (`""` keeps the saved one) and `clear_secret` | `200` the saved settings. `422` when on without an `http(s)` issuer or a client ID, and for `sso_only` while your own account is not linked. |
| `POST /api/v1/settings/sso/test` | `{"issuer"}` | `200` `{"ok":true,"message":"Found <issuer> (sign-in at <url>)."}` or `{"ok":false,"message":"<why>"}`. Saves nothing. |

### Audit log

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/audit?action=<a>&user=<u>&before=<id>&limit=<n>` | | Admin. `200` `{"entries":[{"id","at","user","ip","action","details"}],"actions":[…],"users":[…]}`: entries newest first, `limit` of them (default 100, at most 1000), only those with that action and user when set and with an id below `before`; `actions` and `users` are every action and user in the log, sorted, for filters. `400` when `before` or `limit` is not a positive number |

### API tokens

All admin routes.

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/tokens` | | `200` `[{"id","name","created_at","created_by","last_used"}]`, oldest first |
| `POST /api/v1/tokens` | `{"name"}`, 1 to 64 characters | `201` the token's fields plus `"token":"eyt_…"`, the only time it is shown |
| `DELETE /api/v1/tokens/{id}` | | `204`; the token stops working at once |

A token is sent as `Authorization: Bearer eyt_…`. Tokens can only read
(`GET` and `HEAD`): they are accepted on the read routes below; every other
route answers a request that has only a token with `401`.

### Agents

The admin routes for [agents](../user/agents.md). The agents' own routes,
under `/api/agent/v1/`, are in [Agent protocol](agent-protocol.md#routes).

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/agents` | | `200` `[{"id","name","status","platform","hostname","version","last_seen","last_address","error","workloads","key","key_created","token_expires","created_at"}]` by name. `status` is `pending` (not enrolled), `online` (reported within 90 seconds) or `offline`; `workloads` how many the last report had; `key` the key's fingerprint (`""` when not enrolled); `token_expires` `null` when no enrollment token waits |
| `POST /api/v1/agents` | `{"name"}`, 1 to 64 characters | `201` `{"agent":{…},"token":"eya_…"}`, the one-time enrollment token, valid for an hour and only shown here; `422` for a name in use |
| `POST /api/v1/agents/{id}/token` | | `200` the same, with a new token; the agent's key is forgotten, so it is refused until it enrolls again |
| `DELETE /api/v1/agents/{id}` | | `204`; its monitors go down |
| `GET /api/v1/agents/{id}/workloads` | | `200` `[{"key","name","kind","group","up","status"}]`, the last report's |

### Notification channels

All admin routes. See [Notifications](../user/notifications.md).

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/channel-kinds` | | `200` `[{"id":"ntfy","name":"ntfy"},…]`, in menu order |
| `GET /api/v1/channels` | | `200` [channels](#channel), by name |
| `POST /api/v1/channels` | [channel settings](#channel-settings) | `201` channel. `422` for a field the kind needs. |
| `PUT /api/v1/channels/{id}` | channel settings | `200` channel |
| `DELETE /api/v1/channels/{id}` | | `204`; its monitors stop using it |
| `POST /api/v1/channels/test` | channel settings, with `id` to fill in a saved channel's secrets | `200` `{"ok":true,"message":"Sent."}` or `{"ok":false,"message":"<why>"}`; no retries. For a saved channel the result becomes its last delivery. |
| `GET /api/v1/settings/notifications` | | `200` `{"cert_expiry_days":[21,14,7]}` |
| `PUT /api/v1/settings/notifications` | `{"cert_expiry_days"}`: up to 10 days from 1 to 365 | `200` the saved setting, sorted largest first without repeats; applies at once |

#### Channel

```json
{
  "id": 2, "name": "Phone", "kind": "ntfy", "url_host": "https://ntfy.example.com",
  "has_url": true, "has_token": false, "has_user": false, "to": "", "from": "",
  "enabled": true, "default": true, "monitors": 4,
  "last": { "at": "2027-01-15T08:30:01Z", "error": "" }
}
```

Secrets never leave Eyrie: of the URL only its scheme and host are shown,
of the token and user only whether they are set. `last` is the last
delivery since Eyrie started (`error` empty when it worked), `null` before
the first.

#### Channel settings

`{"name","kind","url","token","user","to","from","enabled","default","apply_to_all","clear_url","clear_token","clear_user"}`.
On a saved channel, an empty `url`, `token` or `user` keeps the saved one
unless its `clear_` flag is set. `apply_to_all` adds the channel to every
monitor.

### Alert analysis

All admin routes. See [Alert analysis](../user/alert-analysis.md).

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/settings/analysis` | | `200` the settings, below |
| `PUT /api/v1/settings/analysis` | `{"enabled","provider","model","base_url","timeout_s","api_key","clear_api_key"}` | `200` the settings; applies at once. `422` for an unknown `provider` (`api`, `claude-code` or `local`), `claude-code` without `EYRIE_CLAUDE_CODE=true`, a timeout outside 5 to 300 seconds, a model name with spaces or starting with `-`, a `base_url` that is not an `http` or `https` URL with a host (or has a user, query or fragment), a subscription token (`sk-ant-oat…`) as `api_key`, turning on `api` without a key, or turning on `local` without `base_url` and `model`. `api_key` and `clear_api_key` are the chosen provider's key: an empty one keeps the saved key unless `clear_api_key` is set; the other provider's key is kept. |
| `POST /api/v1/settings/analysis/test` | the same, not saved, and optionally `channel_id`: a saved channel | `200` `{"ok":true,"message":"<the analysis of a made-up alert>"}` or `{"ok":false,"message":"<why>"}`. With `channel_id`, the made-up alert (event `test`, with `analysis`) is then sent to that channel at once, without retries, as a channel test is; the message ends with `Sent to <channel>.`, or `ok` is `false` and it says why sending failed. `404` without that channel; `422` as for `PUT`. |
| `POST /api/v1/settings/analysis/models` | the same, not saved | `200` `{"models":["<id>",…]}`: the provider's models (the Claude API's newest first, Claude Code's aliases `opus`, `sonnet`, `haiku`, a server's by name), or `{"models":[],"error":"<why>"}`. Waits at most 10 seconds. `422` as for `PUT`. |

```json
{
  "enabled": true, "provider": "local", "model": "llama3.2", "has_api_key": false,
  "base_url": "http://192.0.2.20:11434/v1", "has_local_api_key": false, "timeout_s": 60,
  "default_model": "claude-opus-5-5",
  "claude_code": {"state": "ready", "version": "2.1.308"}
}
```

The keys themselves are never returned. `claude_code` is `null` unless
`EYRIE_CLAUDE_CODE=true`; otherwise its `state` is `installing`, `ready`
(with the `version`) or `failed` (with the `error`).

### Monitors

| Method and path | Access | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/monitor-types` | read | | `200` `[{"name":"dns","label":"DNS"},…]`, by name |
| `GET /api/v1/monitors?beats=<n>` | read | | `200` list of [monitors](#monitor), by name. With `beats` (1 to 100), each has its last `n` heartbeats, oldest first. |
| `GET /api/v1/monitors/{id}` | read | | `200` monitor |
| `GET /api/v1/monitors/{id}/heartbeats?since=<time>&limit=<n>` | read | | `200` [heartbeats](#heartbeat), newest first, from `since` (RFC 3339; default 24 hours ago), at most `limit` (default and most 10000). `400` for a bad `since`. |
| `GET /api/v1/monitors/{id}/hours?days=<n>` | read | | `200` `[{"start","up","down","avg_latency_ms"}]`: hourly counts for the last `n` days (default 7, most 35), oldest first; hours without checks are left out |
| `POST /api/v1/monitors` | admin | [monitor settings](#monitor-settings) | `201` monitor. `422` with the reason for a bad setting. |
| `PUT /api/v1/monitors/{id}` | admin | monitor settings; `type` may be left out but not changed | `200` monitor |
| `DELETE /api/v1/monitors/{id}` | admin | | `204`, with its heartbeats and stats; members of a deleted group leave it |
| `POST /api/v1/monitors/{id}/pause` | admin | | `200` monitor, paused |
| `POST /api/v1/monitors/{id}/resume` | admin | | `200` monitor, checked at once |
| `POST /api/v1/monitors/{id}/check` | admin | | `202`: checked at once; the result arrives as an event. `422` for paused, push and manual monitors. |
| `POST /api/v1/monitors/{id}/status` | admin | `{"status":"up"\|"down","message"}` | `200` monitor; manual monitors only (`422` otherwise) |
| `GET`, `POST /api/push/{token}?status=up\|down&msg=<text>&ping=<ms>` | public: the token | | `200` `{"ok":true}`; `404` `{"ok":false,"msg":"Monitor not found or not active."}` for an unknown token, or a monitor that is paused or not a push monitor. `status` defaults to `up`; `msg` is cut to 500 characters. Not subject to cross-origin protection. |
| `GET /api/v1/events` | read | | `text/event-stream`, below |
| `GET /api/v1/settings/retention` | admin | | `200` `{"heartbeat_days":90}` |
| `PUT /api/v1/settings/retention` | admin | `{"heartbeat_days"}`, 1 to 3650 | `200` the saved setting |

#### Monitor

```json
{
  "id": 3,
  "name": "Example site",
  "type": "http",
  "parent_id": null,
  "active": true,
  "interval": 60,
  "retry_interval": 60,
  "retries": 1,
  "resend_every": 0,
  "timeout": 48,
  "upside_down": false,
  "description": "",
  "settings": { "url": "https://example.com", "method": "GET", "...": "the type's settings, defaults filled in" },
  "secrets_set": [],
  "channels": [2],
  "tags": [{ "id": 1, "name": "prod", "color": "#1f5fbf", "value": "eu" }],
  "push_url": "https://eyrie.example.com/api/push/<token>?status=up&msg=OK&ping=",
  "created_at": "2027-01-15T08:00:00Z",
  "updated_at": "2027-01-15T08:00:00Z",
  "status": {
    "status": "up",
    "since": "2027-01-15T08:00:12Z",
    "message": "200 OK",
    "last_check": "2027-01-15T09:30:12Z",
    "latency_ms": 123.45,
    "cert_expires": "2027-03-01T00:00:00Z",
    "checking": false,
    "maintenance": ""
  },
  "uptime": { "24h": 0.9993, "30d": 0.9998, "1y": null, "avg_latency_24h_ms": 130.2 },
  "beats": []
}
```

Times are in seconds (`interval`, `retry_interval`, `timeout`) or
milliseconds (`latency_ms`). `status.status` is `unknown` (never checked),
`up`, `pending`, `down` or `maintenance` (with the window's title in
`status.maintenance`, `""` otherwise); a paused monitor keeps its last
status with `active` false. `settings` never include secret settings
(passwords, keys, tokens; they are `""`): `secrets_set` names those that are
set. `tags` are by tag name. `push_url` is only on push monitors, only for admins, and uses
`EYRIE_BASE_URL` or the request's address. Uptime shares are from 0 to 1,
`null` without checks in the period. `beats` is only there with `?beats=`.

#### Monitor settings

`{"name","type","parent_id","active","interval","retry_interval","retries","resend_every","timeout","upside_down","description","settings","reset_push_token","clear_secrets"}`.
Missing numbers take their defaults (interval 60, retry interval the
interval, timeout 80% of the interval up to 48, the rest 0) and `active`
defaults to true. The limits and each type's `settings` are in
[Monitors](../user/monitors.md); unknown `settings` keys are refused.
A secret setting left empty (or out) keeps the saved one, unless its name
is in `clear_secrets`. `reset_push_token: true` gives a push monitor a new URL. `channels` lists
the notification channels to use; left out, a new monitor gets the default
channels and a changed one keeps its own (`422` for an unknown channel).
`tags`, `[{"id","value"}]`, are its tags with their values (`""` for none,
up to 100 characters); left out, a changed monitor keeps its own (`422` for
an unknown tag or one given twice).

### Tags

| Method and path | Access | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/tags` | read | | `200` `[{"id","name","color","monitors","created_at"}]`, by name; `monitors` counts the monitors with it |
| `POST /api/v1/tags` | admin | `{"name","color"}` | `201` the tag. `422` for a name not 1 to 50 characters or already taken (whatever the case), or a colour not `#rrggbb`. |
| `PUT /api/v1/tags/{id}` | admin | `{"name","color"}` | `200` the tag; as for `POST` |
| `DELETE /api/v1/tags/{id}` | admin | | `204`; it comes off every monitor and maintenance window |

### Maintenance windows

See [Maintenance windows](../user/maintenance.md).

| Method and path | Access | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/maintenance` | read | | `200` list of windows, by title |
| `GET /api/v1/maintenance/{id}` | read | | `200` window |
| `POST /api/v1/maintenance` | admin | `{"title","description","enabled","schedule","all_monitors","monitors","tags"}` | `201` window; it applies at once. `422` for a title not 1 to 100 characters, a description over 2000, covering nothing (no `all_monitors`, `monitors` or `tags`), an unknown monitor or tag, or a bad schedule. `enabled` left out is true. |
| `PUT /api/v1/maintenance/{id}` | admin | the same | `200` window |
| `DELETE /api/v1/maintenance/{id}` | admin | | `204`; its monitors come out of maintenance at once |

```json
{
  "id": 1,
  "title": "Database upgrade",
  "description": "",
  "enabled": true,
  "schedule": { "strategy": "weekdays", "time_zone": "Etc/UTC", "weekdays": [0], "from": "22:00", "to": "02:00" },
  "all_monitors": false,
  "monitors": [3],
  "tags": [1],
  "state": "scheduled",
  "change": "2027-01-17T22:00:00Z",
  "created_at": "2027-01-15T08:00:00Z",
  "updated_at": "2027-01-15T08:00:00Z"
}
```

`state` is `on`, `scheduled` (`change`: when it starts), `ended` (it will
not be on again) or `paused` (turned off); for `on`, `change` is when it
ends, `null` if it does not (a manual window).

`schedule` has a `strategy` and a `time_zone` (IANA; `""` is `Etc/UTC`),
and the strategy's fields:

| `strategy` | Fields |
| --- | --- |
| `manual` | none: on while `enabled` |
| `once` | `start`, `end`: local times such as `2027-01-15T22:00`; the end after the start |
| `interval` | `every_days` (1 to 3650), `first_day` (required), `from`, `to` |
| `weekdays` | `weekdays` (0 Sunday to 6 Saturday, at least one), `from`, `to` |
| `monthdays` | `month_days` (1 to 31, or -1 for the last day, at least one), `from`, `to` |
| `cron` | `cron` (five fields), `duration_minutes` (1 to 44640) |

`from` and `to` are times of day such as `22:00`; a `to` at or before `from`
is the next day. `interval`, `weekdays`, `monthdays` and `cron` take
`first_day` and `last_day` (dates such as `2027-01-15`, both included; `""`
for no limit).

#### Heartbeat

```json
{ "monitor_id": 3, "at": "2027-01-15T09:30:12Z", "status": "up", "latency_ms": 123.45, "message": "200 OK", "important": false }
```

`status` is `up`, `pending`, `down` or `maintenance`. `important` is true
when the status changed. `latency_ms` is `null` when
there was none (a failed connection, a push without `ping`).

#### Events

`GET /api/v1/events` keeps the connection open and sends
[Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html):

| Event | Data | When |
| --- | --- | --- |
| `beat` | a heartbeat | every check |
| `monitors` | `{"id":<id>}` | a monitor was added, changed, paused, resumed or deleted |

An idle stream gets a `: keepalive` comment every 25 seconds. A listener
that cannot keep up (64 events behind) misses events rather than slowing
the others; the app reloads the list when it reconnects.

### Status pages and incidents

See [Status pages, incidents and badges](../user/status-pages.md).

| Method and path | Access | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/status-pages` | read | | `200` list of pages, by title |
| `GET /api/v1/status-pages/{id}` | read | | `200` page |
| `POST /api/v1/status-pages` | admin | page settings, below | `201` page. `422` for a bad setting, a taken address, an unknown tag or monitor. |
| `PUT /api/v1/status-pages/{id}` | admin | page settings | `200` page; its sections are replaced |
| `DELETE /api/v1/status-pages/{id}` | admin | | `204`, with its logo and incidents |
| `PUT /api/v1/status-pages/{id}/logo` | admin | the image itself (PNG, JPEG, GIF or WebP, at most 1 MB) | `200` page. `422` for anything else. |
| `DELETE /api/v1/status-pages/{id}/logo` | admin | | `200` page |
| `GET /api/v1/status-pages/{id}/incidents` | read | | `200` every incident of the page, newest first |
| `POST /api/v1/status-pages/{id}/incidents` | admin | `{"title","status","message"}` | `201` incident, with its first update. `422` for a title not 1 to 200 characters, an unknown status, a message not 1 to 5000. |
| `POST /api/v1/incidents/{id}/updates` | admin | `{"status","message"}` | `200` incident; its status becomes the update's, and `resolved_at` is set (`resolved`) or cleared |
| `PUT /api/v1/incidents/{id}` | admin | `{"title"}` | `200` incident |
| `DELETE /api/v1/incidents/{id}` | admin | | `204`, with its timeline |

A page, and its settings (the same without `id`, `logo_url`, `created_at`,
`updated_at`; `theme` left out is `auto`):

```json
{
  "id": 1, "slug": "shop", "title": "Shop status", "description": "", "footer": "",
  "published": true, "theme": "auto", "accent": "", "show_tags": false, "show_cert_expiry": false,
  "sections": [{ "title": "Website", "tag_id": 2, "monitors": [3, 4] }],
  "logo_url": "/api/v1/status/shop/logo?v=1800000000000",
  "created_at": "2027-01-15T08:00:00Z", "updated_at": "2027-01-15T08:00:00Z"
}
```

`slug` is 1 to 64 lower-case letters, digits and single dashes; `theme` is
`auto`, `light` or `dark`; `accent` is `#rrggbb` or `""`; at most 50
sections, each title up to 100 characters; `tag_id` is `null` for none.
`logo_url` is `""` without a logo.

An incident:

```json
{
  "id": 7, "title": "Checkout errors", "status": "monitoring",
  "updates": [{ "status": "monitoring", "message": "A fix is out.", "author": "alice", "created_at": "2027-01-15T09:10:00Z" }],
  "created_at": "2027-01-15T08:40:00Z", "updated_at": "2027-01-15T09:10:00Z", "resolved_at": null
}
```

`status` is `investigating`, `identified`, `monitoring` or `resolved`;
`updates` are newest first.

#### The public page

| Method and path | Access | Answer |
| --- | --- | --- |
| `GET /api/v1/status/{slug}` | public (an unpublished page: signed in) | `200` the page as anyone sees it, `Cache-Control: no-store`; `404` otherwise |
| `GET /api/v1/status/{slug}/logo` | as above | `200` the image, cached a day (the URL changes with the logo); `404` without one |

```json
{
  "slug": "shop", "title": "Shop status", "description": "", "footer": "", "published": true,
  "theme": "auto", "accent": "", "logo_url": "", "show_cert_expiry": false,
  "status": "up",
  "sections": [{ "title": "Website", "monitors": [{
    "id": 3, "name": "Shop website", "status": "up",
    "uptime": { "24h": 1, "30d": 0.9996, "90d": 0.9998 },
    "days": [{ "date": "2026-10-18", "uptime": null }, "… 90 days, oldest first"],
    "tags": [{ "name": "prod", "color": "#1f5fbf", "value": "eu" }]
  }] }],
  "incidents": [], "maintenance": [{ "title": "Upgrade", "description": "", "state": "scheduled", "change": "2027-01-17T22:00:00Z" }],
  "generated_at": "2027-01-15T09:30:00Z"
}
```

`status` (the page's) is `up`, `partial`, `down`, `maintenance` or
`unknown`; a monitor's is `up` (pending included), `down`, `maintenance`,
`paused` or `unknown`. `tags` only with `show_tags`, `cert_expires` only
with `show_cert_expiry`. `incidents` are the open ones and those resolved
in the last 14 days, without `author`; `maintenance` the windows covering a
monitor on the page that are on or start within 7 days. Nothing else about
monitors is given.

#### Badges

`GET /api/badge/{id}/{kind}`, public: an SVG image (`image/svg+xml`,
`Cache-Control: public, max-age=60`) for a monitor on a published status
page; `404` for any other monitor, an unknown `kind` or a bad `period`.

| `kind` | Label | Message |
| --- | --- | --- |
| `status` | the monitor's name | its status, as on a status page |
| `uptime` | `uptime 24h` (`?period=` `24h`, `30d` or `1y`) | the share up, never rounded up to 100%, or `no data` |
| `response` | `response` | the last response time in milliseconds, or `no data` |
| `cert` | `certificate` | days until the certificate expires, `expired` or `none` |

`?label=` replaces the label (at most 50 characters).

### Backup and restore

All for admins. See [Backup and restore](../user/backup-and-restore.md).

| Method and path | Body | Answer |
| --- | --- | --- |
| `POST /api/v1/backup` | `{"history","passphrase"}`: history `none`, `30`, `60`, `90` or `all`; a passphrase of at least 12 characters | `200` the backup file (`application/json`, `Content-Disposition: attachment; filename="eyrie-backup-YYYYMMDD-HHMMSS.json"`, `no-store`). `422` for a short passphrase or another history. |
| `POST /api/v1/restore` | `multipart/form-data`: `file`, the backup file (at most 128 MB), and `passphrase` | `200` `{"restored":{"app_version","schema","database","created","history"}}`, and the session cookie cleared: every session has ended. `400` without a file or a form; `413` over 128 MB; `422` with the reason (a wrong passphrase, not a backup, a newer version, no enabled admin), nothing changed. |
| `GET /api/v1/settings/backup` | | `200` the scheduled backups, below |
| `PUT /api/v1/settings/backup` | `{"enabled","time","keep","history","passphrase"}`: `time` `HH:MM` (the server's time), `keep` 1 to 365, `passphrase` at least 12 characters or `""` to keep the saved one | `200` the scheduled backups. `422` for a bad value, or turning them on without a passphrase. |
| `POST /api/v1/backups/run` | | `200` the scheduled backups, after making one now with the saved settings (its failure is in `last.error`). `422` without a saved passphrase. |
| `GET /api/v1/backups/{name}` | | `200` that file of the backup folder, as an attachment. `404` for a name that is not `eyrie-backup-YYYYMMDD-HHMMSS.json` or not there. |

The scheduled backups, never the passphrase:

```json
{
  "enabled": true, "time": "03:00", "keep": 14, "history": "none", "has_passphrase": true,
  "last": { "at": "2027-01-15T03:00:12Z", "file": "eyrie-backup-20270115-030012.json", "error": "", "success": "2027-01-15T03:00:12Z" },
  "files": [{ "name": "eyrie-backup-20270115-030012.json", "size": 48211, "time": "2027-01-15T03:00:13Z" }],
  "dir": "/data/backups", "not_mounted": false, "error": ""
}
```

`last` is `null` before the first scheduled backup, and `last.success`
`null` until one works. `files` are the folder's backup files, newest
first; `error` says why the folder cannot be read. `not_mounted` is true
when the folder is not on a volume of its own (from `/proc/self/mountinfo`;
false where that cannot be read).

### Metrics

`GET /metrics` serves Prometheus' text format (`text/plain; version=0.0.4`)
when `EYRIE_METRICS_TOKEN` is set, to requests with
`Authorization: Bearer <token>` (`401` with `WWW-Authenticate` otherwise;
compared in constant time). Without the setting it is `404`.

| Metric | Type | Labels | Value |
| --- | --- | --- | --- |
| `eyrie_build_info` | gauge | `version` | 1 |
| `eyrie_monitor_status` | gauge | `id`, `monitor`, `type` | 1 up, 0 down, 2 pending, 3 maintenance, -1 not checked yet |
| `eyrie_monitor_active` | gauge | same | 1 running, 0 paused |
| `eyrie_monitor_response_seconds` | gauge | same | the last check's response time; absent when it had none |
| `eyrie_monitor_cert_days_remaining` | gauge | same | days until the certificate seen by an HTTPS check expires |
| `eyrie_monitor_checks_total` | counter | same, `result` (`up`, `down`) | checks since Eyrie started; pending counts as up, maintenance not at all |
| `eyrie_checks_running` | gauge | | checks in progress |
| `eyrie_notifications_total` | counter | `result` (`sent`, `failed`) | notifications since Eyrie started, per channel; failed after every retry |
| `eyrie_alert_analyses_total` | counter | `result` (`ok`, `failed`) | alert analyses since Eyrie started; failed: an error, no answer in time, or more than four at once |
| `eyrie_backup_last_success_timestamp_seconds` | gauge | | when the last scheduled backup was written, Unix seconds; absent until one is |
| `go_goroutines`, `go_memstats_alloc_bytes`, `go_memstats_sys_bytes` | gauge | | Go runtime |
| `process_start_time_seconds` | gauge | | when Eyrie started, Unix seconds |

`monitor` is the monitor's name, with new lines replaced by spaces.

### Everything else

| Request | Answer |
| --- | --- |
| any other request under `/api/` | `404` `{"error":"no such API route"}`, also for another method on a route above |
| everything else | The web app, below |

## Cross-origin requests

Go's `http.CrossOriginProtection` wraps every route: a request other than
`GET`, `HEAD` or `OPTIONS` that the browser marks as cross-site
(`Sec-Fetch-Site`), or whose `Origin` does not match its `Host`, gets `403`
`{"error":"cross-origin request refused"}`. `EYRIE_BASE_URL` is a trusted
origin, for proxies that rewrite `Host`.

## The web app

`internal/ui` serves the Svelte app embedded in the binary (built with the
tag `ui`, as the image is):

| Request | Answer |
| --- | --- |
| A file of the app, such as `/assets/index-1a2b3c.js` | The file. Under `/assets/` (names carry a content hash): `Cache-Control: public, max-age=31536000, immutable`. Elsewhere: `no-cache`. |
| A path without a file extension, such as `/` or `/settings/users` | `index.html`, `no-cache`, so the app's own routes survive a reload and an upgrade takes effect at once. |
| A path with an extension that is not a file | `404` |
| A method other than `GET` or `HEAD` | `405`, `Allow: GET, HEAD` |

A binary built without the tag `ui` answers every app path with `503` and a
page saying it has no web interface.

The app's pages: `/` (monitors), `/monitors/new`, `/monitors/{id}`,
`/monitors/{id}/edit`, `/maintenance`, `/status-pages`,
`/status-pages/new`, `/status-pages/{id}`, `/status/{address}` (a public
status page), `/login`, `/settings` (the same as `/settings/account`),
`/settings/account`, `/settings/users`, `/settings/sso`,
`/settings/api-tokens`, `/settings/tags`, `/settings/notifications`,
`/settings/alert-analysis`, `/settings/history` and `/settings/backup`.
Any other path shows **Not found**. Before the first account exists every
page but public status pages shows the setup form.

## Response headers

On every response:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: same-origin
Content-Security-Policy: default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'
```

The built app has no inline scripts, so scripts can only come from Eyrie
itself. `'unsafe-inline'` for styles is needed by Svelte, which sets styles
at run time. `img-src data:` allows the two-factor QR code.

## Timeouts

Request headers must arrive within 10 seconds. On shutdown, requests in
progress get 10 seconds to finish.
