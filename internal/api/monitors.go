// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/engine"
	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// Limits of a monitor's shared settings.
const (
	MinInterval = 20 * time.Second
	MaxInterval = 24 * time.Hour
	MaxRetries  = 100
	MaxResend   = 10000
)

// beatView is a heartbeat as the API shows it.
type beatView struct {
	MonitorID int64     `json:"monitor_id"`
	At        time.Time `json:"at"`
	Status    string    `json:"status"`
	LatencyMS *float64  `json:"latency_ms"` // null: none
	Message   string    `json:"message"`
	Important bool      `json:"important"`
}

func ms(d time.Duration) *float64 {
	if d < 0 {
		return nil
	}
	v := math.Round(float64(d)/1e4) / 100 // milliseconds, two decimals
	return &v
}

func viewBeat(b store.Beat) beatView {
	return beatView{MonitorID: b.MonitorID, At: b.At, Status: b.Status, LatencyMS: ms(b.Latency), Message: b.Message,
		Important: b.Important}
}

type statusView struct {
	Status      string     `json:"status"` // unknown, up, pending, down or maintenance
	Since       *time.Time `json:"since"`
	Message     string     `json:"message"`
	LastCheck   *time.Time `json:"last_check"`
	LatencyMS   *float64   `json:"latency_ms"`
	CertExpires *time.Time `json:"cert_expires"`
	Checking    bool       `json:"checking"`
	// Maintenance is the title of the maintenance window it is in; "" when
	// it is not in one.
	Maintenance string `json:"maintenance"`
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

type uptimeView struct {
	Day   *float64 `json:"24h"` // share of checks up, 0 to 1; null without checks
	Month *float64 `json:"30d"`
	Year  *float64 `json:"1y"`
	// AvgLatencyMS is the mean response time over 24 hours.
	AvgLatencyMS *float64 `json:"avg_latency_24h_ms"`
}

func share(t store.Totals) *float64 {
	u := t.Uptime()
	if u < 0 {
		return nil
	}
	return &u
}

// monitorView is a monitor as the API shows it.
type monitorView struct {
	ID            int64            `json:"id"`
	Name          string           `json:"name"`
	Type          string           `json:"type"`
	ParentID      *int64           `json:"parent_id"`
	Active        bool             `json:"active"`
	Interval      float64          `json:"interval"` // seconds
	RetryInterval float64          `json:"retry_interval"`
	Retries       int              `json:"retries"`
	ResendEvery   int              `json:"resend_every"`
	Timeout       float64          `json:"timeout"`
	UpsideDown    bool             `json:"upside_down"`
	Description   string           `json:"description"`
	Settings      json.RawMessage  `json:"settings"`    // without secrets
	SecretsSet    []string         `json:"secrets_set"` // the names of the secret settings that are set
	PushURL       string           `json:"push_url,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Channels      []int64          `json:"channels"` // the notification channels it uses
	Tags          []monitorTagView `json:"tags"`
	Status        statusView       `json:"status"`
	Uptime        uptimeView       `json:"uptime"`
	Beats         []beatView       `json:"beats,omitempty"`
}

// monitorTagView is a tag on a monitor, with the monitor's value for it.
type monitorTagView struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Value string `json:"value"`
}

// stats are the uptime figures, channels and tags of every monitor, read
// once per request.
type stats struct {
	day, month, year map[int64]store.Totals
	channels         map[int64][]int64
	tags             map[int64][]store.MonitorTag
	tagByID          map[int64]store.Tag
}

func (s *Server) stats(ctx context.Context) (stats, error) {
	now := s.now()
	var st stats
	var err error
	if st.day, err = s.Store.HourlyTotals(ctx, now.Add(-24*time.Hour)); err != nil {
		return st, err
	}
	if st.month, err = s.Store.HourlyTotals(ctx, now.Add(-30*24*time.Hour)); err != nil {
		return st, err
	}
	if st.year, err = s.Store.DailyTotals(ctx, now.Add(-365*24*time.Hour)); err != nil {
		return st, err
	}
	if st.channels, err = s.Store.MonitorChannels(ctx); err != nil {
		return st, err
	}
	if st.tags, err = s.Store.MonitorTags(ctx); err != nil {
		return st, err
	}
	tags, err := s.Store.Tags(ctx)
	st.tagByID = map[int64]store.Tag{}
	for _, t := range tags {
		st.tagByID[t.ID] = t
	}
	return st, err
}

func (s *Server) viewMonitor(r *http.Request, m store.Monitor, es engine.Status, st stats) monitorView {
	v := monitorView{ID: m.ID, Name: m.Name, Type: m.Type, Active: m.Active, Interval: m.Interval.Seconds(),
		RetryInterval: m.RetryInterval.Seconds(), Retries: m.Retries, ResendEvery: m.ResendEvery,
		Timeout: m.Timeout.Seconds(), UpsideDown: m.UpsideDown, Description: m.Description,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Channels: st.channels[m.ID]}
	if v.Channels == nil {
		v.Channels = []int64{}
	}
	v.Settings, v.SecretsSet = check.Public(m.Type, m.Config)
	v.Tags = []monitorTagView{}
	for _, mt := range st.tags[m.ID] {
		t := st.tagByID[mt.TagID]
		v.Tags = append(v.Tags, monitorTagView{ID: t.ID, Name: t.Name, Color: t.Color, Value: mt.Value})
	}
	if m.ParentID != 0 {
		p := m.ParentID
		v.ParentID = &p
	}
	// The push URL lets its holder report the monitor's status: admins only.
	if sess := sessionFrom(r); m.PushToken != "" && sess != nil && sess.User.IsAdmin() {
		v.PushURL = s.externalURL(r) + "/api/push/" + m.PushToken + "?status=up&msg=OK&ping="
	}
	status := string(es.Status)
	if status == "" {
		status = string(state.Unknown)
	}
	v.Status = statusView{Status: status, Since: timeOrNil(es.Since), Message: es.Message, LastCheck: timeOrNil(es.LastCheck),
		LatencyMS: ms(es.Latency), CertExpires: timeOrNil(es.CertExpires), Checking: es.Running, Maintenance: es.Maintenance}
	if es.LastCheck.IsZero() {
		v.Status.LatencyMS = nil
	}
	day := st.day[m.ID]
	v.Uptime = uptimeView{Day: share(day), Month: share(st.month[m.ID]), Year: share(st.year[m.ID]), AvgLatencyMS: ms(day.AvgLatency())}
	return v
}

// listMonitors answers every monitor; ?beats=N adds each one's last N
// heartbeats (at most 100).
func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mons, err := s.Store.Monitors(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var recent map[int64][]store.Beat
	if n, _ := strconv.Atoi(r.URL.Query().Get("beats")); n > 0 {
		if recent, err = s.Store.RecentBeats(ctx, min(n, 100)); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	statuses := s.Engine.Statuses()
	out := []monitorView{}
	for _, m := range mons {
		v := s.viewMonitor(r, m, statuses[m.ID], st)
		if recent != nil {
			v.Beats = []beatView{}
			for _, b := range recent[m.ID] {
				v.Beats = append(v.Beats, viewBeat(b))
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) monitorFor(w http.ResponseWriter, r *http.Request) (store.Monitor, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.Monitor{}, false
	}
	m, err := s.Store.MonitorByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return store.Monitor{}, false
	}
	return m, true
}

func (s *Server) writeMonitor(w http.ResponseWriter, r *http.Request, code int, m store.Monitor) {
	st, err := s.stats(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, code, s.viewMonitor(r, m, s.Engine.Statuses()[m.ID], st))
}

func (s *Server) getMonitor(w http.ResponseWriter, r *http.Request) {
	if m, ok := s.monitorFor(w, r); ok {
		s.writeMonitor(w, r, http.StatusOK, m)
	}
}

// monitorBody is what creating or changing a monitor sends. Missing
// numbers take their defaults.
type monitorBody struct {
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	ParentID       *int64          `json:"parent_id"`
	Active         *bool           `json:"active"`
	Interval       float64         `json:"interval"`
	RetryInterval  float64         `json:"retry_interval"`
	Retries        int             `json:"retries"`
	ResendEvery    int             `json:"resend_every"`
	Timeout        float64         `json:"timeout"`
	UpsideDown     bool            `json:"upside_down"`
	Description    string          `json:"description"`
	Settings       json.RawMessage `json:"settings"`
	ResetPushToken bool            `json:"reset_push_token"`
	// ClearSecrets are secret settings to remove; an empty secret
	// otherwise keeps the saved one.
	ClearSecrets []string `json:"clear_secrets"`
	// Channels are the notification channels to use; left out, a new
	// monitor gets the default channels and a changed one keeps its own.
	Channels *[]int64 `json:"channels"`
	// Tags are its tags, each with a value ("" for none); left out, a
	// changed monitor keeps its own.
	Tags *[]struct {
		ID    int64  `json:"id"`
		Value string `json:"value"`
	} `json:"tags"`
}

// setTags saves a body's tags for a monitor, if it has any, and tells the
// engine: tags decide which maintenance windows cover it.
func (s *Server) setTags(r *http.Request, id int64, b monitorBody) error {
	if b.Tags == nil {
		return nil
	}
	tags := []store.MonitorTag{}
	for _, t := range *b.Tags {
		tags = append(tags, store.MonitorTag{TagID: t.ID, Value: t.Value})
	}
	if err := s.Store.SetMonitorTags(r.Context(), id, tags); err != nil {
		return err
	}
	return s.Engine.ReloadMaintenance(r.Context())
}

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// apply checks a body and puts it into m. Errors are meant for people.
func (s *Server) apply(b monitorBody, m *store.Monitor) error {
	m.Name, m.Description = strings.TrimSpace(b.Name), strings.TrimSpace(b.Description)
	if m.Name == "" || len([]rune(m.Name)) > 100 {
		return errors.New("the name must be 1 to 100 characters")
	}
	if len(m.Description) > 2000 {
		return errors.New("the description must be at most 2000 characters")
	}
	if err := applyTimes(b, m); err != nil {
		return err
	}
	if b.Retries < 0 || b.Retries > MaxRetries {
		return errors.New("the retries must be from 0 to 100")
	}
	if b.ResendEvery < 0 || b.ResendEvery > MaxResend {
		return errors.New("resend every must be from 0 (never) to 10000 checks")
	}
	m.Retries, m.ResendEvery, m.UpsideDown = b.Retries, b.ResendEvery, b.UpsideDown
	if b.Active != nil {
		m.Active = *b.Active
	}
	m.ParentID = 0
	if b.ParentID != nil {
		m.ParentID = *b.ParentID
	}
	cfg, err := check.Prepare(s.Env, m.Type, b.Settings, m.Config, b.ClearSecrets)
	if err != nil {
		return err
	}
	m.Config = cfg
	if m.Type == "push" && (m.PushToken == "" || b.ResetPushToken) {
		m.PushToken = newPushToken()
	}
	return nil
}

// applyTimes checks the interval, retry interval and timeout, with their
// defaults: 60 seconds, the interval, and 80% of the interval up to 48
// seconds.
func applyTimes(b monitorBody, m *store.Monitor) error {
	if b.Interval == 0 {
		b.Interval = 60
	}
	if b.RetryInterval == 0 {
		b.RetryInterval = b.Interval
	}
	if b.Timeout == 0 {
		b.Timeout = math.Min(48, b.Interval*0.8)
	}
	m.Interval, m.RetryInterval, m.Timeout = seconds(b.Interval), seconds(b.RetryInterval), seconds(b.Timeout)
	for _, d := range []struct {
		name string
		v    time.Duration
	}{{"interval", m.Interval}, {"retry interval", m.RetryInterval}} {
		if d.v < MinInterval || d.v > MaxInterval || d.v%time.Second != 0 {
			return errors.New("the " + d.name + " must be whole seconds from 20 to 86400")
		}
	}
	if m.Timeout < 100*time.Millisecond || m.Timeout > m.Interval {
		return errors.New("the timeout must be from 0.1 seconds to the interval")
	}
	return nil
}

func newPushToken() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) createMonitor(w http.ResponseWriter, r *http.Request) {
	var b monitorBody
	if !readJSON(w, r, &b) {
		return
	}
	if _, ok := check.Types[b.Type]; !ok {
		invalid(w, http.StatusUnprocessableEntity, "The type must be one of "+strings.Join(check.Names(), ", ")+".")
		return
	}
	m := store.Monitor{Type: b.Type, Active: true}
	if err := s.apply(b, &m); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	m, err := s.Store.CreateMonitor(r.Context(), m, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	channels := b.Channels
	if channels == nil {
		ids, err := s.Store.DefaultChannels(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		channels = &ids
	}
	err = s.Store.SetMonitorChannels(r.Context(), m.ID, *channels)
	if err == nil {
		err = s.setTags(r, m.ID, b)
	}
	if err != nil {
		s.Store.DeleteMonitor(r.Context(), m.ID) //nolint:errcheck,gosec // undoing the create; the error below is what matters
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("monitor created", "monitor", m.Name, "type", m.Type, "by", sessionFrom(r).User.Username)
	s.engineChanged(w, r, http.StatusCreated, m)
}

// engineChanged tells the engine about a saved monitor and answers with it.
func (s *Server) engineChanged(w http.ResponseWriter, r *http.Request, code int, m store.Monitor) {
	if err := s.Engine.Upsert(r.Context(), m); err != nil {
		s.fail(w, r, err)
		return
	}
	s.hub.publish("monitors", map[string]int64{"id": m.ID})
	s.writeMonitor(w, r, code, m)
}

func (s *Server) updateMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	var b monitorBody
	if !readJSON(w, r, &b) {
		return
	}
	if b.Type != "" && b.Type != m.Type {
		invalid(w, http.StatusUnprocessableEntity, "The type of a monitor cannot change: make a new one.")
		return
	}
	if err := s.apply(b, &m); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	m, err := s.Store.UpdateMonitor(r.Context(), m, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if b.Channels != nil {
		if err := s.Store.SetMonitorChannels(r.Context(), m.ID, *b.Channels); err != nil {
			s.storeError(w, r, err)
			return
		}
	}
	if err := s.setTags(r, m.ID, b); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("monitor changed", "monitor", m.Name, "by", sessionFrom(r).User.Username)
	s.engineChanged(w, r, http.StatusOK, m)
}

func (s *Server) setActive(active bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, ok := s.monitorFor(w, r)
		if !ok {
			return
		}
		if err := s.Store.SetMonitorActive(r.Context(), m.ID, active, s.now()); err != nil {
			s.storeError(w, r, err)
			return
		}
		m.Active = active
		s.Log.Info("monitor active", "monitor", m.Name, "active", active, "by", sessionFrom(r).User.Username)
		s.engineChanged(w, r, http.StatusOK, m)
	}
}

func (s *Server) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteMonitor(r.Context(), m.ID); err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Engine.Remove(r.Context(), m.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.hub.publish("monitors", map[string]int64{"id": m.ID})
	s.Log.Info("monitor deleted", "monitor", m.Name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}

// checkMonitor checks a monitor at once; the result arrives as an event.
func (s *Server) checkMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	if err := s.Engine.CheckNow(r.Context(), m.ID); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	s.Log.Info("check now", "monitor", m.Name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusAccepted)
}

// setManualStatus sets a manual monitor's status.
func (s *Server) setManualStatus(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	var b struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if err := s.Engine.SetManual(r.Context(), m.ID, state.Status(b.Status), strings.TrimSpace(b.Message)); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	s.Log.Info("manual status", "monitor", m.Name, "status", b.Status, "by", sessionFrom(r).User.Username)
	s.writeMonitor(w, r, http.StatusOK, m)
}

// heartbeats answers a monitor's heartbeats, newest first: ?since=
// (RFC 3339, default 24 hours ago) and ?limit= (default and most 10000).
func (s *Server) heartbeats(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	since := s.now().Add(-24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			invalid(w, http.StatusBadRequest, "since must be a time such as 2027-01-15T08:00:00Z.")
			return
		}
		since = t
	}
	limit := 10000
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, limit)
	}
	beats, err := s.Store.Heartbeats(r.Context(), m.ID, since, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []beatView{}
	for _, b := range beats {
		out = append(out, viewBeat(b))
	}
	writeJSON(w, http.StatusOK, out)
}

// hours answers a monitor's hourly stats for ?days= (default 7, most 35).
func (s *Server) hours(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	days := 7
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && n > 0 {
		days = min(n, 35)
	}
	buckets, err := s.Store.Hours(r.Context(), m.ID, s.now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type hourView struct {
		Start        time.Time `json:"start"`
		Up           int64     `json:"up"`
		Down         int64     `json:"down"`
		AvgLatencyMS *float64  `json:"avg_latency_ms"`
	}
	out := []hourView{}
	for _, b := range buckets {
		out = append(out, hourView{b.Start, b.Up, b.Down, ms(b.AvgLatency())})
	}
	writeJSON(w, http.StatusOK, out)
}

// monitorTypes answers the monitor types, for the form.
func (s *Server) monitorTypes(w http.ResponseWriter, _ *http.Request) {
	type typeView struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	out := []typeView{}
	for _, n := range check.Names() {
		out = append(out, typeView{n, check.Types[n].Label})
	}
	writeJSON(w, http.StatusOK, out)
}

// push records a push from a push monitor's URL: ?status=up (default) or
// down, ?msg= and ?ping= (milliseconds). It answers as Uptime Kuma does, so
// scripts written for it work.
func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.MonitorByPushToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "msg": "Monitor not found or not active."})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	ok := !strings.EqualFold(q.Get("status"), "down")
	latency := time.Duration(-1)
	if p, err := strconv.ParseFloat(q.Get("ping"), 64); err == nil && p >= 0 {
		latency = time.Duration(p * float64(time.Millisecond))
	}
	msg := q.Get("msg")
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if err := s.Engine.Push(r.Context(), m.ID, ok, msg, latency); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "msg": "Monitor not found or not active."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- retention ----

// SettingRetention holds Retention as JSON.
const SettingRetention = "retention"

// Retention is how long heartbeats are kept.
type Retention struct {
	HeartbeatDays int `json:"heartbeat_days"`
}

// DefaultRetention keeps heartbeats for 90 days.
var DefaultRetention = Retention{HeartbeatDays: 90}

func (s *Server) getRetention(w http.ResponseWriter, r *http.Request) {
	v := DefaultRetention
	if _, err := s.Store.GetSetting(r.Context(), SettingRetention, &v); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) saveRetention(w http.ResponseWriter, r *http.Request) {
	var v Retention
	if !readJSON(w, r, &v) {
		return
	}
	if v.HeartbeatDays < 1 || v.HeartbeatDays > 3650 {
		invalid(w, http.StatusUnprocessableEntity, "Heartbeats are kept from 1 to 3650 days.")
		return
	}
	if err := s.Store.SetSetting(r.Context(), SettingRetention, v, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Log.Info("retention saved", "heartbeat_days", v.HeartbeatDays, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, v)
}
