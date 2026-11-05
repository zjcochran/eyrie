// SPDX-License-Identifier: AGPL-3.0-or-later
// The signed-in session, shared by every page, and the API client that
// carries its CSRF token.
import { Client, type Session } from './api.ts';

export const api = new Client();

// welcome is set while the first admin finishes setting up, after their
// account is made.
export const app = $state<{ session: Session | null; error: string; welcome: boolean }>({
  session: null,
  error: '',
  welcome: false,
});

/** Loads the session from the server. */
export async function refresh() {
  try {
    app.session = await api.session();
    app.error = '';
  } catch (e) {
    app.error = e instanceof Error ? e.message : String(e);
  }
}
