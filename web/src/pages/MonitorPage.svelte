<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import Chart from '../components/Chart.svelte';
  import Notice from '../components/Notice.svelte';
  import TagChip from '../components/TagChip.svelte';
  import { type Beat, type Count, type Monitor, message } from '../lib/api.ts';
  import { normalize } from '../lib/appearance.ts';
  import { band, dayOf, lastDays } from '../lib/bento.ts';
  import { ago, daysUntil, duration, millis, percent, type Point } from '../lib/format.ts';
  import { live, onBeat } from '../lib/live.svelte.ts';
  import { go, link } from '../lib/nav.svelte.ts';
  import { api, app } from '../lib/session.svelte.ts';

  let { id }: { id: number } = $props();

  const admin = $derived(app.session?.user?.role === 'admin');
  let monitor = $state<Monitor | null>(null);
  let beats = $state<Beat[]>([]); // newest first
  let period = $state<'24h' | '7d' | '30d'>('24h');
  let points = $state<Point[]>([]);
  // A system-resources monitor's CPU and memory use, in percent.
  let cpuPoints = $state<Point[]>([]);
  let memoryPoints = $state<Point[]>([]);
  const pct = (v: number) => `${Math.round(v)}%`;
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);
  let checking = $state(false); // Check now was clicked; its heartbeat has not arrived
  let manualMessage = $state('');
  let now = $state(Date.now());

  const days = { '24h': 1, '7d': 7, '30d': 30 };
  const periods = ['24h', '7d', '30d'] as const;
  const from = $derived(now - days[period] * 86400000);
  const changes = $derived(beats.filter((b) => b.important).slice(0, 20));
  const resources = $derived(monitor?.type === 'system-resources');
  const look = $derived(normalize(app.session?.user?.appearance).layout);
  // The last 90 days, for the uptime heatmap.
  let daily = $state<Count[]>([]);
  const ninety = $derived(lastDays(90, now));
  // Other monitors that are down, for Aurora's "elsewhere".
  const elsewhere = $derived(live.monitors.filter((x) => x.id !== id && x.active && x.status.status === 'down'));
  const label = (st: string) => st.charAt(0).toUpperCase() + st.slice(1);

  async function load() {
    try {
      monitor = await api.monitor(id);
      beats = await api.heartbeats(id, new Date(Date.now() - 86400000));
      await loadChart();
      api.history(90).then((h) => (daily = h.days.find((d) => d.monitor_id === id)?.days ?? []), () => {});
    } catch (e) {
      notice = { ok: false, text: message(e) };
    }
  }

  async function loadChart() {
    now = Date.now();
    if (period === '24h') {
      const old = [...beats].reverse();
      points = old.map((b) => ({ t: Date.parse(b.at), v: b.latency_ms, down: b.status === 'down' }));
      cpuPoints = old.map((b) => ({ t: Date.parse(b.at), v: b.usage?.cpu ?? null, down: b.status === 'down' }));
      memoryPoints = old.map((b) => ({ t: Date.parse(b.at), v: b.usage?.memory ?? null, down: b.status === 'down' }));
      return;
    }
    const hours = await api.hours(id, days[period]);
    points = hours.map((h) => ({ t: Date.parse(h.start), v: h.avg_latency_ms, down: h.down > 0 }));
    cpuPoints = hours.map((h) => ({ t: Date.parse(h.start), v: h.avg_usage?.cpu ?? null, down: h.down > 0 }));
    memoryPoints = hours.map((h) => ({ t: Date.parse(h.start), v: h.avg_usage?.memory ?? null, down: h.down > 0 }));
  }

  $effect(() => {
    void id;
    monitor = null;
    load();
  });


  // New heartbeats arrive live.
  $effect(() =>
    onBeat((b) => {
      if (b.monitor_id !== id || !monitor) return;
      if (checking) {
        checking = false;
        if (notice?.ok) notice = null;
      }
      beats = [b, ...beats];
      if (b.important && (b.status === 'maintenance') !== (monitor.status.status === 'maintenance')) {
        api.monitor(id).then((m) => (monitor = m), () => {});
      }
      monitor.status = {
        ...monitor.status,
        status: b.status,
        message: b.message,
        last_check: b.at,
        latency_ms: b.latency_ms,
        usage: b.usage,
        since: b.important ? b.at : monitor.status.since,
        checking: false,
      };
      if (period === '24h') {
        now = Date.now();
        const t = Date.parse(b.at);
        const down = b.status === 'down';
        points = [...points, { t, v: b.latency_ms, down }];
        cpuPoints = [...cpuPoints, { t, v: b.usage?.cpu ?? null, down }];
        memoryPoints = [...memoryPoints, { t, v: b.usage?.memory ?? null, down }];
      }
    }),
  );

  async function run(f: () => Promise<string | void>) {
    busy = true;
    notice = null;
    try {
      const text = await f();
      if (text) notice = { ok: true, text };
      monitor = await api.monitor(id);
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  // checkNow asks for a check; its heartbeat clears the notice.
  async function checkNow() {
    checking = true;
    await run(async () => {
      await api.checkMonitor(id);
      return checking ? 'Checking…' : undefined;
    });
    if (notice && !notice.ok) checking = false;
  }

  async function remove() {
    if (!monitor || !confirm(`Delete ${monitor.name} and its history? This cannot be undone.`)) return;
    busy = true;
    try {
      await api.deleteMonitor(id);
      go('/');
    } catch (e) {
      notice = { ok: false, text: message(e) };
      busy = false;
    }
  }

  function copy(text: string) {
    navigator.clipboard?.writeText(text).then(() => (notice = { ok: true, text: 'Copied.' }));
  }
</script>

<Notice bind:notice />

{#if monitor}
  {@const m = monitor}
  {@const group = live.groups.find((g) => g.id === m.group_id)}
  <div class="head">
    <div class="who">
      <span class="badge {m.active ? m.status.status : 'paused'}" aria-hidden="true">
        <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
          ><path d="M2 12h4l2-5 3 10 2-5h9" /></svg
        >
      </span>
      <div class="titles">
        <div class="crumbs mono">
          {m.type} · every {duration(m.interval)}
          {#if m.agent_id}· on {m.agent_name ? `agent ${m.agent_name}` : 'a removed agent'}{/if}
          {#if group}· in <a href="/groups/{group.id}" onclick={link}>{group.name}</a>{/if}
        </div>
        <h1>{m.name}</h1>
        {#if m.description}<p class="muted desc">{m.description}</p>{/if}
      </div>
      <span class="pill {m.active ? m.status.status : 'paused'}"
        ><i></i>{m.active ? label(m.status.status) : 'Paused'}{m.active && m.status.since ? ` · ${ago(m.status.since, now).replace(' ago', '')}` : ''}</span
      >
    </div>
    {#if admin}
      <div class="row">
        {#if m.type !== 'push' && m.type !== 'manual' && m.active}
          <button class="primary" disabled={busy || m.status.checking} onclick={checkNow}>Check now</button>
        {/if}
        {#if m.active}
          <button disabled={busy} onclick={() => run(async () => (await api.pauseMonitor(id), 'Paused.'))}>Pause</button>
        {:else}
          <button disabled={busy} onclick={() => run(async () => (await api.resumeMonitor(id), 'Resumed.'))}>Resume</button>
        {/if}
        <a class="button" href="/monitors/{id}/edit" onclick={link}>Edit</a>
        <button class="danger" disabled={busy} onclick={remove}>Delete</button>
      </div>
    {/if}
  </div>
  {#if m.tags.length}
    <p class="tags">{#each m.tags as t (t.id)}<TagChip name={t.name} color={t.color} value={t.value} />{/each}</p>
  {/if}
  {#if m.status.maintenance}<p class="notice maint">In maintenance: {m.status.maintenance}</p>{/if}

  <div class="kpis">
    {#if resources}
      <div class="card kpi"><span class="k">CPU</span><span class="v">{m.status.usage ? `${m.status.usage.cpu.toFixed(1)}%` : '–'}</span><span class="s muted">now</span></div>
      <div class="card kpi"><span class="k">Memory</span><span class="v">{m.status.usage ? `${m.status.usage.memory.toFixed(1)}%` : '–'}</span><span class="s muted">now</span></div>
    {:else}
      <div class="card kpi">
        <span class="k">Response</span><span class="v">{m.status.status === 'down' ? '—' : millis(m.status.latency_ms)}</span>
        <span class="s muted">24 h average {millis(m.uptime.avg_latency_24h_ms)}</span>
      </div>
      <div class="card kpi">
        <span class="k">Uptime, 24 hours</span><span class="v">{percent(m.uptime['24h'])}</span>
        <span class="s muted">30 d {percent(m.uptime['30d'])} · 1 y {percent(m.uptime['1y'])}</span>
      </div>
    {/if}
    <div class="card kpi">
      <span class="k">Last check</span><span class="v">{ago(m.status.last_check, now)}</span>
      <span class="s" class:bad={m.status.status === 'down'} class:muted={m.status.status !== 'down'}>{m.status.message || '–'}</span>
    </div>
    <div class="card kpi">
      <span class="k">Certificate</span>
      {#if m.status.cert_expires}
        {@const d = daysUntil(m.status.cert_expires, now)}
        <span class="v" class:bad={d < 14}>{d} days</span><span class="s muted">until {new Date(m.status.cert_expires).toLocaleDateString()}</span>
      {:else}
        <span class="v">—</span><span class="s muted">none checked</span>
      {/if}
    </div>
  </div>

  {#if m.push_url}
    <section class="card">
      <h2>Push URL</h2>
      <p>Call this URL from your job at least every {duration(m.interval)}; add <code>status=down</code> to report a failure.</p>
      <div class="row"><code class="url">{m.push_url}</code><button onclick={() => copy(m.push_url ?? '')}>Copy</button></div>
    </section>
  {/if}

  {#if m.type === 'manual' && admin}
    <section class="card">
      <h2>Set the status</h2>
      <form
        class="row"
        onsubmit={(e) => {
          e.preventDefault();
        }}
      >
        <input bind:value={manualMessage} placeholder="Message (optional)" />
        <button disabled={busy} onclick={() => run(async () => (await api.setManualStatus(id, 'up', manualMessage), 'Set to up.'))}
          >Up</button
        >
        <button
          class="danger"
          disabled={busy}
          onclick={() => run(async () => (await api.setManualStatus(id, 'down', manualMessage), 'Set to down.'))}>Down</button
        >
      </form>
    </section>
  {/if}

  <section class="card">
    <div class="row between">
      <h2>{resources ? 'CPU' : 'Response time'}</h2>
      <div class="row" role="group" aria-label="Period">
        {#each periods as p (p)}
          <button
            class:primary={period === p}
            onclick={() => {
              period = p;
              loadChart().catch((e) => (notice = { ok: false, text: message(e) }));
            }}>{p}</button
          >
        {/each}
      </div>
    </div>
    {#if resources}
      <Chart points={cpuPoints} {from} to={now} label="CPU use" format={pct} top={100} empty="No CPU use in this period." />
    {:else}
      <Chart {points} {from} to={now} />
    {/if}
  </section>

  {#if resources}
    <section class="card">
      <h2>Memory</h2>
      <Chart points={memoryPoints} {from} to={now} label="Memory use" format={pct} top={100} empty="No memory use in this period." />
    </section>
  {/if}

  <div class="pair">
    {#if look === 'aurora'}
      <section class="card">
        <h2>Checked from</h2>
        <div class="from">
          <span class="src">{m.agent_id ? 'A' : 'E'}</span>
          <span class="grow"
            >{m.agent_id ? `Agent ${m.agent_name ?? '(removed)'}` : 'Eyrie server (local)'}<span class="muted block small"
              >every {duration(m.interval)}</span
            ></span
          >
          <span class="mono">{millis(m.status.latency_ms)}</span>
        </div>
      </section>
      <section class="card">
        <h2>Elsewhere right now</h2>
        {#each elsewhere.slice(0, 3) as x (x.id)}
          <a class="else" href="/monitors/{x.id}" onclick={link}
            ><i></i><span class="grow"><strong>{x.name} is down</strong><span class="muted block small">{x.status.message}{x.status.since ? ` · ${ago(x.status.since, now)}` : ''}</span></span></a
          >
        {:else}
          <p class="muted">Every other monitor is answering.</p>
        {/each}
      </section>
    {:else}
      <section class="card">
        <div class="row between"><h2>Uptime, 90 days</h2><span class="mono muted">{percent(m.uptime['30d'])} 30 d</span></div>
        <div class="days" aria-hidden="true">
          {#each ninety as d (d)}<span class="day {band(dayOf(daily, d))}" title={new Date(d).toLocaleDateString(undefined, { timeZone: 'UTC' })}></span>{/each}
        </div>
        <div class="row between muted small"><span>{new Date(ninety[0]).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })}</span><span>Today</span></div>
      </section>
    {/if}
    <section class="card">
      <h2>Timeline, last 24 hours</h2>
      {#each changes as b (b.at + b.status)}
        <div class="event">
          <span class="icon {b.status}">{b.status === 'up' ? '✓' : b.status === 'down' ? '!' : '•'}</span>
          <span class="grow"
            ><span class="strong">{label(b.status)}{b.message ? `: ${b.message}` : ''}</span><span class="muted block small"
              >{new Date(b.at).toLocaleString()}</span
            ></span
          >
        </div>
      {:else}
        <p class="muted">No changes.</p>
      {/each}
    </section>
  </div>
{:else if !notice}
  <p class="muted">Loading…</p>
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 1rem;
    align-items: center;
    margin-bottom: 0.75rem;
  }
  .who {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.9rem;
    min-width: 0;
  }
  .titles {
    min-width: 0;
  }
  .crumbs {
    font-size: 0.78rem;
    color: var(--muted);
    letter-spacing: 0.02em;
  }
  .head h1 {
    margin: 0.1rem 0 0;
    font-size: 1.6rem;
  }
  .desc {
    margin: 0.15rem 0 0;
  }
  .badge {
    display: grid;
    place-items: center;
    width: 2.9rem;
    height: 2.9rem;
    border-radius: 14px;
    color: var(--unknown);
    background: color-mix(in srgb, currentColor 14%, transparent);
    border: 1px solid color-mix(in srgb, currentColor 35%, transparent);
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.35rem 0.8rem;
    border-radius: 999px;
    font-weight: 600;
    color: var(--unknown);
    background: color-mix(in srgb, currentColor 13%, transparent);
    border: 1px solid color-mix(in srgb, currentColor 35%, transparent);
  }
  .pill i {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: currentColor;
  }
  .badge.up,
  .pill.up {
    color: var(--up);
  }
  .badge.down,
  .pill.down {
    color: var(--down);
  }
  .badge.pending,
  .pill.pending {
    color: var(--pending);
  }
  .badge.maintenance,
  .pill.maintenance {
    color: var(--maintenance);
  }
  .pill.up {
    color: var(--ok);
  }
  .pill.down {
    color: var(--error);
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    margin: 0 0 0.75rem;
  }
  .maint {
    background: color-mix(in srgb, var(--maintenance) 14%, transparent);
  }
  .kpis {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .kpi {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    margin: 0;
    padding: 0.9rem 1rem;
    min-width: 0;
  }
  .k {
    font-size: 0.8rem;
    color: var(--muted);
  }
  .v {
    font-size: 1.5rem;
    font-weight: 600;
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
  }
  .s {
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }
  .bad {
    color: var(--error);
  }
  .pair {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 0 1rem;
  }
  @media (min-width: 72rem) {
    .pair {
      grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    }
    :global(:root[data-layout='aurora']) .pair {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
  .days {
    display: grid;
    grid-template-columns: repeat(45, minmax(0, 1fr));
    gap: 3px;
    margin: 0.5rem 0;
  }
  .day {
    height: 0.85rem;
    border-radius: 3px;
    background: var(--surface-2, var(--border));
  }
  .day.full {
    background: var(--up);
  }
  .day.high {
    background: color-mix(in srgb, var(--up) 50%, var(--surface));
  }
  .day.low {
    background: var(--pending);
  }
  .day.bad {
    background: var(--down);
  }
  .event {
    display: flex;
    gap: 0.6rem;
    padding-bottom: 0.7rem;
  }
  .icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 1.4rem;
    height: 1.4rem;
    border-radius: 50%;
    font-size: 0.7rem;
    font-weight: 700;
    background: var(--surface-2, var(--border));
    color: var(--muted);
  }
  .icon.up {
    background: var(--ok-bg);
    color: var(--ok);
  }
  .icon.down {
    background: var(--error-bg);
    color: var(--error);
  }
  .strong {
    font-weight: 500;
    overflow-wrap: anywhere;
  }
  .from,
  .else {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .src {
    display: grid;
    place-items: center;
    width: 2.3rem;
    height: 2.3rem;
    border-radius: 12px;
    font-weight: 700;
    background: var(--up);
    color: var(--bg);
  }
  .else {
    padding: 0.7rem 0.8rem;
    margin-bottom: 0.5rem;
    border-radius: 16px;
    color: var(--fg);
    text-decoration: none;
    background: linear-gradient(90deg, color-mix(in srgb, var(--down) 26%, transparent), transparent);
    border: 1px solid color-mix(in srgb, var(--down) 45%, transparent);
  }
  .else i {
    width: 0.6rem;
    height: 0.6rem;
    flex: none;
    transform: rotate(45deg);
    background: var(--down);
    box-shadow: 0 0 12px var(--down);
  }
  .grow {
    flex: 1;
    min-width: 0;
  }
  .block {
    display: block;
  }
  .small {
    font-size: 0.8rem;
  }
  .mono {
    font-family: var(--mono, ui-monospace, monospace);
  }
  .between {
    justify-content: space-between;
  }
  .url {
    flex: 1;
    min-width: 0;
  }

  /* Aurora: figures lit in the scheme's gradient, a larger name. */
  :global(:root[data-layout='aurora']) .v {
    font-size: 1.9rem;
    background: linear-gradient(90deg, var(--logo-a), var(--logo-b));
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
  }
  :global(:root[data-layout='aurora']) .head h1 {
    font-size: 2.1rem;
  }
  :global(:root[data-layout='aurora']) .badge {
    display: none;
  }
  :global(:root[data-layout='aurora']) .pill i {
    box-shadow: 0 0 12px currentColor;
  }
  /* Console: figures in the monospaced face. */
  :global(:root[data-layout='console']) .v {
    font-family: var(--mono);
    font-weight: 500;
  }
</style>
