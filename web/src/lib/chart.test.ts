// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { nearest, thin } from './chart.ts';

describe('chart', () => {
  it('thins points to runs', () => {
    const pts = [1, 2, 3, 4, 5, 6].map((t) => ({ t, v: t === 4 ? null : t * 10, down: t === 5 }));
    assert.equal(thin(pts, 10), pts);
    assert.deepEqual(thin(pts, 3), [
      { t: 2, v: 15, down: false },
      { t: 4, v: 30, down: false },
      { t: 6, v: 55, down: true },
    ]);
    assert.deepEqual(thin([{ t: 1, v: null, down: false }, { t: 2, v: null, down: false }], 1), [{ t: 2, v: null, down: false }]);
  });
  it('finds the nearest point', () => {
    const pts = [10, 20, 30].map((t) => ({ t, v: 1, down: false }));
    assert.equal(nearest(pts, 24), 1);
    assert.equal(nearest(pts, 99), 2);
    assert.equal(nearest([], 5), -1);
  });
});
