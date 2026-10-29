<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import type { Status, StatusChange } from '../lib/api.ts';
  import { ago, daysUntil, millis, percent } from '../lib/format.ts';
  import { live, onBeat } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { certSoonDays, overview } from '../lib/overview.ts';
  import { api } from '../lib/session.svelte.ts';

  // Beside the list: the figures for every monitor, and what needs attention.
  const o = $derived(overview(live.monitors));
  const down = $derived(live.monitors.filter((m) => m.active && m.status.status === 'down'));
  const pending = $derived(live.monitors.filter((m) => m.active && m.status.status === 'pending'));
  const maintenance = $derived(live.monitors.filter((m) => m.active && m.status.status === 'maintenance'));

  // The status bar's parts, in this order, with how many of each.
  const parts = $derived(
    (['up', 'down', 'pending', 'maintenance', 'unknown'] as Status[])
      .map((s) => ({ status: s, n: o.counts[s] }))
      .filter((p) => p.n > 0),
  );

  // The latest status changes, and new ones as they happen.
  const recentSize = 6;
  let recent = $state<StatusChange[]>([]);
  function monitorName(id: number): string {
    return live.monitors.find((m) => m.id === id)?.name ?? `Monitor ${id}`;
  }
  onMount(() => {
    api.statusChanges().then(
      (p) => (recent = p.slice(0, recentSize)),
      () => {
        // The overview works without them.
      },
    );
    return onBeat((b) => {
      if (b.important) recent = [{ ...b, id: 0 }, ...recent].slice(0, recentSize);
    });
  });
</script>

<h2>Overview</h2>
{#if !live.loaded}
  <p class="muted">Loading…</p>
{:else}
  <section class="card">
    <div class="tiles">
      <div class="tile">
        <span class="label">Monitors</span>
        <span class="value">{o.active}</span>
        <span class="muted small">{o.counts.paused ? `${o.counts.paused} paused` : 'active'}</span>
      </div>
      <div class="tile">
        <span class="label">Uptime, 24 hours</span>
        <span class="value">{percent(o.uptimeDay)}</span>
        <span class="muted small">average</span>
      </div>
      <div class="tile">
        <span class="label">Uptime, 30 days</span>
        <span class="value">{percent(o.uptimeMonth)}</span>
        <span class="muted small">average</span>
      </div>
      <div class="tile">
        <span class="label">Response time</span>
        <span class="value">{millis(o.latencyDay)}</span>
        <span class="muted small">average, 24 hours</span>
      </div>
      <div class="tile">
        <span class="label">Certificates</span>
        <span class="value" class:warn={o.certsSoon > 0}>{o.certsSoon}</span>
        <span class="muted small">expiring within {certSoonDays} days</span>
      </div>
    </div>
    {#if o.active}
      <div class="bar" role="img" aria-label={parts.map((p) => `${p.n} ${p.status}`).join(', ')}>
        {#each parts as p (p.status)}
          <span class="part {p.status}" style:flex-grow={p.n} title="{p.n} {p.status}"></span>
        {/each}
      </div>
      <ul class="legend">
        {#each parts as p (p.status)}
          <li><StatusDot status={p.status} /> {p.n} {p.status}</li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if down.length === 0 && pending.length === 0}
    <section class="card">
      <p class="calm"><StatusDot status="up" /> Nothing is down.</p>
      <p class="muted calm">Choose a monitor or group in the list to see it here.</p>
    </section>
  {:else}
    {#each [{ title: 'Down', list: down }, { title: 'Pending', list: pending }] as section (section.title)}
      {#if section.list.length}
        <section class="card">
          <h3>{section.title}</h3>
          <ul class="rows">
            {#each section.list as m (m.id)}
              <li>
                <StatusDot status={m.status.status} />
                <a href="/monitors/{m.id}" onclick={link}>{m.name}</a>
                <span class="muted">{m.status.message}{m.status.since ? ` · ${ago(m.status.since)}` : ''}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    {/each}
  {/if}
  {#if maintenance.length}
    <section class="card">
      <h3>In maintenance</h3>
      <ul class="rows">
        {#each maintenance as m (m.id)}
          <li><StatusDot status="maintenance" /> <a href="/monitors/{m.id}" onclick={link}>{m.name}</a> <span class="muted">{m.status.maintenance}</span></li>
        {/each}
      </ul>
    </section>
  {/if}

  <div class="grid">
    <section class="card">
      <h3>Recent events</h3>
      {#if recent.length}
        <ul class="rows">
          {#each recent as c (`${c.id}-${c.monitor_id}-${c.at}`)}
            <li>
              <StatusDot status={c.status} />
              <a href="/monitors/{c.monitor_id}" onclick={link}>{monitorName(c.monitor_id)}</a>
              <span class="muted end">{ago(c.at)}</span>
            </li>
          {/each}
        </ul>
        <p class="more"><a href="/events" onclick={link}>All events</a></p>
      {:else}
        <p class="muted calm">Nothing yet.</p>
      {/if}
    </section>
    <section class="card">
      <h3>Lowest uptime, 30 days</h3>
      {#if o.lowest.length}
        <ul class="rows">
          {#each o.lowest as m (m.id)}
            <li>
              <StatusDot status={m.status.status} />
              <a href="/monitors/{m.id}" onclick={link}>{m.name}</a>
              <span class="end">{percent(m.uptime['30d'])}</span>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="muted calm">Every monitor was up the whole time.</p>
      {/if}
    </section>
    <section class="card">
      <h3>Slowest, 24 hours</h3>
      {#if o.slowest.length}
        <ul class="rows">
          {#each o.slowest as m (m.id)}
            <li>
              <StatusDot status={m.status.status} />
              <a href="/monitors/{m.id}" onclick={link}>{m.name}</a>
              <span class="end">{millis(m.uptime.avg_latency_24h_ms)}</span>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="muted calm">No response times yet.</p>
      {/if}
    </section>
    {#if o.certs.length}
      <section class="card">
        <h3>Certificates expiring next</h3>
        <ul class="rows">
          {#each o.certs as m (m.id)}
            {@const days = daysUntil(m.status.cert_expires ?? '')}
            <li>
              <StatusDot status={m.status.status} />
              <a href="/monitors/{m.id}" onclick={link}>{m.name}</a>
              <span class="end" class:warn={days < certSoonDays}>{days < 0 ? 'expired' : `${days} days`}</span>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  </div>
{/if}

<style>
  h2 {
    margin-top: 0;
  }
  h3 {
    margin: 0 0 0.75rem;
    font-size: 1rem;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(8.5rem, 1fr));
    gap: 1rem;
  }
  .tile {
    display: grid;
    gap: 0.15rem;
    align-content: start;
  }
  .label {
    color: var(--muted);
    font-size: 0.85rem;
  }
  .value {
    font-size: 1.6rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.2;
  }
  .small {
    font-size: 0.8rem;
  }
  .warn {
    color: var(--pending);
  }
  .bar {
    display: flex;
    gap: 2px;
    height: 0.6rem;
    margin-top: 1.25rem;
    border-radius: 4px;
    overflow: hidden;
  }
  .part {
    flex-basis: 0;
    min-width: 4px;
    background: var(--unknown);
  }
  .part.up {
    background: var(--up);
  }
  .part.down {
    background: var(--down);
  }
  .part.pending {
    background: var(--pending);
  }
  .part.maintenance {
    background: var(--maintenance);
  }
  .legend {
    list-style: none;
    margin: 0.6rem 0 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 1rem;
    font-size: 0.85rem;
    color: var(--muted);
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr));
    gap: 0 1rem;
  }
  .rows {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.4rem;
  }
  .rows li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    overflow-wrap: anywhere;
  }
  .end {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
  }
  .calm {
    margin: 0;
  }
  .calm + .calm {
    margin-top: 0.5rem;
  }
  .more {
    margin: 0.75rem 0 0;
    font-size: 0.9rem;
  }
</style>
