<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { message } from '../lib/api.ts';
  import { go } from '../lib/nav.svelte.ts';
  import { api, app, refresh } from '../lib/session.svelte.ts';
  import { intervals } from '../lib/updates.ts';
  import Appearance from './Appearance.svelte';

  // The first admin's account, then how Eyrie looks for them, then whether
  // it checks for updates.
  let step = $state<'account' | 'look' | 'updates'>('account');
  let checkUpdates = $state(false);
  let interval = $state(24);

  let username = $state('');
  let password = $state('');
  let confirm = $state('');
  let error = $state('');
  let busy = $state(false);
  // Restoring a backup instead.
  let file = $state<FileList | null>(null);
  let passphrase = $state('');
  let restored = $state('');

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (password !== confirm) {
      error = 'The passwords do not match.';
      return;
    }
    busy = true;
    try {
      const session = await api.setup(username, password);
      app.welcome = true;
      app.session = session;
      step = 'look';
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  async function finish() {
    busy = true;
    error = '';
    try {
      if (checkUpdates) {
        const u = await api.updateSettings();
        await api.saveUpdateSettings({
          enabled: true,
          interval_hours: Number(interval),
          proxy: u.proxy,
          proxy_user: u.proxy_user,
          proxy_password: '',
          clear_proxy_password: false,
          notify: u.notify,
          channels: u.channels,
        });
      }
      app.welcome = false;
      go('/', true);
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  async function restore(event: SubmitEvent) {
    event.preventDefault();
    const f = file?.[0];
    if (!f) return;
    busy = true;
    error = '';
    try {
      const r = await api.setupRestore(f, passphrase);
      restored = `Restored the backup made ${new Date(r.restored.created).toLocaleString()}.`;
      await refresh(); // setup is done: the sign-in page follows
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }
</script>

{#if step === 'look'}
  <main class="wide">
    <h1>Set up Eyrie</h1>
    <p class="muted">Step 2 of 3 · How Eyrie looks for you. Each choice shows at once; change it any time in Settings → Appearance.</p>
    <Appearance />
    <div class="next"><button class="primary" onclick={() => (step = 'updates')}>Next</button></div>
  </main>
{:else if step === 'updates'}
  <main class="wide">
    <h1>Set up Eyrie</h1>
    <p class="muted">Step 3 of 3 · Updates</p>
    {#if error}<p class="notice error" role="alert">{error}</p>{/if}
    <section class="card">
      <h2>Check for updates?</h2>
      <p>
        Eyrie can ask GitHub for its latest release now and then. When there is a newer one, admins see its release
        notes when they open Eyrie. Nothing is sent to GitHub but the request itself.
      </p>
      <div class="form">
        <label class="check"><input type="checkbox" bind:checked={checkUpdates} /> Check for updates</label>
        {#if checkUpdates}
          <label
            >How often
            <select bind:value={interval}>
              {#each intervals as c (c.hours)}<option value={c.hours}>{c.label}</option>{/each}
            </select></label
          >
        {/if}
      </div>
      <p class="muted">
        In Settings → Updates you can also send word of a new release to a notification channel, once you have added
        one, and set a proxy if Eyrie reaches the internet through one.
      </p>
    </section>
    <div class="next">
      <button onclick={() => (step = 'look')} disabled={busy}>Back</button>
      <button class="primary" onclick={finish} disabled={busy}>Finish</button>
    </div>
  </main>
{:else}
  <main>
    <h1>Set up Eyrie</h1>
    <p class="muted">Step 1 of 3 · Your account</p>
    <div class="card">
      <p>Create the first account. It is an admin: it can add other users and change every setting.</p>
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      <form onsubmit={submit}>
        <label>Username <input bind:value={username} autocomplete="username" required /></label>
        <label
          >Password <input type="password" bind:value={password} autocomplete="new-password" minlength="12" required /></label
        >
        <span class="muted">At least 12 characters.</span>
        <label
          >Repeat the password
          <input type="password" bind:value={confirm} autocomplete="new-password" minlength="12" required /></label
        >
        <div><button class="primary" disabled={busy}>Create the admin account</button></div>
      </form>
    </div>
    <div class="card">
      <h2>Or restore a backup</h2>
      <p>
        Bring back the monitors, settings and accounts of a backup file from Settings → Backup and restore, then sign in
        with an account from it.
      </p>
      {#if restored}<p class="notice ok" role="status">{restored}</p>{/if}
      <form onsubmit={restore}>
        <label>Backup file <input type="file" accept=".json,application/json" bind:files={file} required /></label>
        <label>Its passphrase <input type="password" bind:value={passphrase} autocomplete="off" required /></label>
        <div><button disabled={busy}>Restore backup</button></div>
      </form>
    </div>
  </main>
{/if}

<style>
  main {
    max-width: 28rem;
    margin: 4rem auto;
    padding: 0 1rem;
  }
  main.wide {
    max-width: 64rem;
    margin-top: 2rem;
  }
  .form {
    display: grid;
    gap: 0.75rem;
    max-width: 28rem;
    margin-bottom: 1rem;
  }
  .next {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 1rem;
  }
</style>
