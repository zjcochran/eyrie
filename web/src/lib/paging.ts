// SPDX-License-Identifier: AGPL-3.0-or-later
import type { PageSize } from './api.ts';

/** The page sizes a list offers; 25 is the default. */
export const sizes: PageSize[] = [25, 50, 100, 'all'];

/** How many pages total rows take; at least 1. */
export function pages(total: number, size: PageSize): number {
  return size === 'all' ? 1 : Math.max(1, Math.ceil(total / size));
}

/** The rows a page shows, as "26–50 of 120"; "" when there are none. */
export function range(offset: number, shown: number, total: number): string {
  if (!shown) return '';
  return `${offset + 1}–${offset + shown} of ${total}`;
}

/** The offset of a page, counted from 1. */
export function offsetOf(page: number, size: PageSize): number {
  return size === 'all' ? 0 : (page - 1) * size;
}
