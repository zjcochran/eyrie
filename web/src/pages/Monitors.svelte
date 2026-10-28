<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import MonitorTree from '../components/MonitorTree.svelte';
  import { link } from '../lib/nav.svelte.ts';
  import type { Route } from '../lib/route.ts';
  import GroupPage from './GroupPage.svelte';
  import Home from './Home.svelte';
  import MonitorForm from './MonitorForm.svelte';
  import MonitorPage from './MonitorPage.svelte';

  // The list of monitors on the left, and what is chosen in it on the
  // right. On a narrow screen, one at a time: the list, or what was chosen.
  let { route, admin }: { route: Route; admin: boolean } = $props();

  const selected = $derived(
    route.page === 'monitor' || route.page === 'edit-monitor' ? `m${route.id}` : route.page === 'group' ? `g${route.id}` : '',
  );
  const chosen = $derived(route.page !== 'home');
</script>

<div class="split" class:chosen>
  <aside class="side" aria-label="Monitors">
    <MonitorTree {selected} {admin} />
  </aside>
  <section class="pane">
    {#if chosen}<p class="back"><a href="/" onclick={link}>← Monitors</a></p>{/if}
    {#if route.page === 'home'}
      <Home />
    {:else if route.page === 'monitor'}
      <MonitorPage id={route.id} />
    {:else if route.page === 'group'}
      {#key route.id}<GroupPage id={route.id} {admin} />{/key}
    {:else if admin}
      {#key route.id}<MonitorForm id={route.id} />{/key}
    {:else}
      <h1>Not found</h1>
    {/if}
  </section>
</div>

<style>
  .split {
    display: grid;
    grid-template-columns: minmax(18rem, 26rem) minmax(0, 1fr);
    gap: 1.5rem;
    align-items: start;
  }
  /* The list keeps its place while the page beside it scrolls. Scrolling
     clips at the padding's edge, so the padding leaves room for focus rings
     and the margin takes it back. */
  .side {
    position: sticky;
    top: 1rem;
    max-height: calc(100vh - 2rem);
    overflow-y: auto;
    padding: 0.25rem 0.25rem 0.5rem;
    margin: -0.25rem 0 0 -0.25rem;
  }
  .pane {
    min-width: 0;
  }
  .back {
    display: none;
  }
  @media (max-width: 56rem) {
    .split {
      grid-template-columns: minmax(0, 1fr);
    }
    .side {
      position: static;
      max-height: none;
      overflow: visible;
      padding: 0;
      margin: 0;
    }
    .chosen .side,
    .split:not(.chosen) .pane {
      display: none;
    }
    .back {
      display: block;
    }
  }
</style>
