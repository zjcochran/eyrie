// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { StatusChange } from './api.ts';
import { byDay, dayOf, happened } from './events.ts';

// Local times, so the tests hold in any time zone.
const at = (y: number, m: number, d: number, h = 12) => new Date(y, m - 1, d, h).toISOString();
const now = new Date(2026, 10, 6, 15);
const change = (iso: string, id: number): StatusChange => ({
  id,
  monitor_id: 1,
  at: iso,
  status: 'down',
  latency_ms: null,
  message: '',
  important: true,
});

describe('events', () => {
  it('says what happened', () => {
    assert.equal(happened('down'), 'went down');
    assert.equal(happened('up'), 'came up');
    assert.equal(happened('pending'), 'started failing');
    assert.equal(happened('maintenance'), 'went into maintenance');
    assert.equal(happened('unknown'), 'has no status');
  });
  it('names the day', () => {
    assert.equal(dayOf(at(2026, 11, 6, 0), now), 'Today');
    assert.equal(dayOf(at(2026, 11, 6, 23), now), 'Today');
    assert.equal(dayOf(at(2026, 11, 5, 23), now), 'Yesterday');
    const older = dayOf(at(2026, 11, 3), now);
    assert.ok(!['Today', 'Yesterday'].includes(older) && !older.includes('2026'), older);
    assert.ok(dayOf(at(2025, 12, 31), now).includes('2025'));
  });
  it('groups runs of a day in order', () => {
    const days = byDay(
      [change(at(2026, 11, 6, 14), 4), change(at(2026, 11, 6, 9), 3), change(at(2026, 11, 5), 2), change(at(2026, 11, 1), 1)],
      now,
    );
    assert.deepEqual(
      days.map((d) => [d.day, d.changes.map((c) => c.id)]),
      [
        ['Today', [4, 3]],
        ['Yesterday', [2]],
        [days[2].day, [1]],
      ],
    );
    assert.deepEqual(byDay([], now), []);
  });
});
