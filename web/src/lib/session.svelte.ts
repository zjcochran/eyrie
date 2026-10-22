// The signed-in session, shared by every page, and the API client that
// carries its CSRF token.
import { Client, type Session } from './api.ts';

export const api = new Client();

export const app = $state<{ session: Session | null; error: string }>({ session: null, error: '' });

/** Loads the session from the server. */
export async function refresh() {
  try {
    app.session = await api.session();
    app.error = '';
  } catch (e) {
    app.error = e instanceof Error ? e.message : String(e);
  }
}
