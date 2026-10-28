<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import { type StatusChange, message } from '../lib/api.ts';
  import { live, onBeat } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { api } from '../lib/session.svelte.ts';

  const pageSize = 100;
  let changes = $state<StatusChange[]>([]);
  // Whether a page came back full, so there may be older changes.
  let more = $state(false);
  let loaded = $state(false);
  let error = $state('');
  let busy = $state(false);

  // The first page, or with older the page after the last change shown.
  async function load(older = false) {
    busy = true;
    error = '';
    try {
      const p = await api.statusChanges(older ? changes[changes.length - 1]?.id : undefined);
      changes = older ? [...changes, ...p] : p;
      more = p.length === pageSize;
      loaded = true;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  function monitorName(id: number): string {
    return live.monitors.find((m) => m.id === id)?.name ?? `Monitor ${id}`;
  }

  onMount(() => {
    load();
    // New changes come first as they happen; they have no ID until reloaded,
    // and only the oldest change's ID is needed for older pages.
    return onBeat((b) => {
      if (b.important && loaded) changes = [{ ...b, id: 0 }, ...changes];
    });
  });
</script>

<h1>Events</h1>
{#if error}<p class="notice error" role="status">{error}</p>{/if}

<section class="card">
  <p class="muted">
    Every monitor's status changes, newest first: down, up, pending and maintenance. They are kept as long as heartbeats
    (Settings → History).
  </p>
  {#if changes.length}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Time</th><th>Monitor</th><th>Status</th><th>Message</th></tr></thead>
        <tbody>
          {#each changes as c (`${c.id}-${c.monitor_id}-${c.at}`)}
            <tr>
              <td class="nowrap">{new Date(c.at).toLocaleString()}</td>
              <td><a href="/monitors/{c.monitor_id}" onclick={link}>{monitorName(c.monitor_id)}</a></td>
              <td class="nowrap"><StatusDot status={c.status} label /></td>
              <td class="message">{c.message}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if more}<button disabled={busy} onclick={() => load(true)}>Older</button>{/if}
  {:else if loaded}
    <p class="muted">Nothing yet.</p>
  {:else if !error}
    <p class="muted">Loading…</p>
  {/if}
</section>

<style>
  .nowrap {
    white-space: nowrap;
  }
  .message {
    word-break: break-word;
  }
</style>
