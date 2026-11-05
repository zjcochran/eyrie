// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { close, closed, intervals } from './updates.ts';

describe('updates', () => {
  it('offers intervals with a day among them', () => {
    assert.equal(intervals.some((i) => i.hours === 24), true);
  });
  it('remembers a closed reminder per version', () => {
    const m = new Map<string, string>();
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
    assert.equal(closed('v1.3.0', store), false);
    close('v1.3.0', store);
    assert.equal(closed('v1.3.0', store), true);
    assert.equal(closed('v1.4.0', store), false);
    const broken = {
      getItem: (): string | null => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('blocked');
      },
    };
    assert.equal(closed('v1.3.0', broken), false);
    close('v1.3.0', broken);
    assert.equal(closed('v1.3.0', null), false);
  });
});
