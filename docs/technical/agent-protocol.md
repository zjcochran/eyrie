# Agent protocol

Code: `internal/agentauth` (keys, signatures, messages), `internal/agents`
(Eyrie's side), `internal/agent` (agent mode), `internal/workload` (what
agents list), `internal/api/agents.go` (routes), `internal/store/agents.go`.
For running agents, see [Agents](../user/agents.md).

Agents call Eyrie; Eyrie never calls an agent. It hands an agent the
checks of the monitors that run on it in answer to the agent's own
requests (see [Checks](#checks)).
The agent routes are on Eyrie under `/api/agent/v1/`, at the same address
as the web app. They are outside sessions and CSRF; Go's cross-origin
protection lets them through, because agents send no `Origin` or
`Sec-Fetch-Site` header. Every route except `enroll` needs an agent
signature. Bodies are limited to 4 MiB.

## Keys

Ed25519, encoded as:

| Kind | Format |
| --- | --- |
| Public key | `ed25519:` + unpadded base64url of the 32-byte key |
| Private key (the agent's `key` file, mode 0600) | `ed25519-private:` + unpadded base64url of the 32-byte seed |
| Fingerprint, shown in Settings → Agents | The first 16 hex digits of the public key's SHA-256 |

Eyrie stores each agent's public key (`agents.public_key`) and, during a
rotation, the one before (`previous_key`). It never has a private key.

## Requests

Every signed request has:

```
Authorization: Eyrie-Agent agent=<id>, ts=<unix seconds>, nonce=<16 random bytes, base64url>, sig=<base64url>
```

`sig` is the agent's signature over these lines, joined with `\n`:

```
<method>
<path and query, as sent>
<agent id>
<ts>
<nonce>
<hex SHA-256 of the body>
```

Eyrie answers `401` with `WWW-Authenticate: Eyrie-Agent` unless all of
these hold:

1. The header parses: each of the four fields exactly once, no others.
2. The agent exists and is enrolled (has a key).
3. The signature verifies with the agent's key, or its previous key while
   a rotation is under way.
4. `ts` is within **5 minutes** of Eyrie's clock.
5. The nonce was not used by that agent in the last **10 minutes** (kept
   in memory).
6. The key is at most **90 days** old (`key_created`).

A refusal is logged and added to the audit log as `agent refused`, with
the reason, at most once an hour per agent. A request signed with the new
key while `previous_key` is set ends a rotation: `previous_key` is
cleared.

## Routes

| Method and path | Body | Answer |
| --- | --- | --- |
| `POST /api/agent/v1/enroll` | `{"token", "public_key", "hostname", "platform", "version"}`, not signed | `200 {"agent", "name"}`; `400` for a malformed body or key; `401` for an unknown, used or expired token; `429` after 10 refused tokens within a minute from one client address |
| `POST /api/agent/v1/report` | `{"version", "platform", "hostname", "error", "workloads": [...]}` | `204`; `400` for more than 10,000 workloads, or a workload with an empty, repeated or over-300-byte key |
| `POST /api/agent/v1/rotate` | `{"new_key", "ts", "proof"}` | `204`; `400` for a malformed key or a proof that does not verify; `401` if signed with the previous key for a key other than the current one |
| `POST /api/agent/v1/jobs` | `{}` | `200 {"jobs": [{"id", "monitor", "type", "settings", "timeout_ms"}]}`: the checks to run, at once when there are some, else after up to 15 seconds with none |
| `POST /api/agent/v1/results` | `{"results": [{"id", "ok", "message", "latency_us", "cert_expires"}]}` | `204`; `400` for more than 10,000 results. Results for jobs that are not the agent's, or no longer awaited, are dropped |

A workload is `{"key", "name", "kind", "group", "up", "status"}`: `kind`
is `container`, `service`, `deployment`, `statefulset`, `daemonset`,
`lxc` or `vm`; `group` the Compose project, stack, namespace or Proxmox
node; `up` and `status` what the agent decided (see
[the workload types](../user/monitors.md#docker-swarm-service-kubernetes-workload-proxmox-ve-guest)). Names, groups and
statuses are cut to 300 bytes, `error` to 1,000.

## Enrollment

1. **Settings → Agents → Add an agent** inserts the agent with the
   SHA-256 of a one-time token, `eya_` + 32 random bytes (base64url),
   valid for 1 hour.
2. The agent loads or makes its key in `EYRIE_AGENT_DIR` and posts the
   token and its public key to `enroll`.
3. In one statement, Eyrie finds the agent by the token's hash and an
   unexpired time, stores the key and `key_created`, clears the token, and
   sets `last_seen`. It adds `agent enrolled` to the audit log.
4. The agent saves `state.json`: its ID, its name, `key_created` and the
   SHA-256 of the token it used.

**New token** clears the key and stores a new token: the agent's next
request is refused. An agent started with a different
`EYRIE_AGENT_TOKEN` than the one in `state.json` makes a new key and
enrolls again.

## Reports

The agent lists its platform's workloads and posts a report at start,
every **30 seconds**, and 2 seconds after Docker reports a container or
service event (`create`, `start`, `die`, `stop`, `kill`, `pause`,
`unpause`, `health_status`, `rename`, `destroy`, `update`, `remove`). When
listing fails it posts the error instead of workloads. A successful report
touches `heartbeat` in its folder, which `eyrie healthcheck` reads. Eyrie
refusing it makes it wait 5 minutes.

Eyrie keeps each agent's last report in memory and in `agents`:
`last_seen`, `last_address` (read through trusted proxies, like every
client address), `version`, `platform`, `hostname`, `error`, and the
workloads when they changed. It compares the workloads with the last
report; for those new, gone or with another `up` or `status`, it checks
the agent's monitors of them at once (`Engine.CheckMatching`): Docker
container monitors by the container's name, Swarm service monitors by
`service/<name>`, Kubernetes workload monitors by
`<namespace>/<kind>/<name>`, Proxmox VE guest monitors by `lxc/<vmid>` and
`qemu/<vmid>`. It checks all of the agent's monitors when the report
carries an error, the last one did, or the agent had been offline. The
reports also give the monitor form its suggestions.

## Checks

A monitor with `agent_id` set is checked on that agent. The engine
schedules it like any other; when a check is due, its checker
(`Manager.Checker`) does this:

1. If the agent was removed, is not enrolled or is offline, the check
   fails at once with that reason.
2. Otherwise it queues a job for the agent: a random 32-hex-digit ID, the
   monitor's ID, type and timeout, and its settings with their secrets
   opened (`check.Opened`).
3. The agent's request for jobs, held by Eyrie for up to **15 seconds**,
   is answered with every queued job; an agent asks again as soon as it
   has an answer, so a job waits moments. After an error it waits 5
   seconds, after a refusal 5 minutes.
4. The agent runs up to 64 checks at once, each with the job's timeout,
   with the same code as Eyrie (`check.Parse`): its own network, its
   `DOCKER_HOST` as the default of Docker container monitors, and its
   platform's workloads for the workload types. Each result is posted to
   `results` as soon as the check ends.
5. Eyrie waits for the result up to the monitor's timeout plus **20
   seconds**. A result in time is the check's result, recorded as if Eyrie
   had checked; otherwise the check fails: `The agent edge did not fetch
   the check in time` (the job is withdrawn) or `The agent edge sent no
   result in time`. Removing the agent fails its waiting checks at once.

The settings travel in the clear inside the HTTPS request; over plain
`http://` they can be read on the way. A forged answer cannot reach an
agent over HTTPS: the agent checks Eyrie's certificate.

## Offline

An enrolled agent with no report for **90 seconds** is offline. Every 15
seconds Eyrie looks for agents that went offline, logs `agent stopped
reporting`, and checks the monitors that run on them, which go down. The next report logs
`agent reporting again`.

## Key rotation

1. When its key is **30 days** old (checked at start and hourly), the
   agent makes a new key and writes it to `key.next`.
2. It posts `rotate`, signed with its current key. The body is the new
   public key, a time, and `proof`: the new key's signature over
   `rotate\n<agent id>\n<new key>\n<ts>`. The time must be within 5
   minutes.
3. Eyrie moves `public_key` to `previous_key`, stores the new key and
   `key_created`, and adds `agent key rotated` to the audit log.
4. The agent renames `key.next` to `key` and saves the new `key_created`.
5. Its first request signed with the new key clears `previous_key`.

If the agent stops between steps 3 and 4, it finds `key.next` at its next
start and posts the same rotation again, signed with the old key, which
Eyrie still accepts as `previous_key`; Eyrie answers `204` because the new
key is already current. A rotation signed with the previous key to any
other key is refused, so a stolen old key cannot rotate the agent away.

## Platforms

| | Docker | Docker Swarm | Kubernetes | Proxmox VE |
| --- | --- | --- | --- | --- |
| `EYRIE_AGENT_PLATFORM` | `docker` | `swarm` | `kubernetes` | `proxmox` |
| Reached through | `DOCKER_HOST` | `DOCKER_HOST`, on a manager | The pod's ServiceAccount (`KUBERNETES_SERVICE_HOST`, `ca.crt` and `token`, read again for every request) | `PROXMOX_URL` with an API token, the certificate pinned by `PROXMOX_FINGERPRINT` |
| API calls | `GET /containers/json?all=1`, `GET /events` | `GET /info` at start, `GET /services?status=true`, `GET /containers/json?all=1`, `GET /events` | `GET /apis/apps/v1/{deployments,statefulsets,daemonsets}`, or under `/namespaces/<ns>/` for each of `EYRIE_AGENT_NAMESPACES` | `GET /api2/json/cluster/resources?type=vm` |
| Workloads | Every container | Every service, and the node's containers that are not tasks | Deployments, StatefulSets, DaemonSets | LXC containers and VMs, not templates |
| Key | The container's name | `service/<name>` | `<namespace>/<kind>/<name>` | `lxc/<vmid>`, `qemu/<vmid>` |
| Up | State `running`, and the status text has no `(unhealthy)` | `RunningTasks` ≥ `DesiredTasks` > 0 | `readyReplicas` ≥ `spec.replicas` (unset: 1) > 0; DaemonSets `numberReady` ≥ `desiredNumberScheduled` > 0 | `status` is `running` |

Requests to Kubernetes and Proxmox time out after 30 seconds; answers are
read up to 32 MiB.
