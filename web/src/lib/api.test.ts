// SPDX-License-Identifier: AGPL-3.0-or-later
// Run with Node.js's test runner (npm test), which strips the types.
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { ApiError, type ChannelChange, Client, fileName, message, type MonitorChange, type Session } from './api.ts';

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

  it('downloads a backup file with its name, and sends a restore as a form', async () => {
    const file = new Response('{"format":"eyrie-backup"}', {
      headers: { 'Content-Disposition': 'attachment; filename="eyrie-backup-20270115-080000.json"' },
    });
    const { calls, client } = fake(file, json({ error: 'The passphrase must be at least 12 characters.' }, 422), json({ restored: {} }), json({}));
    const got = await client.makeBackup('30', 'correct horse battery');
    assert.equal(got.name, 'eyrie-backup-20270115-080000.json');
    assert.equal(await got.blob.text(), '{"format":"eyrie-backup"}');
    assert.equal(calls[0].body, JSON.stringify({ history: '30', passphrase: 'correct horse battery' }));
    await assert.rejects(client.makeBackup('30', 'short'), /at least 12 characters/);
    await client.restore(new Blob(['x']), 'correct horse battery');
    const form = calls[2].body as FormData;
    assert.equal(form.get('passphrase'), 'correct horse battery');
    assert.equal(calls[2].headers['Content-Type'], undefined, 'the browser sets the multipart boundary');
    await client.setupRestore(new Blob(['x']), 'p');
    assert.equal(calls[3].path, '/api/v1/setup/restore');
  });

  it('reads a file name from Content-Disposition', () => {
    assert.equal(fileName('attachment; filename="eyrie-backup-20270115-080000.json"'), 'eyrie-backup-20270115-080000.json');
    assert.equal(fileName(null), 'eyrie-backup.json');
    assert.equal(fileName('attachment; filename=""'), 'eyrie-backup.json');
    assert.equal(fileName('attachment; filename="open'), 'eyrie-backup.json');
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
    const change: MonitorChange = {
      name: 'DB',
      type: 'port',
      parent_id: null,
      active: true,
      interval: 60,
      retry_interval: 60,
      retries: 0,
      resend_every: 0,
      timeout: 48,
      upside_down: false,
      description: '',
      settings: { host: 'db.example.com', port: 5432 },
    };
    const channel: ChannelChange = {
      name: 'Phone',
      kind: 'ntfy',
      url: 'https://ntfy.example.com/alerts',
      token: '',
      user: '',
      to: '',
      from: '',
      enabled: true,
      default: false,
    };
    const routes: [() => Promise<unknown>, string, string][] = [];
    const { calls, client } = fake(...Array.from({ length: 60 }, () => json({})));
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
      [() => client.audit({}), 'GET', '/api/v1/audit'],
      [() => client.audit({ action: 'signed in', user: 'bob', before: 40 }), 'GET', '/api/v1/audit?action=signed+in&user=bob&before=40'],
      [() => client.createToken('ci'), 'POST', '/api/v1/tokens'],
      [() => client.deleteToken(3), 'DELETE', '/api/v1/tokens/3'],
      [() => client.monitorTypes(), 'GET', '/api/v1/monitor-types'],
      [() => client.monitors(), 'GET', '/api/v1/monitors'],
      [() => client.monitors(50), 'GET', '/api/v1/monitors?beats=50'],
      [() => client.monitor(4), 'GET', '/api/v1/monitors/4'],
      [
        () => client.heartbeats(4, new Date('2027-01-15T08:00:00Z')),
        'GET',
        '/api/v1/monitors/4/heartbeats?since=2027-01-15T08%3A00%3A00.000Z',
      ],
      [() => client.hours(4, 7), 'GET', '/api/v1/monitors/4/hours?days=7'],
      [() => client.createMonitor(change), 'POST', '/api/v1/monitors'],
      [() => client.updateMonitor(4, change), 'PUT', '/api/v1/monitors/4'],
      [() => client.deleteMonitor(4), 'DELETE', '/api/v1/monitors/4'],
      [() => client.pauseMonitor(4), 'POST', '/api/v1/monitors/4/pause'],
      [() => client.resumeMonitor(4), 'POST', '/api/v1/monitors/4/resume'],
      [() => client.checkMonitor(4), 'POST', '/api/v1/monitors/4/check'],
      [() => client.setManualStatus(4, 'down', 'outage'), 'POST', '/api/v1/monitors/4/status'],
      [() => client.retention(), 'GET', '/api/v1/settings/retention'],
      [() => client.channelKinds(), 'GET', '/api/v1/channel-kinds'],
      [() => client.channels(), 'GET', '/api/v1/channels'],
      [() => client.createChannel(channel), 'POST', '/api/v1/channels'],
      [() => client.updateChannel(2, channel), 'PUT', '/api/v1/channels/2'],
      [() => client.deleteChannel(2), 'DELETE', '/api/v1/channels/2'],
      [() => client.testChannel(channel), 'POST', '/api/v1/channels/test'],
      [() => client.notifySettings(), 'GET', '/api/v1/settings/notifications'],
      [() => client.saveNotifySettings([21, 7]), 'PUT', '/api/v1/settings/notifications'],
      [() => client.saveRetention(30), 'PUT', '/api/v1/settings/retention'],
      [() => client.backupSchedule(), 'GET', '/api/v1/settings/backup'],
      [
        () => client.saveBackupSchedule({ enabled: true, time: '03:00', keep: 14, history: 'none', passphrase: '' }),
        'PUT',
        '/api/v1/settings/backup',
      ],
      [() => client.runBackup(), 'POST', '/api/v1/backups/run'],
    );
    for (const [call, method, path] of routes) {
      await call();
      const last = calls[calls.length - 1];
      assert.equal(`${last.method} ${last.path}`, `${method} ${path}`);
    }
  });
});
