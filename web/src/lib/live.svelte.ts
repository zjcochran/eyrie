// SPDX-License-Identifier: AGPL-3.0-or-later
// The monitors and their latest heartbeats, kept up to date by the server's
// event stream (/api/v1/events).
import type { Beat, Monitor } from './api.ts';
import { api } from './session.svelte.ts';

/** How many recent heartbeats each monitor keeps for its bar. */
export const recent = 50;

export const live = $state<{ monitors: Monitor[]; loaded: boolean; error: string }>({
  monitors: [],
  loaded: false,
  error: '',
});

const listeners = new Set<(b: Beat) => void>();
let source: EventSource | null = null;

/** Loads every monitor with its recent heartbeats. */
export async function loadMonitors() {
  try {
    live.monitors = await api.monitors(recent);
    live.error = '';
  } catch (e) {
    live.error = e instanceof Error ? e.message : String(e);
  }
  live.loaded = true;
}

/** Starts listening to the server's events; the browser reconnects by itself. */
export function connect() {
  if (source) return;
  source = new EventSource('/api/v1/events');
  source.addEventListener('beat', (e) => apply(JSON.parse((e as MessageEvent).data) as Beat));
  source.addEventListener('monitors', () => loadMonitors());
  // After a lost connection, catch up on what was missed.
  source.addEventListener('open', () => {
    if (live.loaded) loadMonitors();
  });
}

/** Stops listening, when signing out. */
export function disconnect() {
  source?.close();
  source = null;
  live.monitors = [];
  live.loaded = false;
}

/** Calls f with every heartbeat until the returned function is called. */
export function onBeat(f: (b: Beat) => void): () => void {
  listeners.add(f);
  return () => listeners.delete(f);
}

function apply(b: Beat) {
  const m = live.monitors.find((x) => x.id === b.monitor_id);
  if (m) {
    // A maintenance window started or ended: its title comes with the
    // monitor.
    if (b.important && (b.status === 'maintenance') !== (m.status.status === 'maintenance')) loadMonitors();
    if (b.status !== 'maintenance') m.status.maintenance = '';
    m.beats = [...(m.beats ?? []), b].slice(-recent);
    m.status.status = b.status;
    m.status.message = b.message;
    m.status.last_check = b.at;
    m.status.latency_ms = b.latency_ms;
    if (b.important) m.status.since = b.at;
  }
  for (const f of listeners) f(b);
}
