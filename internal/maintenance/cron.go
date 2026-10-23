// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field cron expression: minute, hour, day of the
// month, month and day of the week, each a set of allowed values.
type Cron struct {
	minute, hour, dom, month, dow [64]bool
	// domAny and dowAny: the field was *, so only the other one restricts
	// the day. When both restrict it, a day matching either matches, as in
	// cron.
	domAny, dowAny bool
}

var cronFields = []struct {
	name     string
	min, max int
	names    []string // names of the values from min, for months and days
}{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of the month", 1, 31, nil},
	{"month", 1, 12, []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{"day of the week", 0, 7, []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat", "sun"}},
}

// ParseCron reads a cron expression such as "0 3 * * sun" or
// "30 2 1-7 * 1-5": numbers, names of months and days, *, lists (a,b),
// ranges (a-b) and steps (*/n, a-b/n). Day of the week 0 and 7 are Sunday.
// Errors are meant for people.
func ParseCron(expr string) (Cron, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Cron{}, errors.New("a cron expression has five fields: minute, hour, day of the month, month, day of the week")
	}
	var c Cron
	sets := []*[64]bool{&c.minute, &c.hour, &c.dom, &c.month, &c.dow}
	for i, p := range parts {
		if err := parseField(strings.ToLower(p), i, sets[i]); err != nil {
			return Cron{}, err
		}
	}
	c.domAny, c.dowAny = parts[2] == "*", parts[4] == "*"
	if c.dow[7] {
		c.dow[0] = true
	}
	return c, nil
}

func parseField(s string, i int, set *[64]bool) error {
	f := cronFields[i]
	for _, item := range strings.Split(s, ",") {
		rng, step := item, 1
		if before, after, ok := strings.Cut(item, "/"); ok {
			n, err := strconv.Atoi(after)
			if err != nil || n < 1 || n > f.max {
				return fmt.Errorf("%q is not a step for the %s", after, f.name)
			}
			rng, step = before, n
		}
		lo, hi := f.min, f.max
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = cronValue(a, f.min, f.max, f.names); err != nil {
				return fmt.Errorf("%q is not a value for the %s", a, f.name)
			}
			hi = lo
			if isRange {
				if hi, err = cronValue(b, f.min, f.max, f.names); err != nil || hi < lo {
					return fmt.Errorf("%q is not a range for the %s", rng, f.name)
				}
			} else if step > 1 {
				hi = f.max // a/n: from a to the end
			}
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return nil
}

func cronValue(s string, lo, hi int, names []string) (int, error) {
	for i, n := range names {
		if s == n {
			return lo + i, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < lo || v > hi {
		return 0, errors.New("out of range")
	}
	return v, nil
}

// day says whether the cron runs on a date.
func (c Cron) day(t time.Time) bool {
	if !c.month[t.Month()] {
		return false
	}
	dom, dow := c.dom[t.Day()], c.dow[t.Weekday()]
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}

// cronSearch is how far ahead Next looks: a little over four years, so a
// cron for 29 February is found.
const cronSearch = 4*366 + 1

// Next is the first time from t on (to the minute, t included) when the
// cron runs, in loc. ok is false when it never runs, such as on 31 February.
func (c Cron) Next(t time.Time, loc *time.Location) (time.Time, bool) {
	t = t.In(loc)
	if t.Second() != 0 || t.Nanosecond() != 0 {
		t = t.Truncate(time.Minute).Add(time.Minute)
	}
	y, m, d := t.Date()
	for i := 0; i < cronSearch; i++ {
		day := time.Date(y, m, d+i, 0, 0, 0, 0, loc)
		if !c.day(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			if !c.hour[h] {
				continue
			}
			for mi := 0; mi < 60; mi++ {
				if !c.minute[mi] {
					continue
				}
				run := time.Date(day.Year(), day.Month(), day.Day(), h, mi, 0, 0, loc)
				// A time skipped by a clock change normalizes to another
				// hour; it is not a run.
				if run.Hour() != h || run.Before(t) {
					continue
				}
				return run, true
			}
		}
	}
	return time.Time{}, false
}
