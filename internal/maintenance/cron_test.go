// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"strings"
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	for _, c := range []struct {
		expr, from, want string
	}{
		{"0 3 * * *", "2027-01-15 08:00", "2027-01-16 03:00"},
		{"0 3 * * *", "2027-01-16 03:00", "2027-01-16 03:00"}, // from is included
		{"*/15 * * * *", "2027-01-15 08:01", "2027-01-15 08:15"},
		{"30 2 1-7 * mon", "2027-01-15 08:00", "2027-01-18 02:30"},     // day of month or week
		{"0 0 29 feb *", "2027-01-15 08:00", "2028-02-29 00:00"},       // a leap day
		{"0 12 * jan-mar/2 7", "2027-01-15 08:00", "2027-01-17 12:00"}, // 7 is Sunday
		{"5,10 9 15 * *", "2027-01-15 09:06", "2027-01-15 09:10"},
		{"10-20/5 9 * * *", "2027-01-15 09:11", "2027-01-15 09:15"},
		{"0 3/6 * * *", "2027-01-15 08:00", "2027-01-15 09:00"}, // 3, 9, 15, 21
	} {
		cr, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		got, ok := cr.Next(utc(c.from), time.UTC)
		if !ok || !got.Equal(utc(c.want)) {
			t.Errorf("%s from %s: %s %v, want %s", c.expr, c.from, got, ok, c.want)
		}
	}
	// Seconds round up to the next minute.
	cr, _ := ParseCron("* * * * *")
	if got, _ := cr.Next(utc("2027-01-15 08:00").Add(time.Second), time.UTC); !got.Equal(utc("2027-01-15 08:01")) {
		t.Errorf("seconds: %s", got)
	}
	// A time skipped by daylight saving is not a run: 02:30 on 14 March
	// 2027 does not exist in XST.
	cr, _ = ParseCron("30 2 * * *")
	got, _ := cr.Next(time.Date(2027, 3, 14, 0, 0, 0, 0, xst), xst)
	if want := time.Date(2027, 3, 15, 2, 30, 0, 0, xst); !got.Equal(want) {
		t.Errorf("skipped time: %s", got)
	}
	if _, ok := mustCron(t, "0 0 30 2 *").Next(utc("2027-01-15 08:00"), time.UTC); ok {
		t.Error("30 February runs")
	}
}

func mustCron(t *testing.T, expr string) Cron {
	t.Helper()
	c, err := ParseCron(expr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCronErrors(t *testing.T) {
	for expr, want := range map[string]string{
		"* * * *":        "five fields",
		"60 * * * *":     `"60" is not a value for the minute`,
		"* 24 * * *":     "hour",
		"* * 0 * *":      "day of the month",
		"* * * 13 *":     "month",
		"* * * * 8":      "day of the week",
		"* * * * funday": "day of the week",
		"*/0 * * * *":    "not a step",
		"*/x * * * *":    "not a step",
		"5-1 * * * *":    "not a range",
		"1-x * * * *":    "not a range",
	} {
		if _, err := ParseCron(expr); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", expr, err)
		}
	}
}
