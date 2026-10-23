<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Status } from '../lib/api.ts';

  let { status, active = true, label = false }: { status: Status; active?: boolean; label?: boolean } = $props();
  const text = $derived(active ? status : 'paused');
</script>

<span class="dot {text}" title={text} aria-label={label ? undefined : text}></span>{#if label}<span class="label">{text}</span>{/if}

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
</style>
