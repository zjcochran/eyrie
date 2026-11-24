<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { chartSummary } from '../lib/access.ts';
  import { normalize } from '../lib/appearance.ts';
  import { nearest, thin } from '../lib/chart.ts';
  import { millis, type Point } from '../lib/format.ts';
  import { app } from '../lib/session.svelte.ts';

  // label names the chart; format shows a value; top fixes the scale's top
  // (response times scale to the largest).
  let {
    points: all,
    from,
    to,
    label = 'Response time',
    format = millis,
    top: fixedTop,
    empty = 'No response times in this period.',
  }: {
    points: Point[];
    from: number;
    to: number;
    label?: string;
    format?: (v: number) => string;
    top?: number;
    empty?: string;
  } = $props();

  const w = 800;
  const h = 200;
  const pad = { l: 56, r: 8, t: 8, b: 22 };
  // Each chart on a page needs its own gradients.
  const id = `chart-${Math.random().toString(36).slice(2, 8)}`;
  // Aurora draws the line in the scheme's gradient.
  const glow = $derived(normalize(app.session?.user?.appearance).layout === 'aurora');

  // Enough points for the width; more only make the line noisy.
  const points = $derived(thin(all, 200));
  // At least 1 ms, so sub-millisecond checks still fill the chart.
  const top = $derived(fixedTop ?? Math.max(1, ...points.map((p) => p.v ?? 0)) * 1.1);
  // Below 0 only for values that go there, such as a clock's offset.
  const bottom = $derived(Math.min(0, ...points.map((p) => p.v ?? 0)) * 1.1);
  const x = (t: number) => pad.l + ((t - from) / Math.max(1, to - from)) * (w - pad.l - pad.r);
  const y = (v: number) => pad.t + (1 - (v - bottom) / (top - bottom)) * (h - pad.t - pad.b);
  // Areas fill to 0; markers run to the foot of the plot.
  const base = $derived(y(0));
  const floor = h - pad.b;

  // Lines break where there is no response time; each has an area under it.
  const runs = $derived.by(() => {
    const out: { line: string; area: string }[] = [];
    let cur: [number, number][] = [];
    const end = () => {
      if (!cur.length) return;
      const line = cur.map(([a, b]) => `${a.toFixed(1)},${b.toFixed(1)}`).join(' ');
      out.push({ line, area: `${cur[0][0].toFixed(1)},${base} ${line} ${cur[cur.length - 1][0].toFixed(1)},${base}` });
      cur = [];
    };
    for (const p of points) {
      if (p.v === null) end();
      else cur.push([x(p.t), y(p.v)]);
    }
    end();
    return out;
  });
  const downs = $derived(points.filter((p) => p.down));
  const ticks = $derived([0, 0.5, 1].map((f) => bottom + (top - bottom) * f).map((v) => ({ v, label: format(v) })));
  // Two days or more: dates. Less: date and time, as both ends of 24 hours
  // have the same time of day.
  const fmt = (t: number) =>
    to - from >= 2 * 86400000
      ? new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
      : new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  const any = $derived(points.some((p) => p.v !== null));

  // The point under the pointer, with its time and value.
  let hover = $state(-1);
  function move(e: PointerEvent) {
    const r = (e.currentTarget as SVGElement).getBoundingClientRect();
    const t = from + (((e.clientX - r.left) / r.width) * w - pad.l) / (w - pad.l - pad.r) * (to - from);
    hover = nearest(points, t);
  }
  const at = $derived(hover >= 0 ? points[hover] : null);

  // In words for screen readers; in the accessibility mode, the points one
  // by one from the keyboard, and all of them as a table.
  const summary = $derived(chartSummary(label, points, from, to, format));
  const accessible = $derived(normalize(app.session?.user?.appearance).accessible);
  const time = (t: number) => new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  const say = (p: (typeof points)[number]) => `${time(p.t)}: ${p.v === null ? 'no answer' : format(p.v)}${p.down ? ', down' : ''}`;
  function key(e: KeyboardEvent) {
    const last = points.length - 1;
    const steps: Record<string, number> = { ArrowLeft: -1, ArrowDown: -1, ArrowRight: 1, ArrowUp: 1, PageDown: -10, PageUp: 10 };
    let next = hover < 0 ? last : hover;
    if (e.key in steps) next += steps[e.key];
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = last;
    else return;
    e.preventDefault();
    hover = Math.max(0, Math.min(last, next));
  }
</script>

{#if any}
  <div class="wrap">
    <svg viewBox="0 0 {w} {h}" role="img" aria-label={summary} onpointermove={move} onpointerleave={() => (hover = -1)}>
      <defs>
        <linearGradient id="{id}-fill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" class="fill-top" />
          <stop offset="1" class="fill-bottom" />
        </linearGradient>
        <linearGradient id="{id}-line" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" style="stop-color: var(--logo-a, var(--chart, var(--accent)))" />
          <stop offset="0.55" style="stop-color: var(--chart, var(--accent))" />
          <stop offset="1" style="stop-color: var(--logo-b, var(--chart, var(--accent)))" />
        </linearGradient>
      </defs>
      {#each ticks as tk (tk.v)}
        <line class="grid" x1={pad.l} x2={w - pad.r} y1={y(tk.v)} y2={y(tk.v)} />
        <text class="axis" x={pad.l - 6} y={y(tk.v) + 4} text-anchor="end">{tk.label}</text>
      {/each}
      {#each downs as p (p.t)}
        <line class="down" x1={x(p.t)} x2={x(p.t)} y1={pad.t} y2={floor} />
      {/each}
      {#each runs as r, i (i)}
        <polygon class="area" points={r.area} fill="url(#{id}-fill)" />
        <polyline class="line" points={r.line} style:stroke={glow ? `url(#${id}-line)` : undefined} />
      {/each}
      {#if at && at.v !== null}
        <line class="cursor" x1={x(at.t)} x2={x(at.t)} y1={pad.t} y2={floor} />
        <circle class="mark" cx={x(at.t)} cy={y(at.v)} r="4" />
      {/if}
      <text class="axis" x={pad.l} y={h - 6}>{fmt(from)}</text>
      <text class="axis" x={w - pad.r} y={h - 6} text-anchor="end">{fmt(to)}</text>
    </svg>
    {#if at}
      <div class="tip" style:left="{(x(at.t) / w) * 100}%">
        <div class="muted">{new Date(at.t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</div>
        <div class="value">{at.v === null ? 'no answer' : format(at.v)}{at.down ? ' · down' : ''}</div>
      </div>
    {/if}
  </div>
  {#if accessible}
    <div
      class="stepper"
      role="slider"
      tabindex="0"
      aria-label="{label}, point by point"
      aria-valuemin={0}
      aria-valuemax={points.length - 1}
      aria-valuenow={Math.max(0, hover)}
      aria-valuetext={say(points[hover < 0 ? points.length - 1 : hover])}
      onkeydown={key}
      onfocus={() => hover < 0 && (hover = points.length - 1)}
      onblur={() => (hover = -1)}
    >
      <span>{say(points[hover < 0 ? points.length - 1 : hover])}</span>
      <span class="muted">← → a point, Page Up and Page Down ten, Home and End the ends</span>
    </div>
    <details class="values">
      <summary>Show the values as a table</summary>
      <table>
        <caption class="sr-only">{label}</caption>
        <thead><tr><th scope="col">Time</th><th scope="col">{label}</th><th scope="col">Down</th></tr></thead>
        <tbody>
          {#each points as p (p.t)}
            <tr><td>{time(p.t)}</td><td>{p.v === null ? 'no answer' : format(p.v)}</td><td>{p.down ? 'down' : ''}</td></tr>
          {/each}
        </tbody>
      </table>
    </details>
  {/if}
{:else}<p class="muted">{empty}</p>{/if}

<style>
  .wrap {
    position: relative;
  }
  svg {
    width: 100%;
    height: auto;
    display: block;
    touch-action: pan-y;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .axis {
    fill: var(--muted);
    font-size: 11px;
  }
  .fill-top {
    stop-color: var(--chart, var(--accent));
    stop-opacity: 0.35;
  }
  .fill-bottom {
    stop-color: var(--chart, var(--accent));
    stop-opacity: 0;
  }
  .line {
    fill: none;
    stroke: var(--chart, var(--accent));
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
  .down {
    stroke: var(--down);
    stroke-opacity: 0.3;
    stroke-width: 4;
  }
  .cursor {
    stroke: var(--muted);
    stroke-width: 1;
  }
  .mark {
    fill: var(--chart, var(--accent));
    stroke: var(--surface);
    stroke-width: 2;
  }
  .tip {
    position: absolute;
    top: 0.25rem;
    transform: translateX(-50%);
    pointer-events: none;
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: color-mix(in srgb, var(--surface) 92%, transparent);
    backdrop-filter: blur(8px);
    box-shadow: 0 8px 24px rgb(0 0 0 / 0.25);
    font-size: 0.8rem;
    white-space: nowrap;
  }
  .value {
    font-family: var(--mono, ui-monospace, monospace);
  }
  .stepper {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
    margin-top: 0.5rem;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    font-size: 0.9rem;
  }
  .values {
    margin-top: 0.5rem;
  }
  .values table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  .values th,
  .values td {
    padding: 0.25rem 0.5rem;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
</style>
