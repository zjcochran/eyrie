// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/workload"
)

// fakeLister lists ws, or fails with err.
type fakeLister struct {
	ws  []workload.Workload
	err error
}

func (l fakeLister) List(context.Context) ([]workload.Workload, error) { return l.ws, l.err }

func TestDiscoverDocker(t *testing.T) {
	e, admin, a := agentEnv(t)
	var host string
	lister := fakeLister{ws: []workload.Workload{
		{Key: "shop", Name: "shop", Kind: "container", Group: "store", Up: true, Status: "Running, healthy"},
		{Key: "cache", Name: "cache", Kind: "container", Status: "Exited (exit code 1)"},
		{Key: "service/x", Name: "x", Kind: "service"},
	}}
	e.s.dockerLister = func(h string) (workload.Lister, error) {
		host = h
		return lister, nil
	}
	if code := admin.call("POST", "/api/v1/monitors", map[string]any{"name": "Shop", "type": "docker", "settings": map[string]any{"container": "shop"}}, nil); code != http.StatusCreated {
		t.Fatalf("monitor: %d", code)
	}

	// On Eyrie: its Docker engine's containers, without shop, monitored already.
	var got discovered
	if code := admin.call("POST", "/api/v1/discover", map[string]any{"type": "docker"}, &got); code != http.StatusOK {
		t.Fatalf("discover: %d", code)
	}
	if host != check.DefaultDockerHost || !reflect.DeepEqual(got, discovered{Monitored: 1, Found: []found{{Name: "cache",
		Detail: "Exited (exit code 1)", Settings: map[string]any{"container": "cache"}}}}) {
		t.Errorf("found %s %+v", host, got)
	}
	e.s.Env.DockerHost = "tcp://docker.example.com:2375"
	lister.err = errors.New("cannot reach Docker")
	if code, msg := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "docker"}); code != http.StatusUnprocessableEntity ||
		msg != "Eyrie cannot list the containers of tcp://docker.example.com:2375: cannot reach Docker." {
		t.Errorf("unreachable: %d %s", code, msg)
	}
	e.s.dockerLister = func(string) (workload.Lister, error) { return nil, errors.New("docker host: want unix:///path") }
	if code, msg := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "docker"}); code != http.StatusUnprocessableEntity ||
		msg != "Docker host: want unix:///path." {
		t.Errorf("bad host: %d %s", code, msg)
	}

	// On an agent: what it reported; not yet monitored there.
	got = discovered{}
	if code := admin.call("POST", "/api/v1/discover", map[string]any{"type": "docker", "agent_id": a.id}, &got); code != http.StatusOK ||
		len(got.Found) != 1 || got.Found[0].Name != "web" || got.Monitored != 0 {
		t.Errorf("agent: %d %+v", code, got)
	}
}

func TestDiscoverRefused(t *testing.T) {
	e, admin, a := agentEnv(t)
	viewer, _ := e.addUser(admin, "bob", "viewer")
	for _, c := range []struct {
		client *client
		body   map[string]any
		code   int
		msg    string
	}{
		{viewer, map[string]any{"type": "docker"}, http.StatusForbidden, ""},
		{admin, map[string]any{"type": "http"}, http.StatusBadRequest, "Discovery finds Docker containers"},
		{admin, map[string]any{"type": "docker", "agent_id": 999}, http.StatusNotFound, "No such agent."},
		{admin, map[string]any{"type": "kubernetes"}, http.StatusUnprocessableEntity, "Kubernetes workloads are found by an agent."},
		{admin, map[string]any{"type": "proxmox", "agent_id": a.id}, http.StatusUnprocessableEntity, "The agent edge does not list Proxmox VE guests."},
	} {
		if code, msg := c.client.errorOf("POST", "/api/v1/discover", c.body); code != c.code || !strings.HasPrefix(msg, c.msg) {
			t.Errorf("%v: %d %s", c.body, code, msg)
		}
	}

	// An agent that cannot list, or stopped reporting.
	a.post(agentauth.PathReport, agentauth.Report{Version: "v1.2.3", Platform: "docker", Error: "cannot reach Docker"})
	if code, msg := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "docker", "agent_id": a.id}); code != http.StatusUnprocessableEntity ||
		msg != "The agent edge cannot list Docker containers: cannot reach Docker." {
		t.Errorf("error: %d %s", code, msg)
	}
	e.advance(2 * time.Minute)
	if code, msg := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "docker", "agent_id": a.id}); code != http.StatusUnprocessableEntity ||
		msg != "The agent edge is not reporting: start it, then try again." {
		t.Errorf("offline: %d %s", code, msg)
	}
}

func TestDiscoverWorkloads(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	for _, c := range []struct {
		platform string
		w        workload.Workload
		typ      string
		settings map[string]any
	}{
		{"swarm", workload.Workload{Key: "service/shop_web", Name: "shop_web", Kind: "service", Up: true, Status: "2 of 2 tasks running"},
			"docker-swarm", map[string]any{"service": "shop_web"}},
		{"kubernetes", workload.Workload{Key: "shop/deployment/web", Name: "web", Kind: "deployment", Group: "shop"},
			"kubernetes", map[string]any{"namespace": "shop", "kind": "deployment", "name": "web"}},
		{"proxmox", workload.Workload{Key: "qemu/101", Name: "files", Kind: "vm", Group: "pve1"}, "proxmox", map[string]any{"vmid": float64(101)}},
	} {
		a := e.addAgent(admin, c.platform)
		a.post(agentauth.PathReport, agentauth.Report{Version: "v1.2.3", Platform: c.platform, Workloads: []workload.Workload{c.w}})
		var got discovered
		if code := admin.call("POST", "/api/v1/discover", map[string]any{"type": c.typ, "agent_id": a.id}, &got); code != http.StatusOK ||
			len(got.Found) != 1 || !reflect.DeepEqual(got.Found[0].Settings, c.settings) || got.Found[0].Group != c.w.Group {
			t.Errorf("%s: %d %+v", c.typ, code, got)
		}
	}
	if s := workloadSettings("kubernetes", workload.Workload{Key: "web", Name: "web"}); s["name"] != "web" {
		t.Errorf("odd key: %v", s)
	}
}

func TestDiscoverSNMP(t *testing.T) {
	e, admin, a := agentEnv(t)
	device := check.SNMPDevice{Name: "switch1", Description: "Example switch OS 1.0\nbuilt 2027", Interfaces: []check.SNMPInterface{
		{Index: 1, Name: "ge-0/0/1", Alias: "uplink", Admin: "up", Oper: "up"},
		{Index: 2, Name: "ge-0/0/2", Admin: "down", Oper: "down"},
	}}
	var job agentauth.Job
	jobs := make(chan agentauth.Job, 10)
	a.serve(func(j agentauth.Job) agentauth.Result {
		jobs <- j
		if strings.Contains(string(j.Settings), "silent") {
			return agentauth.Result{Message: "no answer: is the community right?"}
		}
		raw, _ := json.Marshal(device)
		return agentauth.Result{OK: true, Found: raw}
	})
	settings := map[string]any{"host": "switch.example.com", "version": "2c", "community": "private"}
	if code := admin.call("POST", "/api/v1/monitors", map[string]any{"name": "Uplink", "type": "snmp", "agent_id": a.id,
		"settings": map[string]any{"host": "switch.example.com", "oid": "1.3.6.1.2.1.2.2.1.8.1", "expected": "1"}}, nil); code != http.StatusCreated {
		t.Fatalf("monitor: %d", code)
	}
	var got discovered
	if code := admin.call("POST", "/api/v1/discover", map[string]any{"type": "snmp", "agent_id": a.id, "settings": settings}, &got); code != http.StatusOK {
		t.Fatalf("discover: %d", code)
	}
	for job = range jobs {
		if job.Discover {
			break
		}
	}
	if job.Type != "snmp" || !strings.Contains(string(job.Settings), `"private"`) || job.TimeoutMS != 60000 {
		t.Errorf("job %+v", job)
	}
	// Without ge-0/0/1, monitored already.
	want := []found{
		{Name: "switch1", Detail: "Example switch OS 1.0", Up: true, Settings: map[string]any{"oid": "1.3.6.1.2.1.1.3.0", "operator": "==", "expected": ""}},
		{Name: "switch1 ge-0/0/2", Detail: "Down, turned off", Settings: map[string]any{"oid": "1.3.6.1.2.1.2.2.1.8.2", "operator": "==", "expected": "1"}},
	}
	if got.Device != "switch1: Example switch OS 1.0" || !reflect.DeepEqual(got.Found, want) || got.Monitored != 1 {
		t.Errorf("found %+v", got)
	}

	settings["host"] = "silent.example.com"
	if code, msg := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "snmp", "agent_id": a.id, "settings": settings}); code != http.StatusUnprocessableEntity ||
		msg != "No answer: is the community right?" {
		t.Errorf("silent: %d %s", code, msg)
	}

	// From Eyrie, whose test network answers nothing.
	if code, msg := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "snmp", "settings": map[string]any{"host": ""}}); code != http.StatusUnprocessableEntity ||
		msg != "The host is required." {
		t.Errorf("no host: %d %s", code, msg)
	}
	e.up.Store(false)
	if code, _ := admin.errorOf("POST", "/api/v1/discover", map[string]any{"type": "snmp", "settings": settings}); code != http.StatusUnprocessableEntity {
		t.Errorf("unreachable: %d", code)
	}
}

func TestClipText(t *testing.T) {
	if s := clipText(strings.Repeat("é", 150)); len(s) != 200 {
		t.Errorf("clipped to %d bytes", len(s))
	}
	if s := clipText("  one\ntwo"); s != "one" {
		t.Errorf("first line %q", s)
	}
}
