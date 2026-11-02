<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import { type Agent, type Discovered, type Found, message } from '../lib/api.ts';
  import { agentsFor, localName, setDraft, sources } from '../lib/discover.ts';
  import { go } from '../lib/nav.svelte.ts';
  import { api } from '../lib/session.svelte.ts';
  import { fields, shown, toSettings, toValues } from '../lib/settings.ts';

  let agents = $state<Agent[]>([]);
  let type = $state('docker');
  // Where discovery runs: 0 for Eyrie.
  let agentID = $state(0);
  // The SNMP device and its credentials: the snmp monitor's fields, without what a check compares.
  const snmpFields = fields.snmp.filter((f) => !['oid', 'operator', 'expected'].includes(f.key));
  let values = $state<Record<string, any>>(toValues('snmp', {}));
  let result = $state<Discovered | null>(null);
  let error = $state('');
  let busy = $state(false);

  onMount(async () => {
    try {
      agents = await api.agents();
    } catch (e) {
      error = message(e);
    }
  });

  const source = $derived(sources.find((s) => s.type === type) ?? sources[0]);
  const choices = $derived(agentsFor(type, agents));
  // Only Eyrie or the agents that can: the first agent for the types only agents find.
  $effect(() => {
    if (agentID ? !choices.some((a) => a.id === agentID) : !source.local) agentID = source.local ? 0 : (choices[0]?.id ?? 0);
  });
  const grouped = $derived(result?.found.some((f) => f.group) ?? false);

  // The SNMP settings typed here, which every monitor found starts with.
  const base = () => (type === 'snmp' ? toSettings('snmp', values) : {});

  async function discover(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    result = null;
    try {
      result = await api.discover(type, agentID, type === 'snmp' ? base() : undefined);
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  function add(f: Found) {
    setDraft({ type, name: f.name, agentID, settings: { ...base(), ...f.settings } });
    go('/monitors/new');
  }
</script>

<h1>Discover</h1>
<p class="muted">
  Find what a Docker engine, an agent's platform or a network device has, then add a monitor of what you choose. Each one
  opens in the monitor form to check and save; nothing is added until then.
</p>
{#if error}<p class="notice error" role="alert">{error}</p>{/if}

<form class="card" onsubmit={discover}>
  <label
    >Find
    <select bind:value={type} onchange={() => (result = null)}>
      {#each sources as s (s.type)}<option value={s.type}>{s.label}</option>{/each}
    </select>
  </label>
  <label
    >Runs on
    <select bind:value={agentID} onchange={() => (result = null)} disabled={!source.local && !choices.length}>
      {#if source.local}<option value={0}>{localName}</option>{/if}
      {#each choices as a (a.id)}
        <option value={a.id}>Agent {a.name}{a.status === 'online' ? '' : ` (${a.status})`}</option>
      {/each}
    </select>
  </label>
  <span class="muted help">
    {#if !source.local && !choices.length}
      No agent lists {source.label} yet: add one in Settings → Agents.
    {:else if type === 'snmp'}
      The device is asked from here: Eyrie's network, or the agent's.
    {:else if agentID}
      What the agent last reported, every 30 seconds.
    {:else}
      The containers of Eyrie's own Docker engine: its socket must be mounted in Eyrie's container.
    {/if}
  </span>

  {#if type === 'snmp'}
    {#each snmpFields as f (f.key)}
      {#if shown(f, values)}
        {#if f.kind === 'select'}
          <label
            >{f.label}
            <select bind:value={values[f.key]}>
              {#each f.options as o (o)}<option value={o}>{o}</option>{/each}
            </select>
          </label>
        {:else if f.kind === 'secret'}
          <label>{f.label} <input type="password" bind:value={values[f.key]} placeholder={f.placeholder} autocomplete="off" /></label>
        {:else if f.kind === 'number'}
          <label
            >{f.label} <input type="number" bind:value={values[f.key]} min={f.min} max={f.max} placeholder={f.placeholder} /></label
          >
        {:else if f.kind === 'text'}
          <label>{f.label} <input bind:value={values[f.key]} placeholder={f.placeholder} required={f.key === 'host'} /></label>
        {/if}
        {#if f.help}<span class="muted help">{f.help}</span>{/if}
      {/if}
    {/each}
  {/if}

  <div class="row">
    <button class="primary" disabled={busy || (!source.local && !choices.length)}>{busy ? 'Discovering…' : 'Discover'}</button>
  </div>
</form>

{#if result}
  <section class="card">
    <h2>{result.device ?? `${source.label}: ${result.found.length}`}</h2>
    {#if result.monitored}
      <p class="muted">Not listed: {result.monitored} monitored already.</p>
    {/if}
    {#if result.found.length}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Name</th><th>Now</th>{#if grouped}<th>Group</th>{/if}<th></th></tr></thead>
          <tbody>
            {#each result.found as f, i (i)}
              <tr>
                <td>{f.name}</td>
                <td><StatusDot status={f.up ? 'up' : 'down'} /> {f.detail}</td>
                {#if grouped}<td>{f.group ?? ''}</td>{/if}
                <td class="actions"><button onclick={() => add(f)}>Add monitor</button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <p class="muted">{result.monitored ? 'Nothing new found.' : 'Nothing found.'}</p>
    {/if}
  </section>
{/if}

<style>
  form.card {
    max-width: 36rem;
  }
  .help {
    margin-top: -0.5rem;
    font-size: 0.85rem;
  }
  .actions {
    text-align: right;
    white-space: nowrap;
  }
</style>
