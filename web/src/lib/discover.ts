// SPDX-License-Identifier: AGPL-3.0-or-later
// Discovery: what each source finds, the agents it can use, and the
// monitor it hands to the monitor form.

import type { Agent } from './api.ts';
import { type Values, fields, toValues } from './settings.ts';

/** What Runs on calls Eyrie itself. */
export const localName = 'Eyrie server (local)';

/** The monitor types discovery finds; platforms: the agents that list them (null: any), local: Eyrie can too. */
export const sources: { type: string; label: string; platforms: string[] | null; local: boolean }[] = [
  { type: 'docker', label: 'Docker containers', platforms: ['docker', 'swarm'], local: true },
  { type: 'docker-swarm', label: 'Docker Swarm services', platforms: ['swarm'], local: false },
  { type: 'kubernetes', label: 'Kubernetes workloads', platforms: ['kubernetes'], local: false },
  { type: 'proxmox', label: 'Proxmox VE guests', platforms: ['proxmox'], local: false },
  { type: 'snmp', label: 'Network devices (SNMP)', platforms: null, local: true },
];

/** The agents that can discover a type. */
export function agentsFor(type: string, agents: Agent[]): Agent[] {
  const s = sources.find((x) => x.type === type);
  if (!s) return [];
  return agents.filter((a) => a.status !== 'pending' && (s.platforms === null || s.platforms.includes(a.platform)));
}

/** A new monitor for the monitor form to start with. */
export interface Draft {
  type: string;
  name: string;
  /** 0: Eyrie. */
  agentID: number;
  settings: Record<string, unknown>;
}

/** The draft the monitor form takes next; kept in memory only, as it can hold an SNMP community or passwords. */
const pending: { draft: Draft | null } = { draft: null };

/** Hands a draft to the next new monitor form. */
export function setDraft(d: Draft) {
  pending.draft = d;
}

/** Takes the draft, once. */
export function takeDraft(): Draft | null {
  const d = pending.draft;
  pending.draft = null;
  return d;
}

/** A draft's form values, with its secrets: they were typed for discovery and are not saved yet. */
export function draftValues(d: Draft): Values {
  const v = toValues(d.type, d.settings);
  for (const f of fields[d.type] ?? []) {
    const s = d.settings[f.key];
    if (f.kind === 'secret' && typeof s === 'string') v[f.key] = s;
  }
  return v;
}
