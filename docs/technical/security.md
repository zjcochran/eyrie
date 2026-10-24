# Security

## What is being protected

Eyrie's web app and API show what is monitored and, with an admin account,
change it. The threat model for sign-in and accounts:

| Threat | Mitigation |
| --- | --- |
| Someone on the network uses the app | Every API route needs a session or, for reading, an API token, except setup (until the first account exists), sign-in, `/api/v1/session`, `/api/v1/info`, `/healthz`, push URLs (their token) and `/metrics` (its token) |
| Password guessing | argon2id hashes, at least 12 characters, a rate limit per client address and per username |
| A stolen password | Optional two-factor sign-in with an authenticator app |
| A stolen session cookie | `HttpOnly`, `SameSite=Lax`, `Secure` over HTTPS; only a hash stored; sessions end on a password change, a reset or disabling |
| Cross-site request forgery | A per-session CSRF token on every change, plus Go's cross-origin protection |
| Script injection | Svelte escapes text; a Content-Security-Policy without inline scripts; SVG pictures refused |
| Clickjacking | `X-Frame-Options: DENY`, `frame-ancestors 'none'` |
| Forged proxy headers | Believed only from configured trusted proxies |
| Single sign-on attacks | Authorization code with PKCE, state, nonce, ID-token checks, matching on issuer and subject |
| A leaked database | Passwords and tokens hashed; every [stored secret](#stored-secrets) sealed with a key kept apart |
| An open redirect after sign-in | `next` must be a local path |

Eyrie does not protect against someone with access to the host, the data
folder or the database, or against a malicious single sign-on provider you
configured. Over plain HTTP, anyone on the network path can read the
traffic, cookies included: serve Eyrie over HTTPS (through a reverse proxy)
whenever it is reachable by anyone you do not trust.

## Setup window

Until the first account exists, `POST /api/v1/setup` creates an admin
**without authentication**. It creates the account only while the users
table is empty, in one statement, so two setups racing cannot both win.
After that it answers `403`. `POST /api/v1/setup/restore` restores a backup
file the same way: only while the users table is empty, checked again inside
the restore's transaction. Complete setup right after the first start.

## Passwords

- At least 12 characters (`store.MinPasswordLength`); no other rules.
- Hashed with **argon2id**: 64 MiB of memory, 3 passes, 2 lanes, a 16-byte
  random salt and a 32-byte key (RFC 9106's second recommended set), stored
  as `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>`.
- Checked with the parameters stored in the hash, so they can be raised
  later, and compared in constant time.
- A sign-in for an unknown username is checked against a dummy hash, so it
  takes as long as a real one and does not reveal which usernames exist. A
  wrong password, an unknown user and a disabled account get the same
  answer.
- Usernames are unique ignoring case.

## Two-factor sign-in

Optional for each account with a password (`internal/totp`, RFC 6238):

- HMAC-SHA1, 6 digits, 30-second steps, a 160-bit random secret; one step
  either side is accepted. The secret is sealed with the instance key.
- `users.totp_step` keeps the last step accepted, updated with
  `UPDATE … WHERE totp_step < ?`, so each code (and any older one) works
  once, even with two sign-ins racing.
- 10 recovery codes of 12 characters (about 59 bits each) from a
  31-character alphabet, stored as SHA-256 hashes and deleted when used.
- After the right password, no session is made: a random 256-bit token in
  the `eyrie_2fa` cookie (path `/api/v1/login/code`, `HttpOnly`,
  `SameSite=Strict`) points to a pending sign-in kept in memory for 5
  minutes, which allows 5 codes. Wrong codes count toward the sign-in rate
  limit. The sign-in time is recorded only when the code is right.
- Setting it up, making new recovery codes and turning it off need the
  current password. During setup the secret travels to the browser and back
  sealed, so it is stored only after a right code.
- It is not offered to accounts linked to single sign-on, and linking turns
  it off: those accounts use the provider's second factor. It is not offered
  either while password sign-in is off.
- An admin can turn it off for another user; `eyrie reset-2fa` does it from
  the command line.

## Sign-in rate limit

In memory, per process (cleared on restart). A failed password or code
counts against two keys, `ip:<client address>` and
`user:<lower-cased username>`. A sign-in is refused with `429` while either
key has 5 or more failures in the last 15 minutes (a sliding window). A
successful sign-in clears both keys.

## Sessions

- Made at sign-in: a 32-byte random token (base64url) in the cookie
  `eyrie_session`. The database keeps only its SHA-256.
- Cookie: `Path=/`, `HttpOnly`, `SameSite=Lax`, `Max-Age` 7 days, `Secure`
  when the request came over HTTPS (directly, through a trusted proxy with
  `X-Forwarded-Proto: https`, or when `EYRIE_BASE_URL` is `https://…`).
- Lifetime: 7 days, sliding; use extends it, at most once an hour.
- Ended by signing out, a password change or reset (every session of that
  user; a change gives the browser that made it a new one), disabling or
  deleting the user, and expiry. Expired sessions are deleted daily. A
  disabled user's session is refused even before it is deleted.

## CSRF and cross-origin requests

Two independent layers:

1. **CSRF token.** Each session has a random token, which
   `GET /api/v1/session` and every sign-in return. Every request other than
   `GET` and `HEAD` to a session route must send it in `X-CSRF-Token`,
   compared in constant time; otherwise `403`.
2. **Cross-origin protection.** Go's `http.CrossOriginProtection` wraps
   every route and refuses unsafe requests that the browser marks as
   cross-site (`Sec-Fetch-Site`) or whose `Origin` does not match the
   `Host`. `EYRIE_BASE_URL` is a trusted origin.

Setup, sign-in and sign-out have no session yet (or end it) and rely on
layer 2.

## Roles

| Role | Can |
| --- | --- |
| viewer | Read the app: monitors and their history, tags, maintenance windows, status pages (unpublished ones too) and their incidents; change their own password, two-factor sign-in, picture and single sign-on link |
| admin | Everything: monitors, tags, maintenance windows, status pages and incidents, notification channels, alert analysis, users, single sign-on, history retention, API tokens, backups and restores |

Checked for each route on the server, not only by hiding controls. Admins
cannot disable, delete or change the role of themselves, and the last
enabled admin cannot be disabled, deleted or made a viewer.

## API tokens

`eyt_` followed by 32 random bytes in base64url, made by an admin and shown
once. Only a SHA-256 hash is stored; a request is matched by hashing the
token it presents. A token can only read (`GET` and `HEAD`) the read routes
(monitors and their history, the event stream, tags, maintenance windows,
status pages and incidents): it cannot sign in, reach session routes,
settings or backups, or change anything. Its last use is recorded.
Revoking deletes it at once.

## Push URLs

A push monitor's URL carries 18 random bytes (base64url) that are its only
credential: whoever has the URL can report the monitor up or down, and
nothing else. Only admins see it (not viewers or API tokens). The token is
stored as is, since the monitor's page shows the URL to admins. **Make a new
URL** replaces it. Push requests are
exempt from cross-origin protection, as the token is not a cookie.

## Status pages and badges

Published status pages, their logos and the badges of their monitors are
public. They give only what a page is meant to show: monitors' names,
statuses, uptime, and, if the page says so, tags and certificate expiry;
incidents without who wrote them; maintenance windows' titles and
descriptions. Never check messages, URLs, hosts or other settings, which
can be internal. An unpublished page is `404` to anyone not signed in, and
its monitors have no badges. Incident messages and page texts are shown as
plain text, never as HTML.

## Metrics endpoint

Off (`404`) unless `EYRIE_METRICS_TOKEN` is set. With a token, it requires
`Authorization: Bearer <token>`, compared in constant time; otherwise `401`
with `WWW-Authenticate`. Metrics reveal monitor names, types, statuses and
response times, not their settings.

## Monitor checks

Checks reach whatever admins configure, from Eyrie's network position: an
admin can make Eyrie connect to internal addresses and see whether they
answer. Only admins can add or change monitors. HTTP checks follow at most
the configured redirects and read at most 1 MiB of a response; their TLS
certificates are verified unless the monitor ignores TLS errors.

- **Programs.** The Tailscale ping, system service and PM2 types run
  `tailscale ping`, `systemctl is-active` (or `sc.exe query` on Windows)
  and `pm2 jlist` on Eyrie's host, directly and never through a shell. Their
  one argument (a host, a service or a process name) is checked: it cannot
  start with `-`, and host and service names allow no spaces or shell
  characters. In Eyrie's image these programs are not installed, so these
  types fail unless you add them.
- **Docker.** A Docker monitor only reads a container's state
  (`GET /containers/{name}/json`), but whoever can reach the Docker socket
  controls the host: mount it only if you need Docker monitors, and prefer
  a read-only socket proxy that allows just that request.
- **SFTP host keys.** Without a host key fingerprint, an SFTP monitor
  accepts any server key (and shows its fingerprint in the message); set
  the fingerprint to detect a changed key.
- **Real browser.** The browser runs pages Eyrie asks for; it should be its
  own container, not reachable from the internet.
- **Databases.** SQL monitors run their query as written: give them a
  database user with only the rights the query needs (a `SELECT 1` needs
  none). Kafka monitors write a message to their topic at every check.
- **gRPC.** The `.proto` definition is parsed by Eyrie's own reader (at
  most 256 KiB, nesting at most 64 deep when decoding); imports are not
  followed.

## Trusted proxies

`EYRIE_TRUSTED_PROXIES` lists addresses and ranges. For a request whose
direct peer is in it:

- **Client address:** `X-Forwarded-For` (all values joined) is walked from
  the right, skipping trusted addresses; the first other address is the
  client. An entry that is not an address stops the walk. Without one,
  `X-Real-IP`, else the peer.
- **HTTPS:** `X-Forwarded-Proto: https`.
- **Host for the callback URL:** `X-Forwarded-Host`.

From any other peer these headers are ignored. Walking from the right means
a client cannot pick its address by sending its own `X-Forwarded-For`:
proxies append, so the rightmost untrusted entry is the one the trusted
proxy saw.

## Single sign-on

- Discovery from `<issuer>/.well-known/openid-configuration` (10-second
  limit), kept until the settings change.
- Authorization code flow with **PKCE S256**. Each sign-in has a random
  `state` and `nonce` (24 bytes each) and a verifier, kept in memory for 10
  minutes and usable once; a restart drops sign-ins in progress.
- The ID token is checked by go-oidc: the signature against the provider's
  keys (JWKS), `iss` equal to the issuer, `aud` containing the client ID,
  and expiry. Eyrie then checks the `nonce`.
- Algorithms: RS, ES and PS 256/384/512 and EdDSA against the provider's
  keys. HS256 only when the provider lists it and a client secret of at
  least 32 bytes is saved; its key is the client secret (OIDC Core 10.1). An
  HS256 token is never checked against the keys and an asymmetric one never
  against the secret, so the public keys cannot be used as an HMAC key.
- Scopes: `openid profile email`, plus `groups` when `scopes_supported`
  lists it or is absent.
- Accounts are matched by `(issuer, subject)`, never by username or email,
  so someone who controls an account at the provider named like a local
  user gets a separate account at most.
- The allowed and admin groups are compared with the `groups` claim
  ignoring case, from the ID token or, when it has none, from userinfo.
- Linking needs a session when it starts, and the callback checks that the
  same user is still signed in.
- "SSO only" can be saved only by an admin whose own account is linked.
  `EYRIE_LOCAL_LOGIN=force` overrides it from the environment.
- Profile pictures come from the verified ID token's `picture` claim, which
  must be `http` or `https`. The access token is sent only when the URL has
  the issuer's scheme and host. At most 1 MiB is read, within 10 seconds,
  and only PNG, JPEG, GIF and WebP (by content) are kept; SVG is refused
  because, served from Eyrie's origin, it could run script. Uploads have the
  same limits. Pictures are served only to their user and to admins.
- `next` must start with `/`, must not start with `//` and must not contain
  `\`; anything else becomes `/`.

## Notifications

- Requests go only to the URLs admins configure (Pushover's and Telegram's
  own APIs for those kinds). Messages carry the monitor's name, type,
  description, status and check message, never settings or secrets; with
  alert analysis, also the model's explanation, written from the safe settings
  below.
- Webhooks with a signing secret carry `X-Hub-Signature-256`, the
  HMAC-SHA256 of the body, so a receiver can reject forged calls.
- Email is only sent encrypted: STARTTLS is required on `smtp://`, `smtps://`
  uses TLS from the start, certificates are verified (TLS 1.2 or newer), and
  the password is sent only after that.
- Errors show only the scheme and host of a URL, never a path that may hold
  a token. Answers are read only for the status code and the first 300
  bytes of an error.
- Test messages use the form's URL, so only admins can send them.

## Alert analysis

Off by default. When an admin turns it on, each down alert sends the
monitor's data to Anthropic under the admin's API key or subscription, or
to the server URL the admin set. See [what is sent](../user/alert-analysis.md#what-is-sent-to-the-model).

- Monitor settings are sent only through `check.Describe`, which each
  monitor type implements to list what may leave Eyrie: never header
  values, request bodies, or a URL's user, password, query and fragment. A
  new monitor type cannot compile without saying what it shares.
- Check messages can contain text from the monitored service; the prompt
  marks the monitor's data as data, not instructions. The answer is only
  ever text in a notification: the model has no tools and Eyrie runs
  nothing it says.
- The API keys (Anthropic's and the local server's, kept apart so one is
  never sent to the other) are sealed like other secrets and never
  returned. Claude subscription tokens are refused as API keys: Anthropic
  allows them only in its own apps.
- With `EYRIE_CLAUDE_CODE=true`, Eyrie downloads Claude Code over HTTPS
  from Anthropic and runs it only if its size and SHA-256 match the
  release's manifest, from the same place (as Anthropic's installer
  does). The binary is kept in the data folder, readable and runnable
  only by Eyrie's user. It runs as a fixed command with fixed arguments,
  but for the model name, which may not start with `-` or contain spaces,
  with no tools, MCP servers or slash commands, in a new empty folder, and
  is killed at the timeout. Its sign-in, `CLAUDE_CODE_OAUTH_TOKEN`, is
  read only by Claude Code.
- Only admins set the server URL, which must be `http` or `https` without
  a user, query or fragment. Without `https`, the monitor's data crosses
  the network in clear.
- At most four analyses run at once, bounding requests in an outage.

## Stored secrets

Two-factor secrets, the single sign-on client secret, the alert analysis
API keys (Anthropic's and a local server's), the scheduled backups'
passphrase, notification channels' URLs, tokens and users, and monitors'
secret settings (passwords, keys, tokens, SNMP communities, database
connection strings, a real browser's address) are sealed with AES-256-GCM (`internal/secrets`):

- Key: SHA-256 of `EYRIE_SECRET_KEY`, or of the contents of
  `EYRIE_DATA_DIR/secret.key`, made (32 random bytes, mode 0600) at the
  first start when neither exists.
- Format: `v1:` + base64(12-byte random nonce ‖ ciphertext ‖ tag).
- Secrets are never sent to the browser: the settings only say whether a
  client secret is saved, and a monitor's `secrets_set` names its secrets
  that are set. They are opened only to run a check, and never given to
  alert analysis.

## Backup files

Backup files hold password hashes, two-factor secrets and every stored
secret (opened with this instance's key, sealed again with the restoring
one's, so `secret.key` is not needed to move a backup), so they are
encrypted with the passphrase given:

- Key: argon2id(passphrase, 16-byte random salt, time 3, memory 64 MiB,
  2 threads) → 32 bytes. The parameters are stored in the file.
- Data: the gzipped JSON payload, sealed with AES-256-GCM (12-byte random
  nonce, the format name as additional data). A wrong passphrase and a
  modified file fail the same way.
- Readable without the passphrase: the format name and version, the Eyrie
  version, the database, the creation time, the schema version and the
  history range. The encrypted copy of these is the one used.
- Passphrases must be at least 12 characters.

Restoring is limited so a crafted file cannot exhaust the host: the upload
is at most 128 MB, the decompressed payload at most 1 GiB, and files asking
for more than 256 MiB of memory, 10 passes or 8 threads of argon2id are
rejected before any key is derived. Restored rows may only name known tables
and columns, are written with bound values in one transaction, and the
transaction is rolled back if any row is rejected or no enabled admin
remains. Only admins restore, except at setup. A restore ends every session.

Scheduled backups use a passphrase stored in the database, sealed with the
secret key like the other secrets, because nobody is there to type it. The
backup files alone stay protected by it; whoever has the data folder and
the key can recover it. Old files are deleted only if their names match
`eyrie-backup-YYYYMMDD-HHMMSS.json`, and downloads of stored files accept
only such names, so no other path can be read.

## Response headers

See [HTTP interface](http-interface.md#response-headers).
`Strict-Transport-Security` is not set by Eyrie: set it at your HTTPS
reverse proxy.

## Logging

The log has usernames and client addresses for every sign-in, failed or
not, and every sign-out, the groups a provider sent at each single sign-on
sign-in, and a line for every account change (who did it). It never has
passwords, codes, session or CSRF tokens, API tokens or secrets.

At the default level, `info`, Eyrie logs its start and stop, sign-ins and
sign-outs, every change made in the app (monitors, tags, groups, status
pages, incidents, maintenance windows, notification channels, users and
settings) and who made it, **Check now**, every status change of a
monitor, maintenance windows starting and ending, every notification sent
(channel and title), backups, and every failure (`warn`). With
`EYRIE_LOG_LEVEL=debug` it also logs every check (monitor, status,
response time, message) and every HTTP request (method, route, status,
time taken, client address). A request is logged by its route, such as
`POST /api/push/{token}`, not its path, so push tokens stay out of the
log.
