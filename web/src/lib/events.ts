// SPDX-License-Identifier: AGPL-3.0-or-later
// The events list: what happened, in words, and the days it happened on.
import type { StatusChange, Status } from './api.ts';

/** What a change to a status means, after the monitor's name. */
export function happened(status: Status): string {
  switch (status) {
    case 'up':
      return 'came up';
    case 'down':
      return 'went down';
    case 'pending':
      return 'started failing';
    case 'maintenance':
      return 'went into maintenance';
    default:
      return 'has no status';
  }
}

/** The local day a time falls on: Today, Yesterday, or its date (with the
 * year when it is not this one). */
export function dayOf(iso: string, now = new Date()): string {
  const d = new Date(iso);
  const start = (t: Date) => new Date(t.getFullYear(), t.getMonth(), t.getDate()).getTime();
  const days = Math.round((start(now) - start(d)) / 86400000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return d.toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    ...(d.getFullYear() === now.getFullYear() ? {} : { year: 'numeric' }),
  });
}

/** Changes in runs of the same day, in their order. A day can come twice
 * when the list is not in time order (it is in the order they were saved). */
export function byDay(changes: StatusChange[], now = new Date()): { day: string; changes: StatusChange[] }[] {
  const days: { day: string; changes: StatusChange[] }[] = [];
  for (const c of changes) {
    const day = dayOf(c.at, now);
    const last = days[days.length - 1];
    if (last?.day === day) last.changes.push(c);
    else days.push({ day, changes: [c] });
  }
  return days;
}
