// SPDX-License-Identifier: AGPL-3.0-or-later
// Run with Node.js's test runner (npm test), which strips the types.
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { ApiError, Client, message, type Session } from './api.ts';

interface Call {
  path: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

/** A fake fetch that records calls and answers from a list. */
function fake(...answers: Response[]) {
  const calls: Call[] = [];
  const fn = async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      path: String(input),
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: init?.body,
    });
    const next = answers.shift();
    if (!next) throw new Error('no answer left');
    return next;
  };
  return { calls, client: new Client(fn as typeof fetch) };
}

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

const session: Session = {
  setup: false,
  user: null,
  csrf: 'tok',
  sso: { enabled: false, name: 'SSO' },
  password_sign_in: true,
  version: 'v0.2.0',
  source: 'https://git.example.com/eyrie',
};

describe('Client', () => {
  it('reads the server info', async () => {
    const info = { name: 'Eyrie', version: 'v0.1.0', license: 'AGPL-3.0-or-later', source: '' };
    const { calls, client } = fake(json(info));
    assert.deepEqual(await client.info(), info);
    assert.equal(calls[0].path, '/api/v1/info');
    assert.equal(calls[0].method, 'GET');
  });

  it('keeps the CSRF token and sends it with changes only', async () => {
    const { calls, client } = fake(json(session), json([]), json({ id: 1 }, 201));
    await client.session();
    assert.equal(client.csrf, 'tok');
    await client.users();
    assert.equal(calls[1].headers['X-CSRF-Token'], undefined);
    await client.createUser('bob', 'correct horse battery', 'viewer');
    assert.equal(calls[2].headers['X-CSRF-Token'], 'tok');
    assert.equal(calls[2].headers['Content-Type'], 'application/json');
    assert.deepEqual(JSON.parse(String(calls[2].body)), { username: 'bob', password: 'correct horse battery', role: 'viewer' });
  });

  it('signs in with a password and a two-factor code', async () => {
    const { calls, client } = fake(json({ two_factor: true }), json(session), json(session));
    assert.equal(await client.login('alice', 'pw'), null);
    assert.deepEqual(await client.loginCode('123456'), session);
    assert.equal(calls[1].path, '/api/v1/login/code');
    assert.deepEqual(await client.login('alice', 'pw'), session);
  });

  it('forgets the CSRF token when signing out', async () => {
    const { calls, client } = fake(json(session), new Response(null, { status: 204 }));
    await client.setup('alice', 'correct horse battery');
    await client.logout();
    assert.equal(client.csrf, '');
    assert.equal(calls[1].method, 'POST');
  });

  it('sends a picture as is', async () => {
    const { calls, client } = fake(json({ id: 1 }));
    const blob = new Blob(['png']);
    await client.uploadPicture(blob);
    assert.equal(calls[0].body, blob);
    assert.equal(calls[0].headers['Content-Type'], undefined);
  });

  it('turns error answers into ApiError with the message', async () => {
    const { client } = fake(json({ error: 'Wrong username or password.' }, 401), new Response('oops', { status: 502 }));
    await assert.rejects(client.login('alice', 'nope'), /Wrong username or password/);
    await assert.rejects(client.tokens(), /GET \/api\/v1\/tokens answered 502/);
    const e = new ApiError(403, 'no');
    assert.equal(e.status, 403);
    assert.equal(message(e), 'no');
    assert.equal(message('plain'), 'plain');
  });

  it('has a call for every route', async () => {
    const routes: [() => Promise<unknown>, string, string][] = [];
    const { calls, client } = fake(...Array.from({ length: 22 }, () => json({})));
    routes.push(
      [() => client.account(), 'GET', '/api/v1/account'],
      [() => client.changePassword('a', 'b'), 'PUT', '/api/v1/account/password'],
      [() => client.twoFactorSetup(), 'POST', '/api/v1/account/two-factor/setup'],
      [() => client.twoFactorEnable('s', 'c', 'p'), 'POST', '/api/v1/account/two-factor/enable'],
      [() => client.newRecoveryCodes('p'), 'POST', '/api/v1/account/two-factor/recovery-codes'],
      [() => client.twoFactorDisable('p'), 'POST', '/api/v1/account/two-factor/disable'],
      [() => client.removePicture(), 'DELETE', '/api/v1/account/picture'],
      [() => client.useSSOPicture(), 'POST', '/api/v1/account/picture/sso'],
      [() => client.unlinkSSO(), 'DELETE', '/api/v1/account/sso'],
      [() => client.updateUser(2, { role: 'admin' }), 'PATCH', '/api/v1/users/2'],
      [() => client.resetPassword(2, 'p'), 'PUT', '/api/v1/users/2/password'],
      [() => client.resetTwoFactor(2), 'DELETE', '/api/v1/users/2/two-factor'],
      [() => client.deleteUser(2), 'DELETE', '/api/v1/users/2'],
      [() => client.ssoSettings(), 'GET', '/api/v1/settings/sso'],
      [
        () =>
          client.saveSSOSettings({
            enabled: true,
            issuer: 'https://id.example.com',
            client_id: 'eyrie',
            client_secret: '',
            clear_secret: false,
            display_name: '',
            auto_provision: true,
            allowed_group: '',
            admin_group: '',
            sso_only: false,
          }),
        'PUT',
        '/api/v1/settings/sso',
      ],
      [() => client.testSSO('https://id.example.com'), 'POST', '/api/v1/settings/sso/test'],
      [() => client.createToken('ci'), 'POST', '/api/v1/tokens'],
      [() => client.deleteToken(3), 'DELETE', '/api/v1/tokens/3'],
    );
    for (const [call, method, path] of routes) {
      await call();
      const last = calls[calls.length - 1];
      assert.equal(`${last.method} ${last.path}`, `${method} ${path}`);
    }
  });
});
