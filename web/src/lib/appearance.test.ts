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
    assert.deepEqual(normalize({ layout: 'bento', scheme: 'dusk', mode: 'night' }), {
      ...defaultAppearance,
      layout: 'bento',
      scheme: 'ash',
      mode: 'night',
    });
    assert.deepEqual(normalize({ layout: 'console', scheme: 'glacier', mode: 'day' }), {
      ...defaultAppearance,
      scheme: 'glacier',
      mode: 'day',
    });
  });
  it('keeps the accessibility choices it knows', () => {
    const a = normalize({ accessible: true, vision: 'blue-yellow', text_size: 130 });
    assert.equal(a.accessible, true);
    assert.equal(a.vision, 'blue-yellow');
    assert.equal(a.text_size, 130);
    const b = normalize({ accessible: 'yes' as unknown as boolean, vision: 'sepia' as never, text_size: 120 });
    assert.equal(b.accessible, false);
    assert.equal(b.vision, 'typical');
    assert.equal(b.text_size, 100);
  });
  it('shows the lines in the list unless they were turned off', () => {
    assert.equal(normalize({}).sparklines, true);
    assert.equal(normalize({ sparklines: false }).sparklines, false);
  });
  it('keeps the scheme across layouts only where it exists', () => {
    assert.equal(withLayout({ ...defaultAppearance, mode: 'day' }, 'aurora').scheme, 'ash');
    const dusk = withLayout({ ...defaultAppearance, scheme: 'dusk', mode: 'day', vision: 'monochrome' }, 'bento');
    assert.equal(dusk.scheme, 'ash');
    assert.equal(dusk.vision, 'monochrome');
  });
  it('resolves the system mode', () => {
    assert.equal(isNight('system', true), true);
    assert.equal(isNight('system', false), false);
    assert.equal(isNight('day', true), false);
    assert.equal(isNight('night', false), true);
  });
});
