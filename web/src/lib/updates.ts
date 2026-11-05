// SPDX-License-Identifier: AGPL-3.0-or-later

/** How often Eyrie can check for updates; once a day is the default. */
export const intervals: { hours: number; label: string }[] = [
  { hours: 6, label: 'Every 6 hours' },
  { hours: 12, label: 'Every 12 hours' },
  { hours: 24, label: 'Every day' },
  { hours: 72, label: 'Every 3 days' },
  { hours: 168, label: 'Every week' },
];

// Close hides the reminder until the browser tab is closed.
const closedKey = 'eyrie.update-closed';

/** Whether the reminder for version was closed in this tab. */
export function closed(version: string, store: Pick<Storage, 'getItem'> | null = session()): boolean {
  try {
    return store?.getItem(closedKey) === version;
  } catch {
    return false;
  }
}

/** Remembers, for this tab, that the reminder for version was closed. */
export function close(version: string, store: Pick<Storage, 'setItem'> | null = session()) {
  try {
    store?.setItem(closedKey, version);
  } catch {
    // no storage: it shows again on the next page load
  }
}

function session(): Storage | null {
  try {
    return globalThis.sessionStorage ?? null;
  } catch {
    return null;
  }
}
