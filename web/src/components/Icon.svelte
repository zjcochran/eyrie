<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- A line icon for the app's pages. -->
<script lang="ts">
  const paths = {
    monitors: 'M2 12h4l2-5 3 10 2-5h9',
    discover: 'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zM16 8l-2 6-6 2 2-6z',
    events: 'M3 6h18M3 12h18M3 18h12',
    maintenance: 'M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.6 2.6-2.4-.6-.6-2.4z',
    'status-pages': 'M4 4h16v12H4zM8 20h8M12 16v4',
    settings: 'M4 6h16M4 12h16M4 18h16M8 4v4M16 10v4M10 16v4',
  };
  let { name, size = 18 }: { name: keyof typeof paths; size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
  ><path d={paths[name]} /></svg
>
