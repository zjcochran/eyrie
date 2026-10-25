// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { agentSetup, platforms } from './agents.ts';

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
