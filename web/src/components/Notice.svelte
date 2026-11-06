<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The message above a page after an action: one that worked ("Saved.")
  // goes away after a few seconds; an error stays until the next action.
  let { notice = $bindable() }: { notice: { ok: boolean; text: string } | null } = $props();

  $effect(() => {
    if (!notice?.ok) return;
    const t = setTimeout(() => (notice = null), 5000);
    return () => clearTimeout(t);
  });
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}
