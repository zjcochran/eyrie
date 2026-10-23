// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { fieldsFor } from './channels.ts';

describe('fieldsFor', () => {
  it('lists the fields of each kind in form order', () => {
    assert.deepEqual(
      fieldsFor('email').map(([k]) => k),
      ['url', 'token', 'user', 'to', 'from'],
    );
    assert.deepEqual(
      fieldsFor('pushover').map(([k]) => k),
      ['token', 'user'],
    );
    assert.equal(fieldsFor('webhook')[1][1].required, false);
    assert.deepEqual(fieldsFor('carrier-pigeon'), []);
  });
});
