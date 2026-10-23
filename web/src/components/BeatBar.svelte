<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Beat } from '../lib/api.ts';
  import { millis } from '../lib/format.ts';

  let { beats = [], slots = 50 }: { beats?: Beat[]; slots?: number } = $props();
  // Oldest on the left; empty slots before the first heartbeat.
  const cells = $derived([...Array(Math.max(0, slots - beats.length)).fill(null), ...beats.slice(-slots)] as (Beat | null)[]);
</script>

<div class="bar" role="img" aria-label="Last {beats.length} checks">
  {#each cells as b, i (i)}
    <span
      class="cell {b?.status ?? 'empty'}"
      title={b ? `${new Date(b.at).toLocaleString()} · ${b.status} · ${millis(b.latency_ms)}\n${b.message}` : ''}
    ></span>
  {/each}
</div>

<style>
  .bar {
    display: flex;
    gap: 2px;
    height: 1.4rem;
    min-width: 0;
  }
  .cell {
    flex: 1;
    min-width: 2px;
    max-width: 8px;
    border-radius: 2px;
    background: var(--border);
  }
  .up {
    background: var(--up);
  }
  .down {
    background: var(--down);
  }
  .pending {
    background: var(--pending);
  }
</style>
