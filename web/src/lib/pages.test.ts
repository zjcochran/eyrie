// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { detailText, figures, isTimeValue, lasted, outageLength, outages, pageOf, pages, statusWord } from './pages.ts';
import { fields } from './settings.ts';

describe('pages', () => {
  it('has a page for every type with settings, and push and manual', () => {
    for (const t of [...Object.keys(fields), 'push', 'manual']) assert.equal(t in pages, true, t);
  });
  it('shows response times only where they mean something', () => {
    for (const t of ['docker', 'docker-swarm', 'kubernetes', 'proxmox', 'system-service', 'pm2', 'drawbridge', 'mqtt', 'ntp', 'manual']) {
      assert.equal(pages[t]!.latency, false, t);
    }
    for (const t of ['http', 'ping', 'dns', 'postgres', 'steam']) assert.equal(pages[t]!.latency, true, t);
  });
  it('gives an unknown type the plain page', () => {
    assert.equal(pageOf('telepathy').found, 'Last check');
  });
  it('names statuses in the type’s words', () => {
    const d = pageOf('drawbridge');
    assert.equal(statusWord(d, 'up'), 'On primary');
    assert.equal(statusWord(d, 'down'), 'Failed over');
    assert.equal(statusWord(d, 'pending'), 'Pending');
    assert.equal(statusWord(pageOf('http'), 'paused'), 'Paused');
  });
});

describe('figures', () => {
  it('picks the type’s figures from the details, in the page’s order', () => {
    const got = figures(pageOf('drawbridge'), [
      { name: 'State', value: 'Failover' },
      { name: 'Public IP', value: '198.51.100.20' },
      { name: 'ISP in use', value: 'SKYLINK' },
    ]);
    assert.deepEqual(got, [
      { name: 'ISP in use', value: 'SKYLINK' },
      { name: 'Public IP', value: '198.51.100.20' },
    ]);
  });
});

describe('detailText', () => {
  it('shows times in the reader’s zone and the rest as is', () => {
    assert.equal(detailText('2027-01-15T08:30:00Z'), new Date('2027-01-15T08:30:00Z').toLocaleString());
    assert.equal(detailText('5 of 24'), '5 of 24');
  });
});

describe('outages', () => {
  it('pairs each change to down with the next change to up', () => {
    const got = outages([
      { at: '2027-01-15T12:00:00Z', status: 'down', message: 'Failover, on SKYLINK' },
      { at: '2027-01-15T10:00:00Z', status: 'up', message: 'On primary' },
      { at: '2027-01-15T09:30:00Z', status: 'pending', message: 'Timed out' },
      { at: '2027-01-15T09:00:00Z', status: 'down', message: 'Failover, on SKYLINK' },
      { at: '2027-01-15T08:00:00Z', status: 'up', message: 'On primary' },
    ]);
    assert.deepEqual(got, [
      { from: '2027-01-15T12:00:00Z', to: null, message: 'Failover, on SKYLINK' },
      { from: '2027-01-15T09:00:00Z', to: '2027-01-15T10:00:00Z', message: 'Failover, on SKYLINK' },
    ]);
    assert.equal(outageLength(got[1]!, 0), 3600000);
    assert.equal(outageLength(got[0]!, Date.parse('2027-01-15T12:30:00Z')), 1800000);
  });
});

describe('lasted', () => {
  it('says how long, in the two largest units', () => {
    assert.equal(lasted(45000), '45 s');
    assert.equal(lasted(5 * 60000), '5 min');
    assert.equal(lasted(2 * 3600000 + 15 * 60000), '2 h 15 min');
    assert.equal(lasted(3 * 3600000), '3 h');
    assert.equal(lasted(2 * 86400000 + 5 * 3600000 + 60000), '2 d 5 h');
    assert.equal(lasted(86400000), '1 d');
    assert.equal(lasted(-5), '0 s');
  });
  it('tells times from other values', () => {
    assert.equal(isTimeValue('2027-01-15T08:30:00Z'), true);
    assert.equal(isTimeValue('SKYLINK'), false);
  });
});
