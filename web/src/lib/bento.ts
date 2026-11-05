// SPDX-License-Identifier: AGPL-3.0-or-later
// The Bento layout's start page: pure functions, so the tests can run them.

import type { Count, Monitor, StatusPage } from './api.ts';

/** "Good morning" before noon, "Good afternoon" until 6 pm, then "Good evening". */
export function greeting(hour: number): string {
  return hour < 5 ? 'Good evening' : hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
}

/** What the heading says about the monitors that are down. */
export function needs(down: number): string {
  return down === 0 ? 'Everything is up.' : down === 1 ? 'One thing needs you.' : `${down} things need you.`;
}

export type Band = 'full' | 'high' | 'low' | 'bad' | 'none';

/** How a day looks on the heatmap: every check up, 99% or more, 95% or more, less, or no checks. */
export function band(c: Count | undefined): Band {
  if (!c || c.up + c.down === 0) return 'none';
  const share = c.up / (c.up + c.down);
  return share >= 1 ? 'full' : share >= 0.99 ? 'high' : share >= 0.95 ? 'low' : 'bad';
}

/** The last n UTC days, oldest first, as their midnights in ms. */
export function lastDays(n: number, now = Date.now()): number[] {
  const today = now - (now % 86_400_000);
  return Array.from({ length: n }, (_, i) => today - (n - 1 - i) * 86_400_000);
}

/** A monitor's day on the heatmap, from its counts. */
export function dayOf(days: Count[] | undefined, start: number): Count | undefined {
  return days?.find((d) => Date.parse(d.start) === start);
}

/** The ring's arcs: each part's length and where it starts, on a circle of this circumference. */
export function arcs(parts: number[], circumference: number): { length: number; offset: number }[] {
  const total = parts.reduce((a, b) => a + b, 0);
  let at = 0;
  return parts.map((n) => {
    const length = total ? (n / total) * circumference : 0;
    const arc = { length, offset: -at };
    at += length;
    return arc;
  });
}

/** The bars of checks per hour: each hour's height, 0 to 1 of the busiest. */
export function bars(hours: Count[]): { height: number; down: boolean }[] {
  const max = Math.max(1, ...hours.map((h) => h.up + h.down));
  return hours.map((h) => ({ height: (h.up + h.down) / max, down: h.down > 0 }));
}

/** The monitors a status page shows: those with one of its sections' tags, and those it lists. */
export function pageMonitors(page: StatusPage, monitors: Monitor[]): Monitor[] {
  const tags = new Set(page.sections.map((s) => s.tag_id).filter((t): t is number => t !== null));
  const ids = new Set(page.sections.flatMap((s) => s.monitors));
  return monitors.filter((m) => ids.has(m.id) || m.tags.some((t) => tags.has(t.id)));
}
