<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { message } from '../lib/api.ts';
  import { api, app } from '../lib/session.svelte.ts';

  let username = $state('');
  let password = $state('');
  let confirm = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (password !== confirm) {
      error = 'The passwords do not match.';
      return;
    }
    busy = true;
    try {
      app.session = await api.setup(username, password);
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }
</script>

<main>
  <h1>Set up Eyrie</h1>
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
</main>

<style>
  main {
    max-width: 28rem;
    margin: 4rem auto;
    padding: 0 1rem;
  }
</style>
