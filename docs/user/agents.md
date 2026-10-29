# Agents

An agent is Eyrie run as `eyrie agent` where Eyrie cannot see: on another
host or network, next to a Docker engine, on a Docker Swarm manager, in a
Kubernetes cluster, or beside a Proxmox VE cluster. A monitor set to run
on an agent (**Runs on** in its form) is checked there, from the agent's
host, with the same settings and results as in Eyrie. Monitors on
Eyrie are checked by Eyrie, as before.

- Every type that is checked can run on an agent. The types of a
  platform's workloads, **Docker Swarm service**, **Kubernetes workload**
  and **Proxmox VE guest**, run only on an agent set up for that platform
  (below).
- The agent calls Eyrie; Eyrie never calls the agent, and the agent opens
  no port. It needs to reach Eyrie's address, as a browser would. It asks
  Eyrie for checks to run all the time: Eyrie answers at once when one is
  due, or after 15 seconds without one, and the agent asks again. A
  check starts within moments of being due, and its result comes back as
  soon as it ends.
- The agent also lists what runs on its platform and reports it every 30
  seconds, and soon after Docker reports a change. The monitor form
  suggests names from that list, and a change there checks the monitors
  of what changed at once, so a container that stops is down within
  seconds.
- Every request is signed with the agent's own key, made when it first
  starts. Eyrie keeps only the public half. The key is replaced every 30
  days; see [Agent protocol](../technical/agent-protocol.md).
- The agent is the same image as Eyrie: `ghcr.io/zjcochran/eyrie`, with
  the command `agent`. Use the same version as Eyrie.

![Settings → Agents: three agents online on Docker, Docker Swarm and Proxmox VE, and one waiting to enroll](../images/agents-dark.png)

## Adding an agent

1. In Eyrie, **Settings → Agents → Add an agent**, with a name such as
   `edge`.
2. Eyrie shows a one-time enrollment token (`eya_…`), valid for an hour,
   and under **Where it runs**, the file or commands for each platform with
   your Eyrie's address and the token filled in.
3. Run the agent with them, below. At its first start it enrolls with the
   token, and its status turns **online**.
4. Add monitors: **Add a monitor**, choose the type, then the agent under
   **Runs on**.

The token is used once. Once enrolled, the agent keeps its identity in its
folder (`/data/agent`), so keep that folder: in Docker, a volume on
`/data`. If it is lost, make a **New token** and start the agent with it.

## Monitors on an agent

A monitor's **Runs on** is **Eyrie** (the default) or one of the agents;
an admin can move a monitor between them at any time. On its page, a
monitor on an agent says `on agent edge`.

- A check on an agent has the monitor's timeout there, and Eyrie waits
  20 seconds more for the result.
- Retries, notifications, maintenance windows and **Check now** work as
  for any monitor: Eyrie decides when a check is due and what its result
  means; the agent only runs it.
- The monitor is down, with the reason, when its agent was removed from
  Eyrie, is not enrolled, has not reported for 90 seconds (`The agent edge
  has not reported for 1m35s`), or did not fetch the check or send its
  result in time (`The agent edge sent no result in time`).
- Types that run a program (system service, PM2 process, Tailscale ping)
  run it on the agent's host, where it must be installed; Eyrie's image
  has none of them.
- A **Docker container** monitor on an agent checks the agent's Docker
  engine: its default Docker host is the agent's `DOCKER_HOST`.
- A **System resources** monitor on an agent reads the CPU and memory use
  of the agent's host. In an LXC container, that is the container's own,
  not the Proxmox VE node's (see
  [System resources](monitors.md#system-resources)).

The settings of a monitor on an agent, its passwords and tokens included,
are sent to the agent with each check. Use `https://` for
`EYRIE_AGENT_URL`, so they cannot be read on the way.

## Docker

On the Docker host:

```yaml
services:
  eyrie-agent:
    container_name: eyrie-agent
    image: ghcr.io/zjcochran/eyrie:latest
    command: agent
    restart: unless-stopped
    environment:
      EYRIE_AGENT_URL: https://eyrie.example.com
      EYRIE_AGENT_TOKEN: eya_...
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - eyrie-agent:/data

volumes:
  eyrie-agent:
    name: eyrie-agent
```

It reports every container on the host, running or not, by name, grouped
by Compose project, and checks **Docker container** monitors on its
Docker engine.

The agent runs as root, as Eyrie does, so it can use the socket whatever
group owns it on the host. Whoever can use the Docker socket controls the
host. The agent only sends
`GET` requests to `/containers/json`, `/containers/{name}/json`,
`/services`, `/info` and `/events`.
To hold it to that, put a Docker socket proxy that allows only those in
front of the socket, and point the agent at it with
`DOCKER_HOST: tcp://<proxy>:2375`.

### Docker in an LXC container

An agent in an LXC container that runs Docker (in Proxmox VE, with the
container's **nesting** feature on) works as on any Docker host, and
reports that container's Docker containers. To follow the LXC containers
themselves, use [Proxmox VE](#proxmox-ve).

## Docker Swarm

On a manager node, as a stack:

```yaml
services:
  agent:
    image: ghcr.io/zjcochran/eyrie:latest
    command: agent
    environment:
      EYRIE_AGENT_PLATFORM: swarm
      EYRIE_AGENT_URL: https://eyrie.example.com
      EYRIE_AGENT_TOKEN: eya_...
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - agent:/data
    deploy:
      replicas: 1
      placement:
        constraints: [node.role == manager]

volumes:
  agent:
    name: eyrie-agent
```

```sh
docker stack deploy -c eyrie-agent.yaml eyrie
```

It reports every service of the swarm, grouped by stack, and checks
**Docker Swarm service** monitors: up while all the tasks the service
wants run (`2 of 3 tasks running` is down). It also reports the standalone containers of the node it runs on;
containers that are tasks of a service are left out, since their names
change with every update. It stops at start unless its node is a swarm
manager.

The volume is the node's own: if the agent moves to another manager, it
has no key there. Pin it to one manager (`node.hostname == …`), or use a
volume all managers share.

## Kubernetes

[`deploy/kubernetes`](../../deploy/kubernetes/README.md) has the manifest:
a ServiceAccount allowed only to list Deployments, StatefulSets and
DaemonSets, a 64 MiB volume for the key, and the agent's Deployment.

```sh
kubectl create namespace eyrie
kubectl -n eyrie create secret generic eyrie-agent \
  --from-literal=EYRIE_AGENT_URL=https://eyrie.example.com \
  --from-literal=EYRIE_AGENT_TOKEN=eya_...
kubectl apply -f deploy/kubernetes/eyrie-agent.yaml
```

It reports every Deployment, StatefulSet and DaemonSet, grouped by
namespace, and checks **Kubernetes workload** monitors (a namespace, a
kind and a name): up while every replica it wants is ready, or for a
DaemonSet every pod it schedules. `EYRIE_AGENT_NAMESPACES` limits it to some namespaces
(see the [README](../../deploy/kubernetes/README.md#only-some-namespaces)).

## Proxmox VE

The agent reads the Proxmox API, so it can run on any host that reaches
it. Make it a read-only API token, on a Proxmox node:

```sh
pveum user add eyrie@pve
pveum acl modify / --users eyrie@pve --roles PVEAuditor
pveum user token add eyrie@pve agent --privsep 0
```

The last command prints the token's secret. Then:

```yaml
services:
  eyrie-agent:
    container_name: eyrie-agent
    image: ghcr.io/zjcochran/eyrie:latest
    command: agent
    restart: unless-stopped
    environment:
      EYRIE_AGENT_PLATFORM: proxmox
      EYRIE_AGENT_URL: https://eyrie.example.com
      EYRIE_AGENT_TOKEN: eya_...
      PROXMOX_URL: https://pve.example.com:8006
      PROXMOX_TOKEN_ID: eyrie@pve!agent
      PROXMOX_TOKEN_SECRET: the token's secret
      PROXMOX_FINGERPRINT: the certificate's SHA-256 fingerprint
    volumes:
      - eyrie-agent:/data

volumes:
  eyrie-agent:
    name: eyrie-agent
```

Proxmox certificates are usually self-signed, so the agent checks the
server's certificate by its SHA-256 fingerprint (node → **System →
Certificates** in the Proxmox web UI) instead of a certificate authority.
Without `PROXMOX_FINGERPRINT`, the agent reports that it cannot list its
workloads, with the fingerprint the server presented, to check and set.

It reports every LXC container and VM of the cluster, templates left out,
grouped by node, and checks **Proxmox VE guest** monitors (a VM ID): up
while the guest runs.

## Settings

| Variable | Default | What it is |
| --- | --- | --- |
| `EYRIE_AGENT_URL` | (required) | Eyrie's address, as browsers reach it, such as `https://eyrie.example.com`. Over `http://`, the agent warns at start: requests are signed, but they and the monitors' settings, passwords included, can be read on the way |
| `EYRIE_AGENT_TOKEN` | | The one-time enrollment token. Ignored once enrolled, unless it changes: a different token enrolls the agent again, with a new key |
| `EYRIE_AGENT_PLATFORM` | `docker` | `docker`, `swarm`, `kubernetes` or `proxmox`: what it lists, and which workload monitors it can check |
| `EYRIE_AGENT_DIR` | `/data/agent` | The agent's key and state |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | The Docker engine, for `docker` and `swarm`, and the default of Docker container monitors on the agent: `unix:///path` or `tcp://host:port` |
| `EYRIE_AGENT_NAMESPACES` | all | For `kubernetes`: namespaces to list, separated by commas |
| `PROXMOX_URL`, `PROXMOX_TOKEN_ID`, `PROXMOX_TOKEN_SECRET`, `PROXMOX_FINGERPRINT` | | For `proxmox`: the API's address, the token (`user@realm!name` and its secret) and the certificate's SHA-256 fingerprint |
| `EYRIE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

The image's health check (`eyrie healthcheck`) passes while the agent
reported to Eyrie within the last 90 seconds.

## When something is wrong

The agent logs what it does (`docker logs eyrie-agent`). In Eyrie,
**Settings → Agents** shows each agent's status and its last report, and
the [audit log](web-ui.md#audit-log) shows enrollments, key rotations and
refused agents.

| What you see | Why |
| --- | --- |
| The agent stops with `enrollment failed: … the enrollment token is unknown, used or expired` | The token was used, is more than an hour old, or is mistyped: make a **New token** |
| The agent logs `Eyrie refuses this agent` | The agent was removed or given a new token in Eyrie, or its key is more than 90 days old: make a **New token** and start the agent with it. It tries again every 5 minutes |
| The agent logs `the request's time is … from Eyrie's` | The two clocks differ by more than 5 minutes |
| The agent is **offline** | It has not reported for 90 seconds: it stopped, or cannot reach Eyrie (see its log) |
| `The agent edge sent no result in time` | The check took longer than its timeout and 20 seconds, or the agent could not send the result (see its log) |
| `No service api`, `No deployment web in default`, `No LXC container or VM 101` | The agent's platform has no such workload, or the agent is set up for another platform (`EYRIE_AGENT_PLATFORM`) |
| An error under the agent's status, such as `cannot reach Docker: … no such file or directory` | The agent reports, but cannot list: here the Docker socket is not mounted |
| `this Docker engine is not a swarm manager` | `EYRIE_AGENT_PLATFORM=swarm` on a worker node |
| `Kubernetes … 403 Forbidden` | The ServiceAccount is not bound to the ClusterRole in that namespace |
