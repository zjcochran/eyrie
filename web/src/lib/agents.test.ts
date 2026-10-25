// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { agentSetup, platforms } from './agents.ts';

describe('agentSetup', () => {
  it('carries the address and token for every platform', () => {
    for (const p of platforms) {
      const s = agentSetup(p.id, 'https://eyrie.example.com', 'eya_abc');
      assert.match(s, /https:\/\/eyrie\.example\.com/, p.id);
      assert.match(s, /eya_abc/, p.id);
    }
  });
  it('names the platform when it is not Docker', () => {
    assert.doesNotMatch(agentSetup('docker', 'u', 't'), /EYRIE_AGENT_PLATFORM/);
    assert.match(agentSetup('swarm', 'u', 't'), /EYRIE_AGENT_PLATFORM: swarm/);
    assert.match(agentSetup('swarm', 'u', 't'), /node\.role == manager/);
    assert.match(agentSetup('proxmox', 'u', 't'), /EYRIE_AGENT_PLATFORM: proxmox/);
    assert.match(agentSetup('kubernetes', 'u', 't'), /kubectl apply/);
  });
});
