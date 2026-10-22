// SPDX-License-Identifier: AGPL-3.0-or-later
// The app's pages, by path. Pure functions, so the tests can run them.

export type Page = 'home' | 'account' | 'users' | 'sso' | 'tokens' | 'not-found';

/** The settings tabs, in order; admin tabs are hidden from viewers. */
export const settingsTabs: { page: Page; path: string; label: string; admin: boolean }[] = [
  { page: 'account', path: '/settings/account', label: 'Your account', admin: false },
  { page: 'users', path: '/settings/users', label: 'Users', admin: true },
  { page: 'sso', path: '/settings/sso', label: 'Single sign-on', admin: true },
  { page: 'tokens', path: '/settings/api-tokens', label: 'API tokens', admin: true },
];

/** The page for a path; /settings is the account tab. */
export function pageFor(path: string): Page {
  const p = path.replace(/\/+$/, '') || '/';
  if (p === '/') return 'home';
  if (p === '/settings') return 'account';
  return settingsTabs.find((t) => t.path === p)?.page ?? 'not-found';
}

/** Only local paths, so a link cannot send people off-site. */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith('/') || next.startsWith('//') || next.includes('\\')) {
    return '/';
  }
  return next;
}

/** A value from a query string such as location.search. */
export function queryValue(search: string, key: string): string {
  return new URLSearchParams(search).get(key) ?? '';
}
