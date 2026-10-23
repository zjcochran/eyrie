// SPDX-License-Identifier: AGPL-3.0-or-later
// How numbers and times are shown.

/** An uptime share (0 to 1) as a percentage, or a dash without checks. */
export function percent(share: number | null | undefined): string {
  if (share === null || share === undefined) return '–';
  if (share >= 1) return '100%';
  // Never round up to 100%: 99.996% shows as 99.99%.
  return `${(Math.floor(share * 10000) / 100).toFixed(2)}%`;
}

/** Milliseconds, rounded for people. */
export function millis(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '–';
  if (ms >= 10000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.round(ms)} ms`;
}

/** A duration in seconds, in the largest unit that fits. */
export function duration(seconds: number): string {
  const units: [number, string][] = [
    [86400, 'd'],
    [3600, 'h'],
    [60, 'min'],
  ];
  for (const [size, name] of units) {
    if (seconds >= size && seconds % size === 0) return `${seconds / size} ${name}`;
  }
  return `${seconds} s`;
}

/** How long ago a time was, relative to now. */
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return 'never';
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

/** Whole days until a time; negative once it has passed. */
export function daysUntil(iso: string, now = Date.now()): number {
  return Math.floor((Date.parse(iso) - now) / 86400000);
}

/** A chart point: when (ms), the response time (null: none) and whether it was down. */
export interface Point {
  t: number;
  v: number | null;
  down: boolean;
}
