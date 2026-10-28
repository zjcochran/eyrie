// SPDX-License-Identifier: AGPL-3.0-or-later

package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/store"
)

// maxResults is the most results one request may carry.
const maxResults = 10000

// queue is an agent's jobs not yet fetched. wake is closed, and replaced,
// when a job is added.
type queue struct {
	jobs []agentauth.Job
	wake chan struct{}
}

// waiter is a job waiting for its agent's result.
type waiter struct {
	agent  int64
	result chan check.Result
}

func (m *Manager) queue(agent int64) *queue {
	q, ok := m.queues[agent]
	if !ok {
		q = &queue{wake: make(chan struct{})}
		m.queues[agent] = q
	}
	return q
}

// dropJobs forgets a removed agent's jobs; their checks fail at once. The
// caller holds m.mu.
func (m *Manager) dropJobs(agent int64) {
	delete(m.queues, agent)
	for id, w := range m.waiting {
		if w.agent == agent {
			delete(m.waiting, id)
			w.result <- failed("The agent was removed from Eyrie")
		}
	}
}

func failed(format string, args ...any) check.Result {
	return check.Result{Message: fmt.Sprintf(format, args...), Latency: -1}
}

// unavailable says why an agent cannot run checks; "" when it can.
func (m *Manager) unavailable(agent int64) string {
	a, ok := m.Get(agent)
	switch {
	case !ok:
		return "The agent was removed from Eyrie"
	case a.PublicKey == "":
		return fmt.Sprintf("The agent %s is not enrolled", a.Name)
	case !Online(a, m.Now()):
		return fmt.Sprintf("The agent %s has not reported for %s", a.Name, m.Now().Sub(a.LastSeen).Round(time.Second))
	}
	return ""
}

func newJobID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck,gosec // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

// Checker returns what checks a monitor on its agent. The settings are
// opened only to be sent to it.
func (m *Manager) Checker(env *check.Env, mon store.Monitor) (check.Checker, error) {
	settings, err := check.Opened(env, mon.Type, mon.Config)
	if err != nil {
		return nil, err
	}
	return &remote{m: m, mon: mon, settings: settings}, nil
}

type remote struct {
	m        *Manager
	mon      store.Monitor
	settings json.RawMessage
}

func (r *remote) Check(ctx context.Context) check.Result {
	return r.m.Run(ctx, r.mon.AgentID, agentauth.Job{Monitor: r.mon.ID, Type: r.mon.Type, Settings: r.settings,
		TimeoutMS: r.mon.Timeout.Milliseconds()})
}

// Run gives a job to an agent and waits for its result until ctx ends.
func (m *Manager) Run(ctx context.Context, agent int64, job agentauth.Job) check.Result {
	if why := m.unavailable(agent); why != "" {
		return failed("%s", why)
	}
	job.ID = newJobID()
	w := &waiter{agent: agent, result: make(chan check.Result, 1)}
	m.mu.Lock()
	q := m.queue(agent)
	q.jobs = append(q.jobs, job)
	close(q.wake)
	q.wake = make(chan struct{})
	m.waiting[job.ID] = w
	m.mu.Unlock()
	select {
	case r := <-w.result:
		return r
	case <-ctx.Done():
	}
	m.mu.Lock()
	delete(m.waiting, job.ID)
	fetched := true
	if q, ok := m.queues[agent]; ok {
		if i := slices.IndexFunc(q.jobs, func(j agentauth.Job) bool { return j.ID == job.ID }); i >= 0 {
			q.jobs, fetched = slices.Delete(q.jobs, i, i+1), false
		}
	}
	m.mu.Unlock()
	select {
	case r := <-w.result: // it came just now
		return r
	default:
	}
	if why := m.unavailable(agent); why != "" {
		return failed("%s", why)
	}
	a, _ := m.Get(agent)
	if !fetched {
		return failed("The agent %s did not fetch the check in time", a.Name)
	}
	return failed("The agent %s sent no result in time", a.Name)
}

// Jobs returns an agent's jobs, waiting up to wait for one when it has
// none.
func (m *Manager) Jobs(ctx context.Context, agent int64, wait time.Duration) []agentauth.Job {
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		m.mu.Lock()
		q := m.queue(agent)
		jobs, wake := q.jobs, q.wake
		q.jobs = nil
		m.mu.Unlock()
		if len(jobs) > 0 {
			return jobs
		}
		select {
		case <-wake:
		case <-t.C:
			return []agentauth.Job{}
		case <-ctx.Done():
			return []agentauth.Job{}
		}
	}
}

// Deliver hands an agent's results to the checks waiting for them; results
// of jobs that are not this agent's, or no longer awaited, are dropped.
func (m *Manager) Deliver(agent int64, results []agentauth.Result) error {
	if len(results) > maxResults {
		return fmt.Errorf("%w: more than %d results", ErrInvalid, maxResults)
	}
	for _, r := range results {
		m.mu.Lock()
		w, ok := m.waiting[r.ID]
		if ok && w.agent == agent {
			delete(m.waiting, r.ID)
		}
		m.mu.Unlock()
		if !ok || w.agent != agent {
			continue
		}
		res := check.Result{OK: r.OK, Message: clip(r.Message, 1000), Latency: -1}
		if r.LatencyUS >= 0 {
			res.Latency = time.Duration(r.LatencyUS) * time.Microsecond
		}
		if r.CertExpires != nil {
			res.CertExpires = *r.CertExpires
		}
		w.result <- res
	}
	return nil
}

// Matcher matches the monitors that run on agent and, with keys, check
// one of those workloads: the monitors to check at once when the agent's
// report changed (keys) or it went offline or came back (keys nil).
func Matcher(agent int64, keys []string) func(store.Monitor) bool {
	return func(mon store.Monitor) bool {
		if mon.AgentID != agent {
			return false
		}
		if keys == nil {
			return true
		}
		for _, k := range check.WorkloadKeys(mon.Type, mon.Config) {
			if slices.Contains(keys, k) {
				return true
			}
		}
		return false
	}
}
