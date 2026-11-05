<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Markdown from './Markdown.svelte';
  import { type Release, message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';
  import { close, closed } from '../lib/updates.ts';

  // A newer release, for admins, once they open Eyrie: closed for this tab,
  // put off for a day, or never shown again for this version.
  let release = $state<Release | null>(null);
  let current = $state('');
  let error = $state('');

  onMount(() => {
    api.availableUpdate().then(
      (a) => {
        current = a.current;
        if (a.release && !closed(a.release.version)) release = a.release;
      },
      () => {},
    );
  });

  function dismiss() {
    if (release) close(release.version);
    release = null;
  }

  async function remind(choice: 'later' | 'never') {
    if (!release) return;
    try {
      await api.remindUpdate(release.version, choice);
      release = null;
    } catch (e) {
      error = message(e);
    }
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && release && dismiss()} />

{#if release}
  <div class="backdrop">
    <div class="card dialog" role="dialog" aria-modal="true" aria-labelledby="update-title">
      <h2 id="update-title">Eyrie {release.version} is available</h2>
      <p class="muted">
        This is {current}.
        {#if release.published}Released {new Date(release.published).toLocaleDateString()}.{/if}
        <a href={release.url} target="_blank" rel="noopener noreferrer">The release on GitHub</a>
      </p>
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      <div class="notes"><Markdown text={release.notes || 'No release notes.'} /></div>
      <div class="actions">
        <button onclick={() => remind('never')}>Don't remind me again</button>
        <button onclick={() => remind('later')}>Remind me later</button>
        <button class="primary" onclick={dismiss}>Close</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: grid;
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
    box-shadow: 0 24px 64px rgb(0 0 0 / 0.4);
  }
  h2 {
    margin: 0;
  }
  .notes {
    overflow-y: auto;
    padding-right: 0.25rem;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 0.5rem;
    padding-top: 0.5rem;
  }
</style>
