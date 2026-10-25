<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import BeatBar from '../components/BeatBar.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import TagChip from '../components/TagChip.svelte';
  import type { Monitor, MonitorTag } from '../lib/api.ts';
  import { millis, percent } from '../lib/format.ts';
  import { live } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { app } from '../lib/session.svelte.ts';

  const admin = $derived(app.session?.user?.role === 'admin');
  // The tag the list is filtered by; 0 for none.
  let tagFilter = $state(0);

  // Every tag on some monitor, by name.
  const allTags = $derived.by(() => {
    const byID = new Map<number, MonitorTag>();
    for (const m of live.monitors) for (const t of m.tags) byID.set(t.id, t);
    return [...byID.values()].sort((a, b) => a.name.localeCompare(b.name));
  });

  // Groups whose members are hidden, remembered in this browser.
  const collapsedKey = 'eyrie.collapsed-groups';
  let collapsed = $state(new Set<number>(readCollapsed()));
  function readCollapsed(): number[] {
    try {
      const v: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? '[]');
      return Array.isArray(v) ? v.filter((x): x is number => typeof x === 'number') : [];
    } catch {
      return [];
    }
  }
  function toggle(id: number) {
    const next = new Set(collapsed);
    if (!next.delete(id)) next.add(id);
    collapsed = next;
    try {
      localStorage.setItem(collapsedKey, JSON.stringify([...next]));
    } catch {
      // Not remembered, but still collapsed until the page reloads.
    }
  }

  // Groups first, each followed by its members unless it is collapsed; then
  // monitors outside any group. Filtered by a tag, only the monitors with
  // it, without nesting.
  const rows = $derived.by(() => {
    const out: { m: Monitor; depth: number; members: number }[] = [];
    if (tagFilter) {
      return live.monitors.filter((m) => m.tags.some((t) => t.id === tagFilter)).map((m) => ({ m, depth: 0, members: 0 }));
    }
    const byParent = new Map<number, Monitor[]>();
    for (const m of live.monitors) {
      const p = m.parent_id ?? 0;
      byParent.set(p, [...(byParent.get(p) ?? []), m]);
    }
    const ids = new Set(live.monitors.map((m) => m.id));
    const walk = (parent: number, depth: number) => {
      for (const m of byParent.get(parent) ?? []) {
        out.push({ m, depth, members: byParent.get(m.id)?.length ?? 0 });
        if (depth < 10 && !collapsed.has(m.id)) walk(m.id, depth + 1);
      }
    };
    walk(0, 0);
    // Members whose group is not in the list (it should not happen) still show.
    for (const [p, ms] of byParent) if (p !== 0 && !ids.has(p)) for (const m of ms) out.push({ m, depth: 0, members: 0 });
    return out;
  });

  const counts = $derived.by(() => {
    const c = { up: 0, down: 0, pending: 0, maintenance: 0, paused: 0 };
    for (const m of live.monitors) {
      if (!m.active) c.paused++;
      else if (m.status.status === 'maintenance') c.maintenance++;
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
    {#if counts.maintenance}<span><StatusDot status="maintenance" /> {counts.maintenance} in maintenance</span>{/if}
    <span><StatusDot status="unknown" active={false} /> {counts.paused} paused</span>
  </p>
  {#if allTags.length}
    <div class="filter row" role="group" aria-label="Filter by tag">
      <button class:primary={!tagFilter} onclick={() => (tagFilter = 0)}>All</button>
      {#each allTags as t (t.id)}
        <button class:primary={tagFilter === t.id} onclick={() => (tagFilter = tagFilter === t.id ? 0 : t.id)}
          ><TagChip name={t.name} color={t.color} /></button
        >
      {/each}
    </div>
  {/if}
  <div class="card list">
    {#each rows as { m, depth, members } (m.id)}
      <div class="item">
        {#if members}
          <button
            class="toggle"
            style:left="{0.4 + depth * 1.25}rem"
            aria-expanded={!collapsed.has(m.id)}
            aria-label="{collapsed.has(m.id) ? 'Show' : 'Hide'} the members of {m.name}"
            title={collapsed.has(m.id) ? 'Show members' : 'Hide members'}
            onclick={() => toggle(m.id)}
            ><svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"
              ><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg
            ></button
          >
        {/if}
        <a class="monitor" href="/monitors/{m.id}" onclick={link} style:padding-left="{2 + depth * 1.25}rem">
          <span class="name">
            <StatusDot status={m.status.status} active={m.active} />
            <span>{m.name}</span>
            <span class="muted type">{m.type}</span>
            {#if members && collapsed.has(m.id)}<span class="muted type">{members} {members === 1 ? 'member' : 'members'}</span>{/if}
            {#each m.tags as t (t.id)}<TagChip name={t.name} color={t.color} value={t.value} />{/each}
          </span>
          <span class="bar"><BeatBar beats={m.beats} /></span>
          <span class="figures">
            <span title="Uptime, last 24 hours">{percent(m.uptime['24h'])}</span>
            <span class="muted" title="Last response time">{millis(m.status.latency_ms)}</span>
          </span>
        </a>
      </div>
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
  .filter {
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-bottom: 0.75rem;
  }
  .filter button {
    padding: 0.2rem 0.5rem;
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
  .item {
    position: relative;
  }
  .item:last-child .monitor {
    border-bottom: none;
  }
  /* Over the row's left padding, so the row stays one link. */
  .toggle {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    z-index: 1;
    width: 1.5rem;
    height: 1.5rem;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: none;
    color: var(--muted);
    line-height: 0;
  }
  .toggle svg {
    transition: transform 0.15s;
  }
  .toggle[aria-expanded='true'] svg {
    transform: rotate(90deg);
  }
  .toggle:hover {
    color: var(--fg);
    background: var(--border);
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
    /* Level with the name, on the row's first line. */
    .toggle {
      top: 0.6rem;
      transform: none;
    }
  }
</style>
