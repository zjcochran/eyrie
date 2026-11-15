<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Series } from '../lib/storage.ts';

  // One line per series (a volume's use, a disk's temperature), with a
  // legend and the values at the pointer. top fixes the scale's top; floor
  // starts it above 0, as temperatures do.
  let {
    series,
    from,
    to,
    label,
    format,
    top: fixedTop,
    floor = 0,
    limit,
    empty,
  }: {
    series: Series[];
    from: number;
    to: number;
    label: string;
    format: (v: number) => string;
    top?: number;
    floor?: number;
    /** A line across the chart at the monitor's limit. */
    limit?: number;
    empty: string;
  } = $props();

  const w = 800;
  const h = 200;
  const pad = { l: 52, r: 8, t: 8, b: 22 };
  // Fixed hues, in order, so a volume keeps its colour.
  const colors = ['var(--chart, var(--accent))', '#5b8def', '#e3a33b', '#c264d6', '#3fb68b', '#e5604d', '#8e9aaf', '#d6c14a'];
  const color = (i: number) => colors[i % colors.length];

  const values = $derived(series.flatMap((s) => s.points.map((p) => p.v)).filter((v): v is number => v !== null));
  const any = $derived(values.length > 0);
  const top = $derived(fixedTop ?? Math.max(floor + 1, limit ?? 0, ...values) + 5);
  const bottom = $derived(fixedTop !== undefined ? floor : Math.max(0, Math.min(floor, ...values) - 5));
  const x = (t: number) => pad.l + ((t - from) / Math.max(1, to - from)) * (w - pad.l - pad.r);
  const y = (v: number) => pad.t + (1 - (v - bottom) / Math.max(1, top - bottom)) * (h - pad.t - pad.b);
  const base = h - pad.b;
  const ticks = $derived([0, 0.5, 1].map((f) => bottom + (top - bottom) * f));

  // Each series' line, broken where it has no value.
  const lines = $derived(
    series.map((s) => {
      const runs: string[] = [];
      let cur: string[] = [];
      for (const p of s.points) {
        if (p.v === null) {
          if (cur.length) runs.push(cur.join(' '));
          cur = [];
        } else cur.push(`${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`);
      }
      if (cur.length) runs.push(cur.join(' '));
      return runs;
    }),
  );

  // Each series' latest value, marked so that a short history still shows.
  const ends = $derived(
    series.map((s) => {
      for (let i = s.points.length - 1; i >= 0; i--) {
        const { t, v } = s.points[i];
        if (v !== null) return { x: x(t), y: y(v) };
      }
      return null;
    }),
  );

  const fmt = (t: number) =>
    to - from >= 2 * 86400000
      ? new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
      : new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });

  // The time under the pointer: the nearest point of the first series.
  let hover = $state(-1);
  function move(e: PointerEvent) {
    const pts = series[0]?.points ?? [];
    if (!pts.length) return;
    const r = (e.currentTarget as SVGElement).getBoundingClientRect();
    const t = from + ((((e.clientX - r.left) / r.width) * w - pad.l) / (w - pad.l - pad.r)) * (to - from);
    let best = 0;
    for (let i = 1; i < pts.length; i++) if (Math.abs(pts[i].t - t) < Math.abs(pts[best].t - t)) best = i;
    hover = best;
  }
  const at = $derived(hover >= 0 ? (series[0]?.points[hover]?.t ?? null) : null);
</script>

{#if any}
  <div class="wrap">
    <svg viewBox="0 0 {w} {h}" role="img" aria-label={label} onpointermove={move} onpointerleave={() => (hover = -1)}>
      {#each ticks as v (v)}
        <line class="grid" x1={pad.l} x2={w - pad.r} y1={y(v)} y2={y(v)} />
        <text class="axis" x={pad.l - 6} y={y(v) + 4} text-anchor="end">{format(v)}</text>
      {/each}
      {#if limit !== undefined && limit < top}
        <line class="limit" x1={pad.l} x2={w - pad.r} y1={y(limit)} y2={y(limit)} />
      {/if}
      {#each lines as runs, i (series[i].name)}
        {#each runs as pts, j (j)}
          <polyline class="line" points={pts} style:stroke={color(i)} />
        {/each}
        {#if ends[i]}<circle class="end" cx={ends[i]?.x} cy={ends[i]?.y} r="3" style:fill={color(i)} />{/if}
      {/each}
      {#if at !== null}
        <line class="cursor" x1={x(at)} x2={x(at)} y1={pad.t} y2={base} />
      {/if}
      <text class="axis" x={pad.l} y={h - 6}>{fmt(from)}</text>
      <text class="axis" x={w - pad.r} y={h - 6} text-anchor="end">{fmt(to)}</text>
    </svg>
    {#if at !== null}
      <div class="tip" style:left="{(x(at) / w) * 100}%">
        <div class="muted">{new Date(at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</div>
        {#each series as s, i (s.name)}
          {@const v = s.points[hover]?.v ?? null}
          {#if v !== null}<div><i style:background={color(i)}></i>{s.name} <span class="value">{format(v)}</span></div>{/if}
        {/each}
      </div>
    {/if}
  </div>
  {#if series.length > 1}
    <ul class="legend">
      {#each series as s, i (s.name)}<li><i style:background={color(i)}></i>{s.name}</li>{/each}
    </ul>
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
  .limit {
    stroke: var(--down);
    stroke-opacity: 0.6;
    stroke-width: 1;
    stroke-dasharray: 4 4;
  }
  .axis {
    fill: var(--muted);
    font-size: 11px;
  }
  .line {
    fill: none;
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
  .end {
    stroke: var(--surface);
    stroke-width: 1.5;
  }
  .cursor {
    stroke: var(--muted);
    stroke-width: 1;
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
  i {
    display: inline-block;
    width: 0.6rem;
    height: 0.6rem;
    border-radius: 2px;
    margin-right: 0.35rem;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 1rem;
    margin: 0.5rem 0 0;
    padding: 0;
    list-style: none;
    font-size: 0.85rem;
    color: var(--muted);
  }
</style>
