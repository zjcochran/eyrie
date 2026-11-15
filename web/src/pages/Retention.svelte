<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Notice from '../components/Notice.svelte';
  import { message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  let days = $state(90);
  let seconds = $state(60);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  onMount(() => {
    const fail = (e: unknown) => (notice = { ok: false, text: message(e) });
    api.retention().then((r) => (days = r.heartbeat_days), fail);
    api.uptimeSettings().then((u) => (seconds = u.refresh_seconds), fail);
  });

  async function run(f: () => Promise<string>) {
    busy = true;
    try {
      notice = { ok: true, text: await f() };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function saveDays(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      days = (await api.saveRetention(days)).heartbeat_days;
      return 'Saved. Older heartbeats are deleted at the next daily clean-up.';
    });
  }

  function saveSeconds(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      seconds = (await api.saveUptimeSettings(seconds)).refresh_seconds;
      return 'Saved. The uptime figures are worked out now, then at this interval.';
    });
  }
</script>

<Notice bind:notice />
<section class="card">
  <h2>Heartbeats</h2>
  <p>
    Every check is kept as a heartbeat for this many days. Uptime figures do not need them: hourly totals are kept 35
    days and daily totals 400 days.
  </p>
  <form onsubmit={saveDays}>
    <label>Keep heartbeats for (days) <input type="number" bind:value={days} min="1" max="3650" required /></label>
    <div><button class="primary" disabled={busy}>Save</button></div>
  </form>
</section>
<section class="card">
  <h2>Uptime figures</h2>
  <p>
    Every monitor's uptime over 24 hours, 30 days and a year is worked out in the background, so that no page waits for
    it. Pages show the figures as of the last time.
  </p>
  <form onsubmit={saveSeconds}>
    <label
      >Work them out every (seconds) <input type="number" bind:value={seconds} min="10" max="3600" required /></label
    >
    <div><button class="primary" disabled={busy}>Save</button></div>
  </form>
</section>
