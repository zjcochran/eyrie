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

// Every monitor's uptime over 24 hours, 30 days and a year is worked out in
// the background, every UptimeSettings.RefreshSeconds, so that no page
// waits for it: the list of monitors, a monitor's page, status pages and
// badges read the figures last worked out. Before the first are, there are
// none ("no data").

// SettingUptime holds UptimeSettings as JSON.
const SettingUptime = "uptime"

// UptimeSettings are how often the uptime figures are worked out.
type UptimeSettings struct {
	RefreshSeconds int `json:"refresh_seconds"`
}

// DefaultUptime works them out every minute.
var DefaultUptime = UptimeSettings{RefreshSeconds: 60}

// They are worked out every 10 seconds to every hour.
const (
	minUptimeRefresh = 10
	maxUptimeRefresh = 3600
)

// uptimeFigures are every monitor's totals, as worked out at a time.
type uptimeFigures struct {
	day, month, year map[int64]store.Totals
	at               time.Time
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
	s.uptime.latest.Store(f)
	return nil
}

// RunUptime works out the uptime figures until ctx ends, at once and then
// every UptimeSettings.RefreshSeconds. Call it after Handler.
func (s *Server) RunUptime(ctx context.Context) {
	s.uptime.init()
	for {
		if err := s.refreshUptime(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("cannot work out the uptime figures", "err", err)
		}
		every := time.Duration(s.uptimeSettings(ctx).RefreshSeconds) * time.Second
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
	if v.RefreshSeconds < minUptimeRefresh || v.RefreshSeconds > maxUptimeRefresh {
		invalid(w, http.StatusUnprocessableEntity, "Uptime figures are worked out every 10 to 3600 seconds.")
		return
	}
	if err := s.Store.SetSetting(r.Context(), SettingUptime, v, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.uptime.refreshSoon() // and from now on at the new interval
	s.audit(r, "uptime settings saved", "refresh_seconds", v.RefreshSeconds, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, v)
}
