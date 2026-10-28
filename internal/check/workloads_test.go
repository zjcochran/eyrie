// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zjcochran/eyrie/internal/workload"
)

type fakeLister struct {
	ws  []workload.Workload
	err error
}

func (f fakeLister) List(context.Context) ([]workload.Workload, error) { return f.ws, f.err }

func TestWorkloads(t *testing.T) {
	env := testEnv()
	if r := run(t, env, "kubernetes", `{"name":"web"}`); r.OK || r.Message != "A Kubernetes workload monitor is checked on an agent" {
		t.Errorf("in Eyrie: %+v", r)
	}
	env.Workloads = fakeLister{ws: []workload.Workload{
		{Key: "service/api", Up: false, Status: "1 of 3 tasks running"},
		{Key: "default/deployment/web", Up: true, Status: "3 of 3 ready"},
		{Key: "shop/statefulset/db", Up: true, Status: "1 of 1 ready"},
		{Key: "qemu/101", Up: true, Status: "Running"},
		{Key: "lxc/102", Up: false, Status: "Stopped"},
	}}
	for _, c := range []struct {
		typ, settings string
		ok            bool
		msg           string
	}{
		{"docker-swarm", `{"service":" api "}`, false, "1 of 3 tasks running"},
		{"docker-swarm", `{"service":"gone"}`, false, "No service gone"},
		{"kubernetes", `{"name":"web"}`, true, "3 of 3 ready"},
		{"kubernetes", `{"namespace":"shop","kind":"statefulset","name":"db"}`, true, "1 of 1 ready"},
		{"kubernetes", `{"namespace":"shop","name":"db"}`, false, "No deployment db in shop"},
		{"proxmox", `{"vmid":101}`, true, "Running"},
		{"proxmox", `{"vmid":102}`, false, "Stopped"},
		{"proxmox", `{"vmid":103}`, false, "No LXC container or VM 103"},
	} {
		if r := run(t, env, c.typ, c.settings); r.OK != c.ok || r.Message != c.msg || r.Latency != -1 {
			t.Errorf("%s %s: %+v", c.typ, c.settings, r)
		}
	}
	env.Workloads = fakeLister{err: errors.New("cannot reach Docker")}
	if r := run(t, env, "docker-swarm", `{"service":"api"}`); r.OK || r.Message != "Cannot list the workloads: cannot reach Docker" {
		t.Errorf("list error: %+v", r)
	}
	for _, c := range []struct{ typ, settings string }{
		{"docker-swarm", `{"service":""}`},
		{"kubernetes", `{"name":"Web"}`},
		{"kubernetes", `{"namespace":"a b","name":"web"}`},
		{"kubernetes", `{"kind":"pod","name":"web"}`},
		{"proxmox", `{"vmid":99}`},
	} {
		if _, _, err := Parse(env, c.typ, json.RawMessage(c.settings)); err == nil {
			t.Errorf("%s %s accepted", c.typ, c.settings)
		}
	}
	if !AgentOnly("proxmox") || AgentOnly("docker") {
		t.Error("agent only")
	}
	if f := Describe("kubernetes", json.RawMessage(`{"name":"web"}`)); len(f) != 3 || f[2].Value != "web" {
		t.Errorf("describe %+v", f)
	}
	if f := Describe("proxmox", json.RawMessage(`{"vmid":101}`)); len(f) != 1 || f[0].Value != "101" {
		t.Errorf("describe %+v", f)
	}
	if f := Describe("docker-swarm", json.RawMessage(`{"service":"api"}`)); len(f) != 1 || f[0].Value != "api" {
		t.Errorf("describe %+v", f)
	}
}

func TestWorkloadKeys(t *testing.T) {
	for _, c := range []struct {
		typ, settings string
		want          []string
	}{
		{"docker", `{"container":"/web"}`, []string{"web"}},
		{"docker-swarm", `{"service":"api"}`, []string{"service/api"}},
		{"kubernetes", `{"name":"web"}`, []string{"default/deployment/web"}},
		{"proxmox", `{"vmid":101}`, []string{"lxc/101", "qemu/101"}},
		{"http", `{"url":"https://example.com"}`, nil},
		{"docker", `not json`, nil},
		{"docker", `{"container":""}`, nil},
	} {
		if got := WorkloadKeys(c.typ, json.RawMessage(c.settings)); !slices.Equal(got, c.want) {
			t.Errorf("%s %s: %v", c.typ, c.settings, got)
		}
	}
}

func TestOpened(t *testing.T) {
	env := testEnv()
	env.Secrets = sealer{}
	saved, err := Prepare(env, "mqtt", json.RawMessage(`{"host":"mq.example.com","topic":"shop/#","username":"alice","password":"pw"}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "sealed:pw") {
		t.Fatalf("saved %s", saved)
	}
	out, err := Opened(env, "mqtt", saved)
	if err != nil {
		t.Fatal(err)
	}
	var s struct{ Password string }
	if json.Unmarshal(out, &s) != nil || s.Password != "pw" {
		t.Errorf("opened %s", out)
	}
	if _, err := Opened(env, "telepathy", nil); err == nil {
		t.Error("unknown type")
	}
	if _, err := Opened(env, "mqtt", json.RawMessage(`{"host":""}`)); err == nil {
		t.Error("bad settings")
	}
}
