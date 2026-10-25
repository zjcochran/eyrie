// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zjcochran/eyrie/internal/workload"
)

type fakeAgents map[string]workload.Workload

func (f fakeAgents) Workload(agent int64, key string) (workload.Workload, error) {
	if agent != 1 {
		return workload.Workload{}, errors.New("the agent was removed")
	}
	w, ok := f[key]
	if !ok {
		return workload.Workload{}, errors.New("edge has no workload " + key)
	}
	return w, nil
}

func TestAgent(t *testing.T) {
	env := testEnv()
	if r := run(t, env, "agent", `{"agent":1,"workload":"web"}`); r.OK || r.Message != "Agents are not available here" {
		t.Errorf("no agents: %+v", r)
	}
	env.Agents = fakeAgents{
		"web":         {Key: "web", Up: true, Status: "Running, healthy"},
		"service/api": {Key: "service/api", Status: "1 of 3 tasks running"},
	}
	for _, c := range []struct {
		settings string
		ok       bool
		msg      string
	}{
		{`{"agent":1,"workload":" web "}`, true, "Running, healthy"},
		{`{"agent":1,"workload":"service/api"}`, false, "1 of 3 tasks running"},
		{`{"agent":1,"workload":"gone"}`, false, "Edge has no workload gone"},
		{`{"agent":2,"workload":"web"}`, false, "The agent was removed"},
	} {
		if r := run(t, env, "agent", c.settings); r.OK != c.ok || r.Message != c.msg || r.Latency != -1 {
			t.Errorf("%s: %+v", c.settings, r)
		}
	}
	for _, s := range []string{`{"workload":"web"}`, `{"agent":1}`, `{"agent":1,"workload":"  "}`} {
		if _, _, err := Parse(env, "agent", json.RawMessage(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if f := Describe("agent", json.RawMessage(`{"agent":1,"workload":"web"}`)); len(f) != 1 || f[0].Value != "web" {
		t.Errorf("describe %+v", f)
	}
}

func TestAgentTarget(t *testing.T) {
	if a, k, ok := AgentTarget(json.RawMessage(`{"agent":3,"workload":"web"}`)); !ok || a != 3 || k != "web" {
		t.Error(a, k, ok)
	}
	for _, s := range []string{`{"url":"https://example.com"}`, `not json`, `{"agent":"x"}`} {
		if _, _, ok := AgentTarget(json.RawMessage(s)); ok {
			t.Errorf("%s", s)
		}
	}
}
