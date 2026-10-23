<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import TagChip from '../components/TagChip.svelte';
  import { type Channel, type Monitor, type MonitorChange, type Tag, message } from '../lib/api.ts';
  import { go, link } from '../lib/nav.svelte.ts';
  import { api } from '../lib/session.svelte.ts';
  import { fields, toSettings, toValues } from '../lib/settings.ts';

  /** The monitor to edit; 0 makes a new one. */
  let { id = 0 }: { id?: number } = $props();

  let types = $state<{ name: string; label: string }[]>([]);
  let groups = $state<Monitor[]>([]);
  let allChannels = $state<Channel[]>([]);
  // The channels ticked; for a new monitor, the default ones.
  let channels = $state<number[]>([]);
  let allTags = $state<Tag[]>([]);
  // The tags ticked, and each one's value.
  let tagIDs = $state<number[]>([]);
  let tagValues = $state<Record<number, string>>({});
  let existing = $state<Monitor | null>(null);
  let error = $state('');
  let busy = $state(false);

  let type = $state('http');
  let name = $state('');
  let description = $state('');
  let parent = $state(0);
  let active = $state(true);
  let interval = $state(60);
  let retryInterval = $state(60);
  let retries = $state(0);
  let resendEvery = $state(0);
  let timeout = $state(48);
  let upsideDown = $state(false);
  let resetToken = $state(false);
  // Each field's value: text, a number from a number input, or a box's state.
  let values = $state<Record<string, any>>({});

  onMount(async () => {
    try {
      types = await api.monitorTypes();
      const all = await api.monitors();
      groups = all.filter((m) => m.type === 'group' && m.id !== id);
      allChannels = await api.channels();
      allTags = await api.tags();
      channels = allChannels.filter((c) => c.default).map((c) => c.id);
      if (id) {
        const m = await api.monitor(id);
        existing = m;
        type = m.type;
        name = m.name;
        description = m.description;
        parent = m.parent_id ?? 0;
        active = m.active;
        interval = m.interval;
        retryInterval = m.retry_interval;
        retries = m.retries;
        resendEvery = m.resend_every;
        timeout = m.timeout;
        upsideDown = m.upside_down;
        values = toValues(m.type, m.settings);
        channels = [...m.channels];
        tagIDs = m.tags.map((t) => t.id);
        tagValues = Object.fromEntries(m.tags.map((t) => [t.id, t.value]));
      }
    } catch (e) {
      error = message(e);
    }
  });

  const typeFields = $derived(fields[type] ?? []);
  const checked = $derived(type !== 'push' && type !== 'manual');

  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    const change: MonitorChange = {
      name,
      type,
      parent_id: parent || null,
      active,
      interval,
      retry_interval: retryInterval,
      retries,
      resend_every: resendEvery,
      timeout,
      upside_down: upsideDown,
      description,
      settings: toSettings(type, values),
      reset_push_token: resetToken,
      channels,
      tags: tagIDs.map((t) => ({ id: t, value: tagValues[t] ?? '' })),
    };
    try {
      const m = id ? await api.updateMonitor(id, change) : await api.createMonitor(change);
      go(`/monitors/${m.id}`);
    } catch (e) {
      error = message(e);
      busy = false;
    }
  }
</script>

<p><a href={id ? `/monitors/${id}` : '/'} onclick={link}>← {id ? 'Back' : 'Monitors'}</a></p>
<h1>{id ? `Edit ${existing?.name ?? ''}` : 'Add a monitor'}</h1>
{#if error}<p class="notice error" role="alert">{error}</p>{/if}

<form class="card" onsubmit={save}>
  <label
    >Type
    <select bind:value={type} disabled={!!id}>
      {#each types as t (t.name)}<option value={t.name}>{t.label}</option>{/each}
    </select>
  </label>
  <label>Name <input bind:value={name} maxlength="100" required /></label>

  {#each typeFields as f (f.key)}
    {#if f.kind === 'check'}
      <label class="check"><input type="checkbox" bind:checked={values[f.key]} /> {f.label}</label>
    {:else if f.kind === 'select'}
      <label
        >{f.label}
        <select bind:value={values[f.key]}>
          {#each f.options as o (o)}<option value={o}>{o}</option>{/each}
        </select>
      </label>
    {:else if f.kind === 'textarea'}
      <label>{f.label} <textarea bind:value={values[f.key]} placeholder={f.placeholder} rows="3"></textarea></label>
    {:else if f.kind === 'number'}
      <label>{f.label} <input type="number" bind:value={values[f.key]} min={f.min} max={f.max} placeholder={f.placeholder} /></label>
    {:else if f.kind === 'url'}
      <label>{f.label} <input type="url" bind:value={values[f.key]} placeholder={f.placeholder} /></label>
    {:else}
      <label>{f.label} <input bind:value={values[f.key]} placeholder={f.placeholder} /></label>
    {/if}
    {#if f.help}<span class="muted help">{f.help}</span>{/if}
  {/each}

  {#if type === 'push'}
    <p class="muted">
      Eyrie makes a URL for this monitor to be called at least once per interval.
      {#if id}<label class="check"><input type="checkbox" bind:checked={resetToken} /> Make a new URL (the old one stops working)</label>{/if}
    </p>
  {:else if type === 'manual'}
    <p class="muted">Its status is set by hand, on the monitor's page.</p>
  {:else if type === 'group'}
    <p class="muted">A group is down when one of its active members is down. Put monitors in it with their Group setting.</p>
  {/if}

  <fieldset>
    <legend>Schedule</legend>
    <label
      >{type === 'push' ? 'Expect a push every (seconds)' : 'Check every (seconds)'}
      <input type="number" bind:value={interval} min="20" max="86400" required /></label
    >
    {#if checked}
      <label>Timeout (seconds) <input type="number" bind:value={timeout} min="0.1" step="0.1" required /></label>
    {/if}
    <label>Retries before down <input type="number" bind:value={retries} min="0" max="100" /></label>
    <label
      >Retry every (seconds) <input type="number" bind:value={retryInterval} min="20" max="86400" required /></label
    >
    <label
      >Notify again every (checks while down; 0 never)
      <input type="number" bind:value={resendEvery} min="0" max="10000" /></label
    >
  </fieldset>

  {#if allChannels.length}
    <fieldset>
      <legend>Notify</legend>
      {#each allChannels as c (c.id)}
        <label class="check">
          <input type="checkbox" bind:group={channels} value={c.id} />
          {c.name}{#if !c.enabled}<span class="muted"> (off)</span>{/if}
        </label>
      {/each}
    </fieldset>
  {:else}
    <p class="muted">No notification channels yet: add them in Settings → Notifications.</p>
  {/if}

  {#if allTags.length}
    <fieldset>
      <legend>Tags</legend>
      {#each allTags as t (t.id)}
        <div class="tag row">
          <label class="check"><input type="checkbox" bind:group={tagIDs} value={t.id} /> <TagChip name={t.name} color={t.color} /></label>
          {#if tagIDs.includes(t.id)}
            <input bind:value={tagValues[t.id]} maxlength="100" placeholder="Value (optional)" aria-label="Value of {t.name}" />
          {/if}
        </div>
      {/each}
    </fieldset>
  {:else}
    <p class="muted">No tags yet: add them in Settings → Tags.</p>
  {/if}

  <label
    >Group
    <select bind:value={parent}>
      <option value={0}>None</option>
      {#each groups as g (g.id)}<option value={g.id}>{g.name}</option>{/each}
    </select>
  </label>
  <label>Description <input bind:value={description} maxlength="2000" /></label>
  <label class="check"><input type="checkbox" bind:checked={upsideDown} /> Upside down: up when the check fails</label>
  <label class="check"><input type="checkbox" bind:checked={active} /> Active (unticked: paused)</label>
  <div class="row">
    <button class="primary" disabled={busy}>{id ? 'Save' : 'Add'}</button>
    <a class="button" href={id ? `/monitors/${id}` : '/'} onclick={link}>Cancel</a>
  </div>
</form>

<style>
  form.card {
    max-width: 36rem;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    display: grid;
    gap: 0.75rem;
    padding: 0.75rem;
  }
  textarea {
    font: inherit;
    padding: 0.45rem 0.6rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--fg);
  }
  .tag {
    gap: 0.75rem;
  }
  .tag input:not([type]) {
    flex: 1;
    min-width: 0;
  }
  .help {
    margin-top: -0.5rem;
    font-size: 0.85rem;
  }
</style>
