// SPDX-License-Identifier: AGPL-3.0-or-later
// A NAS's storage on its SNMP storage monitor's page: its history as one
// line per volume or disk.

import type { Storage } from './api.ts';

/** One line of a chart: a volume's use or a disk's temperature over time. */
export interface Series {
  name: string;
  points: { t: number; v: number | null }[];
}

/** The history of each volume's use, or each disk's temperature, in the order they first appear. */
export function series(history: Storage['history'], key: 'volumes' | 'temps'): Series[] {
  const names: string[] = [];
  for (const h of history) {
    for (const n of Object.keys(h[key] ?? {})) if (!names.includes(n)) names.push(n);
  }
  return names.map((name) => ({ name, points: history.map((h) => ({ t: Date.parse(h.at), v: h[key]?.[name] ?? null })) }));
}

/** How close a value is to its limit: bad above it, warn within margin of it. */
export function level(value: number, limit: number, margin: number): 'ok' | 'warn' | 'bad' {
  if (value > limit) return 'bad';
  return value > limit - margin ? 'warn' : 'ok';
}

/** A number setting of the monitor, or its default. */
export function limit(settings: Record<string, unknown>, key: string, fallback: number): number {
  const v = settings[key];
  return typeof v === 'number' && v > 0 ? v : fallback;
}
