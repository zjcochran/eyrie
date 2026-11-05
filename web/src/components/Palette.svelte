<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Jump to anything: ⌘K or Ctrl+K, or the search button, opens it. -->
<script lang="ts">
  import { live } from '../lib/live.svelte.ts';
  import { go } from '../lib/nav.svelte.ts';

  let { admin, open = $bindable(false) }: { admin: boolean; open?: boolean } = $props();

  let query = $state('');
  let active = $state(0);
  let input = $state<HTMLInputElement>();

  interface Entry {
    section: string;
    label: string;
    hint: string;
    href: string;
    tone: string;
  }

  const pages: Entry[] = [
    { section: 'Pages', label: 'Monitors', hint: '', href: '/', tone: 'page' },
    { section: 'Pages', label: 'Events', hint: '', href: '/events', tone: 'page' },
    { section: 'Pages', label: 'Maintenance', hint: '', href: '/maintenance', tone: 'page' },
    { section: 'Pages', label: 'Status pages', hint: '', href: '/status-pages', tone: 'page' },
    { section: 'Pages', label: 'Settings › Appearance', hint: '', href: '/settings/appearance', tone: 'page' },
  ];
  const actions: Entry[] = [
    { section: 'Actions', label: 'Add a monitor', hint: '', href: '/monitors/new', tone: 'action' },
    { section: 'Actions', label: 'Discover containers and devices', hint: '', href: '/discover', tone: 'action' },
    { section: 'Actions', label: 'Add a status page', hint: '', href: '/status-pages/new', tone: 'action' },
    { section: 'Actions', label: 'Add a maintenance window', hint: '', href: '/maintenance', tone: 'action' },
  ];

  const entries = $derived.by(() => {
    const q = query.trim().toLowerCase();
    const monitors: Entry[] = live.monitors.map((m) => ({
      section: 'Monitors',
      label: m.name,
      hint: !m.active ? 'paused' : m.status.status,
      href: `/monitors/${m.id}`,
      tone: !m.active ? 'paused' : m.status.status,
    }));
    const groups: Entry[] = live.groups.map((g) => ({ section: 'Groups', label: g.name, hint: 'group', href: `/groups/${g.id}`, tone: 'page' }));
    const all = [...monitors, ...groups, ...(admin ? actions : []), ...pages];
    const hits = q ? all.filter((e) => e.label.toLowerCase().includes(q)) : [...monitors.filter((m) => m.tone === 'down'), ...(admin ? actions : []), ...pages];
    return hits.slice(0, 12);
  });
  const sections = $derived([...new Set(entries.map((e) => e.section))]);

  $effect(() => {
    if (open) {
      query = '';
      active = 0;
      queueMicrotask(() => input?.focus());
    }
  });

  function pick(e: Entry | undefined) {
    if (!e) return;
    open = false;
    go(e.href);
  }

  function key(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      open = !open;
      return;
    }
    if (!open) return;
    if (e.key === 'Escape') {
      open = false;
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      active = Math.min(active + 1, entries.length - 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      active = Math.max(active - 1, 0);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      pick(entries[active]);
    }
  }
</script>

<svelte:window onkeydown={key} />

{#if open}
  <!-- Escape closes it from the keyboard (key, above). -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="backdrop" role="presentation" onclick={(e) => e.target === e.currentTarget && (open = false)}>
    <div class="dialog" role="dialog" aria-modal="true" aria-label="Jump to">
      <div class="search">
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"
          ><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg
        >
        <input
          bind:this={input}
          bind:value={query}
          oninput={() => (active = 0)}
          placeholder="Search monitors, groups, pages and actions…"
          aria-label="Search"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant={entries.length ? `palette-${active}` : undefined}
        />
        <kbd>esc</kbd>
      </div>
      <div class="list" id="palette-list" role="listbox" aria-label="Results">
        {#each sections as s (s)}
          <div class="section" role="presentation">{s}</div>
          {#each entries as e, i (e.href + e.label)}
            {#if e.section === s}
              <div
                id="palette-{i}"
                class="entry"
                class:on={i === active}
                role="option"
                tabindex="-1"
                aria-selected={i === active}
                onclick={() => pick(e)}
                onkeydown={(k) => k.key === 'Enter' && pick(e)}
                onmouseenter={() => (active = i)}
              >
                <span class="chip {e.tone}"></span>
                <span class="label">{e.label}</span>
                <span class="hint">{e.hint}</span>
              </div>
            {/if}
          {/each}
        {:else}
          <p class="none">Nothing matches.</p>
        {/each}
      </div>
      <div class="keys"><span>↑↓ move</span><span>↵ open</span><span>esc close</span></div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    justify-content: center;
    align-items: flex-start;
    padding: 12vh 1rem 1rem;
    background: color-mix(in srgb, var(--bg) 45%, transparent);
    backdrop-filter: blur(4px);
  }
  .dialog {
    width: min(40rem, 100%);
    overflow: hidden;
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) * 1.2);
    background: color-mix(in srgb, var(--surface) 88%, transparent);
    backdrop-filter: blur(28px) saturate(150%);
    box-shadow: 0 40px 80px -20px rgb(0 0 0 / 0.45);
  }
  .search {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.9rem 1.1rem;
    border-bottom: 1px solid var(--border);
    color: var(--chart, var(--accent));
  }
  .search input {
    flex: 1;
    border: 0;
    padding: 0;
    background: transparent;
    color: var(--fg);
    font-size: 1.1rem;
    outline: none;
  }
  kbd,
  .keys {
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.72rem;
    color: var(--muted);
  }
  kbd {
    padding: 0.15rem 0.45rem;
    border-radius: 6px;
    background: var(--surface-2, var(--bg));
  }
  .list {
    max-height: 26rem;
    overflow-y: auto;
    padding: 0.4rem;
  }
  .section {
    padding: 0.6rem 0.7rem 0.3rem;
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.7rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .entry {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0 0.7rem;
    min-height: 2.75rem;
    border-radius: 12px;
    cursor: pointer;
  }
  .entry.on {
    background: color-mix(in srgb, var(--fg) 8%, transparent);
  }
  .chip {
    width: 0.6rem;
    height: 0.6rem;
    flex: none;
    border-radius: 50%;
    background: var(--unknown);
  }
  .chip.up {
    background: var(--up);
  }
  .chip.down {
    background: var(--down);
    box-shadow: 0 0 10px var(--down);
  }
  .chip.pending {
    background: var(--pending);
  }
  .chip.maintenance {
    background: var(--maintenance);
  }
  .chip.action {
    border-radius: 3px;
    background: var(--chart, var(--accent));
  }
  .chip.page {
    border-radius: 3px;
    background: var(--muted);
  }
  .label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .hint {
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.75rem;
    color: var(--muted);
  }
  .none {
    padding: 1.5rem;
    margin: 0;
    text-align: center;
    color: var(--muted);
  }
  .keys {
    display: flex;
    gap: 1.25rem;
    padding: 0.6rem 1.1rem;
    border-top: 1px solid var(--border);
  }
</style>
