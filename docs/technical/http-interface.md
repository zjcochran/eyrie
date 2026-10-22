# HTTP interface

One server (`internal/api`) answers everything on `EYRIE_LISTEN`.

## Routes

| Method and path | Answer |
| --- | --- |
| `GET /healthz` | `200` `ok` while the database answers a ping within 2 seconds; otherwise `503` `database unavailable` and a warning in the log. `text/plain`, `Cache-Control: no-store`. |
| `GET /api/v1/info` | `200` `{"name":"Eyrie","version":"<version>"}`, `application/json`, `Cache-Control: no-store`. |
| any other request under `/api/` | `404` `{"error":"no such API route"}`, also for another method on a route above. |
| everything else | The web app, below. |

No route needs signing in yet.

## The web app

`internal/ui` serves the Svelte app embedded in the binary (built with the
tag `ui`, as the image is):

| Request | Answer |
| --- | --- |
| A file of the app, such as `/assets/index-1a2b3c.js` | The file. Under `/assets/` (names carry a content hash): `Cache-Control: public, max-age=31536000, immutable`. Elsewhere: `no-cache`. |
| A path without a file extension, such as `/` or `/monitors/12` | `index.html`, `no-cache`, so the app's own routes survive a reload and an upgrade takes effect at once. |
| A path with an extension that is not a file | `404` |
| A method other than `GET` or `HEAD` | `405`, `Allow: GET, HEAD` |

A binary built without the tag `ui` answers every app path with `503` and a
page saying it has no web interface.

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
at run time.

## Timeouts

Request headers must arrive within 10 seconds. On shutdown, requests in
progress get 10 seconds to finish.
