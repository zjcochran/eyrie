// SPDX-License-Identifier: AGPL-3.0-or-later

package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/workload"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	m       *Manager
	now     time.Time
	mu      sync.Mutex
	changes []string
	log     bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{now: t0}
	h.m = New(st, slog.New(slog.NewTextHandler(&h.log, nil)))
	h.m.Now = func() time.Time { return h.now }
	h.m.Changed = func(_ int64, keys []string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if keys == nil {
			h.changes = append(h.changes, "all")
			return
		}
		slices.Sort(keys)
		h.changes = append(h.changes, strings.Join(keys, ","))
	}
	return h
}

// took returns the changes since the last call.
func (h *harness) took() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := strings.Join(h.changes, " ")
	h.changes = nil
	return out
}

// enroll adds and enrolls the agent edge, and returns it with its key.
func (h *harness) enroll(t *testing.T) (store.Agent, agentauth.Key) {
	t.Helper()
	ctx := context.Background()
	_, token, err := h.m.Add(ctx, "edge")
	if err != nil {
		t.Fatal(err)
	}
	k, _ := agentauth.NewKey()
	a, err := h.m.Enroll(ctx, agentauth.EnrollRequest{Token: token, PublicKey: k.Public().String(), Hostname: "node1", Platform: "docker", Version: "v1.0.0"}, "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	h.took()
	return a, k
}

// request makes a signed report.
func (h *harness) request(t *testing.T, k agentauth.Key, agent int64, body []byte) *http.Request {
	t.Helper()
	auth, err := agentauth.SignRequest(k, agent, http.MethodPost, agentauth.PathReport, body, h.now)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, agentauth.PathReport, bytes.NewReader(body))
	r.Header.Set("Authorization", auth)
	return r
}

func TestEnroll(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, token, err := h.m.Add(ctx, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := h.m.Get(a.ID); !ok || got.Name != "edge" || got.TokenExpires != t0.Add(time.Hour) {
		t.Errorf("added %+v", got)
	}
	k, _ := agentauth.NewKey()
	if _, err := h.m.Enroll(ctx, agentauth.EnrollRequest{Token: token, PublicKey: "rsa:x"}, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad key: %v", err)
	}
	if _, err := h.m.Enroll(ctx, agentauth.EnrollRequest{Token: "eya_nope", PublicKey: k.Public().String()}, ""); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "Settings → Agents") {
		t.Errorf("bad token: %v", err)
	}
	req := agentauth.EnrollRequest{Token: " " + token + " ", PublicKey: k.Public().String(), Hostname: strings.Repeat("h", 300), Platform: "docker", Version: "v1"}
	a, err = h.m.Enroll(ctx, req, "192.0.2.9")
	if err != nil || len(a.Hostname) != 255 || a.LastAddress != "192.0.2.9" || !Online(a, t0) {
		t.Fatalf("enrolled %+v %v", a, err)
	}
	if h.took() != "all" {
		t.Error("enrolling did not recheck the agent's monitors")
	}
	if l := h.m.List(); len(l) != 1 || l[0].PublicKey != k.Public().String() {
		t.Errorf("list %+v", l)
	}
}

func TestTokensAndRemove(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)
	if _, _, err := h.m.Add(ctx, "edge"); err == nil {
		t.Error("a second edge")
	}
	b, _, _ := h.m.Add(ctx, "core")
	if l := h.m.List(); len(l) != 2 || l[0].Name != "core" {
		t.Errorf("not sorted %+v", l)
	}
	if _, err := h.m.NewToken(ctx, 999); err == nil {
		t.Error("a token for no agent")
	}
	if _, err := h.m.NewToken(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.m.Get(a.ID); got.PublicKey != "" || got.TokenExpires.IsZero() || h.took() != "all" {
		t.Errorf("after a new token %+v", got)
	}
	if name, err := h.m.Remove(ctx, b.ID); err != nil || name != "core" || h.took() != "all" {
		t.Errorf("remove %s %v", name, err)
	}
	if _, err := h.m.Remove(ctx, b.ID); err == nil {
		t.Error("removed twice")
	}
	if _, ok := h.m.Get(b.ID); ok {
		t.Error("still there")
	}
}

func TestAuthenticate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, k := h.enroll(t)
	body := []byte(`{}`)
	r := h.request(t, k, a.ID, body)
	got, key, err := h.m.Authenticate(ctx, r, body)
	if err != nil || got.ID != a.ID || !key.Equal(k.Public()) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, _, err := h.m.Authenticate(ctx, r, body); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "replayed") {
		t.Errorf("replay: %v", err)
	}
	if _, _, err := h.m.Authenticate(ctx, h.request(t, k, a.ID, body), []byte(`{"x":1}`)); !errors.Is(err, ErrRefused) {
		t.Errorf("another body: %v", err)
	}
	other, _ := agentauth.NewKey()
	if _, _, err := h.m.Authenticate(ctx, h.request(t, other, a.ID, body), body); !errors.Is(err, ErrRefused) {
		t.Errorf("another key: %v", err)
	}
	if _, _, err := h.m.Authenticate(ctx, h.request(t, k, 999, body), body); !errors.Is(err, ErrRefused) {
		t.Errorf("unknown agent: %v", err)
	}
	r = httptest.NewRequest(http.MethodPost, agentauth.PathReport, nil)
	if _, _, err := h.m.Authenticate(ctx, r, nil); !errors.Is(err, ErrRefused) {
		t.Errorf("no header: %v", err)
	}
	h.now = t0.Add(agentauth.MaxKeyAge + time.Hour)
	if _, _, err := h.m.Authenticate(ctx, h.request(t, k, a.ID, body), body); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "older than 90 days") {
		t.Errorf("old key: %v", err)
	}

	if !h.m.LogRefusal(a.ID) || h.m.LogRefusal(a.ID) {
		t.Error("refusals are not limited")
	}
	h.now = h.now.Add(2 * time.Hour)
	if !h.m.LogRefusal(a.ID) {
		t.Error("a refusal an hour later is not logged")
	}
}

func TestRotate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, k := h.enroll(t)
	k2, _ := agentauth.NewKey()
	ts := t0.Unix()
	req := agentauth.RotateRequest{NewKey: k2.Public().String(), TS: ts, Proof: k2.Sign(agentauth.RotationProof(a.ID, k2.Public(), ts))}
	if err := h.m.Rotate(ctx, a, k.Public(), agentauth.RotateRequest{NewKey: "x"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad key: %v", err)
	}
	bad := req
	bad.Proof = k.Sign(agentauth.RotationProof(a.ID, k2.Public(), ts))
	if err := h.m.Rotate(ctx, a, k.Public(), bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad proof: %v", err)
	}
	if err := h.m.Rotate(ctx, a, k.Public(), req); err != nil {
		t.Fatal(err)
	}
	got, _ := h.m.Get(a.ID)
	if got.PublicKey != k2.Public().String() || got.PreviousKey != k.Public().String() {
		t.Fatalf("rotated %+v", got)
	}
	// Sent again after a cut-short rotation, signed with the old key.
	if err := h.m.Rotate(ctx, got, k.Public(), req); err != nil {
		t.Errorf("again: %v", err)
	}
	// The old key cannot rotate the agent to another key.
	k3, _ := agentauth.NewKey()
	other := agentauth.RotateRequest{NewKey: k3.Public().String(), TS: ts, Proof: k3.Sign(agentauth.RotationProof(a.ID, k3.Public(), ts))}
	if err := h.m.Rotate(ctx, got, k.Public(), other); !errors.Is(err, ErrRefused) {
		t.Errorf("rotated by the old key: %v", err)
	}
	// The old key still signs until the new one is used, which ends the
	// rotation.
	body := []byte(`{}`)
	if _, _, err := h.m.Authenticate(ctx, h.request(t, k, a.ID, body), body); err != nil {
		t.Errorf("old key during the rotation: %v", err)
	}
	if _, _, err := h.m.Authenticate(ctx, h.request(t, k2, a.ID, body), body); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.m.Get(a.ID); got.PreviousKey != "" {
		t.Error("the rotation did not end")
	}
	if _, _, err := h.m.Authenticate(ctx, h.request(t, k, a.ID, body), body); !errors.Is(err, ErrRefused) {
		t.Errorf("old key after the rotation: %v", err)
	}
	if st, _ := h.m.Store.Agent(ctx, a.ID); st.PreviousKey != "" || st.PublicKey != k2.Public().String() {
		t.Errorf("stored %+v", st)
	}
}

// report sends a report of agent, as it is now.
func (h *harness) report(t *testing.T, agent int64, rep agentauth.Report) {
	t.Helper()
	a, _ := h.m.Get(agent)
	if err := h.m.Report(context.Background(), a, rep, "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
}

func web(up bool, status string) workload.Workload {
	return workload.Workload{Key: "web", Name: "web", Kind: "container", Up: up, Status: status}
}

func TestReport(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)
	if _, err := h.m.Workload(a.ID, "web"); err == nil || err.Error() != "the agent edge reports no web" {
		t.Errorf("before a report: %v", err)
	}
	report := func(rep agentauth.Report) { h.report(t, a.ID, rep) }
	db := workload.Workload{Key: "db", Up: true, Status: "Running"}
	report(agentauth.Report{Workloads: []workload.Workload{web(true, "Running"), db}})
	if got := h.took(); got != "db,web" {
		t.Errorf("first report changed %q", got)
	}
	if w, err := h.m.Workload(a.ID, "web"); err != nil || !w.Up || w.Status != "Running" {
		t.Errorf("web %+v %v", w, err)
	}
	h.now = h.now.Add(30 * time.Second)
	report(agentauth.Report{Workloads: []workload.Workload{web(true, "Running"), db}})
	if got := h.took(); got != "" {
		t.Errorf("the same report changed %q", got)
	}
	report(agentauth.Report{Workloads: []workload.Workload{web(false, "Exited (exit code 1)")}})
	if got := h.took(); got != "db,web" {
		t.Errorf("web down and db gone changed %q", got)
	}
	if st, _ := h.m.Store.Agent(ctx, a.ID); len(st.Workloads) != 1 || st.Workloads[0].Up {
		t.Errorf("stored %+v", st.Workloads)
	}
}

func TestReportErrors(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)
	report := func(rep agentauth.Report) { h.report(t, a.ID, rep) }
	report(agentauth.Report{Workloads: []workload.Workload{web(false, "Exited (exit code 1)")}})
	h.took()
	report(agentauth.Report{Error: "cannot reach Docker"})
	if got := h.took(); got != "all" {
		t.Errorf("an error changed %q", got)
	}
	if _, err := h.m.Workload(a.ID, "web"); err == nil || err.Error() != "the agent edge cannot list its workloads: cannot reach Docker" {
		t.Errorf("error: %v", err)
	}
	if st, _ := h.m.Store.Agent(ctx, a.ID); len(st.Workloads) != 1 {
		t.Error("an error dropped the workloads")
	}
	report(agentauth.Report{Workloads: []workload.Workload{web(true, "Running")}})
	if got := h.took(); got != "all" {
		t.Errorf("recovering changed %q", got)
	}

	// Silent for longer than OfflineAfter: the sweep notices once.
	h.now = h.now.Add(agentauth.OfflineAfter + time.Second)
	h.m.sweep()
	h.m.sweep()
	if got := h.took(); got != "all" || !strings.Contains(h.log.String(), "agent stopped reporting") {
		t.Errorf("offline changed %q; log %s", got, h.log.String())
	}
	if _, err := h.m.Workload(a.ID, "web"); err == nil || err.Error() != "the agent edge has not reported for 1m31s" {
		t.Errorf("offline: %v", err)
	}
	report(agentauth.Report{Workloads: []workload.Workload{web(true, "Running")}})
	if got := h.took(); got != "all" || !strings.Contains(h.log.String(), "agent reporting again") {
		t.Errorf("back changed %q", got)
	}
}

func TestReportInvalid(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)
	for _, bad := range []agentauth.Report{
		{Workloads: []workload.Workload{{Key: ""}}},
		{Workloads: []workload.Workload{{Key: "a"}, {Key: "a"}}},
		{Workloads: []workload.Workload{{Key: strings.Repeat("k", 301)}}},
		{Workloads: make([]workload.Workload, maxWorkloads+1)},
	} {
		if err := h.m.Report(ctx, a, bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %d workloads: %v", len(bad.Workloads), err)
		}
	}

	if _, err := h.m.Workload(999, "web"); err == nil || err.Error() != "the agent was removed from Eyrie" {
		t.Errorf("removed: %v", err)
	}
	if _, err := h.m.NewToken(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Workload(a.ID, "web"); err == nil || err.Error() != "the agent edge is not enrolled" {
		t.Errorf("not enrolled: %v", err)
	}
	// A report from an agent removed meanwhile is dropped.
	gone := a
	gone.ID = 999
	if err := h.m.Report(ctx, gone, agentauth.Report{}, ""); err != nil {
		t.Error(err)
	}
}

func TestLoadAndWatch(t *testing.T) {
	h := newHarness(t)
	a, _ := h.enroll(t)
	m2 := New(h.m.Store, h.m.Log)
	if err := m2.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, ok := m2.Get(a.ID); !ok || got.PublicKey == "" {
		t.Errorf("loaded %+v", got)
	}
	h.now = t0.Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.m.Watch(ctx, time.Millisecond)
		close(done)
	}()
	for h.took() == "" {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	h.m.Store.Close()
	if err := m2.Load(context.Background()); err == nil {
		t.Error("loaded from a closed store")
	}
}

func TestMatcher(t *testing.T) {
	cfg := json.RawMessage(`{"agent":3,"workload":"web"}`)
	if !Matcher(3, nil)(cfg) || !Matcher(3, []string{"db", "web"})(cfg) || Matcher(3, []string{"db"})(cfg) || Matcher(4, nil)(cfg) {
		t.Error("matcher")
	}
	if Matcher(3, nil)(json.RawMessage(`{"url":"x"}`)) {
		t.Error("another type matched")
	}
}

func TestClip(t *testing.T) {
	if got := clip(" héllo ", 2); got != "h" {
		t.Errorf("%q", got)
	}
	if got := clip("abc", 5); got != "abc" {
		t.Errorf("%q", got)
	}
}
