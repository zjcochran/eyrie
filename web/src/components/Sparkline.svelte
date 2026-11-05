<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- A monitor's recent response times as a small line. -->
<script lang="ts">
  import type { Beat } from '../lib/api.ts';

  let { beats = [], w = 84, h = 26 }: { beats?: Beat[]; w?: number; h?: number } = $props();

  const path = $derived.by(() => {
    const vs = beats.slice(-24).map((b) => b.latency_ms);
    const known = vs.filter((v): v is number => v !== null);
    if (known.length < 2) return '';
    const max = Math.max(...known);
    const min = Math.min(...known);
    let d = '';
    let pen = false;
    vs.forEach((v, i) => {
      if (v === null) {
        pen = false;
        return;
      }
      const x = (i / Math.max(1, vs.length - 1)) * w;
      const y = h - 3 - ((v - min) / (max - min || 1)) * (h - 6);
      d += `${pen ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)} `;
      pen = true;
    });
    return d.trim();
  });
</script>

<svg viewBox="0 0 {w} {h}" width={w} height={h} aria-hidden="true"><path d={path} /></svg>

<style>
  svg {
    display: block;
    flex: none;
  }
  path {
    fill: none;
    stroke: currentColor;
    stroke-width: 1.5;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
</style>
