// SPDX-License-Identifier: AGPL-3.0-or-later
// Maintenance window schedules, described for people.
import type { Schedule } from './api.ts';

export const weekdayNames = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/** A list such as "a, b and c". */
function list(items: string[]): string {
  if (items.length <= 1) return items.join('');
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

function ordinal(n: number): string {
  if (n === -1) return 'the last day';
  const s = n % 10 === 1 && n !== 11 ? 'st' : n % 10 === 2 && n !== 12 ? 'nd' : n % 10 === 3 && n !== 13 ? 'rd' : 'th';
  return `the ${n}${s}`;
}

/** When a schedule is on, in words, with its time zone. */
export function describeSchedule(s: Schedule): string {
  const tz = s.time_zone || 'Etc/UTC';
  const hours = `${s.from} to ${s.to}`;
  let when: string;
  switch (s.strategy) {
    case 'manual':
      return 'While turned on';
    case 'once':
      when = `From ${(s.start ?? '').replace('T', ' ')} to ${(s.end ?? '').replace('T', ' ')}`;
      break;
    case 'interval':
      when = `Every ${s.every_days === 1 ? 'day' : `${s.every_days} days`} from ${s.first_day}, ${hours}`;
      break;
    case 'weekdays':
      when = `${list([...(s.weekdays ?? [])].sort().map((d) => weekdayNames[d] + 's'))}, ${hours}`;
      break;
    case 'monthdays':
      when = `On ${list((s.month_days ?? []).map(ordinal))} of each month, ${hours}`;
      break;
    default:
      when = `At ${s.cron} for ${s.duration_minutes} min`;
  }
  const range =
    s.strategy === 'once' || s.strategy === 'interval'
      ? s.last_day
        ? ` until ${s.last_day}`
        : ''
      : s.first_day || s.last_day
        ? ` (${s.first_day ? `from ${s.first_day}` : ''}${s.first_day && s.last_day ? ' ' : ''}${s.last_day ? `until ${s.last_day}` : ''})`
        : '';
  return `${when}${range}, ${tz}`;
}
