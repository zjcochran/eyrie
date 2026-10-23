<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import TagChip from '../components/TagChip.svelte';
  import { type Incident, type PublicPage, message } from '../lib/api.ts';
  import { daysUntil, percent } from '../lib/format.ts';
  import { incidentLabels } from '../lib/incidents.ts';
  import { api } from '../lib/session.svelte.ts';

  let { slug }: { slug: string } = $props();

  let page = $state<PublicPage | null>(null);
  let error = $state('');

  async function load() {
    try {
      page = await api.publicStatus(slug);
      error = '';
    } catch (e) {
      error = message(e);
    }
  }

  onMount(() => {
    load();
    const timer = setInterval(load, 60000);
    return () => {
      clearInterval(timer);
      delete document.documentElement.dataset.theme;
    };
  });

  // The page's theme, and its title in the browser's tab.
  $effect(() => {
    if (!page) return;
    if (page.theme === 'auto') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = page.theme;
    document.title = page.title;
  });

  const banner: Record<PublicPage['status'], string> = {
    up: 'All systems operational',
    partial: 'Some systems are down',
    down: 'All systems are down',
    maintenance: 'Under maintenance',
    unknown: 'No data yet',
  };

  const open = $derived(page?.incidents.filter((i) => !i.resolved_at) ?? []);
  const past = $derived(page?.incidents.filter((i) => i.resolved_at) ?? []);
  const when = (iso: string | null) => (iso ? new Date(iso).toLocaleString() : '');

  /** The colour class of a day's uptime. */
  function dayClass(u: number | null): string {
    if (u === null) return 'none';
    if (u >= 0.999) return 'up';
    if (u >= 0.95) return 'part';
    return 'down';
  }
</script>

{#snippet timeline(i: Incident)}
  <article class="incident card">
    <h3>{i.title} <span class="badge {i.status}">{incidentLabels[i.status]}</span></h3>
    <ol>
      {#each i.updates as u, n (n)}
        <li>
          <strong>{incidentLabels[u.status]}</strong> <span class="muted">· {when(u.created_at)}</span>
          <p class="text">{u.message}</p>
        </li>
      {/each}
    </ol>
  </article>
{/snippet}

<main class="status" style:--accent={page?.accent || undefined}>
  {#if error && !page}
    <h1>Not found</h1>
    <p class="muted">{error}</p>
  {:else if !page}
    <p class="muted">Loading…</p>
  {:else}
    <header>
      {#if page.logo_url}<img src={page.logo_url} alt="" />{/if}
      <h1>{page.title}</h1>
    </header>
    {#if !page.published}<p class="notice">Preview: this page is not published, so only signed-in users see it.</p>{/if}
    {#if page.description}<p class="text">{page.description}</p>{/if}

    <p class="overall {page.status}"><StatusDot status={page.status === 'partial' ? 'down' : page.status} /> {banner[page.status]}</p>

    {#each open as i (i.id)}{@render timeline(i)}{/each}

    {#if page.maintenance.length}
      <section class="card">
        <h2>Maintenance</h2>
        {#each page.maintenance as m, n (n)}
          <p>
            <strong>{m.title}</strong>:
            {#if m.state === 'on'}now{#if m.change}, until {when(m.change)}{/if}{:else}from {when(m.change)}{/if}
            {#if m.description}<br /><span class="muted">{m.description}</span>{/if}
          </p>
        {/each}
      </section>
    {/if}

    {#each page.sections as sec, n (n)}
      <section class="card">
        {#if sec.title}<h2>{sec.title}</h2>{/if}
        {#each sec.monitors as m (m.id)}
          <div class="monitor">
            <div class="row line">
              <span class="name">
                <StatusDot status={m.status === 'paused' ? 'unknown' : m.status} active={m.status !== 'paused'} />
                {m.name}
                {#each m.tags ?? [] as t, k (k)}<TagChip name={t.name} color={t.color} value={t.value} />{/each}
              </span>
              <span class="muted figures">
                {#if m.cert_expires}certificate {daysUntil(m.cert_expires)} days ·{/if}
                {percent(m.uptime['90d'])} over 90 days
              </span>
            </div>
            <div class="days" role="img" aria-label="Uptime of the last 90 days">
              {#each m.days as d (d.date)}
                <span class="day {dayClass(d.uptime)}" title="{d.date}: {d.uptime === null ? 'no data' : percent(d.uptime)}"></span>
              {/each}
            </div>
          </div>
        {:else}
          <p class="muted">No monitors.</p>
        {/each}
      </section>
    {/each}

    {#if past.length}
      <h2>Past incidents</h2>
      {#each past as i (i.id)}{@render timeline(i)}{/each}
    {/if}

    {#if page.footer}<p class="text muted">{page.footer}</p>{/if}
    <p class="muted small">Updated {when(page.generated_at)}; refreshes every minute.</p>
  {/if}
</main>

<style>
  .status {
    max-width: 52rem;
    margin: 1.5rem auto;
    padding: 0 1rem;
  }
  header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  header img {
    max-height: 3rem;
    max-width: 10rem;
  }
  .text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .overall {
    padding: 0.9rem 1rem;
    border-radius: var(--radius);
    font-size: 1.15rem;
    font-weight: 600;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 6px solid var(--up);
  }
  .overall.partial,
  .overall.down {
    border-left-color: var(--down);
  }
  .overall.maintenance {
    border-left-color: var(--maintenance);
  }
  .overall.unknown {
    border-left-color: var(--unknown);
  }
  .monitor {
    padding: 0.5rem 0;
    border-bottom: 1px solid var(--border);
  }
  .monitor:last-child {
    border-bottom: none;
  }
  .line {
    justify-content: space-between;
    flex-wrap: wrap;
  }
  .name {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .figures {
    font-size: 0.85rem;
  }
  .days {
    display: flex;
    gap: 1px;
    height: 1.6rem;
    margin-top: 0.35rem;
  }
  .day {
    flex: 1;
    min-width: 1px;
    border-radius: 1px;
    background: var(--border);
  }
  .day.up {
    background: var(--up);
  }
  .day.part {
    background: var(--pending);
  }
  .day.down {
    background: var(--down);
  }
  .incident h3 {
    margin-top: 0;
  }
  .incident ol {
    list-style: none;
    padding: 0;
    margin: 0;
    border-left: 2px solid var(--border);
  }
  .incident li {
    padding: 0 0 0.6rem 0.8rem;
  }
  .incident p {
    margin: 0.2rem 0 0;
  }
  .badge {
    font-size: 0.75rem;
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    background: var(--down);
    color: #ffffff;
    vertical-align: middle;
  }
  .badge.monitoring {
    background: var(--pending);
    color: #000000;
  }
  .badge.resolved {
    background: var(--up);
    color: #000000;
  }
  .small {
    font-size: 0.8rem;
  }
</style>
