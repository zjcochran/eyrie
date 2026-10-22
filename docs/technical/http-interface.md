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
- **Public** routes need nothing.

See [Security](security.md) for sessions, CSRF, rate limits and tokens.

## Routes

### Health and version

| Method and path | Access | Answer |
| --- | --- | --- |
| `GET /healthz` | public | `200` `ok` while the database answers a ping within 2 seconds; otherwise `503` `database unavailable` and a warning in the log. `text/plain`, `Cache-Control: no-store`. |
| `GET /api/v1/info` | public | `200` `{"name":"Eyrie","version":"<version>"}` |

### Setup and sign-in

| Method and path | Access | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/session` | public | | `200` [session](#session) |
| `POST /api/v1/setup` | public, only while no account exists | `{"username","password"}` | `201` session, signed in as the new admin. `403` once an account exists; `422` for a bad username or password. |
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
  "version": "v0.2.0"
}
```

`setup` is `true` only while no account exists. `user` is `null` and
`csrf` is left out when not signed in. `password_sign_in` is `false` when
single sign-on is the only way in (and `EYRIE_LOCAL_LOGIN` is not `force`).

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

### API tokens

All admin routes.

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /api/v1/tokens` | | `200` `[{"id","name","created_at","created_by","last_used"}]`, oldest first |
| `POST /api/v1/tokens` | `{"name"}`, 1 to 64 characters | `201` the token's fields plus `"token":"eyt_…"`, the only time it is shown |
| `DELETE /api/v1/tokens/{id}` | | `204`; the token stops working at once |

A token is sent as `Authorization: Bearer eyt_…`. Tokens can only read
(`GET` and `HEAD`) and are accepted on the monitor routes that come with
the monitor engine; no route of this version takes one.

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

The app's pages: `/` (monitors), `/login`, `/settings/account`,
`/settings/users`, `/settings/sso` and `/settings/api-tokens`. Before the
first account exists every page shows the setup form.

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
