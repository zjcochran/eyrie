// SPDX-License-Identifier: AGPL-3.0-or-later
// The app's pages, by path. Pure functions, so the tests can run them.

export type Page =
  | 'home'
  | 'monitor'
  | 'new-monitor'
  | 'edit-monitor'
  | 'account'
  | 'users'
  | 'sso'
  | 'tokens'
  | 'retention'
  | 'notifications'
  | 'analysis'
  | 'not-found';

/** A page, with the monitor ID of monitor pages. */
export interface Route {
  page: Page;
  id: number;
}

/** The settings tabs, in order; admin tabs are hidden from viewers. */
export const settingsTabs: { page: Page; path: string; label: string; admin: boolean }[] = [
  { page: 'account', path: '/settings/account', label: 'Your account', admin: false },
  { page: 'users', path: '/settings/users', label: 'Users', admin: true },
  { page: 'sso', path: '/settings/sso', label: 'Single sign-on', admin: true },
  { page: 'tokens', path: '/settings/api-tokens', label: 'API tokens', admin: true },
  { page: 'notifications', path: '/settings/notifications', label: 'Notifications', admin: true },
  { page: 'analysis', path: '/settings/alert-analysis', label: 'Alert analysis', admin: true },
  { page: 'retention', path: '/settings/history', label: 'History', admin: true },
];

/** The page for a path; /settings is the account tab. */
export function routeFor(path: string): Route {
  const p = path.replace(/\/+$/, '') || '/';
  if (p === '/') return { page: 'home', id: 0 };
  if (p === '/settings') return { page: 'account', id: 0 };
  if (p === '/monitors/new') return { page: 'new-monitor', id: 0 };
  const m = /^\/monitors\/([1-9][0-9]*)(\/edit)?$/.exec(p);
  if (m) return { page: m[2] ? 'edit-monitor' : 'monitor', id: Number(m[1]) };
  return { page: settingsTabs.find((t) => t.path === p)?.page ?? 'not-found', id: 0 };
}

/** The page for a path. */
export function pageFor(path: string): Page {
  return routeFor(path).page;
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
