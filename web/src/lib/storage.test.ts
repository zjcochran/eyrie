// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { level, limit, series } from './storage.ts';

describe('series', () => {
  it('makes one line per volume, with gaps where it was missing', () => {
    const history = [
      { at: '2026-01-01T00:00:00Z', volumes: { '/volume1': 40 }, temps: { 'Disk 1': 35 } },
      { at: '2026-01-01T00:05:00Z', volumes: { '/volume1': 41, '/volume2': 10 }, temps: {} },
    ];
    assert.deepEqual(series(history, 'volumes'), [
      { name: '/volume1', points: [{ t: Date.parse('2026-01-01T00:00:00Z'), v: 40 }, { t: Date.parse('2026-01-01T00:05:00Z'), v: 41 }] },
      { name: '/volume2', points: [{ t: Date.parse('2026-01-01T00:00:00Z'), v: null }, { t: Date.parse('2026-01-01T00:05:00Z'), v: 10 }] },
    ]);
    assert.deepEqual(series(history, 'temps')[0].points.map((p) => p.v), [35, null]);
    assert.deepEqual(series([], 'temps'), []);
  });
});

describe('level', () => {
  it('is bad above the limit and warns near it', () => {
    assert.equal(level(95, 90, 10), 'bad');
    assert.equal(level(85, 90, 10), 'warn');
    assert.equal(level(50, 90, 10), 'ok');
  });
  it('takes the monitor\'s setting or the default', () => {
    assert.equal(limit({ max_percent: 80 }, 'max_percent', 90), 80);
    assert.equal(limit({}, 'max_percent', 90), 90);
    assert.equal(limit({ max_percent: 0 }, 'max_percent', 90), 90);
  });
});
