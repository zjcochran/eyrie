<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { statusWords, textSizes, visions } from '../lib/access.ts';
  import { type Appearance, normalize } from '../lib/appearance.ts';
  import { saveLook } from '../lib/look.svelte.ts';
  import { app } from '../lib/session.svelte.ts';

  // Each choice applies at once, and is kept for this user on every device.
  const look = $derived(normalize(app.session?.user?.appearance));
  const swatchStatuses = ['up', 'down', 'pending', 'maintenance'] as const;
  let error = $state('');

  async function save(next: Appearance) {
    error = await saveLook(next);
  }
</script>

{#if error}<p class="notice error" role="alert">{error}</p>{/if}

<section class="card" aria-labelledby="access-mode">
  <h2 id="access-mode">Accessibility mode</h2>
  <label class="check big"
    ><input type="checkbox" checked={look.accessible} onchange={(e) => save({ ...look, accessible: e.currentTarget.checked })} />
    Turn on the accessibility mode</label
  >
  <ul class="muted small what">
    <li>Screen readers are told when a monitor goes down or comes back up, and which page you went to.</li>
    <li>Each item in the list of monitors has a Move button, so you can arrange it without dragging.</li>
    <li>Charts can be stepped through point by point from the keyboard, and shown as a table.</li>
    <li>More contrast, a wide focus ring, underlined links and bigger buttons.</li>
    <li>Nothing moves or is see-through: no animations, and solid cards in Aurora.</li>
    <li>Statuses are marked with a symbol and a pattern as well as a colour.</li>
  </ul>
</section>

<section class="card">
  <h2>Colour vision</h2>
  <div class="visions" role="radiogroup" aria-label="Colour vision">
    {#each visions as v (v.id)}
      <label class="choice" class:on={look.vision === v.id}>
        <input type="radio" name="vision" value={v.id} checked={look.vision === v.id} onchange={() => save({ ...look, vision: v.id })} />
        <span class="statuses" aria-hidden="true">
          {#each swatchStatuses as st, i (st)}
            <span class="status" style:background={v.colours ? v.colours[i] : `var(--${st})`}
              >{v.id === 'typical' ? '' : statusWords[st].symbol}</span
            >
          {/each}
        </span>
        <span class="name">{v.label}</span>
        <span class="muted small">{v.about}</span>
      </label>
    {/each}
  </div>
  <p class="muted small">
    Any colour vision but Typical also marks statuses with a symbol (✓ up, ✕ down, ! pending, ⏸ maintenance, ? not
    checked yet) and the lines of a chart with dashes.
  </p>
</section>

<section class="card">
  <h2>Text size</h2>
  <div class="row" role="radiogroup" aria-label="Text size">
    {#each textSizes as t (t)}
      <label class="swatch" class:on={look.text_size === t}>
        <input type="radio" name="text-size" value={t} checked={look.text_size === t} onchange={() => save({ ...look, text_size: t })} />
        <span style:font-size="{t / 100}rem">{t}%</span>
      </label>
    {/each}
  </div>
  <p class="muted small">Everything grows with the text. The browser's zoom works too.</p>
</section>

<style>
  .check.big {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .what {
    margin: 0.75rem 0 0;
    padding-left: 1.25rem;
  }
  .visions {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(13rem, 1fr));
    gap: 0.75rem;
  }
  .statuses {
    display: flex;
    gap: 0.3rem;
  }
  .status {
    display: inline-grid;
    place-items: center;
    width: 1.4rem;
    height: 1.4rem;
    border-radius: 50%;
    color: #ffffff;
    font-size: 0.8rem;
    font-weight: 800;
  }
  .choice {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.75rem;
    border: 2px solid var(--border);
    border-radius: var(--radius);
    font-weight: 600;
    cursor: pointer;
  }
  .choice.on,
  .swatch.on {
    border-color: var(--chart, var(--accent));
    background: var(--accent-soft, transparent);
  }
  .choice:has(:focus-visible),
  .swatch:has(:focus-visible) {
    outline: 2px solid var(--chart, var(--accent));
    outline-offset: 2px;
  }
  input[type='radio'] {
    position: absolute;
    opacity: 0;
    pointer-events: none;
  }
  .name {
    font-size: 1.05rem;
  }
  .small {
    font-size: 0.85rem;
    font-weight: normal;
  }
  .swatch {
    position: relative;
    display: flex;
    align-items: center;
    padding: 0.4rem 0.85rem;
    border: 2px solid var(--border);
    border-radius: 999px;
    font-weight: 500;
    cursor: pointer;
  }
</style>
