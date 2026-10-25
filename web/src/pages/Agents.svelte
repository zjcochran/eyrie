<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type Agent, message } from '../lib/api.ts';
  import { type Platform, agentSetup, platforms } from '../lib/agents.ts';
  import { api } from '../lib/session.svelte.ts';

  let agents = $state<Agent[]>([]);
  let name = $state('');
  // The enrollment token just made, shown once with how to run the agent.
  let enrolling = $state<{ agent: Agent; token: string } | null>(null);
  let platform = $state<Platform>('docker');
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  async function load() {
    agents = await api.agents();
  }

  onMount(() => {
    load().catch((e) => (notice = { ok: false, text: message(e) }));
    // Statuses change as agents report.
    const t = setInterval(() => load().catch(() => {}), 15000);
    return () => clearInterval(t);
  });

  async function run(f: () => Promise<void>) {
    busy = true;
    notice = null;
    try {
      await f();
      await load();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function add(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      enrolling = await api.addAgent(name);
      name = '';
    });
  }

  function renew(a: Agent) {
    if (confirm(`Make a new enrollment token for ${a.name}? Its agent is refused until it enrolls again with the new token.`)) {
      run(async () => {
        enrolling = await api.renewAgentToken(a.id);
      });
    }
  }

  function remove(a: Agent) {
    if (confirm(`Remove ${a.name}? Its agent is refused, and the monitors of its workloads go down.`)) {
      run(async () => {
        await api.removeAgent(a.id);
        if (enrolling?.agent.id === a.id) enrolling = null;
        notice = { ok: true, text: `${a.name} removed.` };
      });
    }
  }

  const when = (t: string | null) => (t ? new Date(t).toLocaleString() : 'never');
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if enrolling}
  <section class="card">
    <h2>Run the agent for {enrolling.agent.name}</h2>
    <p>
      The token below enrolls it once, within an hour{enrolling.agent.token_expires
        ? ` (until ${new Date(enrolling.agent.token_expires).toLocaleTimeString()})`
        : ''}. Copy it now: it is not shown again.
    </p>
    <p class="mono">{enrolling.token}</p>
    <label
      >Where it runs
      <select bind:value={platform}>
        {#each platforms as p (p.id)}<option value={p.id}>{p.label}</option>{/each}
      </select>
    </label>
    <pre class="mono setup">{agentSetup(platform, location.origin, enrolling.token)}</pre>
    {#if platform === 'docker' || platform === 'swarm'}
      <p class="muted">
        Whoever can use the Docker socket controls the host. The agent only reads through it, and opens no port.
      </p>
    {/if}
    <button onclick={() => (enrolling = null)}>Done</button>
  </section>
{/if}

<section class="card">
  <p>
    Agents run where Eyrie cannot see: next to a Docker engine, on a swarm manager, in a Kubernetes cluster or beside a
    Proxmox VE cluster. They report what runs there every 30 seconds, and Agent workload monitors follow it.
  </p>
  {#if agents.length}
    <div class="table-wrap">
      <table>
        <thead
          ><tr><th>Name</th><th>Status</th><th>Host</th><th>Last report</th><th>Workloads</th><th>Key</th><th></th></tr></thead
        >
        <tbody>
          {#each agents as a (a.id)}
            <tr>
              <td>{a.name}</td>
              <td>
                <span class="status {a.status}">{a.status}</span>
                {#if a.error}<div class="error-text">{a.error}</div>{/if}
              </td>
              <td>
                {#if a.platform}{a.platform} · {a.hostname}<div class="muted">{a.last_address} · {a.version}</div>{:else}<span
                    class="muted">Not enrolled</span
                  >{/if}
              </td>
              <td>{when(a.last_seen)}</td>
              <td>{a.workloads}</td>
              <td class="mono">{a.key || '—'}</td>
              <td class="actions">
                <button disabled={busy} onclick={() => renew(a)}>New token</button>
                <button class="danger" disabled={busy} onclick={() => remove(a)}>Remove</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="muted">No agents yet.</p>
  {/if}
</section>

<section class="card">
  <h2>Add an agent</h2>
  <form onsubmit={add}>
    <label>Name <input bind:value={name} maxlength="64" placeholder="edge" required /></label>
    <div><button class="primary" disabled={busy}>Add</button></div>
  </form>
</section>

<style>
  .setup {
    padding: 0.75rem;
    overflow-x: auto;
    white-space: pre;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    font-size: 0.85rem;
  }
  .status {
    font-weight: 600;
    text-transform: capitalize;
  }
  .status.online {
    color: var(--up);
  }
  .status.offline {
    color: var(--down);
  }
  .status.pending {
    color: var(--pending);
  }
  .error-text {
    color: var(--error);
    font-size: 0.85rem;
  }
  .actions {
    white-space: nowrap;
  }
</style>
