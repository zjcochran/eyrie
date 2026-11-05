// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { offsetOf, pages, range, sizes } from './paging.ts';

describe('paging', () => {
  it('offers the sizes', () => {
    assert.deepEqual(sizes, [25, 50, 100, 'all']);
  });
  it('counts pages', () => {
    assert.equal(pages(0, 25), 1);
    assert.equal(pages(25, 25), 1);
    assert.equal(pages(26, 25), 2);
    assert.equal(pages(1000, 'all'), 1);
  });
  it('shows the range', () => {
    assert.equal(range(0, 0, 0), '');
    assert.equal(range(25, 25, 120), '26–50 of 120');
    assert.equal(range(100, 20, 120), '101–120 of 120');
  });
  it('finds a page offset', () => {
    assert.equal(offsetOf(1, 25), 0);
    assert.equal(offsetOf(3, 50), 100);
    assert.equal(offsetOf(4, 'all'), 0);
  });
});
