<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Avatar from './components/Avatar.svelte';
  import { go, link, nav } from './lib/nav.svelte.ts';
  import { connect, disconnect, loadMonitors } from './lib/live.svelte.ts';
  import { routeFor, settingsTabs } from './lib/route.ts';
  import { api, app, refresh } from './lib/session.svelte.ts';
  import Account from './pages/Account.svelte';
  import Home from './pages/Home.svelte';
  import Login from './pages/Login.svelte';
  import MonitorForm from './pages/MonitorForm.svelte';
  import MonitorPage from './pages/MonitorPage.svelte';
  import Notifications from './pages/Notifications.svelte';
  import Retention from './pages/Retention.svelte';
  import SSO from './pages/SSO.svelte';
  import Setup from './pages/Setup.svelte';
  import Tokens from './pages/Tokens.svelte';
  import Users from './pages/Users.svelte';

  onMount(refresh);

  const user = $derived(app.session?.user ?? null);
  const route = $derived(routeFor(nav.path));
  const page = $derived(route.page);
  const signedIn = $derived(user !== null);
  const inSettings = $derived(settingsTabs.some((t) => t.page === page));

  // Monitors and their live updates, while signed in.
  $effect(() => {
    if (signedIn) {
      loadMonitors();
      connect();
    } else {
      disconnect();
    }
  });
  const tabs = $derived(settingsTabs.filter((t) => !t.admin || user?.role === 'admin'));

  // Signed in on the sign-in page: go to the app.
  $effect(() => {
    if (user && nav.path === '/login') {
      go('/', true);
    }
  });

  async function signOut() {
    try {
      await api.logout();
    } finally {
      await refresh();
      go('/login', true);
    }
  }
</script>

{#if !app.session}
  <main class="narrow">
    {#if app.error}
      <p class="notice error">Eyrie could not be reached: {app.error}</p>
      <button onclick={refresh}>Try again</button>
    {:else}
      <p class="muted">Loading…</p>
    {/if}
  </main>
{:else if app.session.setup}
  <Setup />
{:else if !user}
  <Login />
{:else}
  <header>
    <a class="brand" href="/" onclick={link}>Eyrie</a>
    <nav>
      <a href="/" onclick={link} aria-current={!inSettings ? 'page' : undefined}>Monitors</a>
      <a href="/settings/account" onclick={link} aria-current={inSettings ? 'page' : undefined}>Settings</a>
    </nav>
    <div class="me">
      <Avatar {user} />
      <span>{user.username}</span>
      <button onclick={signOut}>Sign out</button>
    </div>
  </header>
  <main>
    {#if page === 'home'}
      <Home />
    {:else if page === 'monitor'}
      <MonitorPage id={route.id} />
    {:else if (page === 'new-monitor' || page === 'edit-monitor') && user.role === 'admin'}
      {#key route.id}<MonitorForm id={route.id} />{/key}
    {:else if page === 'not-found' || !tabs.some((t) => t.page === page)}
      <h1>Not found</h1>
      <p><a href="/" onclick={link}>Back to the start</a></p>
    {:else}
      <h1>Settings</h1>
      <div class="tabs" role="tablist">
        {#each tabs as t (t.page)}
          <a href={t.path} onclick={link} role="tab" aria-selected={t.page === page}>{t.label}</a>
        {/each}
      </div>
      {#if page === 'account'}
        <Account />
      {:else if page === 'users'}
        <Users />
      {:else if page === 'sso'}
        <SSO />
      {:else if page === 'tokens'}
        <Tokens />
      {:else if page === 'retention'}
        <Retention />
      {:else if page === 'notifications'}
        <Notifications />
      {/if}
    {/if}
  </main>
{/if}

<!-- Eyrie is under the AGPL: every page offers the source of the running version. -->
<footer class="muted">
  Eyrie {app.session?.version ?? ''} ·
  <a href="https://www.gnu.org/licenses/agpl-3.0.html" rel="license noreferrer" target="_blank">AGPL-3.0-or-later</a>
  {#if app.session?.source}
    · <a href={app.session.source} rel="noreferrer" target="_blank">Source code</a>
  {/if}
</footer>

<style>
  header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 1rem;
    padding: 0.6rem 1rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
  }
  .brand {
    font-weight: 700;
    font-size: 1.15rem;
    color: var(--fg);
    text-decoration: none;
  }
  nav {
    flex: 1;
    display: flex;
    gap: 1rem;
  }
  nav a {
    color: var(--muted);
    text-decoration: none;
  }
  nav a[aria-current='page'] {
    color: var(--fg);
    font-weight: 600;
  }
  .me {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  main {
    max-width: 60rem;
    margin: 1.5rem auto;
    padding: 0 1rem;
  }
  main.narrow {
    max-width: 28rem;
    margin-top: 4rem;
  }
  .tabs {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
    margin-bottom: 1rem;
    border-bottom: 1px solid var(--border);
  }
  .tabs a {
    padding: 0.4rem 0.8rem;
    color: var(--muted);
    text-decoration: none;
    border-bottom: 2px solid transparent;
  }
  .tabs a[aria-selected='true'] {
    color: var(--fg);
    border-bottom-color: var(--accent);
  }
  footer {
    text-align: center;
    padding: 2rem 1rem;
    font-size: 0.85rem;
  }
</style>
