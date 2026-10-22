<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { millis, type Point } from '../lib/format.ts';

  let { points, from, to }: { points: Point[]; from: number; to: number } = $props();

  const w = 800;
  const h = 180;
  const pad = { l: 56, r: 8, t: 8, b: 22 };

  const top = $derived(Math.max(10, ...points.map((p) => p.v ?? 0)) * 1.1);
  const x = (t: number) => pad.l + ((t - from) / Math.max(1, to - from)) * (w - pad.l - pad.r);
  const y = (v: number) => pad.t + (1 - v / top) * (h - pad.t - pad.b);

  // Lines break where there is no response time.
  const lines = $derived.by(() => {
    const out: string[] = [];
    let cur: string[] = [];
    for (const p of points) {
      if (p.v === null) {
        if (cur.length) out.push(cur.join(' '));
        cur = [];
      } else {
        cur.push(`${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`);
      }
    }
    if (cur.length) out.push(cur.join(' '));
    return out;
  });
  const downs = $derived(points.filter((p) => p.down));
  const ticks = $derived([0, 0.5, 1].map((f) => ({ v: top * f, label: millis(top * f) })));
  // Two days or more: dates. Less: date and time, as both ends of 24 hours
  // have the same time of day.
  const fmt = (t: number) =>
    to - from >= 2 * 86400000
      ? new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
      : new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  const any = $derived(points.some((p) => p.v !== null));
</script>

{#if any}<svg viewBox="0 0 {w} {h}" role="img" aria-label="Response time">
  {#each ticks as tk (tk.v)}
    <line class="grid" x1={pad.l} x2={w - pad.r} y1={y(tk.v)} y2={y(tk.v)} />
    <text class="axis" x={pad.l - 6} y={y(tk.v) + 4} text-anchor="end">{tk.label}</text>
  {/each}
  {#each downs as p (p.t)}
    <line class="down" x1={x(p.t)} x2={x(p.t)} y1={pad.t} y2={h - pad.b} />
  {/each}
  {#each lines as l, i (i)}
    <polyline class="line" points={l} />
  {/each}
  <text class="axis" x={pad.l} y={h - 6}>{fmt(from)}</text>
  <text class="axis" x={w - pad.r} y={h - 6} text-anchor="end">{fmt(to)}</text>
</svg>
{:else}<p class="muted">No response times in this period.</p>{/if}

<style>
  svg {
    width: 100%;
    height: auto;
    display: block;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .axis {
    fill: var(--muted);
    font-size: 11px;
  }
  .line {
    fill: none;
    stroke: var(--accent);
    stroke-width: 1.5;
  }
  .down {
    stroke: var(--down);
    stroke-opacity: 0.35;
    stroke-width: 3;
  }
</style>
