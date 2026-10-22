// SPDX-License-Identifier: AGPL-3.0-or-later
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

// The build goes where the Go binary embeds it from (internal/ui, with the
// build tag "ui").
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
  },
});
