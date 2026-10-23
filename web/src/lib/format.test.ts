// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { ago, daysUntil, duration, millis, percent } from './format.ts';
import { toSettings, toValues } from './settings.ts';

describe('format', () => {
  it('shows uptime without rounding up to 100%', () => {
    assert.equal(percent(1), '100%');
    assert.equal(percent(0.99996), '99.99%');
    assert.equal(percent(0.5), '50.00%');
    assert.equal(percent(null), '–');
    assert.equal(percent(undefined), '–');
  });
  it('shows response times', () => {
    assert.equal(millis(12.4), '12 ms');
    assert.equal(millis(12345), '12.3 s');
    assert.equal(millis(null), '–');
  });
  it('shows durations', () => {
    assert.equal(duration(60), '1 min');
    assert.equal(duration(90), '90 s');
    assert.equal(duration(7200), '2 h');
    assert.equal(duration(86400), '1 d');
  });
  it('shows how long ago', () => {
    const now = Date.parse('2027-01-15T12:00:00Z');
    assert.equal(ago(null, now), 'never');
    assert.equal(ago('2027-01-15T11:59:30Z', now), '30 s ago');
    assert.equal(ago('2027-01-15T11:30:00Z', now), '30 min ago');
    assert.equal(ago('2027-01-15T09:00:00Z', now), '3 h ago');
    assert.equal(ago('2027-01-13T12:00:00Z', now), '2 d ago');
    assert.equal(ago('2027-01-15T12:00:05Z', now), '0 s ago');
    assert.equal(daysUntil('2027-01-25T13:00:00Z', now), 10);
  });
});

describe('settings', () => {
  it('turns saved settings into form values and back', () => {
    const saved = {
      url: 'https://example.com',
      method: 'GET',
      headers: { 'X-Token': 'a:b' },
      body: '',
      accepted_status_codes: ['200-299', '301'],
      max_redirects: 10,
      ignore_tls: true,
      keyword: 'ok',
      invert_keyword: false,
    };
    const values = toValues('keyword', saved);
    assert.equal(values.headers, 'X-Token: a:b');
    assert.equal(values.accepted_status_codes, '200-299, 301');
    assert.equal(values.ignore_tls, true);
    assert.equal(values.body, '');
    assert.deepEqual(toSettings('keyword', values), {
      url: 'https://example.com',
      method: 'GET',
      headers: { 'X-Token': 'a:b' },
      accepted_status_codes: ['200-299', '301'],
      max_redirects: 10,
      ignore_tls: true,
      keyword: 'ok',
      invert_keyword: false,
    });
  });
  it('leaves empty fields to the server and knows types without settings', () => {
    assert.deepEqual(toSettings('port', { host: ' db.example.com ', port: '' }), { host: 'db.example.com' });
    assert.deepEqual(toSettings('push', {}), {});
    assert.deepEqual(toValues('group', {}), {});
    assert.deepEqual(toValues('ping', { host: 'a', count: 0 }), { host: 'a', count: '0', packet_size: '' });
    assert.deepEqual(toSettings('http', { headers: 'no colon\nA: 1' }), { headers: { A: '1' }, ignore_tls: false });
  });
});
