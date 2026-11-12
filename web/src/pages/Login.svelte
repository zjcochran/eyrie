<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import Logo from '../components/Logo.svelte';
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
  <div class="panel">
    <div class="brand"><Logo size={56} /><span>Eyrie</span></div>
    {#if needCode}
      <h1>Two-factor sign-in</h1>
      <p class="lead">Enter the code from your authenticator app, or one of your recovery codes.</p>
    {:else}
      <h1>Welcome back</h1>
      <p class="lead">Sign in to see your monitors.</p>
    {/if}
    <div class="card">
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      {#if needCode}
        <form onsubmit={sendCode}>
          <label
            >Code
            <input class="code" bind:value={code} autocomplete="one-time-code" inputmode="numeric" required /></label
          >
          <button class="primary wide" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
          <button type="button" class="quiet" onclick={() => ((needCode = false), (code = ''))}>Back</button>
        </form>
      {:else}
        {#if session.sso.enabled}
          <a class="button wide" class:primary={!session.password_sign_in} href={ssoURL}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
              ><circle cx="8" cy="15" r="4" /><path d="M10.8 12.2 20 3M16 7l3 3M14 9l2 2" /></svg
            >
            Sign in with {session.sso.name}
          </a>
        {/if}
        {#if session.password_sign_in}
          {#if session.sso.enabled}<p class="or"><span>or</span></p>{/if}
          <form onsubmit={signIn}>
            <label>Username <input bind:value={username} autocomplete="username" required /></label>
            <label
              >Password <input type="password" bind:value={password} autocomplete="current-password" required /></label
            >
            <button class="primary wide" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
          </form>
        {/if}
      {/if}
    </div>
  </div>
</main>

<style>
  /* In the middle of the screen, over a soft glow in the accent colour
     that fills the window; the footer stays below. */
  main {
    display: grid;
    place-items: center;
    min-height: calc(100dvh - 5.5rem);
    padding: 2rem 1rem 0;
    box-sizing: border-box;
  }
  main::before {
    content: '';
    position: fixed;
    inset: 0;
    z-index: -1;
    background:
      radial-gradient(40rem 26rem at 50% 0%, color-mix(in srgb, var(--accent) 13%, transparent), transparent 70%),
      radial-gradient(36rem 28rem at 85% 100%, color-mix(in srgb, var(--accent) 7%, transparent), transparent 70%);
    pointer-events: none;
  }
  .panel {
    width: 100%;
    max-width: 24rem;
  }
  .brand {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.6rem;
    margin-bottom: 1.75rem;
    font-size: 1.6rem;
    font-weight: 700;
    letter-spacing: -0.02em;
  }
  h1 {
    margin: 0;
    text-align: center;
    font-size: 1.6rem;
    letter-spacing: -0.02em;
  }
  .lead {
    margin: 0.35rem 0 1.5rem;
    text-align: center;
    color: var(--muted);
  }
  .card {
    padding: 1.75rem;
    margin: 0;
    border-radius: calc(var(--radius) * 2);
    background: color-mix(in srgb, var(--surface) 88%, transparent);
    box-shadow:
      0 1px 0 color-mix(in srgb, var(--fg) 6%, transparent) inset,
      0 24px 48px -28px rgb(0 0 0 / 0.35);
    backdrop-filter: blur(12px);
  }
  form {
    max-width: none;
    gap: 1rem;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    min-height: 2.75rem;
  }
  .code {
    text-align: center;
    font-family: 'Geist Mono', ui-monospace, monospace;
    font-size: 1.25rem;
    letter-spacing: 0.3em;
  }
  .wide {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    width: 100%;
    min-height: 2.75rem;
    box-sizing: border-box;
    font-weight: 600;
  }
  form .wide {
    margin-top: 0.25rem;
  }
  .quiet {
    border: 0;
    background: none;
    color: var(--muted);
  }
  .quiet:hover {
    color: var(--fg);
  }
  /* A line either side of "or". */
  .or {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin: 1.25rem 0;
    color: var(--faint, var(--muted));
    font-size: 0.85rem;
  }
  .or::before,
  .or::after {
    content: '';
    flex: 1;
    border-top: 1px solid var(--border);
  }
  @media (max-width: 30rem) {
    .card {
      padding: 1.25rem;
    }
  }
</style>
