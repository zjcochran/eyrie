// SPDX-License-Identifier: AGPL-3.0-or-later
// Accessibility remembered in this browser, so that setup and the sign-in
// page are shown the same way before anyone is signed in.
import type { Appearance } from './appearance.ts';

export type Access = Pick<Appearance, 'accessible' | 'vision' | 'text_size'>;

const storageKey = 'eyrie.access';

function read(): Partial<Access> {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(storageKey) ?? '{}');
    return typeof v === 'object' && v !== null ? (v as Partial<Access>) : {};
  } catch {
    return {};
  }
}

export const remembered = $state<{ access: Partial<Access> }>({ access: read() });

/** Remembers a user's accessibility in this browser. */
export function remember(a: Access) {
  remembered.access = { accessible: a.accessible, vision: a.vision, text_size: a.text_size };
  try {
    localStorage.setItem(storageKey, JSON.stringify(remembered.access));
  } catch {
    // Not remembered: the sign-in page is shown without it.
  }
}
