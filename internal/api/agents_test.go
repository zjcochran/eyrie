// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/workload"
)

// fakeAgent is an agent talking to the test server.
type fakeAgent struct {
	e   *env
	id  int64
	key agentauth.Key
}

// post sends an agent request, signed unless the agent has no ID yet.
func (a *fakeAgent) post(path string, body any) (int, string) {
	a.e.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, a.e.srv.URL+path, bytes.NewReader(raw))
	if a.id != 0 {
		auth, err := agentauth.SignRequest(a.key, a.id, http.MethodPost, path, raw, a.e.clock())
		if err != nil {
			a.e.t.Fatal(err)
		}
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (a *fakeAgent) report(ws ...workload.Workload) int {
	a.e.t.Helper()
	if ws == nil {
		ws = []workload.Workload{}
	}
	code, _ := a.post(agentauth.PathReport, agentauth.Report{Version: "v1.2.3", Platform: "docker", Hostname: "node1", Workloads: ws})
	return code
}

// addAgent adds and enrolls an agent named name.
func (e *env) addAgent(admin *client, name string) *fakeAgent {
	e.t.Helper()
	var added newAgentToken
	if code := admin.call("POST", "/api/v1/agents", map[string]string{"name": name}, &added); code != http.StatusCreated {
		e.t.Fatalf("add agent: %d", code)
	}
	k, _ := agentauth.NewKey()
	a := &fakeAgent{e: e, key: k}
	code, body := a.post(agentauth.PathEnroll, agentauth.EnrollRequest{Token: added.Token, PublicKey: k.Public().String(), Hostname: "node1", Platform: "docker", Version: "v1.2.3"})
	var resp agentauth.EnrollResponse
	if code != http.StatusOK || json.Unmarshal([]byte(body), &resp) != nil || resp.Name != name {
		e.t.Fatalf("enroll: %d %s", code, body)
	}
	a.id = resp.Agent
	return a
}

// agentEnv is a server whose agent edge reports web, up.
func agentEnv(t *testing.T) (*env, *client, *fakeAgent) {
	t.Helper()
	e := newEnv(t)
	e.s.Env.Agents = e.s.Agents
	admin := e.setup()
	a := e.addAgent(admin, "edge")
	if code := a.report(workload.Workload{Key: "web", Name: "web", Kind: "container", Up: true, Status: "Running, healthy"}); code != http.StatusNoContent {
		t.Fatalf("report: %d", code)
	}
	return e, admin, a
}

func TestAgentRoutes(t *testing.T) {
	e, admin, _ := agentEnv(t)
	viewer, _ := e.addUser(admin, "bob", "viewer")
	if code := viewer.call("GET", "/api/v1/agents", nil, nil); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}
	var added newAgentToken
	if code := admin.call("POST", "/api/v1/agents", map[string]string{"name": "core"}, &added); code != http.StatusCreated || added.Agent.Status != "pending" || added.Agent.TokenExpires == nil {
		t.Fatalf("add: %d %+v", code, added)
	}
	if code, msg := admin.errorOf("POST", "/api/v1/agents", map[string]string{"name": "core"}); code != http.StatusUnprocessableEntity || msg != "There is already an agent named core." {
		t.Errorf("twice: %d %s", code, msg)
	}
	var list []agentView
	admin.call("GET", "/api/v1/agents", nil, &list)
	if len(list) != 2 || list[1].Name != "edge" || list[1].Status != "online" || list[1].Workloads != 1 || list[1].Key == "" || list[1].Hostname != "node1" || list[1].LastSeen == nil {
		t.Fatalf("list %+v", list)
	}
	e.advance(2 * time.Minute)
	admin.call("GET", "/api/v1/agents", nil, &list)
	if list[1].Status != "offline" {
		t.Errorf("silent: %+v", list[1])
	}
}

func TestAgentWorkloads(t *testing.T) {
	_, admin, a := agentEnv(t)
	var added newAgentToken
	admin.call("POST", "/api/v1/agents", map[string]string{"name": "core"}, &added)
	var ws []workload.Workload
	if code := admin.call("GET", fmt.Sprintf("/api/v1/agents/%d/workloads", a.id), nil, &ws); code != http.StatusOK || len(ws) != 1 || ws[0].Key != "web" {
		t.Errorf("workloads %d %+v", code, ws)
	}
	if code := admin.call("GET", "/api/v1/agents/999/workloads", nil, nil); code != http.StatusNotFound {
		t.Errorf("no agent's workloads: %d", code)
	}
	var none []workload.Workload
	admin.call("GET", fmt.Sprintf("/api/v1/agents/%d/workloads", added.Agent.ID), nil, &none)
	if none == nil || len(none) != 0 {
		t.Errorf("a pending agent's workloads %v", none)
	}
}

// A monitor of a workload follows the agent's reports.
func TestAgentMonitor(t *testing.T) {
	_, admin, a := agentEnv(t)
	if code, msg := admin.errorOf("POST", "/api/v1/monitors", map[string]any{"name": "Web", "type": "agent", "settings": map[string]any{"agent": 999, "workload": "web"}}); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "no such agent") {
		t.Errorf("no agent: %d %s", code, msg)
	}
	m := create(t, admin, map[string]any{"name": "Web", "type": "agent", "settings": map[string]any{"agent": a.id, "workload": "web"}})
	eventually(t, "web up", func() bool { return statusOf(admin, m.ID) == "up" })
	a.report(workload.Workload{Key: "web", Name: "web", Kind: "container", Status: "Exited (exit code 1)"})
	eventually(t, "web down at once", func() bool { return statusOf(admin, m.ID) == "down" })
	var mv monitorView
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &mv)
	if mv.Status.Message != "Exited (exit code 1)" {
		t.Errorf("message %q", mv.Status.Message)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/agents/%d", a.id), nil, nil); code != http.StatusNoContent {
		t.Errorf("remove: %d", code)
	}
	eventually(t, "the removed agent's monitor down", func() bool {
		admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &mv)
		return mv.Status.Message == "The agent was removed from Eyrie"
	})
}

func TestAgentRefused(t *testing.T) {
	e, _, a := agentEnv(t)
	// A signature from another key, logged once.
	other := &fakeAgent{e: e, id: a.id}
	other.key, _ = agentauth.NewKey()
	for range 2 {
		if code, _ := other.post(agentauth.PathReport, agentauth.Report{}); code != http.StatusUnauthorized {
			t.Errorf("another key: %d", code)
		}
	}
	if n := strings.Count(e.log.String(), "agent refused"); n != 1 {
		t.Errorf("logged %d refusals", n)
	}
	if code, body := a.post(agentauth.PathReport, map[string]any{"workloads": []map[string]string{{"key": ""}}}); code != http.StatusBadRequest {
		t.Errorf("a bad report: %d %s", code, body)
	}
	if code, _ := a.post(agentauth.PathReport, "a string"); code != http.StatusBadRequest {
		t.Errorf("not a report: %d", code)
	}
	if code, _ := a.post(agentauth.PathReport, strings.Repeat("x", agentauth.MaxBody+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("too big: %d", code)
	}
}

func TestAgentKeyAndToken(t *testing.T) {
	e, admin, a := agentEnv(t)
	k2, _ := agentauth.NewKey()
	ts := e.clock().Unix()
	if code, body := a.post(agentauth.PathRotate, agentauth.RotateRequest{NewKey: k2.Public().String(), TS: ts, Proof: k2.Sign(agentauth.RotationProof(a.id, k2.Public(), ts))}); code != http.StatusNoContent {
		t.Fatalf("rotate: %d %s", code, body)
	}
	if code, _ := a.post(agentauth.PathRotate, agentauth.RotateRequest{NewKey: "x"}); code != http.StatusBadRequest {
		t.Errorf("a bad rotation: %d", code)
	}
	a.key = k2
	if code := a.report(); code != http.StatusNoContent {
		t.Errorf("report with the new key: %d", code)
	}

	// A new token: the agent is refused until it enrolls again.
	var renewed newAgentToken
	if code := admin.call("POST", fmt.Sprintf("/api/v1/agents/%d/token", a.id), nil, &renewed); code != http.StatusOK || renewed.Token == "" || renewed.Agent.Status != "pending" {
		t.Errorf("new token: %d %+v", code, renewed)
	}
	if code := a.report(); code != http.StatusUnauthorized {
		t.Errorf("after a new token: %d", code)
	}
	if code := admin.call("POST", "/api/v1/agents/999/token", nil, nil); code != http.StatusNotFound {
		t.Errorf("token for no agent: %d", code)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/agents/%d", a.id), nil, nil); code != http.StatusNoContent {
		t.Errorf("remove: %d", code)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/agents/%d", a.id), nil, nil); code != http.StatusNotFound {
		t.Errorf("remove twice: %d", code)
	}
	var p auditPage
	admin.call("GET", "/api/v1/audit", nil, &p)
	var actions []string
	for _, x := range p.Entries {
		actions = append(actions, x.Action)
	}
	for _, want := range []string{"agent added", "agent enrolled", "agent key rotated", "agent enrollment token made", "agent removed"} {
		if !strings.Contains(strings.Join(actions, "|"), want) {
			t.Errorf("no %q in the audit log %v", want, actions)
		}
	}
}

func TestAgentEnrollLimit(t *testing.T) {
	e := newEnv(t)
	a := &fakeAgent{e: e}
	if code, _ := a.post(agentauth.PathEnroll, "not an enrollment"); code != http.StatusBadRequest {
		t.Errorf("not JSON: %d", code)
	}
	k, _ := agentauth.NewKey()
	if code, _ := a.post(agentauth.PathEnroll, agentauth.EnrollRequest{Token: "eya_x", PublicKey: "rsa:1"}); code != http.StatusBadRequest {
		t.Errorf("bad key: %d", code)
	}
	for range 10 {
		if code, _ := a.post(agentauth.PathEnroll, agentauth.EnrollRequest{Token: "eya_wrong", PublicKey: k.Public().String()}); code != http.StatusUnauthorized {
			t.Errorf("wrong token: %d", code)
		}
	}
	if code, _ := a.post(agentauth.PathEnroll, agentauth.EnrollRequest{Token: "eya_wrong", PublicKey: k.Public().String()}); code != http.StatusTooManyRequests {
		t.Errorf("the 11th: %d", code)
	}
	if code, _ := a.post(agentauth.PathEnroll, strings.Repeat("x", agentauth.MaxBody+1)); code != http.StatusTooManyRequests {
		t.Errorf("limited: %d", code)
	}
}
