// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { agentSetup, platforms, suggestions } from './agents.ts';

describe('agentSetup', () => {
  it('carries the address and token for every platform', () => {
    for (const p of platforms) {
      const s = agentSetup(p.id, 'https://eyrie.example.com', 'eya_abc');
      assert.equal(s.includes('https://eyrie.example.com'), true, p.id);
      assert.equal(s.includes('eya_abc'), true, p.id);
    }
  });
  it('names the platform when it is not Docker', () => {
    assert.equal(agentSetup('docker', 'u', 't').includes('EYRIE_AGENT_PLATFORM'), false);
    assert.equal(agentSetup('swarm', 'u', 't').includes('EYRIE_AGENT_PLATFORM: swarm'), true);
    assert.equal(agentSetup('swarm', 'u', 't').includes('node.role == manager'), true);
    assert.equal(agentSetup('proxmox', 'u', 't').includes('EYRIE_AGENT_PLATFORM: proxmox'), true);
    assert.equal(agentSetup('kubernetes', 'u', 't').includes('kubectl apply'), true);
  });
});

describe('suggestions', () => {
  const ws = [
    { key: 'web', name: 'web', kind: 'container', up: true, status: 'Running' },
    { key: 'db', name: 'db', kind: 'container', up: true, status: 'Running' },
    { key: 'service/api', name: 'api', kind: 'service', up: true, status: '1 of 1 tasks running' },
    { key: 'shop/deployment/app', name: 'app', kind: 'deployment', up: true, status: '1 of 1 ready' },
    { key: 'qemu/110', name: 'mail', kind: 'vm', up: true, status: 'Running' },
    { key: 'lxc/101', name: 'dns', kind: 'lxc', up: true, status: 'Running' },
  ];
  it('suggests the names of the workloads of a kind', () => {
    assert.deepEqual(suggestions('container', ws), ['db', 'web']);
    assert.deepEqual(suggestions('service', ws), ['api']);
    assert.deepEqual(suggestions('kubernetes', ws), ['app']);
  });
  it('suggests Proxmox VE guests by their ID', () => {
    assert.deepEqual(suggestions('proxmox', ws), ['101', '110']);
  });
});
