# Eyrie's agent in Kubernetes

[`eyrie-agent.yaml`](eyrie-agent.yaml) runs Eyrie's agent in a cluster. It
reports the cluster's Deployments, StatefulSets and DaemonSets to Eyrie,
where Agent workload monitors follow them. See [Agents](../../docs/user/agents.md).

1. In Eyrie, **Settings → Agents → Add an agent**, and choose
   **Kubernetes**. Eyrie shows the commands with the agent's one-time
   enrollment token, valid for an hour:

   ```sh
   kubectl create namespace eyrie
   kubectl -n eyrie create secret generic eyrie-agent \
     --from-literal=EYRIE_AGENT_URL=https://eyrie.example.com \
     --from-literal=EYRIE_AGENT_TOKEN=eya_...
   kubectl apply -f eyrie-agent.yaml
   ```

2. The agent enrolls, and its status in **Settings → Agents** turns online
   within 30 seconds. `kubectl -n eyrie logs deploy/eyrie-agent` shows what
   it does.

What the manifest makes, in the namespace `eyrie`:

| Object | What for |
| --- | --- |
| ServiceAccount `eyrie-agent` | The agent's identity in the cluster |
| ClusterRole and ClusterRoleBinding `eyrie-agent` | `list` on Deployments, StatefulSets and DaemonSets, in every namespace. Nothing else: no pods, secrets or changes |
| PersistentVolumeClaim `eyrie-agent` (64 MiB) | The agent's key and state, which must survive the pod |
| Deployment `eyrie-agent` | One replica (`Recreate`), as user 65532, read-only root file system, no capabilities; the liveness probe runs `/eyrie healthcheck`, which fails when the agent has not reported for 90 seconds |

## Only some namespaces

1. Delete the ClusterRoleBinding from the manifest.
2. For each namespace, bind the ClusterRole there:

   ```sh
   kubectl -n shop create rolebinding eyrie-agent --clusterrole=eyrie-agent --serviceaccount=eyrie:eyrie-agent
   ```

3. Uncomment `EYRIE_AGENT_NAMESPACES` in the Deployment and list the
   namespaces, separated by commas.

## Enrolling again

A new token from **New token** in Settings → Agents: put it in the
Secret and restart the agent.

```sh
kubectl -n eyrie create secret generic eyrie-agent --dry-run=client -o yaml \
  --from-literal=EYRIE_AGENT_URL=https://eyrie.example.com \
  --from-literal=EYRIE_AGENT_TOKEN=eya_... | kubectl apply -f -
kubectl -n eyrie rollout restart deploy/eyrie-agent
```
