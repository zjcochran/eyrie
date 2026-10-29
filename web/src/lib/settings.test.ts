// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { fields, shown, toSettings, toValues } from './settings.ts';

describe('toValues', () => {
  it('never shows secrets, and starts selects at their first option', () => {
    const v = toValues('snmp', { host: 'switch.example.com', oid: '1.3.6.1.2.1.1.3.0', community: '' });
    assert.equal(v.version, '2c');
    assert.equal(v.operator, '==');
    assert.equal(v.community, '');
    assert.equal(v.host, 'switch.example.com');
  });
  it('writes maps one per line', () => {
    const v = toValues('grpc-keyword', { metadata: { authorization: 'Bearer t', 'x-env': 'test' } });
    assert.equal(v.metadata, 'authorization: Bearer t\nx-env: test');
  });
});

describe('toSettings', () => {
  it('leaves out hidden fields and empty secrets', () => {
    const v = toValues('snmp', {});
    v.host = 'switch.example.com';
    v.oid = '1.3.6.1.2.1.1.5.0';
    v.username = 'alice';
    v.community = '';
    assert.deepEqual(toSettings('snmp', v), {
      host: 'switch.example.com',
      version: '2c',
      oid: '1.3.6.1.2.1.1.5.0',
      operator: '==',
    });
    v.version = '3';
    v.auth_password = ' pass word ';
    const s = toSettings('snmp', v);
    assert.equal(s.username, 'alice');
    assert.equal(s.auth_password, ' pass word ', 'secrets are sent as typed');
    assert.equal(s.community, undefined);
  });
  it('reads maps and status codes', () => {
    const v = toValues('globalping', {});
    v.kind = 'http';
    v.target = 'https://example.com';
    v.accepted_status_codes = '200-299, 301';
    assert.deepEqual(toSettings('globalping', v), {
      kind: 'http',
      target: 'https://example.com',
      method: 'HEAD',
      accepted_status_codes: ['200-299', '301'],
    });
  });
});

describe('lists', () => {
  it('splits brokers and nodes on commas', () => {
    const v = toValues('kafka-producer', { brokers: ['k1.example.com:9092', 'k2.example.com:9092'] });
    assert.equal(v.brokers, 'k1.example.com:9092, k2.example.com:9092');
    v.topic = 'checks';
    const s = toSettings('kafka-producer', v);
    assert.deepEqual(s.brokers, ['k1.example.com:9092', 'k2.example.com:9092']);
    assert.equal(s.username, undefined, 'hidden without SASL');
  });
});

describe('workload settings', () => {
  it('sends a Proxmox VE guest by its number', () => {
    const v = toValues('proxmox', { vmid: 101 });
    assert.deepEqual(toSettings('proxmox', v), { vmid: 101 });
    assert.deepEqual(toSettings('kubernetes', toValues('kubernetes', { name: 'web' })), { kind: 'deployment', name: 'web' });
  });
});

describe('fields', () => {
  it('has fields for every type that has settings', () => {
    for (const t of ['websocket-upgrade', 'smtp', 'ntp', 'radius', 'snmp', 'docker', 'sip-options', 'tailscale-ping',
      'system-service', 'system-resources', 'pm2', 'docker-swarm', 'kubernetes', 'proxmox', 'globalping', 'sftp', 'steam', 'gamedig', 'grpc-keyword', 'real-browser', 'postgres', 'mysql',
      'sqlserver', 'oracledb', 'mongodb', 'redis', 'mqtt', 'rabbitmq', 'kafka-producer']) {
      assert.equal((fields[t] ?? []).length > 0, true, t);
    }
  });
  it('shows a field only while its condition holds', () => {
    const packets = fields.globalping.find((f) => f.key === 'packets')!;
    assert.equal(shown(packets, { kind: 'ping' }), true);
    assert.equal(shown(packets, { kind: 'http' }), false);
    assert.equal(shown(fields.globalping[0], {}), true);
  });
});
