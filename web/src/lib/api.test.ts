// Run with Node.js's test runner (npm test), which strips the types.
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { getInfo } from './api.ts';

describe('getInfo', () => {
  it('returns the server info', async () => {
    let asked = '';
    const info = await getInfo(async (input) => {
      asked = String(input);
      return new Response(JSON.stringify({ name: 'Eyrie', version: 'v0.1.0' }), {
        headers: { 'Content-Type': 'application/json' },
      });
    });
    assert.equal(asked, '/api/v1/info');
    assert.deepEqual(info, { name: 'Eyrie', version: 'v0.1.0' });
  });

  it('throws when the server refuses', async () => {
    await assert.rejects(getInfo(async () => new Response('', { status: 503 })), /answered 503/);
  });
});
