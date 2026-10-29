// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/check"
)

// Workers is how many checks an agent runs at once.
const Workers = 64

// jobLoop asks Eyrie for checks to run until ctx ends. Eyrie holds the
// request until it has one, up to agentauth.JobsWait, so the agent asks
// again at once; after an error it waits Reconnect, or longer after a
// refusal.
func (a *Agent) jobLoop(ctx context.Context) {
	sem := make(chan struct{}, Workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	for ctx.Err() == nil {
		var resp agentauth.Jobs
		err := a.call(ctx, agentauth.PathJobs, struct{}{}, &resp)
		wait := time.Duration(0)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errRefused):
			wait = refusedWait // reportLoop says why
		case err != nil:
			a.Log.Warn("cannot ask Eyrie for checks", "err", err)
			wait = a.Reconnect
		}
		for _, job := range resp.Jobs {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			wg.Go(func() {
				defer func() { <-sem }()
				a.runJob(ctx, job)
			})
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}
}

// runJob runs one check and sends its result.
func (a *Agent) runJob(ctx context.Context, job agentauth.Job) {
	a.send1(ctx, job.ID, a.check(ctx, job))
}

// send1 sends the result of a job.
func (a *Agent) send1(ctx context.Context, id string, res check.Result) {
	r := agentauth.Result{ID: id, OK: res.OK, Message: res.Message, LatencyUS: -1}
	if res.Latency >= 0 {
		r.LatencyUS = res.Latency.Microseconds()
	}
	if !res.CertExpires.IsZero() {
		t := res.CertExpires
		r.CertExpires = &t
	}
	if res.Usage != nil {
		r.Usage = &agentauth.Usage{CPU: res.Usage.CPU, Memory: res.Usage.Memory}
	}
	if err := a.do(ctx, agentauth.PathResults, agentauth.Results{Results: []agentauth.Result{r}}); err != nil && ctx.Err() == nil {
		a.Log.Warn("cannot send a check's result to Eyrie", "err", err)
	}
}

// check runs a job's check, with its timeout.
func (a *Agent) check(ctx context.Context, job agentauth.Job) check.Result {
	_, c, err := check.Parse(a.Env, job.Type, job.Settings)
	if err != nil {
		return check.Result{Message: "The agent cannot check this monitor: " + err.Error(), Latency: -1}
	}
	if c == nil {
		return check.Result{Message: "The agent cannot check this type of monitor", Latency: -1}
	}
	timeout := time.Duration(job.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 10*time.Minute {
		timeout = 48 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.Check(cctx)
}
