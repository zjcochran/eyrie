// SPDX-License-Identifier: AGPL-3.0-or-later
// What screen readers say: App.svelte shows these in two live regions, one
// polite (said when the reader is idle) and one assertive (said at once).

export const spoken = $state({ polite: '', assertive: '' });

let timer: ReturnType<typeof setTimeout> | undefined;

/** Has screen readers say text; urgent interrupts what they are saying. */
export function announce(text: string, urgent = false) {
  if (!text) return;
  // Emptied first, so the same words twice are said twice.
  spoken.polite = '';
  spoken.assertive = '';
  clearTimeout(timer);
  timer = setTimeout(() => {
    if (urgent) spoken.assertive = text;
    else spoken.polite = text;
  }, 50);
}
