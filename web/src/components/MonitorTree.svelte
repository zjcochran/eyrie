<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import BeatBar from './BeatBar.svelte';
  import StatusDot from './StatusDot.svelte';
  import TagChip from './TagChip.svelte';
  import { message, type MonitorTag } from '../lib/api.ts';
  import { percent } from '../lib/format.ts';
  import { live, loadMonitors } from '../lib/live.svelte.ts';
  import { go, link } from '../lib/nav.svelte.ts';
  import { api } from '../lib/session.svelte.ts';
  import { buildTree, canDrop, type Drop, flatten, groupStatus, type Item, key, layout, move, type Row } from '../lib/tree.ts';

  /** selected: the key of the monitor (m<id>) or group (g<id>) shown beside the list. */
  let { selected = '', admin = false }: { selected?: string; admin?: boolean } = $props();

  // The tag the list is filtered by; 0 for none.
  let tagFilter = $state(0);
  const allTags = $derived.by(() => {
    const byID = new Map<number, MonitorTag>();
    for (const m of live.monitors) for (const t of m.tags) byID.set(t.id, t);
    return [...byID.values()].sort((a, b) => a.name.localeCompare(b.name));
  });

  // Groups whose contents are hidden, remembered in this browser.
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
  function setCollapsed(id: number, hide: boolean) {
    const next = new Set(collapsed);
    if (hide) next.add(id);
    else next.delete(id);
    collapsed = next;
    try {
      localStorage.setItem(collapsedKey, JSON.stringify([...next]));
    } catch {
      // Not remembered, but still collapsed until the page reloads.
    }
  }

  // A move being saved: shown until the list has reloaded.
  let moved = $state<Item[] | null>(null);
  const tree = $derived(moved ?? buildTree(live.groups, live.monitors));
  // Filtered by a tag: only the monitors with it, without groups.
  const rows = $derived<Row[]>(
    tagFilter
      ? live.monitors
          .filter((m) => m.tags.some((t) => t.id === tagFilter))
          .map((m): Row => ({ item: { kind: 'monitor', monitor: m }, depth: 0, size: 0 }))
      : flatten(tree, collapsed),
  );

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

  let error = $state('');

  // A new group: just a name, at the end of the top level.
  let naming = $state(false);
  let newName = $state('');
  let creating = $state(false);
  async function createGroup(event: SubmitEvent) {
    event.preventDefault();
    if (!newName.trim() || creating) return;
    creating = true;
    error = '';
    try {
      const g = await api.createGroup({ name: newName.trim(), parent_id: null, alert: 'none', channels: [], alert_monitors: [] });
      await loadMonitors();
      naming = false;
      newName = '';
      go(`/groups/${g.id}`);
    } catch (e) {
      error = message(e);
    } finally {
      creating = false;
    }
  }

  // Dragging: admins move monitors and groups, before or after another
  // item, or into a group (dropped on its middle), or to the end of the top
  // level (dropped below the list).
  const canDrag = $derived(admin && !tagFilter);
  let dragged = $state('');
  let drop = $state<Drop | null>(null);

  function dropAt(e: DragEvent): Drop | null {
    const el = (e.target as Element | null)?.closest<HTMLElement>('[data-key]');
    if (!el) return null;
    const target = el.dataset.key ?? '';
    if (target === '') return { target: '', where: 'inside' };
    const r = el.getBoundingClientRect();
    const y = (e.clientY - r.top) / r.height;
    if (target.startsWith('g')) return { target, where: y < 0.25 ? 'before' : y > 0.75 ? 'after' : 'inside' };
    return { target, where: y < 0.5 ? 'before' : 'after' };
  }

  function dnd(node: HTMLElement) {
    const start = (e: DragEvent) => {
      const k = (e.target as Element | null)?.closest<HTMLElement>('[data-key]')?.dataset.key ?? '';
      if (!canDrag || !k) {
        e.preventDefault();
        return;
      }
      dragged = k;
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', k);
      }
    };
    const over = (e: DragEvent) => {
      if (!dragged) return;
      const d = dropAt(e);
      if (!d || !canDrop(tree, dragged, d)) {
        drop = null;
        return;
      }
      e.preventDefault(); // allows the drop
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
      drop = d;
    };
    const end = () => {
      dragged = '';
      drop = null;
    };
    const dropped = (e: DragEvent) => {
      e.preventDefault();
      const d = drop;
      const k = dragged;
      end();
      if (d && k) void save(k, d);
    };
    node.addEventListener('dragstart', start);
    node.addEventListener('dragover', over);
    node.addEventListener('drop', dropped);
    node.addEventListener('dragend', end);
    return {
      destroy() {
        node.removeEventListener('dragstart', start);
        node.removeEventListener('dragover', over);
        node.removeEventListener('drop', dropped);
        node.removeEventListener('dragend', end);
      },
    };
  }

  async function save(k: string, d: Drop) {
    const next = move(tree, k, d);
    if (next === tree) return;
    // Dropped into a hidden group: show where it went.
    if (d.where === 'inside' && d.target.startsWith('g')) setCollapsed(Number(d.target.slice(1)), false);
    moved = next;
    error = '';
    try {
      await api.arrange(layout(next));
      await loadMonitors();
    } catch (e) {
      error = message(e);
    } finally {
      moved = null;
    }
  }

  const href = (it: Item) => (it.kind === 'group' ? `/groups/${it.group.id}` : `/monitors/${it.monitor.id}`);
</script>

<div class="head">
  <h1>Monitors</h1>
  {#if admin}
    <div class="row">
      <button onclick={() => (naming = !naming)} aria-expanded={naming}>New group</button>
      <a class="button primary" href="/monitors/new" onclick={link}>Add a monitor</a>
    </div>
  {/if}
</div>

{#if naming}
  <form class="new-group row" onsubmit={createGroup}>
    <!-- svelte-ignore a11y_autofocus -->
    <input bind:value={newName} maxlength="100" placeholder="Group name" aria-label="New group's name" autofocus required />
    <button class="primary" disabled={creating || !newName.trim()}>Create</button>
    <button type="button" onclick={() => ((naming = false), (newName = ''))}>Cancel</button>
  </form>
{/if}

{#if live.error}<p class="notice error">{live.error}</p>{/if}
{#if error}<p class="notice error" role="status">{error}</p>{/if}

{#if !live.loaded}
  <p class="muted">Loading…</p>
{:else if live.monitors.length === 0 && live.groups.length === 0}
  <div class="card">
    <p>There are no monitors yet.{#if admin} <a href="/monitors/new" onclick={link}>Add the first one.</a>{/if}</p>
  </div>
{:else}
  <p class="summary">
    <span><StatusDot status="up" /> {counts.up}</span>
    <span><StatusDot status="down" /> {counts.down}</span>
    <span><StatusDot status="pending" /> {counts.pending}</span>
    {#if counts.maintenance}<span><StatusDot status="maintenance" /> {counts.maintenance}</span>{/if}
    <span><StatusDot status="unknown" active={false} /> {counts.paused}</span>
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
  {#if canDrag}<p class="muted hint">Drag to reorder, or onto a group to move into it.</p>{/if}
  <div class="card list" use:dnd>
    {#each rows as { item, depth, size } (key(item))}
      {@const k = key(item)}
      <div
        class="item"
        class:dragging={dragged === k}
        class:before={drop?.target === k && drop.where === 'before'}
        class:after={drop?.target === k && drop.where === 'after'}
        class:inside={drop?.target === k && drop.where === 'inside'}
      >
        {#if item.kind === 'group' && size}
          {@const open = !collapsed.has(item.group.id)}
          <button
            class="toggle"
            style:left="{0.3 + depth * 1.1}rem"
            aria-expanded={open}
            aria-label="{open ? 'Hide' : 'Show'} what is in {item.group.name}"
            title={open ? 'Hide contents' : 'Show contents'}
            onclick={() => setCollapsed(item.group.id, open)}
            ><svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"
              ><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg
            ></button
          >
        {/if}
        <a
          class="entry"
          class:group={item.kind === 'group'}
          href={href(item)}
          onclick={link}
          data-key={k}
          draggable={canDrag}
          aria-current={selected === k ? 'page' : undefined}
          style:padding-left="{1.9 + depth * 1.1}rem"
        >
          {#if item.kind === 'group'}
            {@const s = groupStatus(item)}
            <span class="line">
              <StatusDot status={s.status} />
              <span class="name">{item.group.name}</span>
              <span class="muted small">{s.total} {s.total === 1 ? 'monitor' : 'monitors'}{s.down ? ` · ${s.down} down` : ''}</span>
            </span>
          {:else}
            {@const m = item.monitor}
            <span class="line">
              <StatusDot status={m.status.status} active={m.active} />
              <span class="name">{m.name}</span>
              <span class="pct" title="Uptime, last 24 hours">{percent(m.uptime['24h'])}</span>
            </span>
            <span class="bar"><BeatBar beats={m.beats} slots={30} /></span>
          {/if}
        </a>
      </div>
    {/each}
    {#if dragged && !tagFilter}
      <div class="end" class:inside={drop?.target === '' && drop.where === 'inside'} data-key="">Drop here for the top level</div>
    {/if}
  </div>
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }
  .head h1 {
    margin: 0;
  }
  .new-group {
    max-width: none;
    margin-bottom: 0.75rem;
  }
  .new-group input {
    flex: 1;
    min-width: 8rem;
  }
  .summary {
    display: flex;
    flex-wrap: wrap;
    gap: 0.9rem;
    margin: 0 0 0.5rem;
  }
  .filter {
    gap: 0.35rem;
    margin-bottom: 0.5rem;
  }
  .filter button {
    padding: 0.15rem 0.45rem;
  }
  .hint {
    font-size: 0.8rem;
    margin: 0 0 0.4rem;
  }
  .list {
    padding: 0;
    overflow: hidden;
  }
  .item {
    position: relative;
    border-bottom: 1px solid var(--border);
  }
  .item:last-of-type {
    border-bottom: none;
  }
  .entry {
    display: grid;
    gap: 0.3rem;
    padding: 0.5rem 0.65rem;
    color: var(--fg);
    text-decoration: none;
  }
  .entry:hover {
    background: var(--bg);
  }
  .entry[aria-current='page'] {
    background: var(--bg);
    box-shadow: inset 3px 0 0 var(--accent);
  }
  .entry.group .name {
    font-weight: 600;
  }
  .line {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .small {
    font-size: 0.8rem;
  }
  .pct {
    margin-left: auto;
    padding-left: 0.5rem;
    font-variant-numeric: tabular-nums;
    font-size: 0.85rem;
  }
  .bar {
    min-width: 0;
  }
  .dragging {
    opacity: 0.45;
  }
  /* Where a drop goes: a line before or after a row, or a ring round a group. */
  .before {
    box-shadow: inset 0 2px 0 var(--accent);
  }
  .after {
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  .inside .entry,
  .end.inside {
    box-shadow: inset 0 0 0 2px var(--accent);
  }
  .end {
    padding: 0.6rem;
    text-align: center;
    color: var(--muted);
    font-size: 0.85rem;
    border-top: 1px dashed var(--border);
  }
  /* Over the row's left padding, so the row stays one link. */
  .toggle {
    position: absolute;
    top: 0.45rem;
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
</style>
