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
  | 'agents'
  | 'retention'
  | 'backup'
  | 'notifications'
  | 'analysis'
  | 'tags'
  | 'audit'
  | 'maintenance'
  | 'status-pages'
  | 'new-status-page'
  | 'status-page'
  | 'status'
  | 'not-found';

/** A page, with the ID of monitor and status page pages, and the address of a public status page. */
export interface Route {
  page: Page;
  id: number;
  slug?: string;
}

/** The settings tabs, in order; admin tabs are hidden from viewers. */
export const settingsTabs: { page: Page; path: string; label: string; admin: boolean }[] = [
  { page: 'account', path: '/settings/account', label: 'Your account', admin: false },
  { page: 'users', path: '/settings/users', label: 'Users', admin: true },
  { page: 'sso', path: '/settings/sso', label: 'Single sign-on', admin: true },
  { page: 'tokens', path: '/settings/api-tokens', label: 'API tokens', admin: true },
  { page: 'agents', path: '/settings/agents', label: 'Agents', admin: true },
  { page: 'tags', path: '/settings/tags', label: 'Tags', admin: true },
  { page: 'notifications', path: '/settings/notifications', label: 'Notifications', admin: true },
  { page: 'analysis', path: '/settings/alert-analysis', label: 'Alert analysis', admin: true },
  { page: 'retention', path: '/settings/history', label: 'History', admin: true },
  { page: 'backup', path: '/settings/backup', label: 'Backup and restore', admin: true },
  { page: 'audit', path: '/settings/audit-log', label: 'Audit log', admin: true },
];

/** The page for a path; /settings is the account tab. */
export function routeFor(path: string): Route {
  const p = path.replace(/\/+$/, '') || '/';
  if (p === '/') return { page: 'home', id: 0 };
  if (p === '/settings') return { page: 'account', id: 0 };
  if (p === '/monitors/new') return { page: 'new-monitor', id: 0 };
  if (p === '/maintenance') return { page: 'maintenance', id: 0 };
  if (p === '/status-pages') return { page: 'status-pages', id: 0 };
  if (p === '/status-pages/new') return { page: 'new-status-page', id: 0 };
  const sp = /^\/status-pages\/([1-9][0-9]*)$/.exec(p);
  if (sp) return { page: 'status-page', id: Number(sp[1]) };
  const pub = /^\/status\/([a-z0-9]+(?:-[a-z0-9]+)*)$/.exec(p);
  if (pub) return { page: 'status', id: 0, slug: pub[1] };
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
