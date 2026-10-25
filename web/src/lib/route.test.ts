// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { pageFor, queryValue, routeFor, safeNext } from './route.ts';

describe('pageFor', () => {
  it('maps paths to pages', () => {
    assert.equal(pageFor('/'), 'home');
    assert.equal(pageFor(''), 'home');
    assert.equal(pageFor('/settings'), 'account');
    assert.equal(pageFor('/settings/account/'), 'account');
    assert.equal(pageFor('/settings/users'), 'users');
    assert.equal(pageFor('/settings/sso'), 'sso');
    assert.equal(pageFor('/settings/api-tokens'), 'tokens');
    assert.equal(pageFor('/settings/history'), 'retention');
    assert.equal(pageFor('/settings/backup'), 'backup');
    assert.equal(pageFor('/settings/notifications'), 'notifications');
    assert.equal(pageFor('/settings/alert-analysis'), 'analysis');
    assert.equal(pageFor('/settings/tags'), 'tags');
    assert.equal(pageFor('/settings/audit-log'), 'audit');
    assert.equal(pageFor('/maintenance/'), 'maintenance');
    assert.equal(pageFor('/nothing'), 'not-found');
  });
  it('reads monitor pages and their IDs', () => {
    assert.deepEqual(routeFor('/monitors/new'), { page: 'new-monitor', id: 0 });
    assert.deepEqual(routeFor('/monitors/12'), { page: 'monitor', id: 12 });
    assert.deepEqual(routeFor('/monitors/12/edit'), { page: 'edit-monitor', id: 12 });
    assert.equal(pageFor('/monitors/0'), 'not-found');
    assert.equal(pageFor('/monitors/x'), 'not-found');
  });
  it('reads status pages', () => {
    assert.deepEqual(routeFor('/status-pages'), { page: 'status-pages', id: 0 });
    assert.deepEqual(routeFor('/status-pages/new'), { page: 'new-status-page', id: 0 });
    assert.deepEqual(routeFor('/status-pages/4'), { page: 'status-page', id: 4 });
    assert.deepEqual(routeFor('/status/shop-status'), { page: 'status', id: 0, slug: 'shop-status' });
    assert.equal(pageFor('/status/Shop'), 'not-found');
    assert.equal(pageFor('/status/'), 'not-found');
  });
});

describe('safeNext', () => {
  it('allows only local paths', () => {
    assert.equal(safeNext('/settings/users?x=1'), '/settings/users?x=1');
    for (const bad of [null, '', 'https://evil.example.net', '//evil.example.net', '/\\evil.example.net']) {
      assert.equal(safeNext(bad), '/');
    }
  });
});

describe('queryValue', () => {
  it('reads a query value', () => {
    assert.equal(queryValue('?error=No+account&x=1', 'error'), 'No account');
    assert.equal(queryValue('', 'error'), '');
  });
});
