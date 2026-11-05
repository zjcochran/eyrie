// SPDX-License-Identifier: AGPL-3.0-or-later
// Charts' points: pure functions, so the tests can run them.

import type { Point } from './format.ts';

/**
 * At most n points: each run of points becomes one, at its middle time,
 * with the mean of its values (null when none has one), down when any is.
 */
export function thin(points: Point[], n: number): Point[] {
  if (points.length <= n) return points;
  const out: Point[] = [];
  const size = points.length / n;
  for (let i = 0; i < n; i++) {
    const run = points.slice(Math.floor(i * size), Math.floor((i + 1) * size));
    if (!run.length) continue;
    const vs = run.map((p) => p.v).filter((v): v is number => v !== null);
    out.push({
      t: run[Math.floor(run.length / 2)].t,
      v: vs.length ? vs.reduce((a, b) => a + b, 0) / vs.length : null,
      down: run.some((p) => p.down),
    });
  }
  return out;
}

/** The index of the point nearest to time t; -1 without points. */
export function nearest(points: Point[], t: number): number {
  let best = -1;
  for (let i = 0; i < points.length; i++) {
    if (best < 0 || Math.abs(points[i].t - t) < Math.abs(points[best].t - t)) best = i;
  }
  return best;
}
