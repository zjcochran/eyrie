<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Eyrie's logo (assets/logo.svg), in the colour scheme's gradient. -->
<script lang="ts">
  let { size = 32 }: { size?: number } = $props();
  // Each logo on a page needs its own gradient.
  const id = `logo-${Math.random().toString(36).slice(2, 8)}`;
</script>

<svg width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
  <defs>
    <linearGradient {id} x1="0" y1="1" x2="1" y2="0">
      <stop offset="0" style="stop-color: var(--logo-a, #4f5490)" />
      <stop offset="1" style="stop-color: var(--logo-b, #8590c4)" />
    </linearGradient>
  </defs>
  <g transform="translate(0 -5)" fill="none" stroke="url(#{id})" stroke-linecap="round" stroke-linejoin="round">
    <path stroke-width="4" d="M4 32 H12 L16 20 L21 40 L25 32 H30 C38 32 45 28 44 21 C43 15 35 14 32 20 C28 27 31 37 40 37 C46 37 51 34 60 32" />
    <path stroke-width="3.5" d="M4 40 C14 56 46 60 60 42" />
    <path stroke-width="3.5" d="M8 48 C20 58 42 58 58 48" />
    <path stroke-width="3.5" d="M6 42 C22 54 44 54 56 38" />
    <path stroke-width="3" d="M14 55 C26 61 40 61 50 55" />
  </g>
</svg>
