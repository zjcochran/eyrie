// SPDX-License-Identifier: AGPL-3.0-or-later
// How the app looks: a layout, one of its colour schemes, and day, night or
// the system's choice. Pure functions, so the tests can run them; the same
// lists as the server's (internal/store/appearance.go).

export type Layout = 'console' | 'bento' | 'aurora';
export type Mode = 'system' | 'day' | 'night';

export interface Appearance {
  layout: Layout;
  scheme: string;
  mode: Mode;
}

/** A new user's, and the signed-out pages'. */
export const defaultAppearance: Appearance = { layout: 'console', scheme: 'ash', mode: 'system' };

export interface Scheme {
  id: string;
  label: string;
  /** Three colours that stand for it, for its swatch. */
  colours: [string, string, string];
}

const ash: Scheme = { id: 'ash', label: 'Ash', colours: ['#3a3d42', '#c3c8cf', '#e8a33d'] };

/** The layouts, each with its colour schemes, its default first. */
export const layouts: { id: Layout; label: string; about: string; schemes: Scheme[] }[] = [
  {
    id: 'console',
    label: 'Console',
    about: 'A sidebar of pages, the monitors in a list, and the chosen one beside it.',
    schemes: [
      ash,
      { id: 'dusk', label: 'Dusk', colours: ['#a97bff', '#c8ff4d', '#ff4d8d'] },
      { id: 'glacier', label: 'Glacier', colours: ['#38bdf8', '#34e3a0', '#ff6a3d'] },
    ],
  },
  {
    id: 'bento',
    label: 'Bento',
    about: 'Pages along the top and the overview in tiles of every size.',
    schemes: [
      ash,
      { id: 'cobalt', label: 'Cobalt', colours: ['#2b3bff', '#ff6a1a', '#b8ff3d'] },
      { id: 'lagoon', label: 'Lagoon', colours: ['#0a9396', '#ee6c4d', '#ffc300'] },
    ],
  },
  {
    id: 'aurora',
    label: 'Aurora',
    about: 'A rail of icons, and frosted glass over slowly drifting colour.',
    schemes: [
      ash,
      { id: 'borealis', label: 'Borealis', colours: ['#00e0c6', '#6e46ff', '#ff3d9a'] },
      { id: 'ember', label: 'Ember', colours: ['#ffc83c', '#ff7a1a', '#ff2e5b'] },
    ],
  },
];

export const modes: { id: Mode; label: string }[] = [
  { id: 'system', label: 'System' },
  { id: 'day', label: 'Day' },
  { id: 'night', label: 'Night' },
];

/** A layout's schemes. */
export function schemesOf(layout: Layout): Scheme[] {
  return (layouts.find((l) => l.id === layout) ?? layouts[0]).schemes;
}

/** An appearance the app can show: the default for what it does not know. */
export function normalize(a: Partial<Appearance> | undefined): Appearance {
  const layout = layouts.find((l) => l.id === a?.layout)?.id ?? defaultAppearance.layout;
  const schemes = schemesOf(layout);
  const scheme = schemes.find((s) => s.id === a?.scheme)?.id ?? schemes[0].id;
  const mode = modes.find((m) => m.id === a?.mode)?.id ?? defaultAppearance.mode;
  return { layout, scheme, mode };
}

/** Another layout, keeping the scheme where it has one of that name. */
export function withLayout(a: Appearance, layout: Layout): Appearance {
  return normalize({ ...a, layout });
}

/** Night or day: the mode, or the system's choice for "system". */
export function isNight(mode: Mode, systemDark: boolean): boolean {
  return mode === 'night' || (mode === 'system' && systemDark);
}
