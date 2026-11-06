<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Markdown from '../components/Markdown.svelte';
  import Notice from '../components/Notice.svelte';
  import { type Channel, type UpdateChange, type UpdateSettings, message } from '../lib/api.ts';
  import { intervals } from '../lib/updates.ts';
  import { api } from '../lib/session.svelte.ts';

  let saved = $state<UpdateSettings | null>(null);
  let form = $state<UpdateChange>({
    enabled: false,
    interval_hours: 24,
    proxy: '',
    proxy_user: '',
    proxy_password: '',
    clear_proxy_password: false,
    notify: false,
    channels: [],
  });
  let channels = $state<Channel[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  function fill(s: UpdateSettings) {
    saved = s;
    form = {
      enabled: s.enabled,
      interval_hours: s.interval_hours,
      proxy: s.proxy,
      proxy_user: s.proxy_user,
      proxy_password: '',
      clear_proxy_password: false,
      notify: s.notify,
      channels: [...s.channels],
    };
  }

  onMount(() => {
    api.updateSettings().then(fill, (e) => (notice = { ok: false, text: message(e) }));
    api.channels().then(
      (cs) => (channels = cs),
      () => {},
    );
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    try {
      fill(await api.saveUpdateSettings({ ...form, interval_hours: Number(form.interval_hours) }));
      notice = { ok: true, text: 'Saved.' };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  async function checkNow() {
    busy = true;
    notice = null;
    try {
      const s = await api.checkUpdates();
      if (saved) saved = { ...saved, status: s.status, available: s.available };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function toggle(id: number, on: boolean) {
    form.channels = on ? [...form.channels, id] : form.channels.filter((c) => c !== id);
  }

  const choices = $derived(
    intervals.some((i) => i.hours === Number(form.interval_hours))
      ? intervals
      : [...intervals, { hours: Number(form.interval_hours), label: `Every ${form.interval_hours} hours` }],
  );
</script>

<Notice bind:notice />

{#if saved}
  <section class="card">
    <h2>Check for updates</h2>
    <p>
      Eyrie can ask GitHub for its latest release now and then. When there is a newer one, admins see its release notes
      when they open Eyrie, and chosen notification channels can be told. Nothing is sent to GitHub but the request
      itself. This is off until you turn it on.
    </p>
    <form onsubmit={save}>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Check for updates</label>
      <label
        >How often
        <select bind:value={form.interval_hours}>
          {#each choices as c (c.hours)}<option value={c.hours}>{c.label}</option>{/each}
        </select></label
      >
      <fieldset>
        <legend>Proxy</legend>
        <p class="muted">
          Leave the URL empty to use the <code>HTTPS_PROXY</code> and <code>NO_PROXY</code> environment variables, if Eyrie
          has them, or to reach GitHub directly.
        </p>
        <label>URL <input type="url" bind:value={form.proxy} placeholder="http://proxy.example.com:3128" /></label>
        <label>Username <input bind:value={form.proxy_user} autocomplete="off" /></label>
        <label
          >Password
          <input
            type="password"
            bind:value={form.proxy_password}
            autocomplete="new-password"
            placeholder={saved.has_proxy_password ? 'Saved; leave blank to keep it' : ''}
          /></label
        >
        {#if saved.has_proxy_password}<label class="check"
            ><input type="checkbox" bind:checked={form.clear_proxy_password} /> Remove the saved password</label
          >{/if}
      </fieldset>
      <fieldset>
        <legend>Notifications</legend>
        <label class="check"
          ><input type="checkbox" bind:checked={form.notify} /> Tell these channels about a newer release, once each</label
        >
        {#if form.notify}
          {#each channels as c (c.id)}
            <label class="check"
              ><input type="checkbox" checked={form.channels.includes(c.id)} onchange={(e) => toggle(c.id, e.currentTarget.checked)} />
              {c.name}{c.enabled ? '' : ' (off)'}</label
            >
          {:else}
            <p class="muted">There are no channels yet: add one in Notifications.</p>
          {/each}
        {/if}
      </fieldset>
      <div><button class="primary" disabled={busy}>Save</button></div>
    </form>
  </section>

  <section class="card">
    <h2>Latest release</h2>
    <p>
      This is Eyrie {saved.current}.
      {#if !saved.repo}
        Its source (<code>EYRIE_SOURCE_URL</code>) is not on GitHub, so there are no releases to check.
      {:else if saved.status.checked}
        Last checked {new Date(saved.status.checked).toLocaleString()}.
      {:else}
        Not checked yet.
      {/if}
    </p>
    {#if saved.status.error}<p class="notice error">The last check failed: {saved.status.error}</p>{/if}
    {#if saved.available}
      <h3>
        <a href={saved.available.url} target="_blank" rel="noopener noreferrer">{saved.available.name || saved.available.version}</a> is
        available
      </h3>
      <Markdown text={saved.available.notes} />
    {:else if saved.status.latest}
      <p class="muted">{saved.status.latest.version} is the latest release: this one is up to date.</p>
    {/if}
    <div><button disabled={busy || !saved.repo} onclick={checkNow}>Check now</button></div>
  </section>
{/if}

<style>
  fieldset {
    display: grid;
    gap: 0.6rem;
  }
  fieldset p {
    margin: 0;
  }
  h3 {
    margin: 0.5rem 0;
  }
</style>
