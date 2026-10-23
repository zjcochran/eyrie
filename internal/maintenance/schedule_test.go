// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// xst is a made-up zone with daylight saving time: UTC-5 (XST), and UTC-4
// (XDT) from the second Sunday of March at 02:00 to the first Sunday of
// November at 02:00.
var xst = fakeZone("XST5XDT,M3.2.0,M11.1.0")

// fakeZone builds a zone from a POSIX TZ rule: TZif data (version 2) with
// one zone type and no transitions, so the rule decides every time.
func fakeZone(rule string) *time.Location {
	var b bytes.Buffer
	block := func() {
		b.WriteString("TZif2")
		b.Write(make([]byte, 15))
		for _, n := range []uint32{0, 0, 0, 0, 1, 4} { // UT, standard, leap second, transition, type, character counts
			binary.Write(&b, binary.BigEndian, n) //nolint:errcheck // a bytes.Buffer
		}
		binary.Write(&b, binary.BigEndian, int32(-5*3600)) //nolint:errcheck // the offset of XST
		b.Write([]byte{0, 0})                              // not daylight saving; abbreviation at 0
		b.WriteString("XST\x00")
	}
	block() // version 1 data
	block() // version 2 data
	b.WriteString("\n" + rule + "\n")
	loc, err := time.LoadLocationFromTZData("XST", b.Bytes())
	if err != nil {
		panic(err)
	}
	return loc
}

func TestFakeZone(t *testing.T) {
	winter := time.Date(2027, 1, 15, 12, 0, 0, 0, xst)
	summer := time.Date(2027, 7, 15, 12, 0, 0, 0, xst)
	if _, off := winter.Zone(); off != -5*3600 {
		t.Errorf("winter offset %d", off)
	}
	if name, off := summer.Zone(); off != -4*3600 || name != "XDT" {
		t.Errorf("summer %s %d", name, off)
	}
}

func mustPlan(t *testing.T, s Schedule) Plan {
	t.Helper()
	p, err := Compile(s)
	if err != nil {
		t.Fatalf("%+v: %v", s, err)
	}
	return p
}

// utc is a time in UTC, such as "2027-01-15 22:00".
func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

// check asserts what At answers at a time.
func check(t *testing.T, p Plan, at string, on bool, change string) {
	t.Helper()
	gotOn, gotChange := p.At(utc(at))
	want := time.Time{}
	if change != "" {
		want = utc(change)
	}
	if gotOn != on || !gotChange.Equal(want) {
		t.Errorf("%s at %s: on %v until %s, want %v until %s", p.Strategy(), at, gotOn, gotChange.UTC().Format("2006-01-02 15:04"), on, change)
	}
}

func TestManualAndOnce(t *testing.T) {
	check(t, mustPlan(t, Schedule{Strategy: Manual}), "2027-01-15 08:00", true, "")
	p := mustPlan(t, Schedule{Strategy: Once, Start: "2027-01-15T22:00", End: "2027-01-16T02:00"})
	check(t, p, "2027-01-15 08:00", false, "2027-01-15 22:00")
	check(t, p, "2027-01-15 22:00", true, "2027-01-16 02:00")
	check(t, p, "2027-01-16 01:59", true, "2027-01-16 02:00")
	check(t, p, "2027-01-16 02:00", false, "")
}

func TestWeekdays(t *testing.T) {
	// Sundays and Wednesdays, 22:00 to 02:00 the next day. 15 January 2027
	// is a Friday.
	p := mustPlan(t, Schedule{Strategy: Weekdays, Weekdays: []int{0, 3}, From: "22:00", To: "02:00"})
	check(t, p, "2027-01-15 08:00", false, "2027-01-17 22:00")
	check(t, p, "2027-01-17 23:00", true, "2027-01-18 02:00")
	check(t, p, "2027-01-18 01:00", true, "2027-01-18 02:00") // Monday, from Sunday's window
	check(t, p, "2027-01-18 02:00", false, "2027-01-20 22:00")
	// Back to back windows merge: every day, all day.
	all := mustPlan(t, Schedule{Strategy: Weekdays, Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, From: "00:00", To: "00:00",
		LastDay: "2027-01-20"})
	check(t, all, "2027-01-15 08:00", true, "2027-01-21 00:00")
}

func TestMonthdays(t *testing.T) {
	p := mustPlan(t, Schedule{Strategy: Monthdays, MonthDays: []int{31, LastOfMonth}, From: "03:00", To: "04:00"})
	check(t, p, "2027-01-15 08:00", false, "2027-01-31 03:00")
	check(t, p, "2027-01-31 03:30", true, "2027-01-31 04:00")
	// February has no 31st; its last day counts.
	check(t, p, "2027-02-01 08:00", false, "2027-02-28 03:00")
	check(t, p, "2027-03-01 08:00", false, "2027-03-31 03:00")
}

func TestInterval(t *testing.T) {
	p := mustPlan(t, Schedule{Strategy: Interval, EveryDays: 3, FirstDay: "2027-01-10", From: "12:00", To: "13:00"})
	check(t, p, "2027-01-01 08:00", false, "2027-01-10 12:00")
	check(t, p, "2027-01-10 12:30", true, "2027-01-10 13:00")
	check(t, p, "2027-01-10 13:00", false, "2027-01-13 12:00")
	// A first day far ahead is still found.
	far := mustPlan(t, Schedule{Strategy: Interval, EveryDays: 1, FirstDay: "2031-06-01", From: "12:00", To: "13:00"})
	check(t, far, "2027-01-01 08:00", false, "2031-06-01 12:00")
}

func TestDaysLimit(t *testing.T) {
	p := mustPlan(t, Schedule{Strategy: Weekdays, Weekdays: []int{5}, From: "08:00", To: "09:00",
		FirstDay: "2027-01-20", LastDay: "2027-01-31"})
	check(t, p, "2027-01-15 08:30", false, "2027-01-22 08:00") // before the first day
	check(t, p, "2027-01-29 08:30", true, "2027-01-29 09:00")
	check(t, p, "2027-01-30 08:30", false, "") // no Friday left
}

func TestDaylightSaving(t *testing.T) {
	// 02:30 every day in XST: 07:30 UTC in winter, 06:30 UTC in summer. On
	// 14 March 2027 02:30 does not exist; the window starts at 03:00.
	p := mustPlan(t, Schedule{Strategy: Weekdays, Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, From: "02:30", To: "03:30"})
	p.loc = xst
	check(t, p, "2027-01-15 00:00", false, "2027-01-15 07:30")
	check(t, p, "2027-07-15 00:00", false, "2027-07-15 06:30")
	// On 14 March 02:30 is skipped: the window starts at 03:00 XDT (07:00
	// UTC) and ends at 03:30 XDT.
	check(t, p, "2027-03-14 05:00", false, "2027-03-14 07:00")
	check(t, p, "2027-03-14 07:10", true, "2027-03-14 07:30")
	// An interval counts calendar days across the change.
	iv := mustPlan(t, Schedule{Strategy: Interval, EveryDays: 2, FirstDay: "2027-03-13", From: "12:00", To: "13:00"})
	iv.loc, iv.first = xst, time.Date(2027, 3, 13, 0, 0, 0, 0, xst)
	check(t, iv, "2027-03-14 00:00", false, "2027-03-15 16:00")
}

func TestCronPlan(t *testing.T) {
	// Sundays at 03:00 for two hours.
	p := mustPlan(t, Schedule{Strategy: CronJob, Cron: "0 3 * * sun", DurationMin: 120})
	check(t, p, "2027-01-15 08:00", false, "2027-01-17 03:00")
	check(t, p, "2027-01-17 04:59", true, "2027-01-17 05:00")
	// Every minute for an hour: runs merge into one stretch until the cron's
	// last run of the day ends.
	m := mustPlan(t, Schedule{Strategy: CronJob, Cron: "* 3 * * *", DurationMin: 60})
	check(t, m, "2027-01-15 03:10", true, "2027-01-15 04:59")
	// Limited to days.
	l := mustPlan(t, Schedule{Strategy: CronJob, Cron: "0 3 * * *", DurationMin: 10, FirstDay: "2027-02-01", LastDay: "2027-02-02"})
	check(t, l, "2027-01-15 08:00", false, "2027-02-01 03:00")
	check(t, l, "2027-02-03 00:00", false, "")
	never := mustPlan(t, Schedule{Strategy: CronJob, Cron: "0 0 31 2 *", DurationMin: 10})
	check(t, never, "2027-01-15 08:00", false, "")
}

func TestCompileErrors(t *testing.T) {
	for want, s := range map[string]Schedule{
		"not a time zone":           {Strategy: Manual, TimeZone: "Nowhere/Fictional"},
		"unknown schedule":          {Strategy: "hourly"},
		"first day must be a date":  {Strategy: Manual, FirstDay: "15/01/2027"},
		"last day must be a date":   {Strategy: Manual, LastDay: "soon"},
		"last day is before":        {Strategy: Manual, FirstDay: "2027-02-01", LastDay: "2027-01-01"},
		"start must be a time":      {Strategy: Once, Start: "tomorrow"},
		"end must be a time":        {Strategy: Once, Start: "2027-01-15T22:00", End: "later"},
		"end must be after":         {Strategy: Once, Start: "2027-01-15T22:00", End: "2027-01-15T22:00"},
		"times of day":              {Strategy: Weekdays, From: "25:00", To: "02:00", Weekdays: []int{1}},
		"interval is from 1":        {Strategy: Interval, From: "01:00", To: "02:00"},
		"needs its first day":       {Strategy: Interval, From: "01:00", To: "02:00", EveryDays: 2},
		"at least one day of the w": {Strategy: Weekdays, From: "01:00", To: "02:00"},
		"not a day of the week":     {Strategy: Weekdays, From: "01:00", To: "02:00", Weekdays: []int{7}},
		"at least one day of the m": {Strategy: Monthdays, From: "01:00", To: "02:00"},
		"not a day of the month":    {Strategy: Monthdays, From: "01:00", To: "02:00", MonthDays: []int{0}},
		"five fields":               {Strategy: CronJob, Cron: "0 3 * *", DurationMin: 10},
		"duration is from 1":        {Strategy: CronJob, Cron: "0 3 * * *"},
	} {
		if _, err := Compile(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if p := mustPlan(t, Schedule{Strategy: Manual, TimeZone: "Etc/UTC"}); p.Strategy() != Manual {
		t.Error("strategy")
	}
}
