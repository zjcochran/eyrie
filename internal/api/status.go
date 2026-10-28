// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/badge"
	"github.com/zjcochran/eyrie/internal/engine"
	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// Days of history a status page shows, and how long resolved incidents
// stay on it.
const (
	statusDays        = 90
	incidentsShownFor = 14 * 24 * time.Hour
	upcomingFor       = 7 * 24 * time.Hour
)

// publicPage is a status page as anyone sees it: only names, statuses,
// uptime and what people wrote, never check messages or settings.
type publicPage struct {
	Slug           string              `json:"slug"`
	Title          string              `json:"title"`
	Description    string              `json:"description"`
	Footer         string              `json:"footer"`
	Published      bool                `json:"published"`
	Theme          string              `json:"theme"`
	Accent         string              `json:"accent"`
	LogoURL        string              `json:"logo_url"`
	ShowCertExpiry bool                `json:"show_cert_expiry"`
	Status         string              `json:"status"` // up, partial, down, maintenance or unknown
	Sections       []publicSection     `json:"sections"`
	Incidents      []incidentView      `json:"incidents"`
	Maintenance    []publicMaintenance `json:"maintenance"`
	GeneratedAt    time.Time           `json:"generated_at"`
}

type publicSection struct {
	Title    string          `json:"title"`
	Monitors []publicMonitor `json:"monitors"`
}

type publicMonitor struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Status      string       `json:"status"` // up, down, maintenance, paused or unknown
	Uptime      publicUptime `json:"uptime"`
	Days        []publicDay  `json:"days"` // statusDays of them, oldest first
	Tags        []publicTag  `json:"tags,omitempty"`
	CertExpires *time.Time   `json:"cert_expires,omitempty"`
}

type publicUptime struct {
	Day     *float64 `json:"24h"`
	Month   *float64 `json:"30d"`
	Quarter *float64 `json:"90d"`
}

type publicDay struct {
	Date   string   `json:"date"`   // UTC, such as 2027-01-15
	Uptime *float64 `json:"uptime"` // null without checks
}

type publicTag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Value string `json:"value"`
}

type publicMaintenance struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"` // on or scheduled
	Change      *time.Time `json:"change"`
}

// visiblePage finds a page by its address for this request: published
// pages for anyone, others only for signed-in users (a preview).
func (s *Server) visiblePage(w http.ResponseWriter, r *http.Request) (store.Page, bool) {
	p, err := s.Store.PageBySlug(r.Context(), r.PathValue("slug"))
	if err == nil && !p.Published {
		sess, serr := s.loadSession(r)
		if serr != nil {
			s.fail(w, r, serr)
			return p, false
		}
		if sess == nil {
			err = store.ErrNotFound
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		invalid(w, http.StatusNotFound, "No such status page.")
		return p, false
	}
	if err != nil {
		s.fail(w, r, err)
		return p, false
	}
	return p, true
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.visiblePage(w, r)
	if !ok {
		return
	}
	out, err := s.buildStatus(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) buildStatus(ctx context.Context, p store.Page) (publicPage, error) {
	now := s.now()
	out := publicPage{Slug: p.Slug, Title: p.Title, Description: p.Description, Footer: p.Footer, Published: p.Published,
		Theme: p.Theme, Accent: p.Accent, LogoURL: logoURL(p), ShowCertExpiry: p.ShowCertExpiry, Sections: []publicSection{},
		Incidents: []incidentView{}, Maintenance: []publicMaintenance{}, GeneratedAt: now}
	mons, err := s.Store.Monitors(ctx)
	if err != nil {
		return out, err
	}
	st, err := s.stats(ctx)
	if err != nil {
		return out, err
	}
	first := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(statusDays - 1))
	days, err := s.Store.Days(ctx, first)
	if err != nil {
		return out, err
	}
	statuses := s.Engine.Statuses()
	byID := map[int64]store.Monitor{}
	for _, m := range mons {
		byID[m.ID] = m
	}
	shown := map[int64]bool{}
	for _, sec := range p.Sections {
		ps := publicSection{Title: sec.Title, Monitors: []publicMonitor{}}
		for _, m := range sectionMonitors(sec, mons, st.tags) {
			pm := publicMonitor{ID: m.ID, Name: m.Name, Status: publicStatus(m, statuses[m.ID]),
				Uptime: publicUptime{Day: share(st.day[m.ID]), Month: share(st.month[m.ID])},
				Days:   dayBars(first, days[m.ID])}
			var quarter store.Totals
			for _, d := range days[m.ID] {
				quarter.Up, quarter.Down = quarter.Up+d.Up, quarter.Down+d.Down
			}
			pm.Uptime.Quarter = share(quarter)
			if p.ShowTags {
				for _, mt := range st.tags[m.ID] {
					t := st.tagByID[mt.TagID]
					pm.Tags = append(pm.Tags, publicTag{Name: t.Name, Color: t.Color, Value: mt.Value})
				}
			}
			if p.ShowCertExpiry {
				pm.CertExpires = timeOrNil(statuses[m.ID].CertExpires)
			}
			shown[m.ID] = true
			ps.Monitors = append(ps.Monitors, pm)
		}
		out.Sections = append(out.Sections, ps)
	}
	out.Status = overall(out.Sections)
	ins, err := s.Store.Incidents(ctx, p.ID, now.Add(-incidentsShownFor))
	if err != nil {
		return out, err
	}
	for _, in := range ins {
		out.Incidents = append(out.Incidents, viewIncident(in, false))
	}
	out.Maintenance, err = s.pageMaintenance(ctx, now, shown, byID, st.tags)
	return out, err
}

// sectionMonitors are the monitors a section shows: those with its tag
// and those it names, by name.
func sectionMonitors(sec store.Section, mons []store.Monitor, tags map[int64][]store.MonitorTag) []store.Monitor {
	var out []store.Monitor
	for _, m := range mons { // by name already
		tagged := sec.TagID != 0 && slices.ContainsFunc(tags[m.ID], func(t store.MonitorTag) bool { return t.TagID == sec.TagID })
		if tagged || slices.Contains(sec.MonitorIDs, m.ID) {
			out = append(out, m)
		}
	}
	return out
}

// publicStatus is a monitor's status as the public sees it: pending counts
// as up until its retries run out.
func publicStatus(m store.Monitor, es engine.Status) string {
	switch {
	case !m.Active:
		return "paused"
	case es.Status == state.Pending:
		return string(state.Up)
	case es.Status == "":
		return string(state.Unknown)
	}
	return string(es.Status)
}

// dayBars are a monitor's daily uptime from first on, one per day.
func dayBars(first time.Time, buckets []store.HourBucket) []publicDay {
	byDay := map[string]store.Totals{}
	for _, b := range buckets {
		byDay[b.Start.UTC().Format(time.DateOnly)] = b.Totals
	}
	out := make([]publicDay, statusDays)
	for i := range out {
		d := first.AddDate(0, 0, i).Format(time.DateOnly)
		out[i] = publicDay{Date: d, Uptime: share(byDay[d])}
	}
	return out
}

// overall sums up a page: down when every monitor that runs is down,
// partial when some are, maintenance when none is down but some are in
// maintenance, up when the rest are up.
func overall(sections []publicSection) string {
	counts := map[string]int{}
	total := 0
	for _, sec := range sections {
		for _, m := range sec.Monitors {
			if m.Status != "paused" {
				counts[m.Status]++
				total++
			}
		}
	}
	switch {
	case counts["down"] > 0 && counts["down"] == total:
		return "down"
	case counts["down"] > 0:
		return "partial"
	case counts["maintenance"] > 0:
		return "maintenance"
	case counts["up"] > 0:
		return "up"
	}
	return "unknown"
}

// pageMaintenance lists the maintenance windows that are on, or start
// within a week, and cover a monitor on the page.
func (s *Server) pageMaintenance(ctx context.Context, now time.Time, shown map[int64]bool, mons map[int64]store.Monitor,
	tags map[int64][]store.MonitorTag) ([]publicMaintenance, error) {
	out := []publicMaintenance{}
	ws, err := s.Store.Windows(ctx)
	if err != nil {
		return out, err
	}
	gs, err := s.Store.Groups(ctx)
	if err != nil {
		return out, err
	}
	parents := map[int64]int64{}
	for _, g := range gs {
		parents[g.ID] = g.ParentID
	}
	for _, w := range ws {
		v := s.viewWindow(w)
		upcoming := v.State == "scheduled" && v.Change != nil && v.Change.Sub(now) <= upcomingFor
		if (v.State != "on" && !upcoming) || !coversAny(w, shown, mons, parents, tags) {
			continue
		}
		out = append(out, publicMaintenance{Title: w.Title, Description: w.Description, State: v.State, Change: v.Change})
	}
	return out, nil
}

// coversAny says whether a window covers one of the monitors shown: by
// itself, by a tag, or through a group it is in (parents: each group's
// parent).
func coversAny(w store.Window, shown map[int64]bool, mons map[int64]store.Monitor, parents map[int64]int64,
	tags map[int64][]store.MonitorTag) bool {
	for id := range shown {
		hasTag := slices.ContainsFunc(tags[id], func(t store.MonitorTag) bool { return slices.Contains(w.TagIDs, t.TagID) })
		if w.AllMonitors || slices.Contains(w.MonitorIDs, id) || hasTag {
			return true
		}
		for g, depth := mons[id].GroupID, 0; g != 0 && depth < 20; g, depth = parents[g], depth+1 {
			if slices.Contains(w.GroupIDs, g) {
				return true
			}
		}
	}
	return false
}

// pageLogo serves a status page's logo, to whoever may see the page.
func (s *Server) pageLogo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.visiblePage(w, r)
	if !ok {
		return
	}
	logo, ok, err := s.Store.PageLogo(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		invalid(w, http.StatusNotFound, "This status page has no logo.")
		return
	}
	w.Header().Set("Content-Type", logo.ContentType)
	// Links carry ?v=<time stored>, so a new logo is a new URL.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(logo.Data) //nolint:errcheck,gosec // nothing to do if the client went away
}

// ---- badges ----

// badgeFor serves /api/badge/{id}/{kind}: status, uptime (?period=24h,
// 30d or 1y), response or cert, with ?label= instead of the usual label.
// Only monitors on a published status page have badges.
func (s *Server) badgeFor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	published, err := s.Store.PublishedMonitors(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.Store.MonitorByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !published[id]) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	b, ok, err := s.makeBadge(r, m, r.PathValue("kind"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if label := strings.TrimSpace(r.URL.Query().Get("label")); label != "" {
		b.Label = string([]rune(label)[:min(len([]rune(label)), 50)])
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write(b.SVG()) //nolint:errcheck,gosec // nothing to do if the client went away
}

func (s *Server) makeBadge(r *http.Request, m store.Monitor, kind string) (badge.Badge, bool, error) {
	es := s.Engine.Statuses()[m.ID]
	switch kind {
	case "status":
		st := publicStatus(m, es)
		color := map[string]string{"up": badge.Green, "down": badge.Red, "maintenance": badge.Violet}[st]
		if color == "" {
			color = badge.Grey
		}
		return badge.Badge{Label: m.Name, Message: st, Color: color}, true, nil
	case "uptime":
		return s.uptimeBadge(r, m)
	case "response":
		b := badge.Badge{Label: "response", Message: "no data", Color: badge.Grey}
		if es.Latency >= 0 && !es.LastCheck.IsZero() {
			ms := float64(es.Latency) / float64(time.Millisecond)
			b.Message, b.Color = millis(ms), badge.Green
			if ms >= 1000 {
				b.Color = badge.Orange
			} else if ms >= 300 {
				b.Color = badge.Yellow
			}
		}
		return b, true, nil
	case "cert":
		b := badge.Badge{Label: "certificate", Message: "none", Color: badge.Grey}
		if !es.CertExpires.IsZero() {
			days := int(es.CertExpires.Sub(s.now()).Hours() / 24)
			switch {
			case days < 0:
				b.Message, b.Color = "expired", badge.Red
			case days <= 7:
				b.Message, b.Color = fmt.Sprintf("%d days", days), badge.Red
			case days <= 21:
				b.Message, b.Color = fmt.Sprintf("%d days", days), badge.Yellow
			default:
				b.Message, b.Color = fmt.Sprintf("%d days", days), badge.Green
			}
		}
		return b, true, nil
	}
	return badge.Badge{}, false, nil
}

func (s *Server) uptimeBadge(r *http.Request, m store.Monitor) (badge.Badge, bool, error) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	var totals map[int64]store.Totals
	var err error
	switch period {
	case "24h":
		totals, err = s.Store.HourlyTotals(r.Context(), s.now().Add(-24*time.Hour))
	case "30d":
		totals, err = s.Store.HourlyTotals(r.Context(), s.now().Add(-30*24*time.Hour))
	case "1y":
		totals, err = s.Store.DailyTotals(r.Context(), s.now().Add(-365*24*time.Hour))
	default:
		return badge.Badge{}, false, nil
	}
	if err != nil {
		return badge.Badge{}, false, err
	}
	b := badge.Badge{Label: "uptime " + period, Message: "no data", Color: badge.Grey}
	if u := totals[m.ID].Uptime(); u >= 0 {
		b.Message, b.Color = percent(u), badge.UptimeColor(u)
	}
	return b, true, nil
}

// millis is a response time for a badge, with decimals under 10 ms: a fast
// check is not 0 ms.
func millis(ms float64) string {
	switch {
	case ms < 1:
		return fmt.Sprintf("%.2f ms", ms)
	case ms < 10:
		return fmt.Sprintf("%.1f ms", ms)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

// percent is an uptime share as a percentage, never rounded up to 100%:
// 0.99996 is 99.99%.
func percent(share float64) string {
	if share >= 1 {
		return "100%"
	}
	return strconv.FormatFloat(math.Floor(share*10000)/100, 'f', 2, 64) + "%"
}
