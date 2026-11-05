// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { Monitor, StatusPage } from './api.ts';
import { arcs, band, bars, dayOf, greeting, lastDays, needs, pageMonitors } from './bento.ts';

describe('bento', () => {
  it('greets by the hour', () => {
    assert.equal(greeting(3), 'Good evening');
    assert.equal(greeting(9), 'Good morning');
    assert.equal(greeting(14), 'Good afternoon');
    assert.equal(greeting(20), 'Good evening');
  });
  it('says what needs attention', () => {
    assert.equal(needs(0), 'Everything is up.');
    assert.equal(needs(1), 'One thing needs you.');
    assert.equal(needs(3), '3 things need you.');
  });
  it('bands a day by its uptime', () => {
    assert.equal(band(undefined), 'none');
    assert.equal(band({ start: '', up: 0, down: 0 }), 'none');
    assert.equal(band({ start: '', up: 100, down: 0 }), 'full');
    assert.equal(band({ start: '', up: 995, down: 5 }), 'high');
    assert.equal(band({ start: '', up: 96, down: 4 }), 'low');
    assert.equal(band({ start: '', up: 9, down: 1 }), 'bad');
  });
  it('lists the last days and finds a day', () => {
    const now = Date.parse('2027-01-15T10:00:00Z');
    const days = lastDays(3, now);
    assert.deepEqual(days, [Date.parse('2027-01-13T00:00:00Z'), Date.parse('2027-01-14T00:00:00Z'), Date.parse('2027-01-15T00:00:00Z')]);
    const c = { start: '2027-01-14T00:00:00Z', up: 1, down: 0 };
    assert.equal(dayOf([c], days[1]), c);
    assert.equal(dayOf([c], days[0]), undefined);
  });
  it('lays out the ring and the bars', () => {
    assert.deepEqual(arcs([3, 1, 0], 100), [
      { length: 75, offset: -0 },
      { length: 25, offset: -75 },
      { length: 0, offset: -100 },
    ]);
    assert.deepEqual(arcs([0], 100), [{ length: 0, offset: -0 }]);
    assert.deepEqual(
      bars([
        { start: '', up: 4, down: 0 },
        { start: '', up: 1, down: 1 },
      ]),
      [
        { height: 1, down: false },
        { height: 0.5, down: true },
      ],
    );
  });
  it('finds the monitors on a status page', () => {
    const m = (id: number, tag?: number) => ({ id, tags: tag ? [{ id: tag, name: '', color: '', value: '' }] : [] }) as unknown as Monitor;
    const page = { sections: [{ title: '', tag_id: 7, monitors: [2] }] } as unknown as StatusPage;
    assert.deepEqual(
      pageMonitors(page, [m(1, 7), m(2), m(3)]).map((x) => x.id),
      [1, 2],
    );
  });
});
