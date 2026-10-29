// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { Monitor, Status } from './api.ts';
import { overview } from './overview.ts';

const monitor = (
  id: number,
  status: Status,
  day: number | null,
  month: number | null,
  latency: number | null,
  cert: string | null = null,
  active = true,
) =>
  ({
    id,
    name: `m${id}`,
    active,
    status: { status, cert_expires: cert },
    uptime: { '24h': day, '30d': month, '1y': null, avg_latency_24h_ms: latency },
  }) as Monitor;

describe('overview', () => {
  const now = Date.parse('2027-01-15T12:00:00Z');
  it('sums up the monitors', () => {
    const o = overview(
      [
        monitor(1, 'up', 1, 0.99, 40, '2027-01-20T00:00:00Z'),
        monitor(2, 'down', 0.5, 0.9, 120, '2027-06-01T00:00:00Z'),
        monitor(3, 'pending', null, null, null),
        monitor(4, 'up', 1, 1, 20),
        monitor(5, 'down', 0, 0, 900, '2027-01-01T00:00:00Z', false),
      ],
      now,
    );
    assert.deepEqual(o.counts, { unknown: 0, up: 2, down: 1, pending: 1, maintenance: 0, paused: 1 });
    assert.equal(o.active, 4);
    assert.equal(o.uptimeDay, 2.5 / 3);
    assert.equal(Math.round((o.uptimeMonth ?? 0) * 1e6), Math.round((2.89 / 3) * 1e6));
    assert.equal(o.latencyDay, 60);
    assert.equal(o.certsSoon, 1);
    assert.deepEqual(
      o.lowest.map((m) => m.id),
      [2, 1],
    );
    assert.deepEqual(
      o.slowest.map((m) => m.id),
      [2, 1, 4],
    );
    assert.deepEqual(
      o.certs.map((m) => m.id),
      [1, 2],
    );
  });
  it('has no figures without monitors', () => {
    const o = overview([], now);
    assert.equal(o.active, 0);
    assert.equal(o.uptimeDay, null);
    assert.equal(o.latencyDay, null);
    assert.equal(o.lowest.length, 0);
  });
  it('lists at most five', () => {
    const o = overview(
      Array.from({ length: 8 }, (_, i) => monitor(i, 'up', 0.9, 0.9, i)),
      now,
    );
    assert.equal(o.lowest.length, 5);
    assert.deepEqual(
      o.slowest.map((m) => m.id),
      [7, 6, 5, 4, 3],
    );
  });
});
