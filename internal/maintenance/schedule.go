// SPDX-License-Identifier: AGPL-3.0-or-later

// Package maintenance decides when maintenance windows are on: a pure
// function of a window's schedule and the time, with no clock or I/O of its
// own.
//
// A schedule is one of:
//
//   - manual: on whenever the window is turned on;
//   - once: from one time to another;
//   - interval: every N days from a first day, between two times of day;
//   - weekdays: on chosen days of the week, between two times of day;
//   - monthdays: on chosen days of the month (or the last), between two
//     times of day;
//   - cron: at the times of a cron expression, for a duration.
//
// Times of day are in the schedule's time zone, so a window at 02:00 stays
// at 02:00 across daylight saving changes. A daily window whose end is not
// after its start ends the next day. Recurring schedules can be limited to
// a range of days.
package maintenance

import (
	"errors"
	"fmt"
	"slices"
	"time"
	_ "time/tzdata" // time zones without the system's zoneinfo
)

// Strategies.
const (
	Manual    = "manual"
	Once      = "once"
	Interval  = "interval"
	Weekdays  = "weekdays"
	Monthdays = "monthdays"
	CronJob   = "cron"
)

// LastOfMonth in MonthDays is the last day of each month.
const LastOfMonth = -1

// MaxDuration is the longest run of a cron schedule.
const MaxDuration = 31 * 24 * 60

// Schedule is when a window is on, as saved (JSON).
type Schedule struct {
	Strategy string `json:"strategy"`
	TimeZone string `json:"time_zone"` // IANA name; "" is Etc/UTC
	// Once: local times such as 2027-01-15T22:00.
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	// Interval, weekdays and monthdays: the time of day it starts and ends,
	// such as 22:00 and 02:00.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// EveryDays is the interval's length; its first day is FirstDay.
	EveryDays int `json:"every_days,omitempty"`
	// Weekdays: 0 Sunday to 6 Saturday.
	Weekdays []int `json:"weekdays,omitempty"`
	// MonthDays: 1 to 31, or LastOfMonth. A month without the day is
	// skipped.
	MonthDays []int `json:"month_days,omitempty"`
	// Cron and DurationMin: each run of the cron starts a window this many
	// minutes long.
	Cron        string `json:"cron,omitempty"`
	DurationMin int    `json:"duration_minutes,omitempty"`
	// FirstDay and LastDay limit a recurring schedule to these days
	// (2027-01-15), both included; "" for no limit. An interval needs a
	// FirstDay: its days are counted from it.
	FirstDay string `json:"first_day,omitempty"`
	LastDay  string `json:"last_day,omitempty"`
}

const (
	localTime = "2006-01-02T15:04"
	dayLayout = "2006-01-02"
	clock     = "15:04"
)

// Plan is a checked schedule, ready to say when it is on.
type Plan struct {
	s          Schedule
	loc        *time.Location
	start, end time.Time // once
	from, to   int       // minutes into the day
	first      time.Time // midnight of FirstDay; zero for none
	last       time.Time // midnight of LastDay; zero for none
	cron       Cron
}

// Compile checks a schedule. Errors are meant for people.
func Compile(s Schedule) (Plan, error) {
	p := Plan{s: s}
	tz := s.TimeZone
	if tz == "" {
		tz = "Etc/UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return p, fmt.Errorf("%q is not a time zone such as Etc/UTC or Europe/Paris", s.TimeZone)
	}
	p.loc = loc
	if p.first, err = p.day(s.FirstDay, "first day"); err != nil {
		return p, err
	}
	if p.last, err = p.day(s.LastDay, "last day"); err != nil {
		return p, err
	}
	if !p.first.IsZero() && !p.last.IsZero() && p.last.Before(p.first) {
		return p, errors.New("the last day is before the first day")
	}
	switch s.Strategy {
	case Manual:
		return p, nil
	case Once:
		return p, p.compileOnce()
	case Interval, Weekdays, Monthdays:
		return p, p.compileDaily()
	case CronJob:
		if p.cron, err = ParseCron(s.Cron); err != nil {
			return p, err
		}
		if s.DurationMin < 1 || s.DurationMin > MaxDuration {
			return p, fmt.Errorf("the duration is from 1 to %d minutes (31 days)", MaxDuration)
		}
		return p, nil
	}
	return p, fmt.Errorf("unknown schedule %q", s.Strategy)
}

func (p *Plan) day(s, name string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation(dayLayout, s, p.loc)
	if err != nil {
		return t, fmt.Errorf("the %s must be a date such as 2027-01-15", name)
	}
	return t, nil
}

func (p *Plan) compileOnce() error {
	var err error
	if p.start, err = time.ParseInLocation(localTime, p.s.Start, p.loc); err != nil {
		return errors.New("the start must be a time such as 2027-01-15T22:00")
	}
	if p.end, err = time.ParseInLocation(localTime, p.s.End, p.loc); err != nil {
		return errors.New("the end must be a time such as 2027-01-16T02:00")
	}
	if !p.end.After(p.start) {
		return errors.New("the end must be after the start")
	}
	return nil
}

func (p *Plan) compileDaily() error {
	from, err1 := time.Parse(clock, p.s.From)
	to, err2 := time.Parse(clock, p.s.To)
	if err1 != nil || err2 != nil {
		return errors.New("the times of day must be such as 22:00 and 02:00")
	}
	p.from, p.to = from.Hour()*60+from.Minute(), to.Hour()*60+to.Minute()
	switch p.s.Strategy {
	case Interval:
		if p.s.EveryDays < 1 || p.s.EveryDays > 3650 {
			return errors.New("the interval is from 1 to 3650 days")
		}
		if p.first.IsZero() {
			return errors.New("an interval needs its first day")
		}
	case Weekdays:
		if len(p.s.Weekdays) == 0 {
			return errors.New("choose at least one day of the week")
		}
		for _, d := range p.s.Weekdays {
			if d < 0 || d > 6 {
				return fmt.Errorf("%d is not a day of the week (0 Sunday to 6 Saturday)", d)
			}
		}
	default:
		if len(p.s.MonthDays) == 0 {
			return errors.New("choose at least one day of the month")
		}
		for _, d := range p.s.MonthDays {
			if (d < 1 || d > 31) && d != LastOfMonth {
				return fmt.Errorf("%d is not a day of the month (1 to 31, or -1 for the last)", d)
			}
		}
	}
	return nil
}

// Strategy is the schedule's strategy.
func (p Plan) Strategy() string { return p.s.Strategy }

// maxRuns bounds the runs At merges into one stretch, such as a cron every
// minute that lasts an hour.
const maxRuns = 10000

// At says whether the schedule is on at t, and when that changes: when it
// ends if it is on, when it next starts if not. change is zero when it
// never changes. A manual schedule is always on: the window being turned
// on or off is what counts.
func (p Plan) At(t time.Time) (on bool, change time.Time) {
	if p.s.Strategy == Manual {
		return true, time.Time{}
	}
	start, end, ok := p.next(t)
	if !ok {
		return false, time.Time{}
	}
	if start.After(t) {
		return false, start
	}
	// On: runs that start before this one ends carry it on.
	for i := 0; i < maxRuns; i++ {
		s, e, ok := p.next(end)
		if !ok || s.After(end) {
			break
		}
		if e.After(end) {
			end = e
		}
	}
	return true, end
}

// next is the first run that ends after t: its start and end.
func (p Plan) next(t time.Time) (time.Time, time.Time, bool) {
	switch p.s.Strategy {
	case Once:
		return p.start, p.end, p.end.After(t)
	case CronJob:
		d := time.Duration(p.s.DurationMin) * time.Minute
		// A run that started up to its duration ago may still be on.
		from := t.Add(-d).Add(time.Minute).Truncate(time.Minute)
		if from.Before(p.first) {
			from = p.first
		}
		for {
			s, ok := p.cron.Next(from, p.loc)
			if !ok || !p.inRange(s) {
				return time.Time{}, time.Time{}, false // past the last day
			}
			if e := s.Add(d); e.After(t) {
				return s, e, true
			}
			from = s.Add(time.Minute)
		}
	}
	// Daily windows: from the day before t, so one that started yesterday
	// and ends today is found, or from the first day.
	y, m, d := t.In(p.loc).Date()
	first := time.Date(y, m, d-1, 0, 0, 0, 0, p.loc)
	if first.Before(p.first) {
		first = p.first
	}
	for i := 0; i <= 2*366; i++ {
		day := first.AddDate(0, 0, i)
		if !p.last.IsZero() && day.After(p.last) {
			break
		}
		if !p.matches(day) {
			continue
		}
		s := at(day, p.from)
		e := at(day, p.to)
		if !e.After(s) {
			e = at(day.AddDate(0, 0, 1), p.to)
		}
		if e.After(t) {
			return s, e, true
		}
	}
	return time.Time{}, time.Time{}, false
}

// daysBetween counts calendar days from a to b, both midnight in one zone.
func daysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua).Hours() / 24)
}

// at is a time of day (minutes) on a day, in the day's zone. A time the
// clocks skip (they go forward past it) is when they reach it: 02:30 on a
// day they go from 02:00 to 03:00 is 03:00.
func at(day time.Time, minutes int) time.Time {
	t := time.Date(day.Year(), day.Month(), day.Day(), minutes/60, minutes%60, 0, 0, day.Location())
	for i := 0; i < 24*60 && t.Hour()*60+t.Minute() < minutes && t.Day() == day.Day(); i++ {
		t = t.Add(time.Minute) // time.Date put it before the gap
	}
	return t
}

// inRange says whether a run starting at t is within the first and last
// days.
func (p Plan) inRange(t time.Time) bool {
	t = t.In(p.loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, p.loc)
	return (p.first.IsZero() || !day.Before(p.first)) && (p.last.IsZero() || !day.After(p.last))
}

// matches says whether a daily window runs on a day (midnight, in loc).
func (p Plan) matches(day time.Time) bool {
	switch p.s.Strategy {
	case Interval:
		days := daysBetween(p.first, day)
		return days >= 0 && days%p.s.EveryDays == 0
	case Weekdays:
		return slices.Contains(p.s.Weekdays, int(day.Weekday()))
	}
	if slices.Contains(p.s.MonthDays, day.Day()) {
		return true
	}
	return slices.Contains(p.s.MonthDays, LastOfMonth) && day.AddDate(0, 0, 1).Day() == 1
}
