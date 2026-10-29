// SPDX-License-Identifier: AGPL-3.0-or-later
// The figures on the overview beside the dashboard's list.

import type { Monitor, Status } from './api.ts';
import { daysUntil } from './format.ts';

/** Certificates expiring within this many days are counted as soon. */
export const certSoonDays = 30;

/** How many monitors each list on the overview shows. */
export const listSize = 5;

export interface Overview {
  /** Active monitors by status, and how many are paused. */
  counts: Record<Status, number> & { paused: number };
  active: number;
  /** The mean of the active monitors' uptime and response times; null without any. */
  uptimeDay: number | null;
  uptimeMonth: number | null;
  latencyDay: number | null;
  /** Certificates expiring within certSoonDays, or already expired. */
  certsSoon: number;
  /** Below 100% over 30 days, lowest first. */
  lowest: Monitor[];
  /** By average response time over 24 hours, slowest first. */
  slowest: Monitor[];
  /** By certificate expiry, soonest first. */
  certs: Monitor[];
}

function mean(values: (number | null)[]): number | null {
  const v = values.filter((x): x is number => x !== null);
  return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null;
}

/** The overview of every monitor. */
export function overview(monitors: Monitor[], now = Date.now()): Overview {
  const active = monitors.filter((m) => m.active);
  const counts = { unknown: 0, up: 0, down: 0, pending: 0, maintenance: 0, paused: monitors.length - active.length };
  for (const m of active) counts[m.status.status]++;
  const certs = active
    .filter((m) => m.status.cert_expires)
    .sort((a, b) => Date.parse(a.status.cert_expires ?? '') - Date.parse(b.status.cert_expires ?? ''));
  return {
    counts,
    active: active.length,
    uptimeDay: mean(active.map((m) => m.uptime['24h'])),
    uptimeMonth: mean(active.map((m) => m.uptime['30d'])),
    latencyDay: mean(active.map((m) => m.uptime.avg_latency_24h_ms)),
    certsSoon: certs.filter((m) => daysUntil(m.status.cert_expires ?? '', now) < certSoonDays).length,
    lowest: active
      .filter((m) => m.uptime['30d'] !== null && m.uptime['30d'] < 1)
      .sort((a, b) => (a.uptime['30d'] ?? 0) - (b.uptime['30d'] ?? 0))
      .slice(0, listSize),
    slowest: active
      .filter((m) => m.uptime.avg_latency_24h_ms !== null)
      .sort((a, b) => (b.uptime.avg_latency_24h_ms ?? 0) - (a.uptime.avg_latency_24h_ms ?? 0))
      .slice(0, listSize),
    certs: certs.slice(0, listSize),
  };
}
