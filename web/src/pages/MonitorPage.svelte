<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import Chart from '../components/Chart.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import TagChip from '../components/TagChip.svelte';
  import { type Beat, type Monitor, message } from '../lib/api.ts';
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
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);
  let checking = $state(false); // Check now was clicked; its heartbeat has not arrived
  let manualMessage = $state('');
  let now = $state(Date.now());

  const days = { '24h': 1, '7d': 7, '30d': 30 };
  const periods = ['24h', '7d', '30d'] as const;
  const from = $derived(now - days[period] * 86400000);
  const changes = $derived(beats.filter((b) => b.important).slice(0, 20));

  async function load() {
    try {
      monitor = await api.monitor(id);
      beats = await api.heartbeats(id, new Date(Date.now() - 86400000));
      await loadChart();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    }
  }

  async function loadChart() {
    now = Date.now();
    if (period === '24h') {
      points = beats.map((b) => ({ t: Date.parse(b.at), v: b.latency_ms, down: b.status === 'down' })).reverse();
      return;
    }
    const hours = await api.hours(id, days[period]);
    points = hours.map((h) => ({ t: Date.parse(h.start), v: h.avg_latency_ms, down: h.down > 0 }));
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
        since: b.important ? b.at : monitor.status.since,
        checking: false,
      };
      if (period === '24h') {
        now = Date.now();
        points = [...points, { t: Date.parse(b.at), v: b.latency_ms, down: b.status === 'down' }];
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

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if monitor}
  {@const m = monitor}
  {@const group = live.groups.find((g) => g.id === m.group_id)}
  <div class="head">
    <div>
      <h1><StatusDot status={m.status.status} active={m.active} /> {m.name}</h1>
      <p class="muted">
        {m.type} · every {duration(m.interval)}
        {#if m.agent_id}· on {m.agent_name ? `agent ${m.agent_name}` : 'a removed agent'}{/if}
        {#if group}· in <a href="/groups/{group.id}" onclick={link}>{group.name}</a>{/if}
        {#if !m.active}· paused{/if}
        {#if m.description}· {m.description}{/if}
      </p>
      {#if m.tags.length}
        <p class="tags">{#each m.tags as t (t.id)}<TagChip name={t.name} color={t.color} value={t.value} />{/each}</p>
      {/if}
    </div>
    {#if admin}
      <div class="row">
        {#if m.type !== 'push' && m.type !== 'manual' && m.active}
          <button disabled={busy || m.status.checking} onclick={checkNow}
            >Check now</button
          >
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

  <section class="card status">
    <div>
      <div class="big"><StatusDot status={m.status.status} active={m.active} label /></div>
      <div class="muted">{m.status.since ? `since ${new Date(m.status.since).toLocaleString()}` : 'not checked yet'}</div>
      {#if m.status.maintenance}<div class="muted">{m.status.maintenance}</div>{/if}
    </div>
    <div>
      <div class="muted">Last check</div>
      <div>{ago(m.status.last_check, now)}</div>
      <div class="muted msg">{m.status.message}</div>
    </div>
    <div>
      <div class="muted">Response</div>
      <div>{millis(m.status.latency_ms)}</div>
      <div class="muted">24 h average {millis(m.uptime.avg_latency_24h_ms)}</div>
    </div>
    <div>
      <div class="muted">Uptime</div>
      <div>24 h {percent(m.uptime['24h'])}</div>
      <div>30 d {percent(m.uptime['30d'])}</div>
      <div>1 y {percent(m.uptime['1y'])}</div>
    </div>
    {#if m.status.cert_expires}
      <div>
        <div class="muted">Certificate</div>
        <div>expires in {daysUntil(m.status.cert_expires, now)} days</div>
        <div class="muted">{new Date(m.status.cert_expires).toLocaleDateString()}</div>
      </div>
    {/if}
  </section>

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
      <h2>Response time</h2>
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
    <Chart {points} {from} to={now} />
  </section>

  <section class="card">
    <h2>Status changes, last 24 hours</h2>
    {#if changes.length === 0}
      <p class="muted">None.</p>
    {:else}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Time</th><th>Status</th><th>Message</th></tr></thead>
          <tbody>
            {#each changes as b (b.at + b.status)}
              <tr>
                <td>{new Date(b.at).toLocaleString()}</td>
                <td><StatusDot status={b.status} label /></td>
                <td>{b.message}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{:else if !notice}
  <p class="muted">Loading…</p>
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 1rem;
    align-items: flex-start;
  }
  .head h1 {
    margin-bottom: 0.25rem;
  }
  .status {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
    gap: 1rem;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    margin: 0.25rem 0 0;
  }
  .big {
    font-size: 1.3rem;
    font-weight: 600;
  }
  .msg {
    overflow-wrap: anywhere;
  }
  .between {
    justify-content: space-between;
  }
  .url {
    flex: 1;
    min-width: 0;
  }
</style>
