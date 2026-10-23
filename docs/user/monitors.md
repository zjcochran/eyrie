# Monitors

A monitor checks one thing, such as a web page, a TCP port or a host
answering pings, at a fixed interval, and records every check as a
**heartbeat**. Admins add, change, pause and delete monitors; everyone signed
in sees them.

## The dashboard

The start page lists every monitor. Groups come first, each followed by its
members (indented). Each row shows:

- the status: green **up**, red **down**, amber **pending**, violet
  **maintenance** (in a [maintenance window](maintenance.md)), grey
  **unknown** (not checked yet) or **paused**;
- its type and [tags](#tags);
- the last 50 checks as a bar, oldest on the left (hover a check for its
  time, response time and message);
- the uptime over the last 24 hours and the last response time.

The counts above the list add up the statuses. When monitors have tags, a
row of them above the list filters it: click a tag to see only the monitors
with it (without groups' nesting), **All** or the tag again for every
monitor. Everything updates live: the page listens to Eyrie's event stream
and needs no reload.

## A monitor's page

Click a monitor for its page:

- its tags;
- its status and since when (in maintenance, the window's title), the last
  check and its message;
- the last and the 24-hour average response time;
- uptime over 24 hours, 30 days and 1 year;
- for HTTPS monitors, how many days the server's certificate has left;
- a response time chart for 24 hours (every check), 7 or 30 days (hourly
  averages), with down periods marked in red;
- the status changes of the last 24 hours.

Admins also get **Check now**, **Pause** or **Resume**, **Edit** and
**Delete** (which also deletes the monitor's history).

## Adding a monitor

**Add a monitor** on the dashboard (admins). Choose the type first; it
cannot change later.

### Settings every type has

| Setting | Default | Meaning |
| --- | --- | --- |
| Name | | 1 to 100 characters |
| Check every | 60 s | 20 seconds to 1 day, whole seconds. For push monitors: how often a push is expected. |
| Timeout | 80% of the interval, at most 48 s | How long a check may take, from 0.1 seconds to the interval. Not used by push, manual and group monitors. |
| Retries before down | 0 | Failed checks in a row that leave the monitor **pending** before it goes **down**. 0: down at the first failure. |
| Retry every | the interval | How soon a pending monitor is checked again. |
| Notify again every | 0 (never) | While down, notify again every this many checks. |
| Notify | the default channels | The [notification channels](notifications.md) it sends to. |
| Tags | none | Its [tags](#tags), each with an optional value. |
| Group | none | The group the monitor is in. |
| Description | | Shown on the monitor's page. |
| Upside down | off | A failed check counts as up and a successful one as down: for things that must *not* answer. |
| Active | on | Unticked, the monitor is paused: not checked, its status kept. |

### How the status is decided

- A successful check makes the monitor **up**.
- A failed check while up makes it **pending** and it is checked again after
  the retry interval, until the retries run out; then it is **down**.
- A check that succeeds while pending makes it up again, with no
  notification.
- Only changes between up and down are [notified](notifications.md), and
  while down again every "Notify again every" checks.
- In a [maintenance window](maintenance.md), checks go on but are recorded
  as **maintenance**: they change nothing and notify nobody. Afterwards the
  monitor takes back its status and is checked at once.
- The next check starts one interval after the last one started, so checks
  do not drift. After a restart, first checks are spread over each
  monitor's interval, and every monitor keeps the status it had.

## Tags

Tags label monitors, such as `prod` or `database`, each with a colour.
Admins make them in **Settings → Tags** (a name of 1 to 50 characters,
unique whatever the case, and a colour) and give them to monitors in the
monitor's settings, each with an optional value of up to 100 characters
(`env: prod`, `region: eu`). Renaming or recolouring a tag changes it on
every monitor; deleting it takes it off every monitor and maintenance
window.

Tags show on the dashboard and a monitor's page, filter the dashboard, and
choose the monitors a [maintenance window](maintenance.md) covers.

## Types

### HTTP(S)

Requests a URL; up when the answer's status code is accepted.

| Setting | Default | Meaning |
| --- | --- | --- |
| URL | | `http://` or `https://` |
| Method | GET | GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS |
| Headers | | One per line, `Name: value` |
| Body | | Sent as is |
| Accepted status codes | `200-299` | Codes or ranges, separated by commas, such as `200-299, 301` |
| Maximum redirects | 10 | From 0 to 20. Redirects beyond it fail the check ("more than N redirects"). |
| Ignore TLS certificate errors | off | Accept expired, self-signed or wrongly named certificates |
| Warn before the certificate expires | off | Notify at the days set in Settings → Notifications; see [Certificate warnings](notifications.md#certificate-warnings) |

The request has `User-Agent: Eyrie/<version>`. Every check opens a new
connection. The response time is from sending the request to the end of
the body (at most 1 MiB of it is read). For HTTPS, the expiry of the
server's certificate is recorded and shown.

Messages: the status, such as `200 OK`, or `Status 503 Service
Unavailable`, `Timed out`, or the connection error.

### HTTP(S) keyword

An HTTP(S) check whose response body must contain a **keyword**
(case-sensitive), in the first 1 MiB. With **Up when the keyword is
missing**, it must not contain it. The message says whether the keyword was
found and, when it was missing, quotes the start of the body.

### HTTP(S) JSON query

An HTTP(S) check whose response must be JSON with a value at a **JSON path**
that compares as wanted with the **expected value**.

- The path names one value: object keys separated by dots, array indexes in
  brackets, keys with other characters in brackets and quotes, with an
  optional `$` or `$.` in front: `status`, `$.data.items[0].state`,
  `checks["db.primary"].ok`.
- Strings are compared as they are; other values as JSON (`true`, `null`,
  `3`, `{"a":1}`).
- **Comparison**: `==` (default), `!=`, `<`, `<=`, `>`, `>=` compare as
  numbers when both sides are numbers, otherwise as text; `contains` looks
  for the expected text in the value.
- The message shows the value found: `$.status is green`, or
  `$.status is red, not == green`.

Uptime Kuma uses JSONata here; Eyrie's paths are the common subset, without
JSONata's functions and filters.

### TCP port

Up when a TCP connection to **host** and **port** opens within the timeout.
The response time is the time to connect.

### Ping

Sends **pings per check** (1 to 10, default 1) ICMP echo requests of
**packet size** bytes (8 to 1400, default 56) to **host**, one after
another, each with an equal share of the timeout. Up when at least one is
answered; the response time is the average of the answers. A host name is
resolved first, and its IPv4 address is preferred.

Eyrie pings without root, through the system's unprivileged ICMP sockets.
Docker allows them for every container. On a Linux host without Docker, the
group Eyrie runs as must be in `net.ipv4.ping_group_range`; otherwise the
check fails with "cannot open an ICMP socket".

### DNS

Asks a **DNS server** (default `1.1.1.1`, port 53) for the **record type**
of a **name**. Up when it answers with at least one record of that type and,
if **A record must contain** is set, one of them contains that text.

- Record types: A, AAAA, CAA, CNAME, MX, NS, PTR, SOA, SRV and TXT. For PTR,
  the name may be an IP address; Eyrie asks for its reverse name.
- The query goes over UDP, and again over TCP when the answer is truncated.
- The message lists the records: `A 192.0.2.10, 192.0.2.11`,
  `MX 10 mail.example.com`, or the server's error, such as `NXDOMAIN`.

### Push

Eyrie does not check anything: something else calls the monitor's **push
URL**, shown to admins on its page, at least once per interval. It is down (after its
retries) when no push arrives in time.

```sh
curl -fsS "https://eyrie.example.com/api/push/<token>?status=up&msg=OK&ping="
```

- `status`: `up` (the default) or `down`, to report a failure.
- `msg`: a message, up to 500 characters.
- `ping`: a response time in milliseconds, optional.
- `GET` and `POST` both work. The answer is `{"ok":true}`, or `404`
  `{"ok":false,"msg":"Monitor not found or not active."}` for an unknown
  token or a paused monitor. This is the same URL format as Uptime Kuma's,
  so scripts written for it work.

The token in the URL is the only credential: keep it secret. **Make a new
URL** in the monitor's settings replaces it.

### Manual

Eyrie does not check anything: an admin sets it **up** or **down**, with an
optional message, on the monitor's page. Use it for things nothing can
check, such as a supplier's outage.

### Group

A group's status follows its active members: down when at least one is
down, up otherwise (pending members do not count). It is evaluated at its
interval and whenever a member's status changes. Its message names the
members that are down. A group can be in another group, but not in itself
or in one of its own members. Deleting a group keeps its members, outside
any group.

## Uptime and history

- Every check is a heartbeat: time, status, response time, message.
- Uptime is the share of checks that were up. **Pending checks count as up**:
  a monitor is not down until its retries run out. Paused periods have no
  checks and do not count, and checks in maintenance count neither way (nor
  towards the average response time).
- The 24-hour and 30-day figures come from hourly totals, the 1-year figure
  from daily totals (days in UTC), so they never read every heartbeat.
- Heartbeats are kept for 90 days by default: **Settings → History**
  (admins), from 1 to 3650 days. Hourly totals are kept 35 days and daily
  totals 400 days. Old data is deleted at start and then once a day.

## Who can do what

| | Viewer | Admin | API token |
| --- | --- | --- | --- |
| See monitors, their pages and history | yes | yes | yes, through the API |
| Add, change, pause, check, delete | | yes | |
| Set a manual monitor | | yes | |
| See tags and maintenance windows | yes | yes | yes, through the API |
| Add, change and delete tags and maintenance windows | | yes | |
