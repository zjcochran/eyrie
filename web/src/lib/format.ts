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

/** Black or white, whichever reads better on a background such as #1f5fbf. */
export function textOn(color: string): string {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(color);
  if (!m) return '#000000';
  const [r, g, b] = m.slice(1).map((h) => {
    const c = parseInt(h, 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  const light = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  // The text with the higher contrast ratio against the background.
  return (light + 0.05) / 0.05 > 1.05 / (light + 0.05) ? '#000000' : '#ffffff';
}

/** A file size: 512 B, 3.4 KB, 12.1 MB. */
export function size(bytes: number): string {
  if (bytes < 1000) return `${bytes} B`;
  if (bytes < 1e6) return `${(bytes / 1e3).toFixed(1)} KB`;
  if (bytes < 1e9) return `${(bytes / 1e6).toFixed(1)} MB`;
  return `${(bytes / 1e9).toFixed(1)} GB`;
}
