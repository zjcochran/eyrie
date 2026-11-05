<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The Bento layout's start page: everything at a glance, in tiles. -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type Agent, type History, type StatusChange, type StatusPage, type Window, message } from '../lib/api.ts';
  import { arcs, band, bars, dayOf, greeting, lastDays, needs, pageMonitors } from '../lib/bento.ts';
  import { ago, daysUntil, millis, percent } from '../lib/format.ts';
  import { live, onBeat } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { certSoonDays, overview } from '../lib/overview.ts';
  import { api, app } from '../lib/session.svelte.ts';

  let { admin }: { admin: boolean } = $props();

  let range = $state(30);
  let history = $state<History>({ days: [], hours: [] });
  let changes = $state<StatusChange[]>([]);
  let windows = $state<Window[]>([]);
  let pages = $state<StatusPage[]>([]);
  let agents = $state<Agent[]>([]);
  let tip = $state('Point at a day');
  let error = $state('');

  async function loadHistory() {
    try {
      history = await api.history(range);
    } catch (e) {
      error = message(e);
    }
  }

  onMount(() => {
    loadHistory();
    // Each tile works without the others.
    api.statusChanges().then((c) => (changes = c), () => {});
    api.windows().then((w) => (windows = w), () => {});
    api.statusPages().then((p) => (pages = p), () => {});
    if (admin) api.agents().then((a) => (agents = a), () => {});
    // The heatmap and the checks per hour, every five minutes.
    const timer = setInterval(loadHistory, 5 * 60_000);
    const stop = onBeat((b) => {
      if (b.important) changes = [{ ...b, id: 0 }, ...changes].slice(0, 20);
    });
    return () => {
      clearInterval(timer);
      stop();
    };
  });

  const o = $derived(overview(live.monitors));
  const active = $derived(live.monitors.filter((m) => m.active));
  const down = $derived(active.filter((m) => m.status.status === 'down'));
  const first = $derived(down[0]);
  const now = new Date();
  const name = $derived(app.session?.user?.username ?? '');

  // The ring: up, down, then the rest (pending, maintenance, unknown and paused).
  const circumference = 2 * Math.PI * 48;
  const other = $derived(o.counts.pending + o.counts.maintenance + o.counts.unknown + o.counts.paused);
  const ring = $derived(arcs([o.counts.up, o.counts.down, other], circumference));
  const hourBars = $derived(bars(history.hours));

  const days = $derived(lastDays(range));
  const dayLabel = (start: number) => new Date(start).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
  const heat = $derived(
    active.map((m) => {
      const counts = history.days.find((d) => d.monitor_id === m.id)?.days;
      return { m, cells: days.map((start) => ({ start, c: dayOf(counts, start) })) };
    }),
  );

  // The next maintenance: one that is on, or the soonest to start.
  const nextWindow = $derived(
    windows.find((w) => w.state === 'on') ??
      windows.filter((w) => w.state === 'scheduled' && w.change).sort((a, b) => Date.parse(a.change ?? '') - Date.parse(b.change ?? ''))[0],
  );
  const when = (iso: string) => new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });

  const shownPages = $derived(
    pages.slice(0, 4).map((p) => {
      const ms = pageMonitors(p, live.monitors);
      return { p, down: ms.filter((m) => m.active && m.status.status === 'down').length, count: ms.length };
    }),
  );
  const online = $derived(agents.filter((a) => a.status === 'online').length);
  const monitorName = (id: number) => live.monitors.find((m) => m.id === id)?.name ?? `Monitor ${id}`;
  const lastDay = $derived(Date.now() - 86_400_000);
  const changesToday = $derived(changes.filter((c) => Date.parse(c.at) > lastDay).length);

  async function checkNow(id: number) {
    try {
      await api.checkMonitor(id);
    } catch (e) {
      error = message(e);
    }
  }
</script>

<div class="top">
  <div>
    <div class="muted date">{now.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' })}</div>
    <h1>{greeting(now.getHours())}, {name}. <span class:alert={down.length > 0}>{needs(down.length)}</span></h1>
  </div>
  {#if admin}<a class="button primary big" href="/monitors/new" onclick={link}>+ Add a monitor</a>{/if}
</div>
{#if error}<p class="notice error" role="alert">{error}</p>{/if}

<div class="bento">
  <section class="tile hero">
    <div class="head"><span>Fleet health</span><span class="chip">live</span></div>
    <div class="health">
      <svg viewBox="0 0 120 120" class="ring" role="img" aria-label="{o.counts.up} up, {o.counts.down} down, {other} paused or other">
        <circle cx="60" cy="60" r="48" class="track" />
        {#each [ring[0], ring[1], ring[2]] as a, i (i)}
          {#if a.length > 0}
            <circle
              cx="60"
              cy="60"
              r="48"
              class="arc arc{i}"
              stroke-dasharray="{Math.max(a.length - 3, 0.5)} {circumference}"
              stroke-dashoffset={a.offset}
              transform="rotate(-90 60 60)"
            />
          {/if}
        {/each}
        <text x="60" y="60" class="big">{o.counts.up}/{o.active + o.counts.paused}</text>
        <text x="60" y="78" class="small">up now</text>
      </svg>
      <div class="figures">
        <div>
          <div class="huge">{percent(o.uptimeMonth)}</div>
          <div class="soft">uptime, all monitors, 30 days</div>
        </div>
        <div class="legend">
          <span><i class="dot up"></i>{o.counts.up} up</span>
          <span><i class="dot down"></i>{o.counts.down} down</span>
          <span><i class="dot other"></i>{other} paused or other</span>
        </div>
      </div>
    </div>
    <div>
      <div class="soft row-between small-text"><span>Checks per hour, last 24 hours</span><span>{history.hours.at(-1) ? `${history.hours.at(-1)!.up + history.hours.at(-1)!.down} this hour` : ''}</span></div>
      <div class="hours" aria-hidden="true">
        {#each hourBars as b, i (i)}<span class:downhour={b.down} class:now={i === hourBars.length - 1} style:height="{Math.max(b.height * 100, 4)}%"></span>{/each}
      </div>
    </div>
    <div class="minis">
      <div><div class="soft small-text">Response, 24 hours</div><div class="mini">{millis(o.latencyDay)}</div></div>
      <div><div class="soft small-text">Changes, 24 hours</div><div class="mini">{changesToday}</div></div>
      <div><div class="soft small-text">Uptime, 24 hours</div><div class="mini">{percent(o.uptimeDay)}</div></div>
    </div>
  </section>

  {#if first}
    <section class="tile incident">
      <div class="row-between">
        <span class="alarm"><i class="pulse"></i>Down {first.status.since ? `for ${ago(first.status.since).replace(' ago', '')}` : ''}</span>
        {#if down.length > 1}<span class="muted small-text">and {down.length - 1} more</span>{/if}
      </div>
      <a class="title" href="/monitors/{first.id}" onclick={link}>{first.name}</a>
      <div class="muted mono small-text">{first.status.message}{first.agent_name ? ` · from agent ${first.agent_name}` : ''}</div>
      <div class="actions">
        <a class="button danger-fill" href="/monitors/{first.id}" onclick={link}>Open monitor</a>
        {#if admin}<button onclick={() => checkNow(first.id)}>Check now</button>{/if}
      </div>
    </section>
  {:else}
    <section class="tile incident calm">
      <span class="ok-text"><i class="dot up"></i> Nothing is down</span>
      <div class="title">All {o.active} monitors are answering.</div>
      {#if changes[0]}<div class="muted small-text">Last change: {monitorName(changes[0].monitor_id)} {changes[0].status}, {ago(changes[0].at)}.</div>{/if}
    </section>
  {/if}

  <section class="tile deep">
    {#if admin && agents.length}
      <div class="row-between"><strong>Agents</strong><span class="small-text">{online} of {agents.length} online</span></div>
      {#each agents.slice(0, 4) as a (a.id)}
        <div class="agent">
          <i class="dot {a.status === 'online' ? 'up' : a.status === 'offline' ? 'down' : 'other'}"></i>
          <span class="grow">{a.name}<span class="soft small-text"> · {a.platform || 'not enrolled'}</span></span>
          <span class="small-text">{a.workloads} workloads</span>
        </div>
      {/each}
      <a class="soft small-text" href="/settings/agents" onclick={link}>All agents</a>
    {:else}
      <strong>Slowest, 24 hours</strong>
      {#each o.slowest.slice(0, 4) as m (m.id)}
        <div class="agent"><a class="grow" href="/monitors/{m.id}" onclick={link}>{m.name}</a><span class="small-text">{millis(m.uptime.avg_latency_24h_ms)}</span></div>
      {/each}
    {/if}
  </section>

  <section class="tile heat">
    <div class="row-between wrap">
      <h2>Every monitor, every day</h2>
      <span class="muted mono small-text">{tip}</span>
      <div class="seg" role="group" aria-label="Days">
        {#each [30, 14] as n (n)}
          <button
            class:on={range === n}
            onclick={() => {
              range = n;
              loadHistory();
            }}>{n} days</button
          >
        {/each}
      </div>
    </div>
    <div class="rows">
      {#each heat as row (row.m.id)}
        <div class="heatrow">
          <a href="/monitors/{row.m.id}" onclick={link}>{row.m.name}</a>
          <span class="cells" style:grid-template-columns="repeat({row.cells.length}, minmax(0, 1fr))">
            {#each row.cells as c (c.start)}
              <span
                class="cell {band(c.c)}"
                role="presentation"
                onmouseenter={() =>
                  (tip = `${row.m.name} · ${dayLabel(c.start)} · ${c.c && c.c.up + c.c.down ? `${((c.c.up / (c.c.up + c.c.down)) * 100).toFixed(2)}%` : 'no checks'}`)}
              ></span>
            {/each}
          </span>
          <span class="mono small-text end">{percent(row.m.uptime['30d'])}</span>
        </div>
      {:else}
        <p class="muted">No monitors yet.</p>
      {/each}
    </div>
    <div class="key muted small-text">
      <span><i class="cell full"></i> 100%</span><span><i class="cell high"></i> 99–100%</span><span><i class="cell low"></i> 95–99%</span>
      <span><i class="cell bad"></i> under 95%</span><span><i class="cell none"></i> no checks</span>
    </div>
  </section>

  <section class="tile certs">
    <h2>Certificates</h2>
    {#each o.certs.slice(0, 4) as m (m.id)}
      {@const d = daysUntil(m.status.cert_expires ?? '')}
      <a class="cert" href="/monitors/{m.id}" onclick={link}>
        <span class="days" class:soon={d < certSoonDays}>{d < 0 ? '!' : d}</span>
        <span class="grow">{m.name}<span class="muted small-text block">{d < 0 ? 'expired' : 'days left'}</span></span>
      </a>
    {:else}
      <p class="muted">No monitor checks a certificate.</p>
    {/each}
  </section>

  <a class="tile bright" href="/maintenance" onclick={link}>
    <strong class="small-text">{nextWindow?.state === 'on' ? 'Maintenance now' : 'Next maintenance'}</strong>
    {#if nextWindow?.change}
      <span class="when">{when(nextWindow.change)}</span>
      <span class="small-text">{nextWindow.state === 'on' ? 'ends then' : 'starts then'}</span>
      <span class="small-text faded">{nextWindow.title}</span>
    {:else}
      <span class="when">None</span>
      <span class="small-text faded">planned</span>
    {/if}
  </a>

  <section class="tile pages">
    <div class="row-between"><h2>Status pages</h2>{#if admin}<a href="/status-pages/new" onclick={link}>New page →</a>{/if}</div>
    <div class="pagegrid">
      {#each shownPages as s (s.p.id)}
        <a class="page" class:bad={s.down > 0} href="/status-pages/{s.p.id}" onclick={link}>
          <strong>{s.p.title}</strong>
          <span class="muted small-text">/status/{s.p.slug}</span>
          <span class="state small-text">
            {#if !s.p.published}Not published{:else if s.down}■ {s.down} of {s.count} down{:else}● All operational{/if}
          </span>
        </a>
      {:else}
        <p class="muted">No status pages yet.</p>
      {/each}
    </div>
  </section>

  <section class="tile feed">
    <div class="row-between"><h2>Latest changes</h2><a href="/events" onclick={link}>All events</a></div>
    {#each changes.slice(0, 5) as c (`${c.id}-${c.monitor_id}-${c.at}`)}
      <div class="change">
        <span class="badge {c.status}">{c.status === 'up' ? '↑' : c.status === 'down' ? '↓' : '•'}</span>
        <span class="grow"
          ><a href="/monitors/{c.monitor_id}" onclick={link}>{monitorName(c.monitor_id)}</a>
          <span class="muted">{c.status === 'up' ? 'is back up' : c.status === 'down' ? `went down: ${c.message}` : c.status}</span></span
        >
        <span class="muted mono small-text">{ago(c.at)}</span>
      </div>
    {:else}
      <p class="muted">Nothing yet.</p>
    {/each}
  </section>
</div>

<style>
  .top {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 1.1rem;
  }
  .date {
    font-weight: 500;
  }
  h1 {
    margin: 0.1rem 0 0;
  }
  h1 .alert {
    color: var(--down);
  }
  h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 800;
  }
  .big.button {
    padding: 0.65rem 1.1rem;
    font-weight: 700;
    box-shadow: 0 8px 20px -8px var(--accent);
  }
  .bento {
    display: grid;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    gap: 1rem;
    margin-bottom: 1.5rem;
  }
  .tile {
    grid-column: span 12;
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
    padding: 1.25rem 1.35rem;
    border-radius: 24px;
    background: var(--surface);
    color: var(--fg);
    box-shadow:
      0 1px 2px rgb(0 0 0 / 0.06),
      0 12px 32px -18px rgb(0 0 0 / 0.25);
    min-width: 0;
    transition:
      transform 0.15s ease,
      box-shadow 0.15s ease;
  }
  .tile:hover {
    transform: translateY(-2px);
  }
  @media (min-width: 64rem) {
    .hero {
      grid-column: span 5;
      grid-row: span 2;
    }
    .incident {
      grid-column: span 4;
    }
    .deep {
      grid-column: span 3;
    }
    .heat {
      grid-column: span 7;
      grid-row: span 2;
    }
    .certs {
      grid-column: span 3;
    }
    .bright {
      grid-column: span 2;
    }
    .pages {
      grid-column: span 5;
    }
    .feed {
      grid-column: span 7;
    }
  }
  @media (min-width: 40rem) and (max-width: 64rem) {
    .incident,
    .deep,
    .certs,
    .bright {
      grid-column: span 6;
    }
  }

  /* The first tile, in the scheme's colour. */
  .hero {
    position: relative;
    overflow: hidden;
    color: var(--hero-fg);
    background:
      radial-gradient(600px 300px at 100% 0%, var(--hero-glow), transparent 70%),
      linear-gradient(160deg, var(--hero-a), var(--hero-b));
    box-shadow: 0 20px 50px -22px var(--hero-a);
    gap: 1.1rem;
  }
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-weight: 600;
  }
  .chip {
    font-family: var(--mono);
    font-size: 0.75rem;
    padding: 0.2rem 0.6rem;
    border-radius: 999px;
    background: rgb(255 255 255 / 0.16);
  }
  .health {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 1.5rem;
  }
  .ring {
    width: 9.5rem;
    height: 9.5rem;
    flex: none;
  }
  .track {
    fill: none;
    stroke: rgb(255 255 255 / 0.16);
    stroke-width: 14;
  }
  .arc {
    fill: none;
    stroke-width: 14;
    stroke-linecap: round;
  }
  .arc0 {
    stroke: var(--hero-up, var(--up));
  }
  .arc1 {
    stroke: var(--hero-down, var(--down));
  }
  .arc2 {
    stroke: rgb(255 255 255 / 0.5);
  }
  .ring text {
    fill: currentColor;
    text-anchor: middle;
  }
  .ring .big {
    font-size: 26px;
    font-weight: 800;
  }
  .ring .small {
    font-size: 11px;
    font-weight: 600;
    opacity: 0.8;
  }
  .figures {
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
  }
  .huge {
    font-size: 2.8rem;
    font-weight: 800;
    letter-spacing: -0.045em;
    line-height: 1;
  }
  .soft {
    opacity: 0.82;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 1rem;
    font-size: 0.85rem;
    font-weight: 600;
  }
  .dot {
    display: inline-block;
    width: 0.6rem;
    height: 0.6rem;
    margin-right: 0.35rem;
    border-radius: 50%;
  }
  .hero .dot.up {
    background: var(--hero-up, var(--up));
  }
  .hero .dot.down {
    background: var(--hero-down, var(--down));
    border-radius: 2px;
  }
  .dot.up {
    background: var(--up);
  }
  .dot.down {
    background: var(--down);
  }
  .dot.other {
    border: 2px solid currentColor;
    opacity: 0.7;
    box-sizing: border-box;
  }
  .hours {
    display: flex;
    align-items: flex-end;
    gap: 3px;
    height: 4.5rem;
    margin-top: 0.4rem;
  }
  .hours span {
    flex: 1;
    border-radius: 4px 4px 2px 2px;
    background: rgb(255 255 255 / 0.35);
  }
  .hours .downhour {
    background: var(--hero-down, var(--down));
  }
  .hours .now {
    background: var(--hero-up, var(--up));
  }
  .minis {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.6rem;
  }
  .minis > div {
    padding: 0.7rem 0.8rem;
    border-radius: 14px;
    background: rgb(255 255 255 / 0.12);
  }
  .mini {
    font-size: 1.35rem;
    font-weight: 800;
  }

  .row-between {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
  }
  .wrap {
    flex-wrap: wrap;
  }
  .small-text {
    font-size: 0.82rem;
  }
  .mono {
    font-family: var(--mono);
  }
  .grow {
    flex: 1;
    min-width: 0;
  }
  .block {
    display: block;
  }

  /* What is down. */
  .incident {
    border: 1px solid color-mix(in srgb, var(--down) 35%, transparent);
  }
  .incident.calm {
    border-color: color-mix(in srgb, var(--up) 30%, transparent);
  }
  .alarm {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 700;
    color: var(--error);
  }
  .ok-text {
    font-weight: 700;
    color: var(--ok);
  }
  .pulse {
    width: 0.65rem;
    height: 0.65rem;
    border-radius: 3px;
    background: var(--down);
    color: var(--down);
    animation: ring 1.6s infinite;
  }
  @keyframes ring {
    0% {
      box-shadow: 0 0 0 0 currentColor;
    }
    80% {
      box-shadow: 0 0 0 10px transparent;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .pulse {
      animation: none;
    }
  }
  .title {
    font-size: 1.4rem;
    font-weight: 800;
    letter-spacing: -0.02em;
    color: var(--fg);
    text-decoration: none;
  }
  .actions {
    display: flex;
    gap: 0.5rem;
    margin-top: auto;
  }
  .actions > * {
    flex: 1;
    text-align: center;
    font-weight: 700;
  }
  .danger-fill {
    background: var(--down);
    border-color: var(--down);
    color: #ffffff;
  }

  /* The dark tile. */
  .deep {
    background: var(--deep, var(--surface-2));
    color: var(--deep-fg, var(--fg));
  }
  .deep a {
    color: inherit;
  }
  .agent {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  /* The heatmap. */
  .seg {
    margin-left: auto;
    display: flex;
    padding: 3px;
    border-radius: 10px;
    background: var(--surface-2);
  }
  .seg button {
    border: 0;
    background: none;
    padding: 0.3rem 0.75rem;
    border-radius: 8px;
    font-weight: 700;
    font-size: 0.8rem;
    color: var(--muted);
  }
  .seg button.on {
    background: var(--surface);
    color: var(--fg);
    box-shadow: 0 1px 3px rgb(0 0 0 / 0.15);
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 5px;
    max-height: 36rem;
    overflow-y: auto;
  }
  .heatrow {
    display: grid;
    grid-template-columns: minmax(6rem, 11rem) minmax(0, 1fr) 4rem;
    gap: 0.75rem;
    align-items: center;
  }
  .heatrow a {
    font-weight: 600;
    font-size: 0.88rem;
    color: var(--fg);
    text-decoration: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cells {
    display: grid;
    gap: 3px;
  }
  .cell {
    display: inline-block;
    height: 1.1rem;
    min-width: 0.6rem;
    border-radius: 4px;
    background: var(--surface-2);
  }
  .cells .cell:hover {
    outline: 2px solid var(--fg);
    outline-offset: 1px;
  }
  .cell.full {
    background: var(--up);
  }
  .cell.high {
    background: color-mix(in srgb, var(--up) 45%, var(--surface));
  }
  .cell.low {
    background: var(--pending);
  }
  .cell.bad {
    background: var(--down);
  }
  .end {
    text-align: right;
  }
  .key {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 1rem;
  }
  .key .cell {
    width: 0.7rem;
    height: 0.7rem;
    min-width: 0;
    vertical-align: -1px;
  }

  /* Certificates. */
  .cert {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    color: var(--fg);
    text-decoration: none;
    font-weight: 600;
  }
  .days {
    display: grid;
    place-items: center;
    flex: none;
    width: 2.75rem;
    height: 2.75rem;
    border-radius: 12px;
    font-weight: 800;
    background: var(--ok-bg);
    color: var(--ok);
  }
  .days.soon {
    background: var(--error-bg);
    color: var(--error);
  }

  /* The bright tile. */
  .bright {
    background: var(--tile, var(--accent));
    color: var(--tile-fg, var(--accent-fg));
    text-decoration: none;
    gap: 0.3rem;
  }
  .when {
    font-size: 1.6rem;
    font-weight: 800;
    letter-spacing: -0.03em;
    line-height: 1.1;
  }
  .faded {
    margin-top: auto;
    opacity: 0.75;
  }

  /* Status pages. */
  .pagegrid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr));
    gap: 0.75rem;
  }
  .page {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    padding: 0.85rem;
    border-radius: 16px;
    background: var(--ok-bg);
    color: var(--fg);
    text-decoration: none;
  }
  .page.bad {
    background: var(--error-bg);
  }
  .state {
    margin-top: 0.35rem;
    font-weight: 700;
    color: var(--ok);
  }
  .page.bad .state {
    color: var(--error);
  }

  /* The latest changes. */
  .change {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.55rem 0;
    border-top: 1px solid var(--border);
  }
  .badge {
    display: grid;
    place-items: center;
    flex: none;
    width: 1.75rem;
    height: 1.75rem;
    border-radius: 9px;
    font-weight: 800;
    font-size: 0.8rem;
    background: var(--surface-2);
    color: var(--muted);
  }
  .badge.up {
    background: var(--ok-bg);
    color: var(--ok);
  }
  .badge.down {
    background: var(--error-bg);
    color: var(--error);
  }
  .change a {
    font-weight: 600;
    color: var(--fg);
    text-decoration: none;
  }
</style>
