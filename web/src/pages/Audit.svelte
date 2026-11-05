<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Pager from '../components/Pager.svelte';
  import { type AuditEntry, type PageSize, message } from '../lib/api.ts';
  import { offsetOf } from '../lib/paging.ts';
  import { api } from '../lib/session.svelte.ts';

  let entries = $state<AuditEntry[]>([]);
  let total = $state(0);
  let actions = $state<string[]>([]);
  let users = $state<string[]>([]);
  let action = $state('');
  let user = $state('');
  let page = $state(1);
  let size = $state<PageSize>(25);
  let error = $state('');
  let busy = $state(false);

  async function load() {
    busy = true;
    error = '';
    try {
      const p = await api.audit({ action, user, offset: offsetOf(page, size), size });
      entries = p.entries;
      total = p.total;
      actions = p.actions;
      users = p.users;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  // A new filter starts at its first page.
  function filter() {
    page = 1;
    load();
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
      <select bind:value={action} onchange={filter}>
        <option value="">Any</option>
        {#each actions as a (a)}<option value={a}>{a}</option>{/each}
      </select>
    </label>
    <label>
      User
      <select bind:value={user} onchange={filter}>
        <option value="">Anyone</option>
        {#each users as u (u)}<option value={u}>{u}</option>{/each}
      </select>
    </label>
  </div>
  {#if entries.length}
    <Pager {total} shown={entries.length} {busy} bind:page bind:size onchange={load} />
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
