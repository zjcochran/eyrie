<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Avatar from './components/Avatar.svelte';
  import Icon from './components/Icon.svelte';
  import Logo from './components/Logo.svelte';
  import Palette from './components/Palette.svelte';
  import UpdatePopup from './components/UpdatePopup.svelte';
  import WhatsNew from './components/WhatsNew.svelte';
  import { isNight, normalize } from './lib/appearance.ts';
  import { go, link, nav } from './lib/nav.svelte.ts';
  import { connect, disconnect, loadMonitors } from './lib/live.svelte.ts';
  import { routeFor, settingsTabs } from './lib/route.ts';
  import { api, app, refresh } from './lib/session.svelte.ts';
  import Account from './pages/Account.svelte';
  import Agents from './pages/Agents.svelte';
  import Analysis from './pages/Analysis.svelte';
  import Appearance from './pages/Appearance.svelte';
  import Audit from './pages/Audit.svelte';
  import Discover from './pages/Discover.svelte';
  import Events from './pages/Events.svelte';
  import Backup from './pages/Backup.svelte';
  import Login from './pages/Login.svelte';
  import Maintenance from './pages/Maintenance.svelte';
  import Monitors from './pages/Monitors.svelte';
  import Notifications from './pages/Notifications.svelte';
  import PublicStatus from './pages/PublicStatus.svelte';
  import Retention from './pages/Retention.svelte';
  import SSO from './pages/SSO.svelte';
  import StatusPageEdit from './pages/StatusPageEdit.svelte';
  import StatusPages from './pages/StatusPages.svelte';
  import Setup from './pages/Setup.svelte';
  import Tags from './pages/Tags.svelte';
  import Tokens from './pages/Tokens.svelte';
  import Updates from './pages/Updates.svelte';
  import Users from './pages/Users.svelte';

  // Whether the system is in dark mode, for the "system" mode.
  const dark = matchMedia('(prefers-color-scheme: dark)');
  let systemDark = $state(dark.matches);
  onMount(() => {
    refresh();
    const follow = () => (systemDark = dark.matches);
    dark.addEventListener('change', follow);
    return () => dark.removeEventListener('change', follow);
  });

  const user = $derived(app.session?.user ?? null);
  // Whether the "Eyrie was updated" window is closed or had nothing to show.
  let newsDone = $state(false);
  const route = $derived(routeFor(nav.path));
  const page = $derived(route.page);
  const signedIn = $derived(user !== null);
  const inSettings = $derived(settingsTabs.some((t) => t.page === page));
  const inStatusPages = $derived(page === 'status-pages' || page === 'status-page' || page === 'new-status-page');
  // The list of monitors with what is chosen in it.
  const inMonitors = $derived(['home', 'monitor', 'group', 'new-monitor', 'edit-monitor'].includes(page));

  // Monitors and their live updates, while signed in.
  $effect(() => {
    if (signedIn) {
      loadMonitors();
      connect();
    } else {
      disconnect();
      newsDone = false;
    }
  });
  const tabs = $derived(settingsTabs.filter((t) => !t.admin || user?.role === 'admin'));

  // How the app looks (themes.css): the user's choice, or the default before
  // signing in. Public status pages keep their own theme.
  const look = $derived(normalize(user?.appearance));
  $effect(() => {
    const root = document.documentElement.dataset;
    if (page === 'status') {
      delete root.layout;
      delete root.scheme;
      delete root.mode;
      return;
    }
    root.layout = look.layout;
    root.scheme = look.scheme;
    root.mode = isNight(look.mode, systemDark) ? 'night' : 'day';
  });

  // Jump to anything (⌘K).
  let palette = $state(false);

  // The pages in the menu.
  const links = $derived(
    [
      { href: '/', label: 'Monitors', icon: 'monitors', current: inMonitors, show: true },
      { href: '/discover', label: 'Discover', icon: 'discover', current: page === 'discover', show: user?.role === 'admin' },
      { href: '/events', label: 'Events', icon: 'events', current: page === 'events', show: true },
      { href: '/maintenance', label: 'Maintenance', icon: 'maintenance', current: page === 'maintenance', show: true },
      { href: '/status-pages', label: 'Status pages', icon: 'status-pages', current: inStatusPages, show: true },
      { href: '/settings/account', label: 'Settings', icon: 'settings', current: inSettings, show: true },
    ] as const,
  );

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
{:else if app.session.setup || app.welcome}
  <Setup />
{:else if !user}
  <Login />
{:else}
  <div class="shell">
    <header>
      <a class="brand" href="/" onclick={link}><Logo size={34} /><span class="label">Eyrie</span></a>
      <button class="search" onclick={() => (palette = true)} title="Search or jump to (⌘K)"
        ><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"
          ><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg
        ><span class="label grow">Search or jump to…</span><kbd class="label">⌘K</kbd></button
      >
      <nav aria-label="Main">
        {#each links as l (l.href)}
          {#if l.show}
            <a href={l.href} onclick={link} aria-current={l.current ? 'page' : undefined} title={l.label}
              ><Icon name={l.icon} /><span class="label">{l.label}</span></a
            >
          {/if}
        {/each}
      </nav>
      <details class="me" bind:this={menu} bind:open={menuOpen}>
        <summary aria-label="Account menu for {user.username}"><Avatar {user} /><span class="label">{user.username}</span></summary>
        <div class="menu">
          <a href="/settings/account" onclick={link}>Your account</a>
          <a href="/settings/appearance" onclick={link}>Appearance</a>
          <button onclick={signOut}>Sign out</button>
        </div>
      </details>
    </header>
    <Palette admin={user.role === 'admin'} bind:open={palette} />
    <!-- After an update, its notes first; then, for admins, any newer release. -->
    <WhatsNew ondone={() => (newsDone = true)} />
    {#if newsDone && user.role === 'admin'}<UpdatePopup />{/if}
    <div class="content">
      <main class:wide={inSettings} class:split={inMonitors}>
        {#if inMonitors}
          <Monitors {route} admin={user.role === 'admin'} />
        {:else if page === 'discover' && user.role === 'admin'}
          <Discover />
        {:else if page === 'events'}
          <Events />
        {:else if page === 'maintenance'}
          <Maintenance />
        {:else if page === 'status-pages'}
          <StatusPages />
        {:else if page === 'status-page' || (page === 'new-status-page' && user.role === 'admin')}
          {#key route.id}<StatusPageEdit id={route.id} />{/key}
        {:else if page === 'not-found' || !tabs.some((t) => t.page === page)}
          <h1>Not found</h1>
          <p><a href="/" onclick={link}>Back to the start</a></p>
        {:else}
          <h1>Settings</h1>
          <div class="settings">
            <div class="tabs" role="tablist" aria-orientation="vertical">
              {#each tabs as t (t.page)}
                <a href={t.path} onclick={link} role="tab" aria-selected={t.page === page}>{t.label}</a>
              {/each}
            </div>
            <div class="tab">
              {#if page === 'account'}
                <Account />
              {:else if page === 'appearance'}
                <Appearance />
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
              {:else if page === 'updates'}
                <Updates />
              {:else if page === 'audit'}
                <Audit />
              {/if}
            </div>
          </div>
        {/if}
      </main>
      {@render source()}
    </div>
  </div>
{/if}
{#if !user || page === 'status'}
  {@render source()}
{/if}

<!-- Eyrie is under the AGPL: every page offers the source of the running version. -->
{#snippet source()}
  <footer class="muted">
    Eyrie {app.session?.version ?? ''} ·
    <a href="https://www.gnu.org/licenses/agpl-3.0.html" rel="license noreferrer" target="_blank">AGPL-3.0-or-later</a>
    {#if app.session?.source}
      · <a href={app.session.source} rel="noreferrer" target="_blank">Source code</a>
    {/if}
  </footer>
{/snippet}

<style>
  /* The menu of pages: along the top (Bento, and every layout on a phone),
     a sidebar with names (Console) or a rail of icons (Aurora). */
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
    gap: 0.5rem;
    font-weight: 700;
    font-size: 1.15rem;
    letter-spacing: -0.02em;
    color: var(--fg);
    text-decoration: none;
  }
  nav {
    flex: 1;
    display: flex;
    gap: 0.25rem;
  }
  nav a {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.45rem 0.7rem;
    border-radius: calc(var(--radius) * 0.7);
    color: var(--muted);
    text-decoration: none;
  }
  nav a:hover {
    color: var(--fg);
    background: color-mix(in srgb, var(--fg) 6%, transparent);
  }
  nav a[aria-current='page'] {
    color: var(--fg);
    font-weight: 600;
    background: color-mix(in srgb, var(--fg) 8%, transparent);
  }
  .me {
    position: relative;
  }
  .me summary {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.3rem 0.45rem;
    border-radius: calc(var(--radius) * 0.7);
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
    background: color-mix(in srgb, var(--fg) 6%, transparent);
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
    box-shadow: 0 10px 30px rgb(0 0 0 / 0.3);
  }
  .menu a,
  .menu button {
    display: block;
    box-sizing: border-box;
    width: 100%;
    padding: 0.5rem 0.65rem;
    border: 0;
    border-radius: calc(var(--radius) * 0.55);
    background: none;
    color: var(--fg);
    font: inherit;
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }
  .menu a:hover,
  .menu button:hover {
    background: color-mix(in srgb, var(--fg) 7%, transparent);
  }
  .content {
    min-width: 0;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.65rem;
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) * 0.7);
    background: var(--surface-2, var(--bg));
    color: var(--muted);
    font-size: 0.9rem;
  }
  .search:hover {
    color: var(--fg);
  }
  .search .grow {
    flex: 1;
    text-align: left;
  }
  .search kbd {
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.72rem;
    padding: 0 0.35rem;
    border: 1px solid var(--border);
    border-radius: 4px;
  }
  :global(:root[data-layout='bento']) .search {
    order: 1;
    min-width: 14rem;
    border-radius: 12px;
  }
  :global(:root[data-layout='bento']) .me {
    order: 2;
  }

  /* Bento: a floating bar, the pages as pills. */
  :global(:root[data-layout='bento']) header {
    margin: 1rem 1.5rem 0;
    padding: 0.5rem 0.75rem;
    border: 0;
    border-radius: 999px;
    background: color-mix(in srgb, var(--surface) 72%, transparent);
    backdrop-filter: blur(12px);
    box-shadow: 0 1px 2px rgb(0 0 0 / 0.06);
  }
  :global(:root[data-layout='bento']) nav {
    justify-content: center;
  }
  :global(:root[data-layout='bento']) nav a {
    border-radius: 999px;
    padding: 0.45rem 0.95rem;
  }
  :global(:root[data-layout='bento']) nav a :global(svg) {
    display: none;
  }
  :global(:root[data-layout='bento']) nav a[aria-current='page'] {
    background: var(--fg);
    color: var(--bg);
  }
  :global(:root[data-layout='bento']) .me summary {
    border-radius: 999px;
  }

  /* Console and Aurora on a wide screen: the menu down the left. */
  @media (min-width: 56rem) {
    :global(:root[data-layout='console']) .shell,
    :global(:root[data-layout='aurora']) .shell {
      display: grid;
      grid-template-columns: auto minmax(0, 1fr);
      min-height: 100vh;
    }
    :global(:root[data-layout='console']) header,
    :global(:root[data-layout='aurora']) header {
      position: sticky;
      top: 0;
      height: 100vh;
      box-sizing: border-box;
      flex-direction: column;
      flex-wrap: nowrap;
      align-items: stretch;
      gap: 0.75rem;
    }
    :global(:root[data-layout='console']) nav,
    :global(:root[data-layout='aurora']) nav {
      flex-direction: column;
    }
    :global(:root[data-layout='console']) .menu,
    :global(:root[data-layout='aurora']) .menu {
      top: auto;
      bottom: calc(100% + 0.35rem);
      left: 0;
      right: auto;
    }

    :global(:root[data-layout='console']) header {
      width: 15rem;
      padding: 1rem 0.75rem;
      border-bottom: 0;
      border-right: 1px solid var(--border);
      background: color-mix(in srgb, var(--surface) 70%, var(--bg));
    }
    :global(:root[data-layout='console']) .brand {
      padding: 0 0.4rem 0.75rem;
    }
    :global(:root[data-layout='console']) .me {
      border-top: 1px solid var(--border);
      padding-top: 0.6rem;
    }

    :global(:root[data-layout='aurora']) header {
      width: 4.75rem;
      margin: 1rem 0 1rem 1rem;
      height: calc(100vh - 2rem);
      top: 1rem;
      padding: 0.85rem 0.6rem;
      align-items: center;
      border: 1px solid color-mix(in srgb, var(--fg) 13%, transparent);
      border-radius: 24px;
      background: color-mix(in srgb, var(--surface) 50%, transparent);
      backdrop-filter: blur(24px) saturate(140%);
    }
    :global(:root[data-layout='aurora']) .label {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }
    :global(:root[data-layout='aurora']) nav a {
      justify-content: center;
      width: 2.9rem;
      height: 2.9rem;
      padding: 0;
      border-radius: 14px;
    }
    :global(:root[data-layout='aurora']) nav a[aria-current='page'] {
      background: var(--fg);
      color: var(--bg);
    }
    :global(:root[data-layout='aurora']) .me summary::after {
      content: none;
    }
    :global(:root[data-layout='aurora']) .search {
      justify-content: center;
      width: 2.9rem;
      height: 2.9rem;
      padding: 0;
      border-radius: 14px;
    }
  }

  /* On a phone: the account menu at the top right, beside the logo, and
     the page menu on its own line below them. */
  @media (max-width: 48rem) {
    .brand {
      flex: 1;
    }
    .search .label {
      display: none;
    }
    :global(:root[data-layout='bento']) .search {
      order: 0;
      min-width: 0;
    }
    nav {
      order: 1;
      flex-basis: 100%;
      flex-wrap: wrap;
    }
    :global(:root[data-layout='bento']) header {
      margin: 0.75rem 0.75rem 0;
      border-radius: 24px;
    }
  }
  main {
    max-width: 60rem;
    margin: 1.5rem auto;
    padding: 0 1rem;
  }
  :global(:root[data-layout='aurora']) main,
  :global(:root[data-layout='console']) main {
    padding: 0 1.5rem;
  }
  /* Settings: room for wide forms and tables beside the menu. */
  main.wide {
    max-width: 96rem;
  }
  main.split {
    max-width: 96rem;
  }
  main.narrow {
    max-width: 28rem;
    margin-top: 4rem;
  }
  /* The settings menu down the left of its page. */
  .settings {
    display: grid;
    grid-template-columns: 12rem minmax(0, 1fr);
    gap: 1.5rem;
    align-items: start;
  }
  .tabs {
    position: sticky;
    top: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .tabs a {
    padding: 0.45rem 0.7rem;
    border-radius: calc(var(--radius) * 0.7);
    color: var(--muted);
    text-decoration: none;
  }
  .tabs a:hover {
    color: var(--fg);
    background: color-mix(in srgb, var(--fg) 6%, transparent);
  }
  .tabs a[aria-selected='true'] {
    color: var(--fg);
    font-weight: 600;
    background: color-mix(in srgb, var(--fg) 8%, transparent);
    box-shadow: inset 3px 0 0 var(--chart, var(--accent));
  }
  :global(:root[data-layout='bento']) .tabs a {
    border-radius: 999px;
    padding: 0.45rem 0.95rem;
  }
  :global(:root[data-layout='bento']) .tabs a[aria-selected='true'] {
    background: var(--fg);
    color: var(--bg);
    box-shadow: none;
  }
  /* On a phone: the menu above the page, wrapping. */
  @media (max-width: 48rem) {
    .settings {
      grid-template-columns: minmax(0, 1fr);
      gap: 1rem;
    }
    .tabs {
      position: static;
      flex-direction: row;
      flex-wrap: wrap;
    }
  }
  footer {
    text-align: center;
    padding: 2rem 1rem;
    font-size: 0.85rem;
  }
</style>
