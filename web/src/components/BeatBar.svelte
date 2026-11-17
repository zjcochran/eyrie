<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { beatSummary } from '../lib/access.ts';
  import type { Beat } from '../lib/api.ts';
  import { millis } from '../lib/format.ts';

  let { beats = [], slots = 50 }: { beats?: Beat[]; slots?: number } = $props();
  // Oldest on the left; empty slots before the first heartbeat.
  const cells = $derived([...Array(Math.max(0, slots - beats.length)).fill(null), ...beats.slice(-slots)] as (Beat | null)[]);
</script>

<div class="bar" role="img" aria-label={beatSummary(beats.slice(-slots))}>
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
  .maintenance {
    background: var(--maintenance);
  }
  /* With a colour vision chosen or in the accessibility mode, a pattern
     marks each status too: down hatched, pending dotted, maintenance
     striped across. */
  :global(:root[data-vision]) .down,
  :global(:root[data-accessible]) .down {
    background-image: repeating-linear-gradient(45deg, transparent 0 2px, var(--bg) 2px 3.5px);
  }
  :global(:root[data-vision]) .pending,
  :global(:root[data-accessible]) .pending {
    background-image: radial-gradient(circle, var(--bg) 1px, transparent 1.5px);
    background-size: 4px 4px;
  }
  :global(:root[data-vision]) .maintenance,
  :global(:root[data-accessible]) .maintenance {
    background-image: repeating-linear-gradient(0deg, transparent 0 3px, var(--bg) 3px 4.5px);
  }
  @media (forced-colors: active) {
    .cell {
      forced-color-adjust: none;
    }
    .down {
      background-image: repeating-linear-gradient(45deg, transparent 0 2px, Canvas 2px 3.5px);
    }
  }
</style>
