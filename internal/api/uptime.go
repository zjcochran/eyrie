// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// Every figure added up from the hourly and daily stats is worked out in
// the background, every UptimeSettings.RefreshMinutes, so that no page
// waits for it: every monitor's uptime over 24 hours, 30 days and a year,
// its daily counts (the overview, status pages), its hourly counts and
// response times (its charts), and every monitor's hourly counts added
// together. The list of monitors, a monitor's page, status pages, badges
// and the overview read the figures last worked out. Before the first are, there are none
// ("no data").

// SettingUptime holds UptimeSettings as JSON.
const SettingUptime = "uptime"

// UptimeSettings are how often the uptime figures are worked out.
type UptimeSettings struct {
	RefreshMinutes int `json:"refresh_minutes"`
}

// DefaultUptime works them out every 30 minutes.
var DefaultUptime = UptimeSettings{RefreshMinutes: 30}

// They are worked out every minute to once a day.
const (
	minUptimeRefresh = 1
	maxUptimeRefresh = 1440
)

// uptimeFigures are every monitor's totals, its daily counts for the last
// 90 days and every monitor's hourly counts for the last 24 hours (the
// overview), as worked out at a time.
type uptimeFigures struct {
	day, month, year map[int64]store.Totals
	days             map[int64][]store.HourBucket // the last 90 days
	quarter          map[int64]store.Totals       // those days added up
	hourly           map[int64][]store.HourBucket // the last 35 days, for a monitor's charts
	hours            []store.HourBucket           // every monitor's, added together
	at               time.Time
	// overviews are the overview of every monitor, as sent, by number of
	// days: encoded once, when first asked for.
	overviews sync.Map
}

type uptimeWorker struct {
	latest atomic.Pointer[uptimeFigures]
	once   sync.Once
	wake   chan struct{}
}

func (u *uptimeWorker) init() { u.once.Do(func() { u.wake = make(chan struct{}, 1) }) }

// read fills st with the latest figures, empty before the first.
func (u *uptimeWorker) read(st *stats) {
	if f := u.latest.Load(); f != nil {
		st.day, st.month, st.year = f.day, f.month, f.year
	}
}

// refreshSoon has the figures worked out again at once: after a restore,
// or a new interval.
func (u *uptimeWorker) refreshSoon() {
	u.init()
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

// refreshUptime works out every monitor's uptime figures now.
func (s *Server) refreshUptime(ctx context.Context) error {
	now := s.now()
	f := &uptimeFigures{at: now}
	var err error
	if f.day, err = s.Store.HourlyTotals(ctx, now.Add(-24*time.Hour)); err != nil {
		return err
	}
	if f.month, err = s.Store.HourlyTotals(ctx, now.Add(-30*24*time.Hour)); err != nil {
		return err
	}
	if f.year, err = s.Store.DailyTotals(ctx, now.Add(-365*24*time.Hour)); err != nil {
		return err
	}
	if f.days, err = s.Store.Days(ctx, now.Add(-(overviewDays-1)*24*time.Hour)); err != nil {
		return err
	}
	f.quarter = map[int64]store.Totals{}
	for id, days := range f.days {
		var t store.Totals
		for _, d := range days {
			t.Up, t.Down = t.Up+d.Up, t.Down+d.Down
		}
		f.quarter[id] = t
	}
	if f.hours, err = s.Store.FleetHours(ctx, now.Add(-23*time.Hour)); err != nil {
		return err
	}
	if f.hourly, err = s.Store.AllHours(ctx, now.Add(-hoursDays*24*time.Hour)); err != nil {
		return err
	}
	s.uptime.latest.Store(f)
	return nil
}

// RunUptime works out the uptime figures until ctx ends, at once and then
// every UptimeSettings.RefreshMinutes. Call it after Handler.
func (s *Server) RunUptime(ctx context.Context) {
	s.uptime.init()
	for {
		if err := s.refreshUptime(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("cannot work out the uptime figures", "err", err)
		}
		every := time.Duration(s.uptimeSettings(ctx).RefreshMinutes) * time.Minute
		if s.uptimeEvery > 0 {
			every = s.uptimeEvery
		}
		t := time.NewTimer(every)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.uptime.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// uptimeSettings reads the settings, with their defaults.
func (s *Server) uptimeSettings(ctx context.Context) UptimeSettings {
	v := DefaultUptime
	if _, err := s.Store.GetSetting(ctx, SettingUptime, &v); err != nil && ctx.Err() == nil {
		s.Log.Warn("cannot read the uptime settings", "err", err)
	}
	return v
}

func (s *Server) getUptimeSettings(w http.ResponseWriter, r *http.Request) {
	v := DefaultUptime
	if _, err := s.Store.GetSetting(r.Context(), SettingUptime, &v); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) saveUptimeSettings(w http.ResponseWriter, r *http.Request) {
	var v UptimeSettings
	if !readJSON(w, r, &v) {
		return
	}
	if v.RefreshMinutes < minUptimeRefresh || v.RefreshMinutes > maxUptimeRefresh {
		invalid(w, http.StatusUnprocessableEntity, "Uptime figures are worked out every 1 to 1440 minutes.")
		return
	}
	if err := s.Store.SetSetting(r.Context(), SettingUptime, v, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.uptime.refreshSoon() // and from now on at the new interval
	s.audit(r, "uptime settings saved", "refresh_minutes", v.RefreshMinutes, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, v)
}
