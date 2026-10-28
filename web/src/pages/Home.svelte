<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import StatusDot from '../components/StatusDot.svelte';
  import { ago } from '../lib/format.ts';
  import { live } from '../lib/live.svelte.ts';
  import { link } from '../lib/nav.svelte.ts';

  // Beside the list: what needs attention.
  const down = $derived(live.monitors.filter((m) => m.active && m.status.status === 'down'));
  const pending = $derived(live.monitors.filter((m) => m.active && m.status.status === 'pending'));
  const maintenance = $derived(live.monitors.filter((m) => m.active && m.status.status === 'maintenance'));
</script>

<h2>Overview</h2>
{#if !live.loaded}
  <p class="muted">Loading…</p>
{:else if down.length === 0 && pending.length === 0}
  <section class="card">
    <p><StatusDot status="up" /> Nothing is down.</p>
    <p class="muted">Choose a monitor or group in the list to see it here.</p>
  </section>
{:else}
  {#each [{ title: 'Down', list: down }, { title: 'Pending', list: pending }] as section (section.title)}
    {#if section.list.length}
      <section class="card">
        <h3>{section.title}</h3>
        <ul>
          {#each section.list as m (m.id)}
            <li>
              <StatusDot status={m.status.status} />
              <a href="/monitors/{m.id}" onclick={link}>{m.name}</a>
              <span class="muted">{m.status.message}{m.status.since ? ` · ${ago(m.status.since)}` : ''}</span>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/each}
{/if}
{#if maintenance.length}
  <section class="card">
    <h3>In maintenance</h3>
    <ul>
      {#each maintenance as m (m.id)}
        <li><StatusDot status="maintenance" /> <a href="/monitors/{m.id}" onclick={link}>{m.name}</a> <span class="muted">{m.status.maintenance}</span></li>
      {/each}
    </ul>
  </section>
{/if}

<style>
  h2 {
    margin-top: 0;
  }
  h3 {
    margin: 0 0 0.5rem;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.4rem;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    overflow-wrap: anywhere;
  }
</style>
