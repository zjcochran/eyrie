<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type Channel, type ChannelChange, message } from '../lib/api.ts';
  import { fieldsFor } from '../lib/channels.ts';
  import { ago } from '../lib/format.ts';
  import { api } from '../lib/session.svelte.ts';

  let channels = $state<Channel[]>([]);
  let kinds = $state<{ id: string; name: string }[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);
  let test = $state<{ ok: boolean; message: string } | null>(null);
  let days = $state('21, 14, 7');

  // The channel being edited: null for none; id 0 for a new one.
  let editing = $state<Channel | null>(null);
  let form = $state<ChannelChange>(blank());

  function blank(): ChannelChange {
    return { name: '', kind: 'ntfy', url: '', token: '', user: '', to: '', from: '', enabled: true, default: false };
  }

  async function load() {
    channels = await api.channels();
  }

  onMount(async () => {
    try {
      kinds = await api.channelKinds();
      await load();
      days = (await api.notifySettings()).cert_expiry_days.join(', ');
    } catch (e) {
      notice = { ok: false, text: message(e) };
    }
  });

  function edit(c: Channel | null) {
    test = null;
    editing = c ?? ({ id: 0 } as Channel);
    form = c
      ? { ...blank(), name: c.name, kind: c.kind, to: c.to, from: c.from, enabled: c.enabled, default: c.default }
      : blank();
  }

  const kindName = (id: string) => kinds.find((k) => k.id === id)?.name ?? id;
  const kindFields = $derived(fieldsFor(form.kind));

  async function run(f: () => Promise<string>) {
    busy = true;
    notice = null;
    try {
      notice = { ok: true, text: await f() };
      await load();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function save(event: SubmitEvent) {
    event.preventDefault();
    const id = editing?.id ?? 0;
    run(async () => {
      const c = id ? await api.updateChannel(id, form) : await api.createChannel(form);
      editing = null;
      return `${c.name} saved.`;
    });
  }

  async function sendTest() {
    busy = true;
    test = null;
    try {
      test = await api.testChannel({ ...form, id: editing?.id || undefined });
    } catch (e) {
      test = { ok: false, message: message(e) };
    } finally {
      busy = false;
    }
  }

  function remove(c: Channel) {
    if (confirm(`Delete ${c.name}? Its monitors stop sending to it.`)) {
      run(async () => (await api.deleteChannel(c.id), `${c.name} deleted.`));
    }
  }

  function saveDays(event: SubmitEvent) {
    event.preventDefault();
    const list = days
      .split(',')
      .map((d) => Number(d.trim()))
      .filter((d) => d > 0);
    run(async () => {
      days = (await api.saveNotifySettings(list)).cert_expiry_days.join(', ');
      return 'Certificate warnings saved.';
    });
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

<section class="card">
  <div class="row between">
    <h2>Channels</h2>
    <button class="primary" onclick={() => edit(null)} disabled={busy}>Add a channel</button>
  </div>
  <p class="muted">
    Monitors notify the channels ticked in their settings when they go down, come back up, or stay down (if set). New
    monitors get the default channels.
  </p>
  {#if channels.length === 0}
    <p class="muted">No channels yet.</p>
  {:else}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Name</th><th>Kind</th><th>Monitors</th><th>Last sent</th><th></th></tr></thead>
        <tbody>
          {#each channels as c (c.id)}
            <tr>
              <td>
                {c.name}
                {#if !c.enabled}<span class="muted">(off)</span>{/if}
                {#if c.default}<span class="muted">(default)</span>{/if}
                {#if c.url_host}<div class="muted small">{c.url_host}</div>{/if}
              </td>
              <td>{kindName(c.kind)}</td>
              <td>{c.monitors}</td>
              <td>
                {#if !c.last}<span class="muted">not since Eyrie started</span>
                {:else if c.last.error}<span class="bad" title={c.last.error}>failed {ago(c.last.at)}</span>
                {:else}{ago(c.last.at)}{/if}
              </td>
              <td>
                <div class="row">
                  <button disabled={busy} onclick={() => edit(c)}>Edit</button>
                  <button class="danger" disabled={busy} onclick={() => remove(c)}>Delete</button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if editing}
  {@const saved = editing.id ? editing : null}
  <section class="card">
    <h2>{saved ? `Edit ${saved.name}` : 'Add a channel'}</h2>
    <form onsubmit={save}>
      <label>Name <input bind:value={form.name} maxlength="100" required placeholder="Phone" /></label>
      <label
        >Kind
        <select bind:value={form.kind} disabled={!!saved}>
          {#each kinds as k (k.id)}<option value={k.id}>{k.name}</option>{/each}
        </select>
      </label>
      {#each kindFields as [key, f] (key)}
        {#if key === 'to'}
          <label>{f.label} <input bind:value={form.to} placeholder={f.placeholder} required={f.required} /></label>
        {:else if key === 'from'}
          <label>{f.label} <input bind:value={form.from} placeholder={f.placeholder} required={f.required} /></label>
        {:else}
          {@const has = saved ? (key === 'url' ? saved.has_url : key === 'token' ? saved.has_token : saved.has_user) : false}
          <label
            >{f.label}
            <input
              type="password"
              autocomplete="off"
              bind:value={form[key]}
              placeholder={has ? 'Saved; leave blank to keep it' : (f.placeholder ?? '')}
              required={f.required && !has}
            />
          </label>
          {#if has && !f.required}
            <label class="check">
              {#if key === 'url'}<input type="checkbox" bind:checked={form.clear_url} />
              {:else if key === 'token'}<input type="checkbox" bind:checked={form.clear_token} />
              {:else}<input type="checkbox" bind:checked={form.clear_user} />{/if}
              Remove the saved value
            </label>
          {/if}
        {/if}
      {/each}
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Send to this channel</label>
      <label class="check"><input type="checkbox" bind:checked={form.default} /> Give it to new monitors</label>
      <label class="check"
        ><input type="checkbox" bind:checked={form.apply_to_all} /> Add it to every existing monitor</label
      >
      <div class="row">
        <button class="primary" disabled={busy}>Save</button>
        <button type="button" disabled={busy} onclick={sendTest}>Send test</button>
        <button type="button" onclick={() => (editing = null)}>Cancel</button>
        {#if test}<span class:good={test.ok} class:bad={!test.ok}>{test.ok ? '✓' : '✗'} {test.message}</span>{/if}
      </div>
    </form>
  </section>
{/if}

<section class="card">
  <h2>Certificate warnings</h2>
  <p class="muted">
    Monitors with “Warn before the certificate expires” notify once at each of these days before the server's TLS
    certificate expires, and again for a new certificate.
  </p>
  <form onsubmit={saveDays}>
    <label>Days before expiry <input bind:value={days} placeholder="21, 14, 7" /></label>
    <div><button class="primary" disabled={busy}>Save</button></div>
  </form>
</section>

<style>
  .between {
    justify-content: space-between;
  }
  .small {
    font-size: 0.8rem;
  }
  .good {
    color: var(--ok);
  }
  .bad {
    color: var(--error);
  }
</style>
