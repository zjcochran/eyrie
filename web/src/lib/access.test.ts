// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { announcement, beatSummary, chartSummary, statusWords, textSizes, visions, when } from './access.ts';
import type { Beat, Status } from './api.ts';
import { millis } from './format.ts';

const beat = (status: Status, important = false, message = ''): Beat => ({
  monitor_id: 1,
  at: '2026-01-02T03:04:05Z',
  status,
  latency_ms: 12,
  message,
  important,
});

describe('choices', () => {
  it('lists the colour visions and text sizes the server takes', () => {
    assert.deepEqual(
      visions.map((v) => v.id),
      ['typical', 'red-green', 'blue-yellow', 'monochrome'],
    );
    assert.deepEqual([...textSizes], [100, 115, 130, 150]);
  });
  it('gives every status its own symbol', () => {
    const symbols = Object.values(statusWords).map((s) => s.symbol);
    assert.equal(new Set(symbols).size, symbols.length);
  });
});

describe('beatSummary', () => {
  it('counts the checks by status and says the latest', () => {
    assert.equal(beatSummary([]), 'No checks yet.');
    assert.equal(
      beatSummary([beat('up'), beat('down'), beat('up'), beat('pending')]),
      'Last 4 checks: 2 up, 1 down, 1 pending. The latest was pending.',
    );
    assert.equal(beatSummary([beat('maintenance')]), 'Last 1 check: 1 in maintenance. The latest was in maintenance.');
  });
});

describe('chartSummary', () => {
  const day = Date.UTC(2026, 0, 2);
  it('gives the lowest, average, highest and latest values, and the downs', () => {
    const points = [
      { t: day, v: 10, down: false },
      { t: day + 1, v: null, down: true },
      { t: day + 2, v: 30, down: false },
      { t: day + 3, v: null, down: false },
    ];
    const s = chartSummary('Response time', points, day, day + 3600000, millis);
    assert.match(s, /^Response time, .+ to .+: lowest 10 ms, average 20 ms, highest 30 ms, latest 30 ms; down at 1 point\.$/);
  });
  it('says when there is nothing to show', () => {
    assert.match(chartSummary('Use', [], day, day + 1, millis), /^Use, .+: no values\.$/);
  });
  it('shows dates over two days or more', () => {
    assert.equal(when(day, day + 3 * 86400000, day), new Date(day).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }));
    assert.notEqual(when(day, day + 3600000, day), when(day, day + 3 * 86400000, day));
  });
});

describe('announcement', () => {
  it('says only changes of status', () => {
    assert.equal(announcement('Web', 'up', beat('down', true, 'connection  refused\n')), 'Web is down: connection refused.');
    assert.equal(announcement('Web', 'down', beat('up', true)), 'Web is up again.');
    assert.equal(announcement('Web', 'maintenance', beat('up', true)), 'Web is out of maintenance and up.');
    assert.equal(announcement('Web', 'up', beat('pending', true, 'slow')), 'Web is pending: slow.');
    assert.equal(announcement('Web', 'up', beat('maintenance', true)), 'Web is in maintenance.');
    assert.equal(announcement('Web', 'up', beat('up', true)), '');
    assert.equal(announcement('Web', 'up', beat('down', false)), '');
    assert.equal(announcement('Web', 'down', beat('unknown', true)), '');
  });
  it('is quiet for a first check that is up', () => {
    assert.equal(announcement('Web', 'unknown', beat('up', true)), '');
    assert.equal(announcement('Web', 'unknown', beat('down', true)), 'Web is down.');
  });
});
