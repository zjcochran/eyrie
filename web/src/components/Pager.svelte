<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { PageSize } from '../lib/api.ts';
  import { offsetOf, pages, range, sizes } from '../lib/paging.ts';

  // A list's page and page size; onchange loads the page they name.
  let {
    total,
    shown,
    page = $bindable(1),
    size = $bindable<PageSize>(25),
    busy = false,
    onchange,
  }: {
    total: number;
    shown: number;
    page?: number;
    size?: PageSize;
    busy?: boolean;
    onchange: () => void;
  } = $props();

  const count = $derived(pages(total, size));
  const offset = $derived(offsetOf(page, size));

  function go(p: number) {
    page = p;
    onchange();
  }
</script>

<div class="pager">
  <label>
    Show
    <select
      bind:value={size}
      onchange={() => {
        page = 1;
        onchange();
      }}
    >
      {#each sizes as s (s)}<option value={s}>{s === 'all' ? 'All' : s}</option>{/each}
    </select>
  </label>
  <span class="muted">{range(offset, shown, total)}</span>
  {#if count > 1}
    <div class="pages">
      <button disabled={busy || page <= 1} onclick={() => go(page - 1)}>‹ Newer</button>
      <span class="muted">Page {page} of {count}</span>
      <button disabled={busy || page >= count} onclick={() => go(page + 1)}>Older ›</button>
    </div>
  {/if}
</div>

<style>
  .pager {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem 1.25rem;
    margin-bottom: 0.75rem;
  }
  label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: normal;
  }
  .pages {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-left: auto;
  }
</style>
