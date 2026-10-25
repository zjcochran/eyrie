// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zjcochran/eyrie/internal/workload"
)

// Agents is what Eyrie's agents last reported, for agent checks.
type Agents interface {
	// Workload returns a workload an agent reported. The error, meant for
	// people, says why there is none: the agent was removed, has not
	// enrolled, stopped reporting, could not list its workloads, or does
	// not have this one.
	Workload(agent int64, key string) (workload.Workload, error)
}

// Agent is the settings of the agent type: a workload one of Eyrie's
// agents reports must be up.
type Agent struct {
	Agent    int64  `json:"agent"`
	Workload string `json:"workload"`
}

func (a *Agent) normalize() error {
	a.Workload = strings.TrimSpace(a.Workload)
	switch {
	case a.Agent <= 0:
		return errors.New("choose an agent")
	case a.Workload == "" || len(a.Workload) > 300:
		return errors.New("choose a workload")
	}
	return nil
}

func (a *Agent) checker(env *Env) Checker { return &agentChecker{a: a, env: env} }

func (a *Agent) describe() []Fact { return []Fact{{"Workload", a.Workload}} }

type agentChecker struct {
	a   *Agent
	env *Env
}

func (c *agentChecker) Check(context.Context) Result {
	if c.env.Agents == nil {
		return fail("Agents are not available here")
	}
	w, err := c.env.Agents.Workload(c.a.Agent, c.a.Workload)
	if err != nil {
		return failErr(err)
	}
	return Result{OK: w.Up, Message: w.Status, Latency: -1}
}

// AgentTarget returns the agent and workload an agent monitor's saved
// settings name; ok is false for other settings.
func AgentTarget(raw json.RawMessage) (agent int64, key string, ok bool) {
	var a Agent
	if json.Unmarshal(raw, &a) != nil || a.Agent <= 0 {
		return 0, "", false
	}
	return a.Agent, a.Workload, true
}
