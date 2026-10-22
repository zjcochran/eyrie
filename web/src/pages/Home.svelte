<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import BeatBar from '../components/BeatBar.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import type { Monitor } from '../lib/api.ts';
  import { millis, percent } from '../lib/format.ts';
  import { live } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { app } from '../lib/session.svelte.ts';

  const admin = $derived(app.session?.user?.role === 'admin');

  // Groups first, each followed by its members; then monitors outside any group.
  const rows = $derived.by(() => {
    const out: { m: Monitor; depth: number }[] = [];
    const byParent = new Map<number, Monitor[]>();
    for (const m of live.monitors) {
      const p = m.parent_id ?? 0;
      byParent.set(p, [...(byParent.get(p) ?? []), m]);
    }
    const ids = new Set(live.monitors.map((m) => m.id));
    const walk = (parent: number, depth: number) => {
      for (const m of byParent.get(parent) ?? []) {
        out.push({ m, depth });
        if (depth < 10) walk(m.id, depth + 1);
      }
    };
    walk(0, 0);
    // Members whose group is not in the list (it should not happen) still show.
    for (const [p, ms] of byParent) if (p !== 0 && !ids.has(p)) for (const m of ms) out.push({ m, depth: 0 });
    return out;
  });

  const counts = $derived.by(() => {
    const c = { up: 0, down: 0, pending: 0, paused: 0 };
    for (const m of live.monitors) {
      if (!m.active) c.paused++;
      else if (m.status.status === 'up') c.up++;
      else if (m.status.status === 'down') c.down++;
      else if (m.status.status === 'pending') c.pending++;
    }
    return c;
  });
</script>

<div class="head row">
  <h1>Monitors</h1>
  {#if admin}<a class="button primary" href="/monitors/new" onclick={link}>Add a monitor</a>{/if}
</div>

{#if live.error}<p class="notice error">{live.error}</p>{/if}

{#if !live.loaded}
  <p class="muted">Loading…</p>
{:else if live.monitors.length === 0}
  <div class="card">
    <p>There are no monitors yet.{#if admin} <a href="/monitors/new" onclick={link}>Add the first one.</a>{/if}</p>
  </div>
{:else}
  <p class="summary">
    <span><StatusDot status="up" /> {counts.up} up</span>
    <span><StatusDot status="down" /> {counts.down} down</span>
    <span><StatusDot status="pending" /> {counts.pending} pending</span>
    <span><StatusDot status="unknown" active={false} /> {counts.paused} paused</span>
  </p>
  <div class="card list">
    {#each rows as { m, depth } (m.id)}
      <a class="monitor" href="/monitors/{m.id}" onclick={link} style:padding-left="{0.75 + depth * 1.25}rem">
        <span class="name">
          <StatusDot status={m.status.status} active={m.active} />
          <span>{m.name}</span>
          <span class="muted type">{m.type}</span>
        </span>
        <span class="bar"><BeatBar beats={m.beats} /></span>
        <span class="figures">
          <span title="Uptime, last 24 hours">{percent(m.uptime['24h'])}</span>
          <span class="muted" title="Last response time">{millis(m.status.latency_ms)}</span>
        </span>
      </a>
    {/each}
  </div>
{/if}

<style>
  .head {
    justify-content: space-between;
    margin-bottom: 0.5rem;
  }
  .head h1 {
    margin: 0;
  }
  .summary {
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
  }
  .list {
    padding: 0;
  }
  .monitor {
    display: grid;
    grid-template-columns: minmax(10rem, 1.2fr) minmax(8rem, 2fr) 7rem;
    gap: 1rem;
    align-items: center;
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid var(--border);
    color: var(--fg);
    text-decoration: none;
  }
  .monitor:last-child {
    border-bottom: none;
  }
  .monitor:hover {
    background: var(--bg);
  }
  .name {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .type {
    font-size: 0.8rem;
  }
  .bar {
    min-width: 0;
  }
  .figures {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    font-variant-numeric: tabular-nums;
  }
  @media (max-width: 40rem) {
    .monitor {
      grid-template-columns: 1fr 5rem;
    }
    .bar {
      grid-column: 1 / -1;
      grid-row: 2;
    }
  }
</style>
