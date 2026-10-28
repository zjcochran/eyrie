# Agents

An agent is Eyrie run as `eyrie agent` where Eyrie cannot see: next to a
Docker engine on another host, on a Docker Swarm manager, in a Kubernetes
cluster, or beside a Proxmox VE cluster. It lists what runs there and
reports it to Eyrie every 30 seconds, and soon after Docker reports a
change. [Agent workload](monitors.md#agent-workload) monitors follow those
reports.

- The agent only reads. Eyrie never sends it anything to do.
- The agent calls Eyrie; Eyrie never calls the agent, and the agent opens
  no port. It needs to reach Eyrie's address, as a browser would.
- Every report is signed with the agent's own key, made when it first
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
4. Add monitors: **Add a monitor**, type **Agent workload**, then the
   agent and a workload.

The token is used once. Once enrolled, the agent keeps its identity in its
folder (`/data/agent`), so keep that folder: in Docker, a volume on
`/data`. If it is lost, make a **New token** and start the agent with it.

## Docker

On the Docker host:

```yaml
services:
  eyrie-agent:
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
by Compose project.

The agent runs as root, as Eyrie does, so it can use the socket whatever
group owns it on the host. Whoever can use the Docker socket controls the
host. The agent only sends
`GET` requests to `/containers/json`, `/services`, `/info` and `/events`.
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

It reports every service of the swarm as `service/<name>`, grouped by
stack, up while all the tasks it wants run (`2 of 3 tasks running` is
down). It also reports the standalone containers of the node it runs on;
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

It reports every Deployment, StatefulSet and DaemonSet as
`<namespace>/<kind>/<name>`, grouped by namespace, up while every replica
it wants is ready. `EYRIE_AGENT_NAMESPACES` limits it to some namespaces
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
as `lxc/<vmid>` or `qemu/<vmid>`, grouped by node, up while running.

## Settings

| Variable | Default | What it is |
| --- | --- | --- |
| `EYRIE_AGENT_URL` | (required) | Eyrie's address, as browsers reach it, such as `https://eyrie.example.com`. Over `http://`, the agent warns at start: reports are signed but can be read on the way |
| `EYRIE_AGENT_TOKEN` | | The one-time enrollment token. Ignored once enrolled, unless it changes: a different token enrolls the agent again, with a new key |
| `EYRIE_AGENT_PLATFORM` | `docker` | `docker`, `swarm`, `kubernetes` or `proxmox` |
| `EYRIE_AGENT_DIR` | `/data/agent` | The agent's key and state |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | The Docker engine, for `docker` and `swarm`: `unix:///path` or `tcp://host:port` |
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
| An error under the agent's status, such as `cannot reach Docker: … no such file or directory` | The agent reports, but cannot list: here the Docker socket is not mounted |
| `this Docker engine is not a swarm manager` | `EYRIE_AGENT_PLATFORM=swarm` on a worker node |
| `Kubernetes … 403 Forbidden` | The ServiceAccount is not bound to the ClusterRole in that namespace |
