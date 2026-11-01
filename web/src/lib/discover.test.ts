// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { Agent } from './api.ts';
import { agentsFor, draftValues, setDraft, takeDraft } from './discover.ts';

const agent = (id: number, platform: string, status: Agent['status'] = 'online') => ({ id, platform, status }) as Agent;

describe('agentsFor', () => {
  const agents = [
    agent(1, 'docker'),
    agent(2, 'swarm'),
    agent(3, 'kubernetes'),
    agent(4, 'proxmox', 'offline'),
    agent(5, 'docker', 'pending'),
  ];
  it('keeps the agents that list the type', () => {
    assert.deepEqual(agentsFor('docker', agents).map((a) => a.id), [1, 2]);
    assert.deepEqual(agentsFor('docker-swarm', agents).map((a) => a.id), [2]);
    assert.deepEqual(agentsFor('kubernetes', agents).map((a) => a.id), [3]);
    assert.deepEqual(agentsFor('proxmox', agents).map((a) => a.id), [4]);
  });
  it('takes any enrolled agent for SNMP, and none for other types', () => {
    assert.deepEqual(agentsFor('snmp', agents).map((a) => a.id), [1, 2, 3, 4]);
    assert.deepEqual(agentsFor('http', agents), []);
  });
});

describe('drafts', () => {
  it('are taken once', () => {
    setDraft({ type: 'docker', name: 'shop', agentID: 0, settings: { container: 'shop' } });
    assert.equal(takeDraft()?.name, 'shop');
    assert.equal(takeDraft(), null);
  });
  it('keep their secrets in the form values', () => {
    const settings = { host: 'switch.example.com', community: 'private', oid: '1.3.6.1.2.1.1.3.0' };
    const v = draftValues({ type: 'snmp', name: 'switch', agentID: 2, settings });
    assert.equal(v.host, 'switch.example.com');
    assert.equal(v.community, 'private');
    assert.equal(v.oid, '1.3.6.1.2.1.1.3.0');
    assert.equal(v.auth_password, '');
  });
});
