<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type AuditEntry, message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  const pageSize = 100;
  let entries = $state<AuditEntry[]>([]);
  let actions = $state<string[]>([]);
  let users = $state<string[]>([]);
  let action = $state('');
  let user = $state('');
  // Whether a page came back full, so there may be older entries.
  let more = $state(false);
  let error = $state('');
  let busy = $state(false);

  // The first page, or with older the page after the last entry shown.
  async function load(older = false) {
    busy = true;
    error = '';
    try {
      const before = older ? entries[entries.length - 1]?.id : undefined;
      const p = await api.audit({ action, user, before });
      entries = older ? [...entries, ...p.entries] : p.entries;
      actions = p.actions;
      users = p.users;
      more = p.entries.length === pageSize;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  onMount(() => load());
</script>

{#if error}<p class="notice error" role="status">{error}</p>{/if}

<section class="card">
  <p>
    Who did what: sign-ins, failed sign-ins and sign-outs, and every change made in Eyrie, newest first. Entries are kept 400
    days.
  </p>
  <div class="filters row">
    <label>
      Action
      <select bind:value={action} onchange={() => load()}>
        <option value="">Any</option>
        {#each actions as a (a)}<option value={a}>{a}</option>{/each}
      </select>
    </label>
    <label>
      User
      <select bind:value={user} onchange={() => load()}>
        <option value="">Anyone</option>
        {#each users as u (u)}<option value={u}>{u}</option>{/each}
      </select>
    </label>
  </div>
  {#if entries.length}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Time</th><th>User</th><th>Action</th><th>Details</th><th>Address</th></tr></thead>
        <tbody>
          {#each entries as e (e.id)}
            <tr>
              <td class="nowrap">{new Date(e.at).toLocaleString()}</td>
              <td>{e.user || '–'}</td>
              <td class="nowrap">{e.action}</td>
              <td class="mono">{e.details}</td>
              <td class="mono">{e.ip}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if more}<button disabled={busy} onclick={() => load(true)}>Older</button>{/if}
  {:else if !busy}
    <p class="muted">Nothing yet{action || user ? ' that matches' : ''}.</p>
  {/if}
</section>

<style>
  .filters {
    flex-wrap: wrap;
    gap: 1rem;
    margin-bottom: 1rem;
  }
  .nowrap {
    white-space: nowrap;
  }
  td.mono {
    font-size: 0.85rem;
    word-break: break-word;
  }
</style>
