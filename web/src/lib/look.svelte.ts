// SPDX-License-Identifier: AGPL-3.0-or-later
// Saving the signed-in user's look, shared by the Appearance and
// Accessibility settings.
import { message } from './api.ts';
import type { Appearance } from './appearance.ts';
import { api, app } from './session.svelte.ts';

/**
 * Applies a look at once and saves it for this user on every device; on a
 * failure it puts the old one back and returns the error, else ''.
 */
export async function saveLook(next: Appearance): Promise<string> {
  const user = app.session?.user;
  if (!user) return '';
  const before = user.appearance;
  user.appearance = next;
  try {
    user.appearance = (await api.setAppearance(next)).appearance;
    return '';
  } catch (e) {
    user.appearance = before;
    return message(e);
  }
}
