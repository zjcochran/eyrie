<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type StatusPage, message } from '../lib/api.ts';
  import { link } from '../lib/nav.svelte.ts';
  import { api, app } from '../lib/session.svelte.ts';

  const admin = $derived(app.session?.user?.role === 'admin');
  let pages = $state<StatusPage[] | null>(null);
  let error = $state('');

  onMount(() => {
    api.statusPages().then(
      (p) => (pages = p),
      (e) => (error = message(e)),
    );
  });
</script>

<div class="head row">
  <h1>Status pages</h1>
  {#if admin}<a class="button primary" href="/status-pages/new" onclick={link}>Add a status page</a>{/if}
</div>
{#if error}<p class="notice error">{error}</p>{/if}

<section class="card">
  <p class="muted">
    A status page shows the status and uptime of chosen monitors, grouped in sections, with incidents and maintenance,
    to anyone once it is published (only names and statuses, never check messages or settings). Its monitors also get
    status badges.
  </p>
  {#if pages === null}
    <p class="muted">Loading…</p>
  {:else if pages.length === 0}
    <p class="muted">No status pages yet.</p>
  {:else}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Page</th><th>Address</th><th>Sections</th><th></th></tr></thead>
        <tbody>
          {#each pages as p (p.id)}
            <tr>
              <td>
                {p.title}
                {#if !p.published}<span class="muted">(not published)</span>{/if}
              </td>
              <td><a href="/status/{p.slug}" onclick={link}>/status/{p.slug}</a></td>
              <td>{p.sections.length}</td>
              <td><a class="button" href="/status-pages/{p.id}" onclick={link}>{admin ? 'Edit' : 'Incidents'}</a></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<style>
  .head {
    justify-content: space-between;
    margin-bottom: 0.5rem;
  }
  .head h1 {
    margin: 0;
  }
</style>
