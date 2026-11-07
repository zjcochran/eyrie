<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import Pager from '../components/Pager.svelte';
  import { type PageSize, type StatusChange, message } from '../lib/api.ts';
  import { byDay, happened } from '../lib/events.ts';
  import { ago, millis } from '../lib/format.ts';
  import { live, onBeat } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { offsetOf } from '../lib/paging.ts';
  import { api } from '../lib/session.svelte.ts';

  let changes = $state<StatusChange[]>([]);
  let total = $state(0);
  let page = $state(1);
  let size = $state<PageSize>(25);
  let loaded = $state(false);
  let error = $state('');
  let busy = $state(false);
  // The change whose details are open.
  let chosen = $state<StatusChange | null>(null);

  async function load() {
    busy = true;
    error = '';
    try {
      const p = await api.statusChangePage(offsetOf(page, size), size);
      changes = p.changes;
      total = p.total;
      loaded = true;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  const timeOf = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });

  function monitorName(id: number): string {
    return live.monitors.find((m) => m.id === id)?.name ?? `Monitor ${id}`;
  }

  onMount(() => {
    load();
    // New changes come first on the first page as they happen; they have no
    // ID until reloaded.
    return onBeat((b) => {
      if (!b.important || !loaded) return;
      total += 1;
      if (page === 1) changes = [{ ...b, id: 0 }, ...changes].slice(0, size === 'all' ? undefined : size);
    });
  });
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && chosen && (chosen = null)} />

<h1>Events</h1>
{#if error}<p class="notice error" role="status">{error}</p>{/if}

<section class="card">
  <p class="muted">
    Every monitor's status changes, newest first. Choose one for its details. They are kept as long as heartbeats (Settings →
    History).
  </p>
  {#if changes.length}
    <Pager {total} shown={changes.length} {busy} bind:page bind:size onchange={load} />
    {#each byDay(changes) as d, i (i)}
      <h2 class="day">{d.day}</h2>
      <ul class="events">
        {#each d.changes as c (`${c.id}-${c.monitor_id}-${c.at}`)}
          <li>
            <button class="event" onclick={() => (chosen = c)}>
              <StatusDot status={c.status} />
              <span class="what"><strong>{monitorName(c.monitor_id)}</strong> {happened(c.status)}</span>
              <time datetime={c.at}>{timeOf(c.at)}</time>
            </button>
          </li>
        {/each}
      </ul>
    {/each}
  {:else if loaded}
    <p class="muted">Nothing yet.</p>
  {:else if !error}
    <p class="muted">Loading…</p>
  {/if}
</section>

{#if chosen}
  <!-- The details of one change: on a phone, a sheet from the bottom.
       Escape closes it from the keyboard (svelte:window, above). -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="backdrop" role="presentation" onclick={(e) => e.target === e.currentTarget && (chosen = null)}>
    <div class="card dialog" role="dialog" aria-modal="true" aria-labelledby="event-title">
      <h2 id="event-title"><StatusDot status={chosen.status} /> {monitorName(chosen.monitor_id)} {happened(chosen.status)}</h2>
      <dl>
        <dt>When</dt>
        <dd>{new Date(chosen.at).toLocaleString(undefined, { dateStyle: 'full', timeStyle: 'medium' })} · {ago(chosen.at)}</dd>
        {#if chosen.message}
          <dt>Message</dt>
          <dd class="message">{chosen.message}</dd>
        {/if}
        {#if chosen.latency_ms !== null}
          <dt>Response time</dt>
          <dd>{millis(chosen.latency_ms)}</dd>
        {/if}
      </dl>
      <div class="actions">
        <a class="button" href="/monitors/{chosen.monitor_id}" onclick={(e) => {
            link(e);
            chosen = null;
          }}>Open the monitor</a>
        <!-- svelte-ignore a11y_autofocus -->
        <button class="primary" onclick={() => (chosen = null)} autofocus>Close</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .day {
    margin: 1.25rem 0 0.4rem;
    font-size: 0.8rem;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .events {
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) * 0.7);
    overflow: hidden;
  }
  .events li + li {
    border-top: 1px solid var(--border);
  }
  .event {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    width: 100%;
    min-height: 3rem;
    padding: 0.6rem 0.85rem;
    border: 0;
    border-radius: 0;
    background: none;
    color: var(--fg);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .event:hover {
    background: color-mix(in srgb, var(--fg) 5%, transparent);
  }
  .what {
    flex: 1;
    min-width: 0;
  }
  time {
    color: var(--muted);
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.85rem;
    white-space: nowrap;
  }
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    place-items: center;
    padding: 1rem;
    background: rgb(0 0 0 / 0.5);
  }
  /* Solid, even in Aurora, so the list does not show through. */
  .backdrop .card.dialog {
    background: var(--surface);
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
  }
  .dialog {
    width: min(32rem, 100%);
    box-sizing: border-box;
    margin: 0;
    overflow-wrap: anywhere;
    box-shadow: 0 24px 64px rgb(0 0 0 / 0.4);
  }
  .dialog h2 {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin: 0 0 0.75rem;
    font-size: 1.15rem;
  }
  dl {
    display: grid;
    gap: 0.2rem;
    margin: 0;
  }
  dt {
    margin-top: 0.5rem;
    color: var(--muted);
    font-size: 0.85rem;
  }
  dd {
    margin: 0;
  }
  .message {
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.9rem;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 1.25rem;
  }
  @media (max-width: 48rem) {
    .backdrop {
      place-items: end center;
      padding: 0;
    }
    .dialog {
      width: 100%;
      border-radius: var(--radius) var(--radius) 0 0;
      padding-bottom: calc(1.25rem + env(safe-area-inset-bottom));
    }
    .actions > * {
      flex: 1;
      text-align: center;
    }
  }
</style>
