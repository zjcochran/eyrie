<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import SeriesChart from './SeriesChart.svelte';
  import { type Storage, message } from '../lib/api.ts';
  import { ago, size } from '../lib/format.ts';
  import { api } from '../lib/session.svelte.ts';
  import { level, limit, series } from '../lib/storage.ts';

  // A NAS's storage on its SNMP storage monitor's page: its volumes, pools,
  // disks and system as last read, and how full its volumes and how hot its
  // disks were over time. version changes with each new check; ignore,
  // for admins, stops or starts alerting on a volume's space.
  let {
    id,
    settings,
    version,
    ignore,
  }: {
    id: number;
    settings: Record<string, unknown>;
    version: number;
    ignore?: (name: string, ignore: boolean) => void;
  } = $props();

  let storage = $state<Storage | null>(null);
  let error = $state('');
  let period = $state<'24h' | '7d' | '30d'>('24h');
  let now = $state(Date.now());
  const days = { '24h': 1, '7d': 7, '30d': 30 };
  const periods = ['24h', '7d', '30d'] as const;

  async function load() {
    try {
      storage = await api.storage(id, days[period]);
      now = Date.now();
      error = '';
    } catch (e) {
      error = message(e);
    }
  }
  $effect(() => {
    void id;
    void version;
    void period;
    load();
  });

  const d = $derived(storage?.device ?? null);
  const maxPct = $derived(limit(settings, 'max_percent', 90));
  const maxC = $derived(limit(settings, 'max_celsius', 50));
  const ignored = $derived(Array.isArray(settings.ignore_volumes) ? (settings.ignore_volumes as string[]) : []);
  const temp = (name: string) => d?.temps?.find((t) => t.name === name)?.celsius;
  // Disks by name, with their health and temperature, from either table.
  const disks = $derived.by(() => {
    const names = [...(d?.disks ?? []).map((x) => x.name)];
    for (const t of d?.temps ?? []) if (!names.includes(t.name)) names.push(t.name);
    return names.map((name) => ({ name, health: d?.disks?.find((x) => x.name === name), celsius: temp(name) }));
  });
  const volumeLines = $derived(series(storage?.history ?? [], 'volumes'));
  const tempLines = $derived(series(storage?.history ?? [], 'temps'));
  const from = $derived(now - days[period] * 86400000);
  const pct = (v: number) => `${Math.round(v)}%`;
  const deg = (v: number) => `${Math.round(v)} °C`;
</script>

{#if error}<p class="notice error" role="alert">{error}</p>{/if}

{#if storage && !d}
  <section class="card"><h2>Storage</h2><p class="muted">Nothing read yet: the first check fills this in.</p></section>
{:else if d}
  <section class="card">
    <div class="row between">
      <h2>Volumes</h2>
      <span class="muted small">{[d.description || d.name, storage?.at ? `read ${ago(storage.at, now)}` : ''].filter(Boolean).join(' · ')}</span>
    </div>
    {#each d.volumes ?? [] as v (v.name)}
      {@const off = ignored.includes(v.name)}
      {@const lv = off ? 'off' : level(v.percent, maxPct, 10)}
      <div class="vol">
        <div class="row between">
          <strong class="mono">{v.name}</strong>
          <span class="row">
            {#if ignore}<button class="toggle" onclick={() => ignore?.(v.name, !off)}>{off ? 'Alert on its space' : 'Ignore its space'}</button>{/if}
            <span class="mono {lv}">{v.percent.toFixed(1)}%</span>
          </span>
        </div>
        <div class="bar" role="meter" aria-label="{v.name} used" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(v.percent)}>
          <span class={lv} style:width="{Math.min(100, v.percent)}%"></span>{#if !off}<b style:left="{maxPct}%"></b>{/if}
        </div>
        <div class="muted small">
          {size(v.total - v.free)} used · {size(v.free)} free of {size(v.total)}{v.inode_percent >= 0 ? ` · inodes ${v.inode_percent}% used` : ''}{off
            ? ' · space ignored'
            : ''}
        </div>
      </div>
    {:else}
      <p class="muted">The device reports no volumes.</p>
    {/each}
  </section>

  <div class="pair">
    <section class="card">
      <h2>Disks</h2>
      {#if disks.length}
        <div class="table-wrap">
          <table>
            <thead><tr><th>Disk</th><th>Health</th><th class="num">Temperature</th></tr></thead>
            <tbody>
              {#each disks as x (x.name)}
                <tr>
                  <td>{x.name}</td>
                  <td class:bad={x.health && !x.health.ok}>{x.health ? x.health.state : '—'}</td>
                  <td class="num mono {x.celsius === undefined ? '' : level(x.celsius, maxC, 5)}">{x.celsius === undefined ? '—' : deg(x.celsius)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p class="muted">The device reports no disks.</p>
      {/if}
    </section>

    <section class="card">
      <h2>Health</h2>
      <ul class="states">
        {#each d.pools ?? [] as p (p.name)}
          <li><i class={p.ok ? (p.repairing ? 'warn' : 'ok') : 'bad'}></i><span class="name">{p.name}</span><span class="state muted">{p.state}</span></li>
        {/each}
        {#if d.system}
          <li><i class={d.system.ok ? 'ok' : 'bad'}></i><span class="name">System</span><span class="state muted">{d.system.state}</span></li>
        {/if}
        {#each d.commands ?? [] as c (c.name)}
          <li><i class={c.status === 0 ? 'ok' : 'bad'}></i><span class="name mono">{c.name}</span><span class="state muted">{c.output || `exit status ${c.status}`}</span></li>
        {/each}
      </ul>
      {#if !d.pools?.length && !d.system && !d.commands?.length}<p class="muted">The device reports no volume or system health.</p>{/if}
    </section>
  </div>

  <section class="card">
    <div class="row between">
      <h2>Volume use</h2>
      <div class="row" role="group" aria-label="Period">
        {#each periods as p (p)}<button class:primary={period === p} onclick={() => (period = p)}>{p}</button>{/each}
      </div>
    </div>
    <SeriesChart series={volumeLines} {from} to={now} label="Volume use" format={pct} top={100} limit={maxPct} empty="No volume use in this period." />
  </section>

  <section class="card">
    <h2>Disk temperature</h2>
    <SeriesChart series={tempLines} {from} to={now} label="Disk temperature" format={deg} floor={20} limit={maxC} empty="No disk temperatures in this period." />
  </section>
{/if}

<style>
  .between {
    justify-content: space-between;
  }
  .vol {
    display: grid;
    gap: 0.3rem;
    padding: 0.6rem 0;
    border-top: 1px solid var(--border);
  }
  .vol:first-of-type {
    border-top: 0;
  }
  .bar {
    position: relative;
    height: 0.6rem;
    border-radius: 999px;
    background: color-mix(in srgb, var(--muted) 18%, transparent);
    overflow: hidden;
  }
  .bar span {
    display: block;
    height: 100%;
    border-radius: 999px;
    background: var(--up);
  }
  .bar span.warn {
    background: var(--pending);
  }
  .bar span.bad {
    background: var(--down);
  }
  /* A quiet action beside a volume's use. */
  .toggle {
    padding: 0.1rem 0.5rem;
    font-size: 0.8rem;
    border-color: transparent;
    background: none;
    color: var(--muted);
  }
  .toggle:hover {
    color: var(--text, currentColor);
    border-color: var(--border);
  }
  .bar span.off {
    background: var(--muted);
    opacity: 0.6;
  }
  .bar b {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 2px;
    background: var(--text, currentColor);
    opacity: 0.45;
  }
  .warn {
    color: var(--pending);
  }
  .bad {
    color: var(--down);
  }
  .off {
    color: var(--muted);
  }
  .pair {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
  }
  @media (min-width: 56rem) {
    .pair {
      grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    }
  }
  .num {
    text-align: right;
  }
  .states {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.5rem;
  }
  .states li {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
  }
  .states i {
    flex: none;
    width: 0.6rem;
    height: 0.6rem;
    border-radius: 50%;
    background: var(--up);
  }
  .states i.warn {
    background: var(--pending);
  }
  .states i.bad {
    background: var(--down);
  }
  .name {
    flex: none;
  }
  /* A long state (the system's, a command's output) wraps on the right. */
  .state {
    flex: 1;
    min-width: 0;
    text-align: right;
  }
  .small {
    font-size: 0.85rem;
  }
</style>
