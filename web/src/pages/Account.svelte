<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Avatar from '../components/Avatar.svelte';
  import { type Account, type TwoFactorSetup, message } from '../lib/api.ts';
  import { go, nav } from '../lib/nav.svelte.ts';
  import { queryValue } from '../lib/route.ts';
  import { api, app, refresh } from '../lib/session.svelte.ts';

  let account = $state<Account | null>(null);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  // Password change.
  let current = $state('');
  let next = $state('');
  let repeat = $state('');

  // Two-factor sign-in.
  let setup = $state<TwoFactorSetup | null>(null);
  let code = $state('');
  let confirmPassword = $state('');
  let codes = $state<string[]>([]);

  async function load() {
    account = await api.account();
  }

  onMount(() => {
    // Linking single sign-on comes back here with a message in the query.
    const ok = queryValue(nav.search, 'message');
    const error = queryValue(nav.search, 'error');
    if (ok || error) {
      notice = { ok: !!ok, text: ok || error };
      go(nav.path, true);
    }
    load().catch((e) => (notice = { ok: false, text: message(e) }));
  });

  async function run(f: () => Promise<string | void>) {
    busy = true;
    notice = null;
    try {
      const text = await f();
      if (text) {
        notice = { ok: true, text };
      }
      await load();
      await refresh();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function changePassword(event: SubmitEvent) {
    event.preventDefault();
    if (next !== repeat) {
      notice = { ok: false, text: 'The new passwords do not match.' };
      return;
    }
    run(async () => {
      app.session = await api.changePassword(current, next);
      current = next = repeat = '';
      return 'Password changed. Your other sessions were signed out.';
    });
  }

  function upload(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (file) {
      run(async () => {
        await api.uploadPicture(file);
        return 'Picture saved.';
      });
    }
  }

  function startTwoFactor() {
    run(async () => {
      setup = await api.twoFactorSetup();
      code = confirmPassword = '';
    });
  }

  function enableTwoFactor(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      codes = (await api.twoFactorEnable(setup!.sealed, code, confirmPassword)).codes;
      setup = null;
      confirmPassword = '';
      return 'Two-factor sign-in is on.';
    });
  }

  function newCodes(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      codes = (await api.newRecoveryCodes(confirmPassword)).codes;
      confirmPassword = '';
      return 'New recovery codes made. The old ones no longer work.';
    });
  }

  function disableTwoFactor() {
    run(async () => {
      await api.twoFactorDisable(confirmPassword);
      confirmPassword = '';
      codes = [];
      return 'Two-factor sign-in is off.';
    });
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if account}
  {@const u = account.user}
  <section class="card">
    <h2>Profile picture</h2>
    <div class="row">
      <Avatar user={u} size={64} />
      <label class="button">
        Upload a picture
        <input type="file" accept="image/png,image/jpeg,image/gif,image/webp" onchange={upload} hidden />
      </label>
      {#if u.picture}<button onclick={() => run(async () => (await api.removePicture(), 'Picture removed.'))} disabled={busy}
          >Remove picture</button
        >{/if}
      {#if u.sso && !u.sso_picture}
        <button onclick={() => run(async () => (await api.useSSOPicture(), 'Your picture will come from single sign-on from your next sign-in.'))} disabled={busy}
          >Use the {account.sso.name} picture again</button
        >
      {/if}
    </div>
    <p class="muted">PNG, JPEG, GIF or WebP, up to 1 MB.</p>
  </section>

  {#if u.password}
    <section class="card">
      <h2>Password</h2>
      <form onsubmit={changePassword}>
        <label>Current password <input type="password" bind:value={current} autocomplete="current-password" required /></label>
        <label
          >New password <input type="password" bind:value={next} autocomplete="new-password" minlength="12" required /></label
        >
        <label
          >Repeat the new password
          <input type="password" bind:value={repeat} autocomplete="new-password" minlength="12" required /></label
        >
        <div><button class="primary" disabled={busy}>Change password</button></div>
      </form>
    </section>
  {/if}

  {#if account.two_factor.available || account.two_factor.on}
    <section class="card">
      <h2>Two-factor sign-in</h2>
      {#if codes.length}
        <p>
          Save these recovery codes somewhere other than your phone. Each works once instead of a code from the app. They
          are not shown again.
        </p>
        <ul class="codes mono">
          {#each codes as c (c)}<li>{c}</li>{/each}
        </ul>
        <p><button onclick={() => (codes = [])}>I saved them</button></p>
      {:else if account.two_factor.on}
        <p>On. {account.two_factor.codes_left} recovery codes left.</p>
        <form onsubmit={newCodes}>
          <label
            >Your current password
            <input type="password" bind:value={confirmPassword} autocomplete="current-password" required /></label
          >
          <div class="row">
            <button disabled={busy}>New recovery codes</button>
            <button type="button" class="danger" disabled={busy || !confirmPassword} onclick={disableTwoFactor}
              >Turn off</button
            >
          </div>
        </form>
      {:else if setup}
        <p>Scan the code with your authenticator app, or type in the key.</p>
        <img class="qr" src={setup.qr} alt="QR code for your authenticator app" />
        <p class="mono">{setup.secret}</p>
        <form onsubmit={enableTwoFactor}>
          <label
            >Code from the app <input bind:value={code} autocomplete="one-time-code" inputmode="numeric" required /></label
          >
          <label
            >Your current password
            <input type="password" bind:value={confirmPassword} autocomplete="current-password" required /></label
          >
          <div class="row">
            <button class="primary" disabled={busy}>Turn on</button>
            <button type="button" onclick={() => (setup = null)}>Cancel</button>
          </div>
        </form>
      {:else}
        <p>Ask for a code from an authenticator app after your password.</p>
        <button onclick={startTwoFactor} disabled={busy}>Set up</button>
      {/if}
    </section>
  {/if}

  {#if account.sso.enabled}
    <section class="card">
      <h2>Single sign-on</h2>
      {#if account.identities.length}
        {#each account.identities as id (id.issuer + id.email)}
          <p>Linked to {account.sso.name}{id.email ? ` as ${id.email}` : ''}.</p>
        {/each}
        {#if u.password}
          <button onclick={() => run(async () => (await api.unlinkSSO(), 'Single sign-on unlinked.'))} disabled={busy}
            >Unlink</button
          >
        {/if}
      {:else}
        <p>Link your {account.sso.name} account to sign in with it. Linking turns off two-factor sign-in here: use {account
            .sso.name}'s.</p>
        <a class="button" href="/auth/oidc/login?link=1">Link my {account.sso.name} account</a>
      {/if}
    </section>
  {/if}
{/if}

<style>
  .qr {
    image-rendering: pixelated;
    max-width: 100%;
  }
  .codes {
    columns: 2;
    padding-left: 1.2rem;
  }
</style>
