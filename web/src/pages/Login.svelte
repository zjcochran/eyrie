<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { message } from '../lib/api.ts';
  import { go, nav } from '../lib/nav.svelte.ts';
  import { queryValue, safeNext } from '../lib/route.ts';
  import { api, app } from '../lib/session.svelte.ts';

  const session = $derived(app.session!);
  // Where to go after signing in: the page asked for, unless it was this one.
  const next = $derived(
    nav.path === '/login' ? safeNext(queryValue(nav.search, 'next')) : safeNext(nav.path + nav.search),
  );
  const ssoURL = $derived('/auth/oidc/login?' + new URLSearchParams({ next }).toString());

  let username = $state('');
  let password = $state('');
  let code = $state('');
  let needCode = $state(false);
  let error = $state('');
  let busy = $state(false);

  // An error from a single sign-on attempt comes back in the query.
  $effect(() => {
    const e = queryValue(nav.search, 'error');
    if (e) {
      error = e;
    }
  });

  async function run(f: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await f();
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  function signIn(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      const s = await api.login(username, password);
      if (s) {
        finish(s);
      } else {
        needCode = true;
      }
    });
  }

  function sendCode(event: SubmitEvent) {
    event.preventDefault();
    run(async () => finish(await api.loginCode(code)));
  }

  function finish(s: typeof app.session) {
    app.session = s;
    go(next, true);
  }
</script>

<main>
  <h1>Sign in to Eyrie</h1>
  <div class="card">
    {#if error}<p class="notice error" role="alert">{error}</p>{/if}
    {#if needCode}
      <form onsubmit={sendCode}>
        <label
          >Code from your authenticator app
          <input bind:value={code} autocomplete="one-time-code" inputmode="numeric" required /></label
        >
        <span class="muted">Or one of your recovery codes.</span>
        <div class="row">
          <button class="primary" disabled={busy}>Sign in</button>
          <button type="button" onclick={() => ((needCode = false), (code = ''))}>Back</button>
        </div>
      </form>
    {:else}
      {#if session.sso.enabled}
        <p><a class="button primary" href={ssoURL}>Sign in with {session.sso.name}</a></p>
      {/if}
      {#if session.password_sign_in}
        {#if session.sso.enabled}<p class="muted">Or with your password:</p>{/if}
        <form onsubmit={signIn}>
          <label>Username <input bind:value={username} autocomplete="username" required /></label>
          <label
            >Password <input type="password" bind:value={password} autocomplete="current-password" required /></label
          >
          <div><button class="primary" disabled={busy}>Sign in</button></div>
        </form>
      {/if}
    {/if}
  </div>
</main>

<style>
  main {
    max-width: 28rem;
    margin: 4rem auto;
    padding: 0 1rem;
  }
</style>
