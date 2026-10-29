// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// fakeClock moves only when the test advances it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) At(t time.Time) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if !t.After(c.now) {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, waiter{t, ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
}

var t0 = time.UnixMilli(1_800_000_000_000).UTC()

type harness struct {
	t      *testing.T
	st     *store.Store
	e      *Engine
	clock  *fakeClock
	beats  chan store.Beat
	notes  chan store.Beat
	since  chan time.Time
	cert   time.Time   // expiry of the certificate HTTP checks see
	up     atomic.Bool // whether the fake port answers
	cancel context.CancelFunc
	done   chan error
	log    *strings.Builder
	logMu  sync.Mutex
}

func (h *harness) Write(p []byte) (int, error) {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	return h.log.Write(p)
}

func (h *harness) logged() string {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	return h.log.String()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{t: t, st: st, clock: &fakeClock{now: t0}, beats: make(chan store.Beat, 100),
		notes: make(chan store.Beat, 100), since: make(chan time.Time, 100), log: &strings.Builder{}}
	h.up.Store(true)
	env := &check.Env{
		Dial: func(context.Context, string, string) (net.Conn, error) {
			if !h.up.Load() {
				return nil, errors.New("connection refused")
			}
			a, b := net.Pipe()
			b.Close()
			return a, nil
		},
		Transport: func(bool) http.RoundTripper { return http.DefaultTransport },
		Resolve:   func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("none") },
		Ping:      func(context.Context, netip.Addr, int, int) (time.Duration, error) { return 0, errors.New("none") },
	}
	h.e = New(st, env, slog.New(slog.NewTextHandler(h, nil)))
	h.e.Clock = h.clock
	h.e.Publish = func(ev Event) { h.beats <- ev.Beat }
	h.e.Env.Transport = func(bool) http.RoundTripper { return h }
	h.e.OnNotify = func(_ store.Monitor, b store.Beat, since time.Time) {
		h.notes <- b
		h.since <- since
	}
	return h
}

// RoundTrip answers HTTP checks with 200 and a certificate that expires
// at h.cert.
func (h *harness) RoundTrip(r *http.Request) (*http.Response, error) {
	h.logMu.Lock()
	exp := h.cert
	h.logMu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: http.NoBody, Request: r,
		TLS: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{NotAfter: exp}}}}, nil
}

func (h *harness) setCert(t time.Time) {
	h.logMu.Lock()
	h.cert = t
	h.logMu.Unlock()
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan error, 1)
	go func() { h.done <- h.e.Run(ctx) }()
	h.t.Cleanup(h.stop)
	h.e.do(context.Background(), func() error { return nil }) // loaded

}

func (h *harness) stop() {
	if h.cancel != nil {
		h.cancel()
		<-h.done
		h.cancel = nil
	}
}

func (h *harness) add(m store.Monitor) store.Monitor {
	h.t.Helper()
	m, err := h.st.CreateMonitor(context.Background(), m, t0)
	if err != nil {
		h.t.Fatal(err)
	}
	if h.cancel != nil {
		if err := h.e.Upsert(context.Background(), m); err != nil {
			h.t.Fatal(err)
		}
	}
	return m
}

func monitor(name, typ, config string) store.Monitor {
	return store.Monitor{Name: name, Type: typ, Active: true, Interval: time.Minute, RetryInterval: 20 * time.Second,
		Timeout: 5 * time.Second, Config: json.RawMessage(config)}
}

const portConfig = `{"host":"db.example.com","port":5432}`

// next waits for the next heartbeat.
func (h *harness) next() store.Beat {
	h.t.Helper()
	select {
	case b := <-h.beats:
		return b
	case <-time.After(5 * time.Second):
		h.t.Fatal("no heartbeat")
	}
	return store.Beat{}
}

// none checks that no heartbeat comes for a moment.
func (h *harness) none() {
	h.t.Helper()
	select {
	case b := <-h.beats:
		h.t.Fatalf("unexpected heartbeat %+v", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestScheduleRetriesAndRecovery(t *testing.T) {
	h := newHarness(t)
	h.start()
	m := monitor("DB", "port", portConfig)
	m.Retries = 1
	m = h.add(m)
	// Added while running: checked at once.
	if b := h.next(); b.Status != "up" || b.Message != "Connected" || !b.Important || b.MonitorID != m.ID {
		t.Fatalf("first check %+v", b)
	}
	h.none()
	h.up.Store(false)
	h.clock.Advance(time.Minute)
	if b := h.next(); b.Status != "pending" || b.Message != "connection refused" {
		t.Fatalf("pending %+v", b)
	}
	h.clock.Advance(20 * time.Second) // the retry interval
	if b := h.next(); b.Status != "down" || !b.Important {
		t.Fatalf("down %+v", b)
	}
	if n := <-h.notes; n.Status != "down" {
		t.Errorf("notification %+v", n)
	}
	if since := <-h.since; !since.IsZero() {
		t.Errorf("since of a new down %v", since)
	}
	downAt := h.clock.Now()
	h.up.Store(true)
	h.clock.Advance(time.Minute)
	if b := h.next(); b.Status != "up" {
		t.Fatalf("back up %+v", b)
	}
	if since := <-h.since; !since.Equal(downAt) {
		t.Errorf("down since %v, want %v", since, downAt)
	}
	if n := <-h.notes; n.Status != "up" {
		t.Errorf("notification %+v", n)
	}
	s := h.e.Statuses()[m.ID]
	if s.Status != state.Up || s.Up != 3 || s.Down != 1 || !s.Active || s.Latency < 0 { // pending counts as up
		t.Errorf("status %+v", s)
	}
	if !strings.Contains(h.logged(), `msg="monitor status" monitor=DB status=down`) {
		t.Errorf("log %s", h.logged())
	}
}

func TestStopSavesBeats(t *testing.T) {
	h := newHarness(t)
	h.start()
	m := h.add(monitor("DB", "port", portConfig))
	h.next()
	h.up.Store(false)
	h.clock.Advance(time.Minute)
	h.next()
	h.stop()
	beats, err := h.st.Heartbeats(context.Background(), m.ID, time.Time{}, 10)
	if err != nil || len(beats) != 2 {
		t.Fatalf("saved %d beats: %v", len(beats), err)
	}
	states, _ := h.st.States(context.Background())
	if states[m.ID].Status != "down" {
		t.Errorf("saved state %+v", states[m.ID])
	}
}

func TestRestartKeepsState(t *testing.T) {
	h := newHarness(t)
	m := h.add(monitor("DB", "port", portConfig))
	h.st.Save(context.Background(), []store.Record{{
		Beat:  store.Beat{MonitorID: m.ID, At: t0, Status: "down", Latency: -1},
		State: store.MonitorState{MonitorID: m.ID, Status: "down", Since: t0.Add(-time.Hour), LastCheck: t0, Latency: -1},
	}})
	bad := h.add(monitor("Broken", "port", `{"host":"","port":1}`))
	paused := monitor("Paused", "port", portConfig)
	paused.Active = false
	paused = h.add(paused)
	h.start()
	if s := h.e.Statuses()[m.ID]; s.Status != state.Down || !s.Since.Equal(t0.Add(-time.Hour)) {
		t.Errorf("loaded %+v", s)
	}
	if !strings.Contains(h.logged(), "Broken") {
		t.Error("bad settings not logged")
	}
	// The first checks are spread over the interval; all happen within it.
	h.clock.Advance(time.Minute)
	b := h.next()
	if b.MonitorID != m.ID || b.Status != "up" {
		t.Fatalf("after restart %+v", b)
	}
	if n := <-h.notes; n.Status != "up" {
		t.Errorf("recovery after restart not notified: %+v", n)
	}
	h.none() // neither the broken nor the paused monitor is checked
	if s := h.e.Statuses()[paused.ID]; s.Active || s.Status != state.Unknown {
		t.Errorf("paused %+v", s)
	}
	_ = bad
}

func TestPush(t *testing.T) {
	h := newHarness(t)
	h.start()
	p := h.add(monitor("Backup", "push", `{}`))
	h.none() // nothing until a push or the deadline
	if err := h.e.Push(context.Background(), p.ID, true, "", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if b := h.next(); b.Status != "up" || b.Message != "OK" || b.Latency != 1500*time.Millisecond {
		t.Errorf("push %+v", b)
	}
	h.clock.Advance(30 * time.Second)
	h.none()
	h.clock.Advance(31 * time.Second)
	if b := h.next(); b.Status != "down" || b.Message != "No push received in 1m0s" {
		t.Errorf("missed push %+v", b)
	}
	if err := h.e.Push(context.Background(), p.ID, false, "disk full", -1); err != nil {
		t.Fatal(err)
	}
	if b := h.next(); b.Status != "down" || b.Message != "disk full" {
		t.Errorf("pushed down %+v", b)
	}
	other := h.add(monitor("DB", "port", portConfig))
	h.next()
	for _, id := range []int64{other.ID, 9999} {
		if err := h.e.Push(context.Background(), id, true, "", -1); !errors.Is(err, ErrNotPush) {
			t.Errorf("push to %d: %v", id, err)
		}
	}
	if err := h.e.CheckNow(context.Background(), p.ID); err == nil {
		t.Error("checked a push monitor")
	}
}

func TestManual(t *testing.T) {
	h := newHarness(t)
	h.start()
	m := h.add(monitor("Office power", "manual", `{}`))
	h.none()
	ctx := context.Background()
	if err := h.e.SetManual(ctx, m.ID, state.Down, "outage"); err != nil {
		t.Fatal(err)
	}
	if b := h.next(); b.Status != "down" || b.Message != "outage" || !b.Important {
		t.Errorf("manual down %+v", b)
	}
	if err := h.e.SetManual(ctx, m.ID, state.Up, ""); err != nil {
		t.Fatal(err)
	}
	if b := h.next(); b.Status != "up" || b.Message != "Set to up" {
		t.Errorf("manual up %+v", b)
	}
	if err := h.e.SetManual(ctx, m.ID, state.Pending, ""); err == nil {
		t.Error("set to pending")
	}
	db := h.add(monitor("DB", "port", portConfig))
	h.next()
	if err := h.e.SetManual(ctx, db.ID, state.Up, ""); err == nil {
		t.Error("set a port monitor by hand")
	}
	if err := h.e.CheckNow(ctx, m.ID); err == nil {
		t.Error("checked a manual monitor")
	}
}

func TestCommands(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Before Run, commands wait for the engine to be ready.
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := h.e.CheckNow(short, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("before start: %v", err)
	}
	h.start()
	if err := h.e.CheckNow(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	m := monitor("DB", "port", portConfig)
	m.Active = false
	m = h.add(m)
	if err := h.e.CheckNow(ctx, m.ID); err == nil {
		t.Error("checked a paused monitor")
	}
	m.Config = json.RawMessage(`{"host":"","port":0}`)
	if err := h.e.Upsert(ctx, m); err == nil {
		t.Error("bad settings accepted")
	}
	m.Config, m.Active = json.RawMessage(portConfig), true
	if err := h.e.Upsert(ctx, m); err != nil {
		t.Fatal(err)
	}
	h.next()
	if err := h.e.CheckNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	h.next()
	h.stop()
	stopped, cancel2 := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel2()
	if err := h.e.CheckNow(stopped, m.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("after stop: %v", err)
	}
}

// TestStaleResult checks that a result of settings since changed is dropped.
func TestStaleResult(t *testing.T) {
	h := newHarness(t)
	h.start()
	m := h.add(monitor("DB", "port", portConfig))
	h.next()
	h.e.do(context.Background(), func() error {
		h.e.apply(result{id: m.ID, gen: -1, res: check.Result{OK: false}})
		h.e.apply(result{id: 9999})
		return nil
	})
	h.none()
}

func TestSpread(t *testing.T) {
	if spread(1, 0) != 0 {
		t.Error("no interval")
	}
	for id := int64(1); id < 100; id++ {
		if d := spread(id, time.Minute); d < 0 || d >= time.Minute {
			t.Fatalf("spread %d: %v", id, d)
		}
	}
}

func TestLoadFails(t *testing.T) {
	h := newHarness(t)
	h.st.Close()
	if err := h.e.Run(context.Background()); err == nil {
		t.Error("ran without a database")
	}
}

func TestWriteFails(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.add(monitor("DB", "port", portConfig))
	h.next()
	h.st.Close()
	h.stop()
	if !strings.Contains(h.logged(), "cannot save heartbeats") {
		t.Errorf("log %s", h.logged())
	}
}

func TestCertWarnings(t *testing.T) {
	h := newHarness(t)
	type warning struct {
		id   int64
		days int
	}
	warnings := make(chan warning, 10)
	h.e.CertDays = func() []int { return []int{21, 14, 7} }
	h.e.OnCert = func(m store.Monitor, _ time.Time, days int) { warnings <- warning{m.ID, days} }
	h.setCert(t0.Add(20*24*time.Hour + time.Hour))
	h.start()
	watched := h.add(monitor("Shop", "http", `{"url":"https://shop.example.com","notify_cert_expiry":true}`))
	h.add(monitor("Blog", "http", `{"url":"https://blog.example.com"}`)) // no warnings wanted
	h.next()
	h.next()
	if w := <-warnings; w != (warning{watched.ID, 20}) {
		t.Errorf("first warning %+v", w)
	}
	expect := func(want ...warning) {
		t.Helper()
		h.clock.Advance(time.Minute)
		h.next()
		h.next()
		for _, w := range want {
			if got := <-warnings; got != w {
				t.Errorf("warning %+v, want %+v", got, w)
			}
		}
		select {
		case w := <-warnings:
			t.Errorf("extra warning %+v", w)
		default:
		}
	}
	expect() // the 21-day warning was sent already
	h.setCert(t0.Add(10 * 24 * time.Hour))
	expect(warning{watched.ID, 9}) // 14 days
	h.setCert(t0.Add(10*24*time.Hour + time.Second))
	expect(warning{watched.ID, 9}) // a new certificate starts again: still within 14 days
	h.setCert(t0.Add(3 * 24 * time.Hour))
	expect(warning{watched.ID, 2}) // 7 days
	expect()
	if s := h.e.Statuses()[watched.ID]; !s.CertExpires.Equal(t0.Add(3 * 24 * time.Hour)) {
		t.Errorf("status %+v", s)
	}
	h.stop()
	states, _ := h.st.States(context.Background())
	if states[watched.ID].CertNotified != 7 {
		t.Errorf("saved %+v", states[watched.ID])
	}
}

func TestReplace(t *testing.T) {
	h := newHarness(t)
	h.start()
	old := h.add(monitor("Old", "port", portConfig))
	b := h.next()
	ctx := context.Background()
	var kept int
	err := h.e.Replace(ctx, func(ctx context.Context) error {
		// The heartbeat made before is saved by now.
		beats, _ := h.st.Heartbeats(ctx, old.ID, b.At.Add(-time.Second), 10)
		kept = len(beats)
		if err := h.st.DeleteMonitor(ctx, old.ID); err != nil {
			return err
		}
		_, err := h.st.CreateMonitor(ctx, monitor("New", "manual", `{}`), t0)
		return err
	})
	if err != nil || kept != 1 {
		t.Fatalf("replace: %v, %d beats saved before", err, kept)
	}
	st := h.e.Statuses()
	if _, ok := st[old.ID]; ok || len(st) != 1 {
		t.Errorf("statuses after: %+v", st)
	}
	// A check of the old data that ends now is dropped.
	h.e.do(ctx, func() error {
		h.e.apply(result{id: old.ID + 1, gen: 1, epoch: 0, res: check.Result{OK: true}})
		return nil
	})
	h.none()
	if err := h.e.Replace(ctx, func(context.Context) error { return errors.New("refused") }); err == nil || err.Error() != "refused" {
		t.Errorf("failed swap: %v", err)
	}
	if len(h.e.Statuses()) != 1 {
		t.Error("monitors not reloaded after a failed swap")
	}
}

func TestCheckMatching(t *testing.T) {
	h := newHarness(t)
	h.start()
	ctx := context.Background()
	a := h.add(monitor("Web", "port", portConfig))
	if b := h.next(); b.MonitorID != a.ID {
		t.Fatalf("first check %+v", b)
	}
	if err := h.e.CheckMatching(ctx, func(store.Monitor) bool { return false }); err != nil {
		t.Fatal(err)
	}
	h.none()
	var seen store.Monitor
	if err := h.e.CheckMatching(ctx, func(m store.Monitor) bool { seen = m; return true }); err != nil {
		t.Fatal(err)
	}
	if b := h.next(); b.MonitorID != a.ID || seen.ID != a.ID {
		t.Errorf("matched %+v %+v", b, seen)
	}
}

// fakeAgents checks monitors on agents with a fixed result.
type fakeAgents struct{ res check.Result }

func (f fakeAgents) Checker(_ *check.Env, m store.Monitor) (check.Checker, error) {
	if m.Name == "Cache" {
		return nil, errors.New("cannot open the secrets")
	}
	return f, nil
}

func (f fakeAgents) Check(context.Context) check.Result { return f.res }

func TestAgentMonitors(t *testing.T) {
	h := newHarness(t)
	h.start()
	m := monitor("Edge", "port", portConfig)
	m.AgentID = 7
	a := h.add(m)
	if b := h.next(); b.MonitorID != a.ID || b.Status != "down" || b.Message != "Agents are not available here" {
		t.Errorf("without agents %+v", b)
	}

	h = newHarness(t)
	h.e.Agents = fakeAgents{check.Result{OK: true, Message: "Checked on the agent", Latency: 3 * time.Millisecond,
		Usage: &check.Usage{CPU: 12.5, Memory: 40}}}
	h.start()
	a = h.add(m)
	if b := h.next(); b.MonitorID != a.ID || b.Status != "up" || b.Message != "Checked on the agent" || b.Latency != 3*time.Millisecond ||
		b.Usage == nil || *b.Usage != (store.Usage{CPU: 12.5, Memory: 40}) {
		t.Errorf("on the agent %+v", b)
	}
	for _, st := range h.e.Statuses() {
		if st.MonitorID == a.ID && (st.Usage == nil || st.Usage.CPU != 12.5) {
			t.Errorf("status %+v", st)
		}
	}
	bad := monitor("Cache", "port", portConfig)
	bad.AgentID = 7
	bad, err := h.st.CreateMonitor(context.Background(), bad, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.Upsert(context.Background(), bad); err == nil || err.Error() != "cannot open the secrets" {
		t.Errorf("upsert %v", err)
	}
}
