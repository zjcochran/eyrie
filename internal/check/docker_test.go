// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocker(t *testing.T) {
	states := map[string]string{
		"shop":       `{"State":{"Status":"running","Running":true}}`,
		"db":         `{"State":{"Status":"running","Running":true,"Health":{"Status":"healthy"}}}`,
		"cache":      `{"State":{"Status":"running","Running":true,"Health":{"Status":"starting"}}}`,
		"queue":      `{"State":{"Status":"running","Running":true,"Health":{"Status":"unhealthy"}}}`,
		"worker":     `{"State":{"Status":"exited","Running":false,"ExitCode":137}}`,
		"paused":     `{"State":{"Status":"paused","Running":true}}`,
		"restarting": `{"State":{"Status":"restarting","Running":true}}`,
		"garbage":    `not json`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
		if name == "broken" {
			http.Error(w, "oops", http.StatusInternalServerError)
			return
		}
		s, ok := states[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(s))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	env := testEnv()
	var dialed string
	env.Dial = func(ctx context.Context, network, a string) (net.Conn, error) {
		dialed = network + " " + a
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	for _, c := range []struct {
		name string
		ok   bool
		msg  string
	}{
		{"shop", true, "Running"},
		{"/db", true, "Running, healthy"},
		{"cache", true, "Running, health check starting"},
		{"queue", false, "Running, unhealthy"},
		{"worker", false, "Exited (exit code 137)"},
		{"paused", false, "Paused"},
		{"restarting", false, "Restarting"},
		{"garbage", false, "Docker's answer is not JSON"},
		{"broken", false, "Docker answered 500 Internal Server Error"},
		{"gone", false, "No container gone"},
	} {
		if r := run(t, env, "docker", `{"container":"`+c.name+`"}`); r.OK != c.ok || r.Message != c.msg {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	if dialed != "unix /var/run/docker.sock" {
		t.Errorf("dialed %s", dialed)
	}
	if r := run(t, env, "docker", `{"container":"shop","docker_host":"tcp://docker.example.com:2375"}`); !r.OK || dialed != "tcp docker.example.com:2375" {
		t.Errorf("tcp: %+v %s", r, dialed)
	}
	if r := run(t, env, "docker", `{"container":"shop","docker_host":"https://docker.example.com:2376"}`); r.OK {
		t.Errorf("https to a plain server: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("no socket") }
	if r := run(t, env, "docker", `{"container":"shop"}`); r.OK || r.Message != "Cannot reach Docker: no socket" {
		t.Errorf("no socket: %+v", r)
	}
	for _, s := range []string{`{"container":""}`, `{"container":"a b"}`, `{"container":"shop","docker_host":"ftp://x"}`,
		`{"container":"shop","docker_host":"unix://"}`, `{"container":"shop","docker_host":"%zz"}`} {
		if _, _, err := Parse(env, "docker", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if f := (&Docker{Container: "shop", DockerHost: "tcp://docker.example.com:2375"}).describe(); f[1].Value != "tcp://docker.example.com:2375" {
		t.Errorf("describe %v", f)
	}
}
