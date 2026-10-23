<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { textOn } from '../lib/format.ts';

  let { name, color, value = '' }: { name: string; color: string; value?: string } = $props();
</script>

<span class="chip" style:background={color} style:color={textOn(color)}>{name}{#if value}: {value}{/if}</span>

<style>
  .chip {
    display: inline-block;
    padding: 0.05rem 0.45rem;
    border-radius: 999px;
    font-size: 0.75rem;
    line-height: 1.4;
    white-space: nowrap;
  }
</style>
