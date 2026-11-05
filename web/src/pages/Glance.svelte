<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The Aurora layout's start page: the figures lit in the scheme's colours,
     the fleet's response times, and what needs attention. -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Chart from '../components/Chart.svelte';
  import { type History, type StatusChange, message } from '../lib/api.ts';
  import { ago, millis, percent } from '../lib/format.ts';
  import { live, onBeat } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { certSoonDays, overview } from '../lib/overview.ts';
  import { api } from '../lib/session.svelte.ts';

  let history = $state<History>({ days: [], hours: [] });
  let changes = $state<StatusChange[]>([]);
  let error = $state('');
  let now = $state(Date.now());

  async function load() {
    now = Date.now();
    try {
      history = await api.history(1);
    } catch (e) {
      error = message(e);
    }
  }
  onMount(() => {
    load();
    api.statusChanges().then((c) => (changes = c), () => {});
    const timer = setInterval(load, 5 * 60_000);
    const stop = onBeat((b) => {
      if (b.important) changes = [{ ...b, id: 0 }, ...changes].slice(0, 20);
    });
    return () => {
      clearInterval(timer);
      stop();
    };
  });

  const o = $derived(overview(live.monitors));
  const attention = $derived(live.monitors.filter((m) => m.active && (m.status.status === 'down' || m.status.status === 'pending')));
  const points = $derived(history.hours.map((h) => ({ t: Date.parse(h.start), v: h.avg_latency_ms ?? null, down: h.down > 0 })));
  const monitorName = (id: number) => live.monitors.find((m) => m.id === id)?.name ?? `Monitor ${id}`;
</script>

<header class="top">
  <div>
    <div class="crumbs">ALL MONITORS · {o.active} ACTIVE{o.counts.paused ? ` · ${o.counts.paused} PAUSED` : ''}</div>
    <h1>Everything in sight</h1>
  </div>
  <span class="pill" class:bad={attention.length > 0}
    ><i></i>{attention.length ? `${attention.length} need${attention.length === 1 ? 's' : ''} you` : 'All up'}</span
  >
</header>
{#if error}<p class="notice error" role="alert">{error}</p>{/if}

<div class="stats">
  <div class="card stat"><span class="k">Up now</span><span class="v">{o.counts.up}/{o.active}</span><span class="s">{o.counts.down} down · {o.counts.pending} pending</span></div>
  <div class="card stat"><span class="k">Uptime, 30 days</span><span class="v">{percent(o.uptimeMonth)}</span><span class="s">24 h {percent(o.uptimeDay)}</span></div>
  <div class="card stat"><span class="k">Response, 24 hours</span><span class="v">{millis(o.latencyDay)}</span><span class="s">mean of every monitor</span></div>
  <div class="card stat">
    <span class="k">Certificates</span><span class="v">{o.certsSoon}</span><span class="s">expiring within {certSoonDays} days</span>
  </div>
</div>

<section class="card">
  <div class="row between"><h2>Response, last 24 hours</h2><span class="mono muted">every monitor, by the hour</span></div>
  <Chart {points} from={now - 86_400_000} to={now} empty="No checks in the last 24 hours." />
</section>

<div class="pair">
  <section class="card">
    <h2>Needs attention</h2>
    {#each attention.slice(0, 5) as m (m.id)}
      <a class="alert" class:pending={m.status.status === 'pending'} href="/monitors/{m.id}" onclick={link}
        ><i></i><span class="grow"
          ><strong>{m.name} is {m.status.status}</strong><span class="muted block small"
            >{m.status.message}{m.status.since ? ` · ${ago(m.status.since)}` : ''}</span
          ></span
        ><span class="open">Open</span></a
      >
    {:else}
      <p class="calm"><i></i> Nothing is down. Choose a monitor in the list to see it.</p>
    {/each}
  </section>
  <section class="card">
    <div class="row between"><h2>Latest changes</h2><a href="/events" onclick={link}>All events</a></div>
    {#each changes.slice(0, 6) as c (`${c.id}-${c.monitor_id}-${c.at}`)}
      <div class="change">
        <span class="dot {c.status}"></span>
        <a class="grow" href="/monitors/{c.monitor_id}" onclick={link}>{monitorName(c.monitor_id)}</a>
        <span class="muted small">{c.status} · {ago(c.at)}</span>
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
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
  }
  .crumbs {
    font-family: var(--mono);
    font-size: 0.75rem;
    letter-spacing: 0.06em;
    color: var(--muted);
  }
  h1 {
    margin: 0.1rem 0 0;
    font-size: 2.1rem;
    font-weight: 600;
  }
  h2 {
    margin: 0;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.45rem 0.9rem;
    border-radius: 999px;
    font-weight: 600;
    color: var(--ok);
    background: var(--ok-bg);
    border: 1px solid color-mix(in srgb, var(--up) 40%, transparent);
  }
  .pill.bad {
    color: var(--error);
    background: var(--error-bg);
    border-color: color-mix(in srgb, var(--down) 45%, transparent);
  }
  .pill i {
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background: currentColor;
    box-shadow: 0 0 12px currentColor;
  }
  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 0.9rem;
    margin-bottom: 1rem;
  }
  .stat {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    margin: 0;
  }
  .k {
    font-size: 0.85rem;
    color: var(--muted);
  }
  .v {
    font-size: 2.1rem;
    font-weight: 600;
    letter-spacing: -0.03em;
    background: linear-gradient(90deg, var(--logo-a), var(--logo-b));
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
  }
  .s {
    font-family: var(--mono);
    font-size: 0.75rem;
    color: var(--faint, var(--muted));
  }
  .between {
    justify-content: space-between;
    margin-bottom: 0.5rem;
  }
  .mono {
    font-family: var(--mono);
    font-size: 0.8rem;
  }
  .pair {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr));
    gap: 0 1rem;
  }
  .pair h2 {
    margin-bottom: 0.75rem;
  }
  .alert {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    padding: 0.75rem 0.9rem;
    margin-bottom: 0.5rem;
    border-radius: 16px;
    color: var(--fg);
    text-decoration: none;
    background: linear-gradient(90deg, color-mix(in srgb, var(--down) 26%, transparent), transparent);
    border: 1px solid color-mix(in srgb, var(--down) 45%, transparent);
  }
  .alert i {
    width: 0.6rem;
    height: 0.6rem;
    flex: none;
    transform: rotate(45deg);
    background: var(--down);
    box-shadow: 0 0 14px var(--down);
  }
  .alert.pending {
    background: linear-gradient(90deg, color-mix(in srgb, var(--pending) 22%, transparent), transparent);
    border-color: color-mix(in srgb, var(--pending) 45%, transparent);
  }
  .alert.pending i {
    background: var(--pending);
    box-shadow: 0 0 14px var(--pending);
  }
  .open {
    font-weight: 600;
    color: var(--error);
  }
  .calm {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin: 0;
  }
  .calm i {
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background: var(--up);
    box-shadow: 0 0 12px var(--up);
  }
  .change {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.45rem 0;
    border-top: 1px solid color-mix(in srgb, var(--fg) 8%, transparent);
  }
  .change a {
    color: var(--fg);
    text-decoration: none;
    font-weight: 500;
  }
  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--unknown);
  }
  .dot.up {
    background: var(--up);
  }
  .dot.down {
    background: var(--down);
  }
  .dot.pending {
    background: var(--pending);
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
</style>
