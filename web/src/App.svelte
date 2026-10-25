<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import logo from './assets/logo.svg';
  import Avatar from './components/Avatar.svelte';
  import { go, link, nav } from './lib/nav.svelte.ts';
  import { connect, disconnect, loadMonitors } from './lib/live.svelte.ts';
  import { routeFor, settingsTabs } from './lib/route.ts';
  import { api, app, refresh } from './lib/session.svelte.ts';
  import Account from './pages/Account.svelte';
  import Agents from './pages/Agents.svelte';
  import Analysis from './pages/Analysis.svelte';
  import Audit from './pages/Audit.svelte';
  import Backup from './pages/Backup.svelte';
  import Home from './pages/Home.svelte';
  import Login from './pages/Login.svelte';
  import Maintenance from './pages/Maintenance.svelte';
  import MonitorForm from './pages/MonitorForm.svelte';
  import MonitorPage from './pages/MonitorPage.svelte';
  import Notifications from './pages/Notifications.svelte';
  import PublicStatus from './pages/PublicStatus.svelte';
  import Retention from './pages/Retention.svelte';
  import SSO from './pages/SSO.svelte';
  import StatusPageEdit from './pages/StatusPageEdit.svelte';
  import StatusPages from './pages/StatusPages.svelte';
  import Setup from './pages/Setup.svelte';
  import Tags from './pages/Tags.svelte';
  import Tokens from './pages/Tokens.svelte';
  import Users from './pages/Users.svelte';

  onMount(refresh);

  const user = $derived(app.session?.user ?? null);
  const route = $derived(routeFor(nav.path));
  const page = $derived(route.page);
  const signedIn = $derived(user !== null);
  const inSettings = $derived(settingsTabs.some((t) => t.page === page));
  const inStatusPages = $derived(page === 'status-pages' || page === 'status-page' || page === 'new-status-page');

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

  // The account menu: closed by a click outside it, Escape or going to
  // another page.
  let menu = $state<HTMLDetailsElement>();
  let menuOpen = $state(false);
  $effect(() => {
    void nav.path;
    menuOpen = false;
  });
  function closeMenu(e: MouseEvent | KeyboardEvent) {
    if (!menuOpen) return;
    if (e instanceof KeyboardEvent) {
      if (e.key !== 'Escape') return;
      menuOpen = false;
      menu?.querySelector('summary')?.focus();
    } else if (!menu?.contains(e.target as Node)) {
      menuOpen = false;
    }
  }

  async function signOut() {
    try {
      await api.logout();
    } finally {
      await refresh();
      go('/login', true);
    }
  }
</script>

<svelte:window onclick={closeMenu} onkeydown={closeMenu} />

{#if page === 'status'}
  <!-- Public: anyone may see a published status page, signed in or not. -->
  <PublicStatus slug={route.slug ?? ''} />
{:else if !app.session}
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
    <a class="brand" href="/" onclick={link}><img src={logo} alt="" width="28" height="28" /> Eyrie</a>
    <nav>
      <a href="/" onclick={link} aria-current={!inSettings && !inStatusPages && page !== 'maintenance' ? 'page' : undefined}
        >Monitors</a
      >
      <a href="/maintenance" onclick={link} aria-current={page === 'maintenance' ? 'page' : undefined}>Maintenance</a>
      <a href="/status-pages" onclick={link} aria-current={inStatusPages ? 'page' : undefined}>Status pages</a>
      <a href="/settings/account" onclick={link} aria-current={inSettings ? 'page' : undefined}>Settings</a>
    </nav>
    <details class="me" bind:this={menu} bind:open={menuOpen}>
      <summary aria-label="Account menu for {user.username}"><Avatar {user} /><span>{user.username}</span></summary>
      <div class="menu">
        <a href="/settings/account" onclick={link}>Your account</a>
        <button onclick={signOut}>Sign out</button>
      </div>
    </details>
  </header>
  <main class:wide={inSettings}>
    {#if page === 'home'}
      <Home />
    {:else if page === 'monitor'}
      <MonitorPage id={route.id} />
    {:else if page === 'maintenance'}
      <Maintenance />
    {:else if page === 'status-pages'}
      <StatusPages />
    {:else if page === 'status-page' || (page === 'new-status-page' && user.role === 'admin')}
      {#key route.id}<StatusPageEdit id={route.id} />{/key}
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
      {:else if page === 'agents'}
        <Agents />
      {:else if page === 'retention'}
        <Retention />
      {:else if page === 'backup'}
        <Backup />
      {:else if page === 'notifications'}
        <Notifications />
      {:else if page === 'tags'}
        <Tags />
      {:else if page === 'analysis'}
        <Analysis />
      {:else if page === 'audit'}
        <Audit />
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
    display: flex;
    align-items: center;
    gap: 0.4rem;
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
    position: relative;
  }
  .me summary {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.25rem 0.4rem;
    border-radius: var(--radius);
    color: var(--muted);
    cursor: pointer;
    list-style: none;
  }
  .me summary::-webkit-details-marker {
    display: none;
  }
  .me summary::after {
    content: '▾';
    font-size: 0.75rem;
  }
  .me summary:hover,
  .me[open] summary {
    color: var(--fg);
    background: var(--bg);
  }
  .menu {
    position: absolute;
    right: 0;
    top: calc(100% + 0.35rem);
    z-index: 20;
    display: grid;
    min-width: 11rem;
    padding: 0.35rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.25);
  }
  .menu a,
  .menu button {
    display: block;
    width: 100%;
    padding: 0.5rem 0.65rem;
    border: 0;
    border-radius: 6px;
    background: none;
    color: var(--fg);
    font: inherit;
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }
  .menu a:hover,
  .menu button:hover {
    background: var(--bg);
  }
  /* On a phone: the account menu at the top right, beside the logo, and
     the page menu on its own line below them. */
  @media (max-width: 48rem) {
    .brand {
      flex: 1;
    }
    nav {
      order: 1;
      flex-basis: 100%;
      flex-wrap: wrap;
    }
  }
  main {
    max-width: 60rem;
    margin: 1.5rem auto;
    padding: 0 1rem;
  }
  /* Room for every settings tab on one line. */
  main.wide {
    max-width: 78rem;
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
