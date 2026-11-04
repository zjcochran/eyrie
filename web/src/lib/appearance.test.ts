// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { defaultAppearance, isNight, layouts, normalize, schemesOf, withLayout } from './appearance.ts';

describe('appearance', () => {
  it('gives every layout three schemes, Ash first', () => {
    for (const l of layouts) {
      assert.equal(l.schemes.length, 3);
      assert.equal(l.schemes[0].id, 'ash');
    }
    assert.deepEqual(
      schemesOf('aurora').map((s) => s.id),
      ['ash', 'borealis', 'ember'],
    );
  });
  it('falls back to the defaults for what it does not know', () => {
    assert.deepEqual(normalize(undefined), defaultAppearance);
    assert.deepEqual(normalize({ layout: 'bento', scheme: 'dusk', mode: 'night' }), { layout: 'bento', scheme: 'ash', mode: 'night' });
    assert.deepEqual(normalize({ layout: 'console', scheme: 'glacier', mode: 'day' }), { layout: 'console', scheme: 'glacier', mode: 'day' });
  });
  it('keeps the scheme across layouts only where it exists', () => {
    assert.equal(withLayout({ layout: 'console', scheme: 'ash', mode: 'day' }, 'aurora').scheme, 'ash');
    assert.equal(withLayout({ layout: 'console', scheme: 'dusk', mode: 'day' }, 'bento').scheme, 'ash');
  });
  it('resolves the system mode', () => {
    assert.equal(isNight('system', true), true);
    assert.equal(isNight('system', false), false);
    assert.equal(isNight('day', true), false);
    assert.equal(isNight('night', false), true);
  });
});
