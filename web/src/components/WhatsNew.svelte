<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Markdown from './Markdown.svelte';
  import { type Upgrade } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  // Once per user after Eyrie is updated: the notes of the releases since
  // the version that ran before. ondone runs when there is nothing to show
  // or it is closed.
  let { ondone }: { ondone: () => void } = $props();

  let upgrade = $state<Upgrade | null>(null);
  let url = $state('');

  onMount(() => {
    api.whatsNew().then(
      (n) => {
        url = n.url;
        upgrade = n.upgrade;
        if (!upgrade) ondone();
      },
      () => ondone(),
    );
  });

  function dismiss() {
    if (upgrade) api.seenNews(upgrade.to).catch(() => {});
    upgrade = null;
    ondone();
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && upgrade && dismiss()} />

{#if upgrade}
  <div class="backdrop">
    <div class="card dialog" role="dialog" aria-modal="true" aria-labelledby="news-title">
      <h2 id="news-title">Eyrie was updated to {upgrade.to}</h2>
      <p class="muted">
        From {upgrade.from || 'an earlier version'}.
        {#if url}<a href={url} target="_blank" rel="noopener noreferrer">The releases on GitHub</a>{/if}
      </p>
      <div class="notes">
        {#each upgrade.releases as r (r.version)}
          <section>
            <h3>
              <a href={r.url} target="_blank" rel="noopener noreferrer">{r.name || r.version}</a>
              {#if r.published}<span class="muted">{new Date(r.published).toLocaleDateString()}</span>{/if}
            </h3>
            <Markdown text={r.notes || 'No release notes.'} />
          </section>
        {:else}
          <p>This build has no release notes.</p>
        {/each}
      </div>
      <div class="actions"><button class="primary" onclick={dismiss}>Close</button></div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: grid;
    /* No wider than the screen, whatever the notes hold. */
    grid-template-columns: minmax(0, 1fr);
    place-items: center;
    padding: 1rem;
    background: rgb(0 0 0 / 0.5);
  }
  .dialog {
    width: min(40rem, 100%);
    max-height: min(85vh, 48rem);
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    box-sizing: border-box;
    overflow-wrap: anywhere;
    box-shadow: 0 24px 64px rgb(0 0 0 / 0.4);
  }
  h2,
  h3 {
    margin: 0;
  }
  h3 {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.5rem;
  }
  h3 .muted {
    font-size: 0.85rem;
    font-weight: normal;
  }
  .notes {
    overflow-y: auto;
    padding-right: 0.25rem;
  }
  section + section {
    border-top: 1px solid var(--border);
    margin-top: 0.75rem;
    padding-top: 0.75rem;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    padding-top: 0.5rem;
  }
</style>
