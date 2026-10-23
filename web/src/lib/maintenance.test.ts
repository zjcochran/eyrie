// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { textOn } from './format.ts';
import { describeSchedule } from './maintenance.ts';

describe('describeSchedule', () => {
  it('describes each strategy', () => {
    assert.equal(describeSchedule({ strategy: 'manual', time_zone: '' }), 'While turned on');
    assert.equal(
      describeSchedule({ strategy: 'once', time_zone: 'Etc/UTC', start: '2027-01-15T22:00', end: '2027-01-16T02:00' }),
      'From 2027-01-15 22:00 to 2027-01-16 02:00, Etc/UTC',
    );
    assert.equal(
      describeSchedule({ strategy: 'interval', time_zone: '', every_days: 3, first_day: '2027-01-10', from: '12:00', to: '13:00' }),
      'Every 3 days from 2027-01-10, 12:00 to 13:00, Etc/UTC',
    );
    assert.equal(
      describeSchedule({ strategy: 'interval', time_zone: '', every_days: 1, first_day: '2027-01-10', last_day: '2027-02-01', from: '12:00', to: '13:00' }),
      'Every day from 2027-01-10, 12:00 to 13:00 until 2027-02-01, Etc/UTC',
    );
    assert.equal(
      describeSchedule({ strategy: 'weekdays', time_zone: 'Etc/UTC', weekdays: [3, 0], from: '22:00', to: '02:00' }),
      'Sundays and Wednesdays, 22:00 to 02:00, Etc/UTC',
    );
    assert.equal(
      describeSchedule({ strategy: 'monthdays', time_zone: '', month_days: [1, 2, 3, 11, 22, -1], from: '03:00', to: '04:00', last_day: '2027-12-31' }),
      'On the 1st, the 2nd, the 3rd, the 11th, the 22nd and the last day of each month, 03:00 to 04:00 (until 2027-12-31), Etc/UTC',
    );
    assert.equal(
      describeSchedule({ strategy: 'cron', time_zone: '', cron: '0 3 * * sun', duration_minutes: 120, first_day: '2027-01-01' }),
      'At 0 3 * * sun for 120 min (from 2027-01-01), Etc/UTC',
    );
    assert.equal(
      describeSchedule({ strategy: 'weekdays', time_zone: '', weekdays: [5], from: '08:00', to: '09:00', first_day: '2027-01-01', last_day: '2027-02-01' }),
      'Fridays, 08:00 to 09:00 (from 2027-01-01 until 2027-02-01), Etc/UTC',
    );
  });
});

describe('textOn', () => {
  it('picks the text colour that reads better', () => {
    assert.equal(textOn('#1f5fbf'), '#ffffff');
    assert.equal(textOn('#fdb022'), '#000000');
    assert.equal(textOn('#FFFFFF'), '#000000');
    assert.equal(textOn('blue'), '#000000');
  });
});
