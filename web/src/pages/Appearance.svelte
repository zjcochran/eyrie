<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { message } from '../lib/api.ts';
  import { type Appearance, type Layout, type Mode, layouts, modes, normalize, schemesOf, withLayout } from '../lib/appearance.ts';
  import { api, app } from '../lib/session.svelte.ts';

  // Each choice applies at once, and is kept for this user on every device.
  const look = $derived(normalize(app.session?.user?.appearance));
  let error = $state('');

  async function save(next: Appearance) {
    const user = app.session?.user;
    if (!user) return;
    const before = user.appearance;
    user.appearance = next;
    error = '';
    try {
      user.appearance = (await api.setAppearance(next)).appearance;
    } catch (e) {
      user.appearance = before;
      error = message(e);
    }
  }
</script>

{#if error}<p class="notice error" role="alert">{error}</p>{/if}

<section class="card">
  <h2>Layout</h2>
  <div class="layouts" role="radiogroup" aria-label="Layout">
    {#each layouts as l (l.id)}
      <label class="choice" class:on={look.layout === l.id}>
        <input type="radio" name="layout" value={l.id} checked={look.layout === l.id} onchange={() => save(withLayout(look, l.id as Layout))} />
        <svg class="preview" viewBox="0 0 120 72" aria-hidden="true">
          <rect width="120" height="72" rx="8" class="ground" />
          {#if l.id === 'console'}
            <rect x="4" y="4" width="24" height="64" rx="4" class="panel" />
            {#each [12, 20, 28, 36] as y (y)}<rect x="8" {y} width="16" height="3" rx="1.5" class="ink" />{/each}
            <rect x="32" y="4" width="32" height="64" rx="4" class="panel" />
            {#each [10, 22, 34, 46] as y (y)}<circle cx="37" cy={y + 2} r="2" class="up" /><rect x="42" {y} width="18" height="4" rx="2" class="ink" />{/each}
            <rect x="68" y="4" width="48" height="64" rx="4" class="panel" />
            <polyline points="72,52 80,44 88,48 96,34 104,40 112,28" class="line" />
          {:else if l.id === 'bento'}
            <rect x="30" y="5" width="60" height="8" rx="4" class="panel" />
            <rect x="6" y="18" width="48" height="48" rx="7" class="hero" />
            <rect x="58" y="18" width="56" height="22" rx="7" class="panel" />
            <rect x="58" y="44" width="26" height="22" rx="7" class="panel" />
            <rect x="88" y="44" width="26" height="22" rx="7" class="accent" />
            <circle cx="30" cy="42" r="12" class="ring" />
          {:else}
            <circle cx="30" cy="16" r="26" class="glow1" />
            <circle cx="100" cy="56" r="30" class="glow2" />
            <rect x="5" y="6" width="14" height="60" rx="6" class="glass" />
            {#each [16, 26, 36, 46] as y (y)}<rect x="9" {y} width="6" height="6" rx="2" class="ink" />{/each}
            <rect x="24" y="6" width="90" height="26" rx="8" class="glass" />
            <rect x="24" y="36" width="43" height="30" rx="8" class="glass" />
            <rect x="71" y="36" width="43" height="30" rx="8" class="glass" />
            <polyline points="30,26 44,18 58,22 72,12 86,16 106,10" class="line" />
          {/if}
        </svg>
        <span class="name">{l.label}</span>
        <span class="muted small">{l.about}</span>
      </label>
    {/each}
  </div>
</section>

<section class="card">
  <h2>Colours</h2>
  <div class="row" role="radiogroup" aria-label="Colour scheme">
    {#each schemesOf(look.layout) as s (s.id)}
      <label class="swatch" class:on={look.scheme === s.id}>
        <input type="radio" name="scheme" value={s.id} checked={look.scheme === s.id} onchange={() => save({ ...look, scheme: s.id })} />
        <span class="dot" style:background="conic-gradient({s.colours[0]} 0 33%, {s.colours[1]} 0 66%, {s.colours[2]} 0)"></span>
        {s.label}
      </label>
    {/each}
  </div>
  <h2 class="gap">Day and night</h2>
  <div class="row" role="radiogroup" aria-label="Day and night">
    {#each modes as m (m.id)}
      <label class="swatch plain" class:on={look.mode === m.id}>
        <input type="radio" name="mode" value={m.id} checked={look.mode === m.id} onchange={() => save({ ...look, mode: m.id as Mode })} />
        {m.label}
      </label>
    {/each}
  </div>
  <p class="muted small">System follows this device's light or dark setting.</p>
</section>

<style>
  .layouts {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(14rem, 1fr));
    gap: 0.75rem;
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
  .preview {
    width: 100%;
    height: auto;
    margin-bottom: 0.25rem;
  }
  .ground {
    fill: var(--bg);
    stroke: var(--border);
  }
  .panel {
    fill: var(--surface);
    stroke: var(--border);
  }
  .glass {
    fill: color-mix(in srgb, var(--surface) 50%, transparent);
    stroke: color-mix(in srgb, var(--fg) 15%, transparent);
  }
  .glow1 {
    fill: var(--glow-1, var(--accent));
  }
  .glow2 {
    fill: var(--glow-2, var(--accent));
  }
  .ink {
    fill: var(--muted);
    opacity: 0.5;
  }
  .up {
    fill: var(--up);
  }
  .hero {
    fill: var(--hero-a, var(--accent));
  }
  .accent {
    fill: var(--accent);
  }
  .ring {
    fill: none;
    stroke: var(--hero-fg, #ffffff);
    stroke-width: 4;
    opacity: 0.8;
  }
  .line {
    fill: none;
    stroke: var(--chart, var(--accent));
    stroke-width: 2;
    stroke-linejoin: round;
  }
  .swatch {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.85rem 0.4rem 0.45rem;
    border: 2px solid var(--border);
    border-radius: 999px;
    font-weight: 500;
    cursor: pointer;
  }
  /* Without a colour dot, the same space on both sides. */
  .swatch.plain {
    padding-left: 0.85rem;
  }
  .dot {
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
  }
  .gap {
    margin-top: 1.25rem;
  }
</style>
