// SPDX-License-Identifier: AGPL-3.0-or-later

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/store"
)

// runAsync runs a job on agent and returns where its result comes.
func (h *harness) runAsync(ctx context.Context, agent int64, mon int64) <-chan check.Result {
	out := make(chan check.Result, 1)
	go func() { out <- h.m.Run(ctx, agent, agentauth.Job{Monitor: mon, Type: "port", TimeoutMS: 5000}) }()
	return out
}

func TestJobs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)
	if jobs := h.m.Jobs(ctx, a.ID, 10*time.Millisecond); len(jobs) != 0 {
		t.Errorf("jobs before any %+v", jobs)
	}

	// The agent waits for a job; one comes.
	got := make(chan []agentauth.Job, 1)
	go func() { got <- h.m.Jobs(ctx, a.ID, 5*time.Second) }()
	res := h.runAsync(ctx, a.ID, 3)
	jobs := <-got
	if len(jobs) != 1 || jobs[0].Monitor != 3 || jobs[0].ID == "" || jobs[0].TimeoutMS != 5000 {
		t.Fatalf("jobs %+v", jobs)
	}
	expires := t0.Add(30 * 24 * time.Hour)
	if err := h.m.Deliver(a.ID, []agentauth.Result{
		{ID: "unknown", OK: true},
		{ID: jobs[0].ID, OK: true, Message: "Port 5432 is open", LatencyUS: 1500, CertExpires: &expires},
	}); err != nil {
		t.Fatal(err)
	}
	if r := <-res; !r.OK || r.Message != "Port 5432 is open" || r.Latency != 1500*time.Microsecond || !r.CertExpires.Equal(expires) {
		t.Errorf("result %+v", r)
	}

}

func TestJobsLateOrWrong(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)

	// Another agent's result for this one's job is dropped.
	other, _ := h.enrollNamed(t, "core")
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	res := h.runAsync(cctx, a.ID, 4)
	jobs := h.m.Jobs(ctx, a.ID, time.Second)
	h.m.Deliver(other.ID, []agentauth.Result{{ID: jobs[0].ID, OK: true}})
	if r := <-res; r.OK || r.Message != "The agent edge sent no result in time" {
		t.Errorf("other agent's result %+v", r)
	}
	cancel()

	// A job not fetched in time.
	cctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if r := <-h.runAsync(cctx, a.ID, 5); r.OK || r.Message != "The agent edge did not fetch the check in time" || r.Latency != -1 {
		t.Errorf("not fetched %+v", r)
	}
	if jobs := h.m.Jobs(ctx, a.ID, 10*time.Millisecond); len(jobs) != 0 {
		t.Errorf("a job given up on is still queued: %+v", jobs)
	}

	// The latency is none when the agent has none.
	res = h.runAsync(ctx, a.ID, 6)
	jobs = h.m.Jobs(ctx, a.ID, time.Second)
	h.m.Deliver(a.ID, []agentauth.Result{{ID: jobs[0].ID, Message: strings.Repeat("x", 2000), LatencyUS: -1}})
	if r := <-res; r.Latency != -1 || len(r.Message) != 1000 {
		t.Errorf("no latency %+v", r)
	}
	if err := h.m.Deliver(a.ID, make([]agentauth.Result, maxResults+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many results: %v", err)
	}
	cctx, cancel = context.WithCancel(ctx)
	cancel()
	if jobs := h.m.Jobs(cctx, a.ID, time.Second); len(jobs) != 0 {
		t.Errorf("jobs after the request ended %+v", jobs)
	}
}

func TestJobsUnavailable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if r := h.m.Run(ctx, 99, agentauth.Job{}); r.OK || r.Message != "The agent was removed from Eyrie" {
		t.Errorf("unknown agent %+v", r)
	}
	pending, _, err := h.m.Add(ctx, "spare")
	if err != nil {
		t.Fatal(err)
	}
	if r := h.m.Run(ctx, pending.ID, agentauth.Job{}); r.Message != "The agent spare is not enrolled" {
		t.Errorf("not enrolled %+v", r)
	}
	a, _ := h.enroll(t)
	h.now = h.now.Add(agentauth.OfflineAfter + time.Second)
	if r := h.m.Run(ctx, a.ID, agentauth.Job{}); r.Message != "The agent edge has not reported for 1m31s" {
		t.Errorf("offline %+v", r)
	}
	h.now = t0

	// Gone offline while waiting.
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	res := h.runAsync(cctx, a.ID, 3)
	h.m.Jobs(ctx, a.ID, time.Second)
	h.mu.Lock()
	h.now = h.now.Add(agentauth.OfflineAfter + time.Second)
	h.mu.Unlock()
	if r := <-res; r.Message != "The agent edge has not reported for 1m31s" {
		t.Errorf("offline while waiting %+v", r)
	}
	h.now = t0

	// Removed while waiting: the check ends at once.
	res = h.runAsync(ctx, a.ID, 4)
	h.m.Jobs(ctx, a.ID, time.Second)
	if _, err := h.m.Remove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if r := <-res; r.Message != "The agent was removed from Eyrie" {
		t.Errorf("removed while waiting %+v", r)
	}
}

func TestChecker(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _ := h.enroll(t)
	env := &check.Env{}
	if _, err := h.m.Checker(env, store.Monitor{Type: "port", Config: json.RawMessage(`{"host":""}`)}); err == nil {
		t.Error("bad settings")
	}
	c, err := h.m.Checker(env, store.Monitor{ID: 3, AgentID: a.ID, Type: "port", Timeout: 2 * time.Second,
		Config: json.RawMessage(`{"host":"db.example.com","port":5432}`)})
	if err != nil {
		t.Fatal(err)
	}
	res := make(chan check.Result, 1)
	go func() { res <- c.Check(ctx) }()
	jobs := h.m.Jobs(ctx, a.ID, time.Second)
	if len(jobs) != 1 || jobs[0].Type != "port" || jobs[0].TimeoutMS != 2000 || !strings.Contains(string(jobs[0].Settings), `"db.example.com"`) {
		t.Fatalf("jobs %+v", jobs)
	}
	h.m.Deliver(a.ID, []agentauth.Result{{ID: jobs[0].ID, OK: true, Message: "Open", LatencyUS: -1}})
	if r := <-res; !r.OK {
		t.Errorf("result %+v", r)
	}
}

func TestMatcher(t *testing.T) {
	web := store.Monitor{AgentID: 3, Type: "docker", Config: json.RawMessage(`{"container":"web"}`)}
	api := store.Monitor{AgentID: 3, Type: "http", Config: json.RawMessage(`{"url":"https://example.com"}`)}
	local := store.Monitor{Type: "docker", Config: json.RawMessage(`{"container":"web"}`)}
	if !Matcher(3, nil)(web) || !Matcher(3, nil)(api) || !Matcher(3, []string{"db", "web"})(web) {
		t.Error("not matched")
	}
	if Matcher(3, []string{"db"})(web) || Matcher(3, []string{"web"})(api) || Matcher(4, nil)(web) || Matcher(3, nil)(local) {
		t.Error("matched")
	}
}
