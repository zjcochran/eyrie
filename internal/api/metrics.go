// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/state"
)

// started is when Eyrie started, for process_start_time_seconds.
var started = time.Now()

// metrics serves Prometheus' text format. It is off (404) unless
// MetricsToken is set, and then needs "Authorization: Bearer <token>".
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if s.MetricsToken == "" {
		http.NotFound(w, r)
		return
	}
	got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.MetricsToken)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="eyrie"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	mons, err := s.Store.Monitors(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	statuses := s.Engine.Statuses()
	var b strings.Builder
	metric := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	metric("eyrie_build_info", "Eyrie's version.", "gauge")
	fmt.Fprintf(&b, "eyrie_build_info{version=%q} 1\n", label(s.Version))

	sort.Slice(mons, func(i, j int) bool { return mons[i].ID < mons[j].ID })
	labels := make([]string, len(mons))
	for i, m := range mons {
		labels[i] = fmt.Sprintf("id=\"%d\",monitor=%q,type=%q", m.ID, label(m.Name), m.Type)
	}
	metric("eyrie_monitor_status", "1 up, 0 down, 2 pending, -1 not checked yet.", "gauge")
	running := 0
	for i, m := range mons {
		st := statuses[m.ID]
		v := map[state.Status]int{state.Up: 1, state.Down: 0, state.Pending: 2}[st.Status]
		if st.Status == state.Unknown || st.Status == "" {
			v = -1
		}
		fmt.Fprintf(&b, "eyrie_monitor_status{%s} %d\n", labels[i], v)
		if st.Running {
			running++
		}
	}
	metric("eyrie_monitor_active", "1 when the monitor runs, 0 when it is paused.", "gauge")
	for i, m := range mons {
		fmt.Fprintf(&b, "eyrie_monitor_active{%s} %d\n", labels[i], map[bool]int{true: 1}[m.Active])
	}
	metric("eyrie_monitor_response_seconds", "Response time of the last check.", "gauge")
	for i, m := range mons {
		if st := statuses[m.ID]; st.Latency >= 0 && !st.LastCheck.IsZero() {
			fmt.Fprintf(&b, "eyrie_monitor_response_seconds{%s} %s\n", labels[i], float(st.Latency.Seconds()))
		}
	}
	metric("eyrie_monitor_cert_days_remaining", "Days until the TLS certificate the monitor saw expires.", "gauge")
	for i, m := range mons {
		if st := statuses[m.ID]; !st.CertExpires.IsZero() {
			fmt.Fprintf(&b, "eyrie_monitor_cert_days_remaining{%s} %s\n", labels[i], float(st.CertExpires.Sub(s.now()).Hours()/24))
		}
	}
	metric("eyrie_monitor_checks_total", "Checks since Eyrie started, by result (pending counts as up).", "counter")
	for i, m := range mons {
		st := statuses[m.ID]
		fmt.Fprintf(&b, "eyrie_monitor_checks_total{%s,result=\"up\"} %d\n", labels[i], st.Up)
		fmt.Fprintf(&b, "eyrie_monitor_checks_total{%s,result=\"down\"} %d\n", labels[i], st.Down)
	}
	metric("eyrie_checks_running", "Checks in progress.", "gauge")
	fmt.Fprintf(&b, "eyrie_checks_running %d\n", running)
	sent, failed := s.Notifier.Counts()
	metric("eyrie_notifications_total", "Notifications sent since Eyrie started, by result (failed: after every retry).", "counter")
	fmt.Fprintf(&b, "eyrie_notifications_total{result=\"sent\"} %d\neyrie_notifications_total{result=\"failed\"} %d\n", sent, failed)
	ok, failed := s.Analyzer.Counts()
	metric("eyrie_alert_analyses_total", "Alert analyses since Eyrie started, by result (failed: an error, a timeout or too many at once).", "counter")
	fmt.Fprintf(&b, "eyrie_alert_analyses_total{result=\"ok\"} %d\neyrie_alert_analyses_total{result=\"failed\"} %d\n", ok, failed)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	metric("go_goroutines", "Goroutines that currently exist.", "gauge")
	fmt.Fprintf(&b, "go_goroutines %d\n", runtime.NumGoroutine())
	metric("go_memstats_alloc_bytes", "Bytes of allocated heap objects.", "gauge")
	fmt.Fprintf(&b, "go_memstats_alloc_bytes %d\n", mem.Alloc)
	metric("go_memstats_sys_bytes", "Bytes of memory obtained from the system.", "gauge")
	fmt.Fprintf(&b, "go_memstats_sys_bytes %d\n", mem.Sys)
	metric("process_start_time_seconds", "When Eyrie started, in Unix seconds.", "gauge")
	fmt.Fprintf(&b, "process_start_time_seconds %d\n", started.Unix())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(b.String())) //nolint:errcheck,gosec // nothing to do if the client went away
}

// label makes a value safe inside a quoted label: %q escapes quotes and
// backslashes; new lines become spaces.
func label(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

func float(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
