<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  let days = $state(90);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  onMount(() => {
    api.retention().then(
      (r) => (days = r.heartbeat_days),
      (e) => (notice = { ok: false, text: message(e) }),
    );
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    try {
      days = (await api.saveRetention(days)).heartbeat_days;
      notice = { ok: true, text: 'Saved. Older heartbeats are deleted at the next daily clean-up.' };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}
<section class="card">
  <p>
    Every check is kept as a heartbeat for this many days. Uptime figures do not need them: hourly totals are kept 35
    days and daily totals 400 days.
  </p>
  <form onsubmit={save}>
    <label>Keep heartbeats for (days) <input type="number" bind:value={days} min="1" max="3650" required /></label>
    <div><button class="primary" disabled={busy}>Save</button></div>
  </form>
</section>
