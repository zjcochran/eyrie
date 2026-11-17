// SPDX-License-Identifier: AGPL-3.0-or-later
// Accessibility: the words a screen reader says for charts, heartbeat bars
// and changes of status, and the choices of colour vision and text size.
// Pure functions, so the tests can run them.
import type { Beat, Status } from './api.ts';
import type { Point } from './format.ts';

export type Vision = 'typical' | 'red-green' | 'blue-yellow' | 'monochrome';

/**
 * The colour visions the status colours can be chosen for (access.css), with
 * their day colours for up, down, pending and maintenance, for a swatch.
 */
export const visions: { id: Vision; label: string; about: string; colours?: [string, string, string, string] }[] = [
  { id: 'typical', label: 'Typical', about: "The colour scheme's own colours for each status." },
  {
    id: 'red-green',
    label: 'Red-green',
    about: 'For protanopia and deuteranopia: blue for up, orange for down, dark gold for pending, lilac for maintenance.',
    colours: ['#005ba3', '#e66100', '#6b5200', '#8f6fd1'],
  },
  {
    id: 'blue-yellow',
    label: 'Blue-yellow',
    about: 'For tritanopia: teal for up, crimson for down, brown for pending, violet for maintenance.',
    colours: ['#00796b', '#c2185b', '#a15c00', '#5e35b1'],
  },
  {
    id: 'monochrome',
    label: 'Monochrome',
    about: 'For no colour vision: shades of grey from black (down) to light grey (paused), each with its own symbol.',
    colours: ['#5e5e5e', '#000000', '#3b3b3b', '#7a7a7a'],
  },
];

/** The text sizes, in percent. */
export const textSizes = [100, 115, 130, 150] as const;

/** What each status is called, and the symbol that marks it beside its colour. */
export const statusWords: Record<Status | 'paused', { word: string; symbol: string }> = {
  up: { word: 'up', symbol: '✓' },
  down: { word: 'down', symbol: '✕' },
  pending: { word: 'pending', symbol: '!' },
  maintenance: { word: 'in maintenance', symbol: '⏸' },
  unknown: { word: 'not checked yet', symbol: '?' },
  paused: { word: 'paused', symbol: '–' },
};

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** A heartbeat bar in words: "Last 30 checks: 28 up, 2 down." */
export function beatSummary(beats: Beat[]): string {
  if (!beats.length) return 'No checks yet.';
  const counts = new Map<Status, number>();
  for (const b of beats) counts.set(b.status, (counts.get(b.status) ?? 0) + 1);
  const order: Status[] = ['up', 'down', 'pending', 'maintenance', 'unknown'];
  const parts = order.filter((s) => counts.get(s)).map((s) => `${counts.get(s)} ${statusWords[s].word}`);
  const last = beats[beats.length - 1];
  return `Last ${plural(beats.length, 'check')}: ${parts.join(', ')}. The latest was ${statusWords[last.status].word}.`;
}

/** A chart in words: its period, the lowest, average, highest and latest values, and the checks that were down. */
export function chartSummary(label: string, points: Point[], from: number, to: number, format: (v: number) => string): string {
  const values = points.map((p) => p.v).filter((v): v is number => v !== null);
  const span = `${when(from, to, from)} to ${when(from, to, to)}`;
  if (!values.length) return `${label}, ${span}: no values.`;
  const sum = values.reduce((a, b) => a + b, 0);
  const latest = [...points].reverse().find((p) => p.v !== null)?.v ?? 0;
  const downs = points.filter((p) => p.down).length;
  return (
    `${label}, ${span}: lowest ${format(Math.min(...values))}, average ${format(sum / values.length)}, ` +
    `highest ${format(Math.max(...values))}, latest ${format(latest)}` +
    (downs ? `; down at ${plural(downs, 'point')}.` : '.')
  );
}

/** A time on a chart: a date over two days or more, else the date and time. */
export function when(from: number, to: number, t: number): string {
  return to - from >= 2 * 86400000
    ? new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    : new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

/**
 * What to announce for a heartbeat, given the monitor's name and its status
 * before: only a change of status is said ("Web is down: timeout."), and
 * nothing for a monitor's first check unless it is down.
 */
export function announcement(name: string, before: Status, b: Beat): string {
  if (b.status === before || !b.important) return '';
  if (before === 'unknown' && b.status === 'up') return '';
  const why = b.message ? `: ${b.message.replace(/\s+/g, ' ').trim().slice(0, 200)}` : '';
  switch (b.status) {
    case 'down':
      return `${name} is down${why}.`;
    case 'up':
      return before === 'maintenance' ? `${name} is out of maintenance and up.` : `${name} is up again.`;
    case 'pending':
      return `${name} is pending${why}.`;
    case 'maintenance':
      return `${name} is in maintenance.`;
    default:
      return '';
  }
}
