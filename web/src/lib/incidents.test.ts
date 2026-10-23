// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { badgeURLs, incidentLabels, incidentStatuses } from './incidents.ts';

describe('incidents', () => {
  it('labels every status', () => {
    assert.deepEqual(
      incidentStatuses.map((s) => incidentLabels[s]),
      ['Investigating', 'Identified', 'Monitoring', 'Resolved'],
    );
  });
  it('lists the badges of a monitor', () => {
    const urls = badgeURLs('https://eyrie.example.com', 3);
    assert.equal(urls.length, 6);
    assert.equal(urls[0].url, 'https://eyrie.example.com/api/badge/3/status');
    assert.equal(urls[2].url, 'https://eyrie.example.com/api/badge/3/uptime?period=30d');
  });
});
