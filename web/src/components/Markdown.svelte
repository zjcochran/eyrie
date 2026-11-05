<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { type Inline, blocks } from '../lib/markdown.ts';

  // Markdown shown as text, never as HTML (see lib/markdown.ts).
  let { text }: { text: string } = $props();
  const parts = $derived(blocks(text));
</script>

{#snippet line(ins: Inline[])}
  {#each ins as x, i (i)}
    {#if x.kind === 'code'}<code>{x.text}</code>{:else if x.kind === 'strong'}<strong>{x.text}</strong>{:else if x.kind === 'link'}<a
        href={x.href}
        target="_blank"
        rel="noopener noreferrer">{x.text}</a
      >{:else}{x.text}{/if}
  {/each}
{/snippet}

<div class="md">
  {#each parts as b, i (i)}
    {#if b.kind === 'heading'}
      <p class="heading">{@render line(b.text)}</p>
    {:else if b.kind === 'list'}
      <svelte:element this={b.ordered ? 'ol' : 'ul'}>
        {#each b.items as item, j (j)}<li>{@render line(item)}</li>{/each}
      </svelte:element>
    {:else if b.kind === 'code'}
      <pre><code>{b.text}</code></pre>
    {:else}
      <p>{@render line(b.text)}</p>
    {/if}
  {/each}
</div>

<style>
  .md {
    line-height: 1.5;
  }
  .md > :first-child {
    margin-top: 0;
  }
  .heading {
    font-weight: 600;
    margin: 1rem 0 0.4rem;
  }
  ul,
  ol {
    padding-left: 1.4rem;
  }
  pre {
    overflow-x: auto;
    padding: 0.6rem 0.8rem;
    border-radius: 6px;
    background: color-mix(in srgb, var(--fg) 6%, transparent);
  }
  code {
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.88em;
  }
</style>
