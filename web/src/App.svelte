<script lang="ts">
  import { onMount } from 'svelte';
  import { getInfo, type Info } from './lib/api.ts';

  let info = $state<Info | null>(null);
  let error = $state('');

  onMount(() => {
    getInfo()
      .then((i) => (info = i))
      .catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
  });
</script>

<main>
  <h1>Eyrie</h1>
  {#if info}
    <p>Version {info.version}</p>
  {:else if error}
    <p class="error">The server could not be reached: {error}</p>
  {:else}
    <p>Loading…</p>
  {/if}
</main>

<style>
  main {
    max-width: 40rem;
    margin: 4rem auto;
    padding: 0 1rem;
  }
  .error {
    color: var(--error);
  }
</style>
