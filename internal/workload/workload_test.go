// SPDX-License-Identifier: AGPL-3.0-or-later

package workload

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// unixServer serves h on a Unix socket in a temporary folder.
func unixServer(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wl") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: h, ReadHeaderTimeout: time.Second}}
	srv.Start()
	t.Cleanup(srv.Close)
	return "unix://" + sock
}

const containersJSON = `[
 {"Id":"aaaaaaaaaaaaaaaa","Names":["/shop-web-1"],"State":"running","Status":"Up 2 hours (healthy)","Labels":{"com.docker.compose.project":"shop"}},
 {"Id":"bbbbbbbbbbbbbbbb","Names":["/shop-db-1"],"State":"running","Status":"Up 3 seconds (health: starting)","Labels":{"com.docker.compose.project":"shop"}},
 {"Id":"cccccccccccccccc","Names":["/queue"],"State":"running","Status":"Up 1 hour (unhealthy)","Labels":{}},
 {"Id":"dddddddddddddddd","Names":["/cron"],"State":"running","Status":"Up 1 hour","Labels":{}},
 {"Id":"eeeeeeeeeeeeeeee","Names":["/worker"],"State":"exited","Status":"Exited (137) 5 minutes ago","Labels":{}},
 {"Id":"ffffffffffffffff","Names":[],"State":"paused","Status":"Up 1 hour (Paused)","Labels":{}},
 {"Id":"gggggggggggggggg","Names":["/web.1.x"],"State":"running","Status":"Up 1 hour","Labels":{"com.docker.swarm.service.id":"s1"}}
]`

func dockerHandler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/json":
			if r.URL.Query().Get("all") != "1" {
				t.Errorf("not all containers: %s", r.URL)
			}
			fmt.Fprint(w, containersJSON)
		case "/services":
			fmt.Fprint(w, `[
			 {"Spec":{"Name":"edge_proxy","Labels":{"com.docker.stack.namespace":"edge"}},"ServiceStatus":{"RunningTasks":2,"DesiredTasks":2}},
			 {"Spec":{"Name":"edge_api","Labels":{"com.docker.stack.namespace":"edge"}},"ServiceStatus":{"RunningTasks":1,"DesiredTasks":3}},
			 {"Spec":{"Name":"idle","Labels":{}},"ServiceStatus":{"RunningTasks":0,"DesiredTasks":0}},
			 {"Spec":{"Name":"old","Labels":{}}}]`)
		case "/info":
			fmt.Fprint(w, `{"Swarm":{"LocalNodeState":"active","ControlAvailable":true}}`)
		case "/events":
			fmt.Fprintln(w, `{"Action":"start"}`)
			fmt.Fprintln(w, `not json`)
			fmt.Fprintln(w, `{"Action":"exec_start: sh"}`)
			fmt.Fprintln(w, `{"Action":"health_status: unhealthy"}`)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestDockerList(t *testing.T) {
	d, err := NewDocker(unixServer(t, dockerHandler(t)))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := d.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range ws {
		got[w.Key] = fmt.Sprintf("%v %s %s %s", w.Up, w.Status, w.Kind, w.Group)
	}
	want := map[string]string{
		"shop-web-1":   "true Running, healthy container shop",
		"shop-db-1":    "true Running, health check starting container shop",
		"queue":        "false Running, unhealthy container ",
		"cron":         "true Running container ",
		"worker":       "false Exited (exit code 137) container ",
		"ffffffffffff": "false Paused container ",
		"web.1.x":      "true Running container ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if ws[len(ws)-1].Group != "shop" || ws[0].Group != "" {
		t.Errorf("not sorted by group: %+v", ws)
	}
}

func TestContainerStatus(t *testing.T) {
	for _, c := range []struct {
		state, text, want string
		up                bool
	}{
		{"restarting", "Restarting (1) 2 seconds ago", "Restarting", false},
		{"created", "Created", "Created", false},
		{"exited", "Exited", "Exited", false},
		{"dead", "Dead", "Dead", false},
		{"", "", "Unknown", false},
	} {
		up, msg := containerStatus(c.state, c.text)
		if up != c.up || msg != c.want {
			t.Errorf("%s %q: %v %s", c.state, c.text, up, msg)
		}
	}
}

func TestDockerEvents(t *testing.T) {
	d, err := NewDocker(unixServer(t, dockerHandler(t)))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = d.Events(context.Background(), func() { n++ })
	if n != 2 || err == nil || !strings.Contains(err.Error(), "ended") {
		t.Errorf("%d changes, %v", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Events(ctx, func() {}); err == nil {
		t.Error("a cancelled context streamed")
	}
}

func TestSwarm(t *testing.T) {
	d, err := NewDocker(unixServer(t, dockerHandler(t)))
	if err != nil {
		t.Fatal(err)
	}
	s := Swarm{d}
	if err := s.CheckManager(context.Background()); err != nil {
		t.Fatal(err)
	}
	ws, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range ws {
		got[w.Key] = fmt.Sprintf("%v %s", w.Up, w.Status)
	}
	for k, v := range map[string]string{
		"service/edge_proxy": "true 2 of 2 tasks running",
		"service/edge_api":   "false 1 of 3 tasks running",
		"service/idle":       "false Scaled to 0",
		"service/old":        "false Unknown: Docker gave no task counts",
		"shop-web-1":         "true Running, healthy",
	} {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["web.1.x"]; ok {
		t.Error("a task container is listed")
	}

	worker := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"Swarm":{"LocalNodeState":"active","ControlAvailable":false}}`)
	}))
	wd, _ := NewDocker(worker)
	if err := (Swarm{wd}).CheckManager(context.Background()); err == nil || !strings.Contains(err.Error(), "manager") {
		t.Errorf("a worker node: %v", err)
	}
}

func TestDockerErrors(t *testing.T) {
	for _, h := range []string{"ftp://x", "unix://", "tcp://", "::"} {
		if _, err := NewDocker(h); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/json":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"engine on fire"}`)
		case "/services":
			http.Error(w, "This node is not a swarm manager.", http.StatusServiceUnavailable)
		case "/info":
			fmt.Fprint(w, `not json`)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()
	d, err := NewDocker("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := d.List(ctx); err == nil || err.Error() != "Docker answered 500 Internal Server Error: engine on fire" {
		t.Errorf("list: %v", err)
	}
	if _, err := (Swarm{d}).List(ctx); err == nil || !strings.Contains(err.Error(), "not a swarm manager") {
		t.Errorf("services: %v", err)
	}
	if err := (Swarm{d}).CheckManager(ctx); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("info: %v", err)
	}
	if err := d.Events(ctx, func() {}); err == nil || err.Error() != "Docker answered 418 I'm a teapot" {
		t.Errorf("events: %v", err)
	}
	d.http.Transport = failing{}
	if _, err := d.List(ctx); err == nil || !strings.Contains(err.Error(), "cannot reach Docker") {
		t.Errorf("closed: %v", err)
	}
	if err := d.Events(ctx, func() {}); err == nil {
		t.Error("events from a closed server")
	}
	if err := d.get(ctx, "/\x7f", nil); err == nil {
		t.Error("a bad path")
	}
}

func kubeServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, []byte) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return srv, ca
}

func TestKubernetes(t *testing.T) {
	var paths []string
	srv, ca := kubeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		paths = append(paths, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			fmt.Fprint(w, `{"items":[
			 {"metadata":{"name":"web","namespace":"shop"},"spec":{"replicas":3},"status":{"readyReplicas":3}},
			 {"metadata":{"name":"api","namespace":"shop"},"spec":{"replicas":3},"status":{"readyReplicas":1}},
			 {"metadata":{"name":"off","namespace":"shop"},"spec":{"replicas":0},"status":{}},
			 {"metadata":{"name":"one","namespace":"shop"},"spec":{},"status":{"readyReplicas":1}}]}`)
		case strings.HasSuffix(r.URL.Path, "/statefulsets"):
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"db","namespace":"shop"},"spec":{"replicas":1},"status":{}}]}`)
		default:
			fmt.Fprint(w, `{"items":[
			 {"metadata":{"name":"logs","namespace":"ops"},"status":{"desiredNumberScheduled":4,"numberReady":4}},
			 {"metadata":{"name":"gpu","namespace":"ops"},"status":{"desiredNumberScheduled":0}}]}`)
		}
	})
	k, err := NewKubernetes(srv.URL+"/", ca, func() (string, error) { return "tok", nil })
	if err != nil {
		t.Fatal(err)
	}
	ws, err := k.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range ws {
		got[w.Key] = fmt.Sprintf("%v %s %s %s %s", w.Up, w.Status, w.Kind, w.Name, w.Group)
	}
	want := map[string]string{
		"shop/deployment/web": "true 3 of 3 ready deployment web shop",
		"shop/deployment/api": "false 1 of 3 ready deployment api shop",
		"shop/deployment/off": "false Scaled to 0 deployment off shop",
		"shop/deployment/one": "true 1 of 1 ready deployment one shop",
		"shop/statefulset/db": "false 0 of 1 ready statefulset db shop",
		"ops/daemonset/logs":  "true 4 of 4 ready daemonset logs ops",
		"ops/daemonset/gpu":   "false Scheduled on no nodes daemonset gpu ops",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if paths[0] != "/apis/apps/v1/deployments" {
		t.Errorf("paths %v", paths)
	}

	paths = nil
	k.Namespaces = []string{"shop", "ops"}
	if _, err := k.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 6 || paths[0] != "/apis/apps/v1/namespaces/shop/deployments" || paths[5] != "/apis/apps/v1/namespaces/ops/daemonsets" {
		t.Errorf("namespaced paths %v", paths)
	}
}

func TestKubernetesErrors(t *testing.T) {
	if _, err := NewKubernetes("https://k.example.com", []byte("no"), nil); err == nil {
		t.Error("a bad CA was accepted")
	}
	srv, ca := kubeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/namespaces/broken/") {
			fmt.Fprint(w, `{`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"kind":"Status","message":"deployments.apps is forbidden"}`)
	})
	k, _ := NewKubernetes(srv.URL, ca, func() (string, error) { return "tok", nil })
	ctx := context.Background()
	if _, err := k.List(ctx); err == nil || !strings.Contains(err.Error(), "403 Forbidden: deployments.apps is forbidden") {
		t.Errorf("forbidden: %v", err)
	}
	k.Namespaces = []string{"broken"}
	if _, err := k.List(ctx); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("bad JSON: %v", err)
	}
	k.token = func() (string, error) { return "", errors.New("no token file") }
	if _, err := k.List(ctx); err == nil || !strings.Contains(err.Error(), "no token file") {
		t.Errorf("no token: %v", err)
	}
	k.token = func() (string, error) { return "tok", nil }
	k.http.Transport = failing{}
	if _, err := k.List(ctx); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("closed: %v", err)
	}
	k.base = "\x7f"
	if _, err := k.List(ctx); err == nil {
		t.Error("a bad URL")
	}
}

func TestInCluster(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if _, err := InCluster(getenv, ""); err == nil || !strings.Contains(err.Error(), "not running in a Kubernetes pod") {
		t.Errorf("outside a pod: %v", err)
	}
	env["KUBERNETES_SERVICE_HOST"], env["KUBERNETES_SERVICE_PORT"] = "192.0.2.1", "443"
	dir := t.TempDir()
	if _, err := InCluster(getenv, dir); err == nil {
		t.Error("no CA file")
	}
	srv, ca := kubeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rotated" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"items":[]}`)
	})
	os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0o600)
	os.WriteFile(filepath.Join(dir, "token"), []byte("rotated\n"), 0o600)
	k, err := InCluster(getenv, dir)
	if err != nil {
		t.Fatal(err)
	}
	if k.base != "https://192.0.2.1:443" {
		t.Errorf("base %s", k.base)
	}
	k.base = srv.URL
	if ws, err := k.List(context.Background()); err != nil || len(ws) != 0 {
		t.Errorf("%v %v", ws, err)
	}
	if _, err := InCluster(getenv, "/nonexistent"); err == nil {
		t.Error("the default folder outside a pod")
	}
}

func TestProxmox(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=eyrie@pve!agent=s3cret" || r.URL.Path != "/api2/json/cluster/resources" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":[
		 {"type":"lxc","node":"pve1","vmid":101,"name":"dns","status":"running"},
		 {"type":"qemu","node":"pve2","vmid":200,"name":"","status":"stopped"},
		 {"type":"qemu","node":"pve2","vmid":900,"name":"tpl","status":"stopped","template":1},
		 {"type":"lxc","node":"pve1","vmid":102,"name":"odd","status":""},
		 {"type":"storage","node":"pve1"}]}`)
	}))
	defer srv.Close()
	fp := Fingerprint(srv.Certificate().Raw)
	p, err := NewProxmox(srv.URL, "eyrie@pve!agent", "s3cret", strings.ToLower(strings.ReplaceAll(fp, ":", "")))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, w := range ws {
		got = append(got, fmt.Sprintf("%s %s %s %s %v %s", w.Key, w.Name, w.Kind, w.Group, w.Up, w.Status))
	}
	want := []string{"lxc/101 dns lxc pve1 true Running", "lxc/102 odd lxc pve1 false Unknown", "qemu/200 200 vm pve2 false Stopped"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}

	ctx := context.Background()
	p, _ = NewProxmox(srv.URL, "eyrie@pve!agent", "s3cret", "")
	if _, err := p.List(ctx); err == nil || !strings.Contains(err.Error(), "PROXMOX_FINGERPRINT is not set; the server's certificate has "+fp) {
		t.Errorf("no fingerprint: %v", err)
	}
	p, _ = NewProxmox(srv.URL, "eyrie@pve!agent", "s3cret", "AA:BB")
	if _, err := p.List(ctx); err == nil || !strings.Contains(err.Error(), "not PROXMOX_FINGERPRINT") {
		t.Errorf("wrong fingerprint: %v", err)
	}
	p, _ = NewProxmox(srv.URL, "eyrie@pve!other", "s3cret", fp)
	if _, err := p.List(ctx); err == nil || err.Error() != "Proxmox answered 401 Unauthorized" {
		t.Errorf("wrong token: %v", err)
	}
	for _, c := range [][3]string{{"http://pve.example.com:8006", "a@b!c", "s"}, {"https://pve.example.com:8006", "abc", "s"}, {"https://pve.example.com", "a@b!c", ""}} {
		if _, err := NewProxmox(c[0], c[1], c[2], fp); err == nil {
			t.Errorf("%v accepted", c)
		}
	}

	bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>") }))
	defer bad.Close()
	p, _ = NewProxmox(bad.URL, "eyrie@pve!agent", "s3cret", Fingerprint(bad.Certificate().Raw))
	if _, err := p.List(ctx); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("not JSON: %v", err)
	}
	p.base = "\x7f"
	if _, err := p.List(ctx); err == nil {
		t.Error("a bad URL")
	}
}

// failing is a transport whose every request fails, as a host that is
// down would.
type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestReady(t *testing.T) {
	if up, s := ready(2, 2, "ready"); !up || s != "2 of 2 ready" {
		t.Error(up, s)
	}
	if sentence("") != "" {
		t.Error("sentence")
	}
}
