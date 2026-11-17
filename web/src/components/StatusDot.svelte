<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Status } from '../lib/api.ts';

  import { statusWords } from '../lib/access.ts';

  let { status, active = true, label = false }: { status: Status; active?: boolean; label?: boolean } = $props();
  const text = $derived(active ? status : 'paused');
  const words = $derived(statusWords[text]);
</script>

<!-- A symbol marks the status too, with a colour vision chosen or in the
     accessibility mode (access.css). -->
{#if label}<span class="dot {text}" title={text} aria-hidden="true"><span class="symbol">{words.symbol}</span></span><span class="label"
    >{text}</span
  >{:else}<span class="dot {text}" title={text} role="img" aria-label={words.word}><span class="symbol" aria-hidden="true">{words.symbol}</span></span
  >{/if}

<style>
  .dot {
    display: inline-block;
    width: 0.7rem;
    height: 0.7rem;
    border-radius: 50%;
    background: var(--unknown);
    flex: none;
    vertical-align: middle;
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
  .label {
    margin-left: 0.4rem;
    text-transform: capitalize;
  }
  .symbol {
    display: none;
  }
  :global(:root[data-vision]) .dot,
  :global(:root[data-accessible]) .dot {
    display: inline-grid;
    place-items: center;
    width: 1.05rem;
    height: 1.05rem;
  }
  :global(:root[data-vision]) .symbol,
  :global(:root[data-accessible]) .symbol {
    display: block;
    color: var(--bg);
    font-size: 0.7rem;
    font-weight: 800;
    line-height: 1;
  }
  /* Windows' high-contrast mode drops backgrounds: keep the colours, with a
     ring and the symbol. */
  @media (forced-colors: active) {
    .dot {
      forced-color-adjust: none;
      display: inline-grid;
      place-items: center;
      width: 1.05rem;
      height: 1.05rem;
      border: 1px solid CanvasText;
    }
    .symbol {
      display: block;
      color: var(--bg);
      font-size: 0.7rem;
      font-weight: 800;
      line-height: 1;
    }
  }
</style>
