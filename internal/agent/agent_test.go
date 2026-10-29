// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/workload"
)

// eyrie is a fake Eyrie: it enrolls agents with the token "eya_good" and
// keeps the reports they sign.
type eyrie struct {
	t       *testing.T
	mu      sync.Mutex
	key     agentauth.PublicKey
	enrolls int
	reports []agentauth.Report
	rotated []string
	refuse  bool
	jobs    []agentauth.Job    // handed out at the next request for jobs
	results []agentauth.Result // results sent back
}

func (e *eyrie) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	if r.URL.Path == agentauth.PathEnroll {
		var req agentauth.EnrollRequest
		if json.Unmarshal(body, &req) != nil || req.Token != "eya_good" {
			http.Error(w, `{"error":"the enrollment token is unknown"}`, http.StatusUnauthorized)
			return
		}
		e.key, _ = agentauth.ParsePublicKey(req.PublicKey)
		e.enrolls++
		json.NewEncoder(w).Encode(agentauth.EnrollResponse{Agent: 7, Name: "edge"})
		return
	}
	auth, err := agentauth.ParseAuth(r.Header.Get("Authorization"))
	if err == nil && auth.Agent != 7 {
		err = errors.New("wrong agent")
	}
	if err == nil {
		_, err = auth.Verify([]agentauth.PublicKey{e.key}, r.Method, r.URL.RequestURI(), body, time.Now())
	}
	if err != nil || e.refuse {
		http.Error(w, "refused", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case agentauth.PathReport:
		var rep agentauth.Report
		json.Unmarshal(body, &rep)
		e.reports = append(e.reports, rep)
	case agentauth.PathRotate:
		var req agentauth.RotateRequest
		json.Unmarshal(body, &req)
		k, _ := agentauth.ParsePublicKey(req.NewKey)
		if !k.Verify(agentauth.RotationProof(7, k, req.TS), req.Proof) {
			http.Error(w, "bad proof", http.StatusBadRequest)
			return
		}
		e.key = k
		e.rotated = append(e.rotated, req.NewKey)
	case agentauth.PathJobs:
		jobs := e.jobs
		e.jobs = nil
		if len(jobs) == 0 {
			// As Eyrie, wait a little rather than answer at once.
			e.mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			e.mu.Lock()
			jobs = []agentauth.Job{}
		}
		json.NewEncoder(w).Encode(agentauth.Jobs{Jobs: jobs})
		return
	case agentauth.PathResults:
		var req agentauth.Results
		json.Unmarshal(body, &req)
		e.results = append(e.results, req.Results...)
	default:
		http.Error(w, "no", http.StatusNotFound)
	}
	w.WriteHeader(http.StatusNoContent)
}

// got returns the results sent back so far.
func (e *eyrie) got() []agentauth.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]agentauth.Result(nil), e.results...)
}

func (e *eyrie) setRefuse(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refuse = v
}

func (e *eyrie) last() agentauth.Report {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reports[len(e.reports)-1]
}

func (e *eyrie) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.reports)
}

type lister struct {
	mu  sync.Mutex
	err error
}

func (l *lister) List(context.Context) ([]workload.Workload, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	return []workload.Workload{{Key: "web", Up: true, Status: "Running"}}, nil
}

func setup(t *testing.T) (*eyrie, *Agent) {
	t.Helper()
	e := &eyrie{t: t}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	cfg := Config{URL: srv.URL, Token: "eya_good", Platform: PlatformDocker, Dir: filepath.Join(t.TempDir(), "agent"), Version: "v1.0.0", Hostname: "node1"}
	a := New(cfg, &lister{}, nil, slog.New(slog.DiscardHandler))
	a.ReportEvery, a.Settle, a.Reconnect = 10*time.Millisecond, time.Millisecond, time.Millisecond
	return e, a
}

// runUntil runs the agent until done reports true.
func runUntil(t *testing.T, a *Agent, done func() bool) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- a.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		select {
		case err := <-errc:
			return err
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	return <-errc
}

func TestRun(t *testing.T) {
	e, a := setup(t)
	if err := runUntil(t, a, func() bool { return e.count() >= 2 }); err != nil {
		t.Fatal(err)
	}
	if e.enrolls != 1 || e.reports[0].Hostname != "node1" || len(e.reports[0].Workloads) != 1 || e.reports[0].Platform != "docker" {
		t.Errorf("enrolls %d, reports %+v", e.enrolls, e.reports)
	}
	if err := Healthy(a.Cfg.Dir, time.Now()); err != nil {
		t.Error(err)
	}
	if err := Healthy(a.Cfg.Dir, time.Now().Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "last reported") {
		t.Errorf("stale: %v", err)
	}
	if err := Healthy(t.TempDir(), time.Now()); err == nil {
		t.Error("healthy without a heartbeat")
	}
	var st state
	// The test's own temporary folder.
	// nosemgrep
	b, _ := os.ReadFile(filepath.Join(a.Cfg.Dir, stateFile))
	if json.Unmarshal(b, &st) != nil || st.Agent != 7 || st.Name != "edge" || st.Token != tokenHash("eya_good") {
		t.Errorf("state %s", b)
	}
	if fi, _ := os.Stat(filepath.Join(a.Cfg.Dir, keyFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v", fi.Mode())
	}

	// Started again with the same token: no second enrollment.
	a2 := New(a.Cfg, a.Lister, nil, a.Log)
	a2.ReportEvery = 10 * time.Millisecond
	n := e.count()
	if err := runUntil(t, a2, func() bool { return e.count() > n }); err != nil {
		t.Fatal(err)
	}
	if e.enrolls != 1 {
		t.Errorf("enrolled %d times", e.enrolls)
	}

	// A new token: enrolled again, with a new key.
	old := e.key
	a3 := New(a.Cfg, a.Lister, nil, a.Log)
	os.WriteFile(filepath.Join(a.Cfg.Dir, stateFile), []byte(`{"agent":7,"name":"edge","token":"other"}`), 0o600)
	n = e.count()
	if err := runUntil(t, a3, func() bool { return e.count() > n }); err != nil {
		t.Fatal(err)
	}
	if e.enrolls != 2 || e.key.Equal(old) {
		t.Errorf("re-enrolled %d times, same key %v", e.enrolls, e.key.Equal(old))
	}
}

func TestEnrollErrors(t *testing.T) {
	_, a := setup(t)
	a.Cfg.Token = ""
	if err := a.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "EYRIE_AGENT_TOKEN") {
		t.Errorf("no token: %v", err)
	}
	a.Cfg.Token = "eya_bad"
	if err := a.Run(context.Background()); err == nil || err.Error() != "enrollment failed: refused by Eyrie: the enrollment token is unknown" {
		t.Errorf("bad token: %v", err)
	}
	os.WriteFile(filepath.Join(a.Cfg.Dir, keyFile), []byte("garbage"), 0o600)
	if err := a.Run(context.Background()); err == nil {
		t.Error("a garbage key was used")
	}
	os.Remove(filepath.Join(a.Cfg.Dir, keyFile))
	os.WriteFile(filepath.Join(a.Cfg.Dir, stateFile), []byte("{"), 0o600)
	if err := a.Run(context.Background()); err == nil {
		t.Error("a garbage state was used")
	}
	a.Cfg.Dir = "/dev/null/agent"
	if err := a.Run(context.Background()); err == nil {
		t.Error("an impossible folder")
	}
}

func TestReportOnce(t *testing.T) {
	e, a := setup(t)
	if err := runUntil(t, a, func() bool { return e.count() >= 1 }); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := a.Lister.(*lister)
	l.mu.Lock()
	l.err = errors.New("cannot reach Docker")
	l.mu.Unlock()
	if a.reportOnce(ctx) != 0 || e.last().Error != "cannot reach Docker" {
		t.Errorf("list error: %+v", e.last())
	}
	e.setRefuse(true)
	if a.reportOnce(ctx) != refusedWait {
		t.Error("a refusal does not wait")
	}
	e.setRefuse(false)
	a.Cfg.URL = "http://192.0.2.1:1/x\x7f"
	if a.reportOnce(ctx) != 0 {
		t.Error("a bad URL")
	}
}

func TestEvents(t *testing.T) {
	e, a := setup(t)
	a.ReportEvery = time.Hour
	calls := 0
	a.Events = func(ctx context.Context, changed func()) error {
		calls++
		if calls == 1 {
			return errors.New("stream broke")
		}
		changed()
		changed()
		<-ctx.Done()
		return ctx.Err()
	}
	// The first stream breaks and the agent reconnects; then a change
	// reports at once.
	if err := runUntil(t, a, func() bool { return e.count() >= 2 }); err != nil {
		t.Fatal(err)
	}
}

// rotated reports whether Eyrie has n rotations and the agent finished
// the last one.
func rotated(e *eyrie, dir string, n int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := os.Stat(filepath.Join(dir, nextKeyFile))
	return len(e.rotated) == n && os.IsNotExist(err)
}

func TestRotate(t *testing.T) {
	e, a := setup(t)
	if err := runUntil(t, a, func() bool { return e.count() >= 1 }); err != nil {
		t.Fatal(err)
	}
	// Due: rotated at start.
	a2 := New(a.Cfg, a.Lister, nil, a.Log)
	a2.ReportEvery = 10 * time.Millisecond
	old, _ := json.Marshal(state{Agent: 7, Name: "edge", Token: tokenHash("eya_good"), KeyCreated: time.Now().Add(-agentauth.RotateEvery)})
	os.WriteFile(filepath.Join(a.Cfg.Dir, stateFile), old, 0o600)
	if err := runUntil(t, a2, func() bool { return rotated(e, a.Cfg.Dir, 1) }); err != nil {
		t.Fatal(err)
	}
	// The test's own temporary folder.
	// nosemgrep
	b, _ := os.ReadFile(filepath.Join(a.Cfg.Dir, keyFile))
	k, err := agentauth.ParseKey(string(b))
	if err != nil || k.Public().String() != e.rotated[0] {
		t.Errorf("the key file has not the new key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.Dir, nextKeyFile)); !os.IsNotExist(err) {
		t.Error("key.next left")
	}

	// A rotation cut short is finished at the next start.
	next, _ := agentauth.NewKey()
	os.WriteFile(filepath.Join(a.Cfg.Dir, nextKeyFile), []byte(next.String()), 0o600)
	a3 := New(a.Cfg, a.Lister, nil, a.Log)
	a3.ReportEvery = 10 * time.Millisecond
	if err := runUntil(t, a3, func() bool { return rotated(e, a.Cfg.Dir, 2) }); err != nil {
		t.Fatal(err)
	}
	if e.rotated[1] != next.Public().String() {
		t.Error("not the saved key")
	}
	// A garbage key.next is dropped.
	os.WriteFile(filepath.Join(a.Cfg.Dir, nextKeyFile), []byte("garbage"), 0o600)
	if err := a3.finishRotation(context.Background()); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.Dir, nextKeyFile)); !os.IsNotExist(err) {
		t.Error("garbage key.next left")
	}
	// A refused rotation leaves the key alone.
	e.setRefuse(true)
	if err := a3.rotate(context.Background()); !errors.Is(err, errRefused) {
		t.Errorf("refused rotation: %v", err)
	}
}

func TestConfig(t *testing.T) {
	env := map[string]string{"EYRIE_AGENT_URL": " https://eyrie.example.com/ ", "EYRIE_AGENT_TOKEN": "eya_x", "EYRIE_AGENT_NAMESPACES": "shop, ops,,"}
	getenv := func(k string) string { return env[k] }
	c, err := ConfigFromEnv(getenv, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "https://eyrie.example.com" || c.Platform != "docker" || c.Dir != "/data/agent" || c.DockerHost != "unix:///var/run/docker.sock" ||
		fmt.Sprint(c.Namespaces) != "[shop ops]" || c.Version != "v1" {
		t.Errorf("%+v", c)
	}
	env["EYRIE_AGENT_PLATFORM"] = "nomad"
	if _, err := ConfigFromEnv(getenv, ""); err == nil || !strings.Contains(err.Error(), "docker, swarm, kubernetes, proxmox") {
		t.Errorf("platform: %v", err)
	}
	env["EYRIE_AGENT_PLATFORM"] = "swarm"
	for _, u := range []string{"", "eyrie.example.com", "ftp://eyrie.example.com", "https://eyrie.example.com/?a=1"} {
		env["EYRIE_AGENT_URL"] = u
		if _, err := ConfigFromEnv(getenv, ""); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	getenv := func(string) string { return "" }
	l, ev, err := Open(ctx, Config{Platform: PlatformDocker, DockerHost: "tcp://docker.example.com:2375"}, getenv)
	if _, ok := l.(*workload.Docker); !ok || ev == nil || err != nil {
		t.Errorf("docker: %T %v", l, err)
	}
	if _, _, err := Open(ctx, Config{Platform: PlatformDocker, DockerHost: "ftp://x"}, getenv); err == nil {
		t.Error("a bad Docker host")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"Swarm":{"LocalNodeState":"active","ControlAvailable":true}}`)
	}))
	defer srv.Close()
	l, ev, err = Open(ctx, Config{Platform: PlatformSwarm, DockerHost: "tcp://" + strings.TrimPrefix(srv.URL, "http://")}, getenv)
	if _, ok := l.(workload.Swarm); !ok || ev == nil || err != nil {
		t.Errorf("swarm: %T %v", l, err)
	}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"Swarm":{}}`) }))
	defer worker.Close()
	if _, _, err := Open(ctx, Config{Platform: PlatformSwarm, DockerHost: "tcp://" + strings.TrimPrefix(worker.URL, "http://")}, getenv); err == nil {
		t.Error("a worker node")
	}
	if _, _, err := Open(ctx, Config{Platform: PlatformKubernetes}, getenv); err == nil || !strings.Contains(err.Error(), "Kubernetes pod") {
		t.Errorf("kubernetes outside a pod: %v", err)
	}
	l, ev, err = Open(ctx, Config{Platform: PlatformProxmox, ProxmoxURL: "https://pve.example.com:8006", ProxmoxTokenID: "eyrie@pve!agent", ProxmoxSecret: "s"}, getenv)
	if _, ok := l.(*workload.Proxmox); !ok || ev != nil || err != nil {
		t.Errorf("proxmox: %T %v", l, err)
	}
}
