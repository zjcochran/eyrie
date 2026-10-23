// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeRun answers programs from a table of "name args" → output and error.
func fakeRun(t *testing.T, answers map[string]struct {
	out string
	err error
}) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmd := strings.Join(append([]string{name}, args...), " ")
		for k, a := range answers {
			if strings.HasPrefix(cmd, k) {
				return []byte(a.out), a.err
			}
		}
		t.Errorf("unexpected %s", cmd)
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
}

type answer = struct {
	out string
	err error
}

var errExit = errors.New("exit status 1")

func TestTailscale(t *testing.T) {
	env := testEnv()
	env.Run = fakeRun(t, map[string]answer{
		"tailscale ping --c 1 --timeout": {"pong from nas (100.64.0.2) via 192.0.2.10:41641 in 12.5ms\n", nil},
	})
	r := run(t, env, "tailscale-ping", `{"host":"nas"}`)
	if !r.OK || r.Latency != 12500*time.Microsecond || !strings.HasPrefix(r.Message, "Pong from nas") {
		t.Errorf("pong: %+v", r)
	}
	env.Run = fakeRun(t, map[string]answer{"tailscale": {"ping \"nas\" timed out\nno reply\n", errExit}})
	if r := run(t, env, "tailscale-ping", `{"host":"nas"}`); r.OK || r.Message != "No reply" {
		t.Errorf("no reply: %+v", r)
	}
	env.Run = fakeRun(t, map[string]answer{"tailscale": {"", &exec.Error{Name: "tailscale", Err: exec.ErrNotFound}}})
	if r := run(t, env, "tailscale-ping", `{"host":"nas"}`); r.OK || r.Message != "tailscale is not installed where Eyrie runs" {
		t.Errorf("missing: %+v", r)
	}
	env.Run = fakeRun(t, map[string]answer{"tailscale": {"", errExit}})
	if r := run(t, env, "tailscale-ping", `{"host":"nas"}`); r.OK || r.Message != "Exit status 1" {
		t.Errorf("failed: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, c, _ := Parse(env, "tailscale-ping", []byte(`{"host":"nas"}`))
	if r := c.Check(ctx); r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{"host":""}`, `{"host":"-v"}`} {
		if _, _, err := Parse(env, "tailscale-ping", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestService(t *testing.T) {
	env := testEnv()
	env.OS = "linux"
	env.Run = fakeRun(t, map[string]answer{
		"systemctl is-active -- nginx": {"active\n", nil},
		"systemctl is-active -- cron":  {"inactive\n", errExit},
		"systemctl is-active -- gone":  {"", errExit},
	})
	if r := run(t, env, "system-service", `{"service":"nginx"}`); !r.OK || r.Message != "Active" {
		t.Errorf("active: %+v", r)
	}
	if r := run(t, env, "system-service", `{"service":"cron"}`); r.OK || r.Message != "Inactive" {
		t.Errorf("inactive: %+v", r)
	}
	if r := run(t, env, "system-service", `{"service":"gone"}`); r.OK || r.Message != "Exit status 1" {
		t.Errorf("gone: %+v", r)
	}
	env.OS = "windows"
	env.Run = fakeRun(t, map[string]answer{
		"sc.exe query Spooler": {"SERVICE_NAME: Spooler\r\n        TYPE               : 110  WIN32_OWN_PROCESS\r\n        STATE              : 4  RUNNING\r\n", nil},
		"sc.exe query W32Time": {"SERVICE_NAME: W32Time\r\n        STATE              : 1  STOPPED\r\n", nil},
		"sc.exe query Odd":     {"SERVICE_NAME: Odd\r\n", nil},
		"sc.exe query Gone":    {"[SC] EnumQueryServicesStatus:OpenService FAILED 1060:\r\n", errExit},
	})
	if r := run(t, env, "system-service", `{"service":"Spooler"}`); !r.OK || r.Message != "Running" {
		t.Errorf("running: %+v", r)
	}
	if r := run(t, env, "system-service", `{"service":"W32Time"}`); r.OK || r.Message != "Stopped" {
		t.Errorf("stopped: %+v", r)
	}
	if r := run(t, env, "system-service", `{"service":"Odd"}`); r.OK {
		t.Errorf("no state: %+v", r)
	}
	if r := run(t, env, "system-service", `{"service":"Gone"}`); r.OK || !strings.Contains(r.Message, "1060") {
		t.Errorf("gone: %+v", r)
	}
	env.OS = "darwin"
	if r := run(t, env, "system-service", `{"service":"nginx"}`); r.OK || !strings.Contains(r.Message, "not darwin") {
		t.Errorf("darwin: %+v", r)
	}
	for _, s := range []string{`{"service":""}`, `{"service":"-x"}`, `{"service":"a b"}`} {
		if _, _, err := Parse(env, "system-service", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestPM2(t *testing.T) {
	env := testEnv()
	list := `[PM2] Spawning PM2 daemon
[{"name":"api","pm_id":0,"pm2_env":{"status":"online","restart_time":2},"monit":{"memory":83886080,"cpu":12}},
 {"name":"worker","pm_id":1,"pm2_env":{"status":"errored","restart_time":15},"monit":{"memory":0,"cpu":0}}]`
	env.Run = fakeRun(t, map[string]answer{"pm2 jlist": {list, nil}})
	if r := run(t, env, "pm2", `{"process":"api"}`); !r.OK || r.Message != "Online, 2 restarts, 12% CPU, 80 MB" {
		t.Errorf("online: %+v", r)
	}
	if r := run(t, env, "pm2", `{"process":"1"}`); r.OK || r.Message != "Errored, 15 restarts" {
		t.Errorf("errored: %+v", r)
	}
	if r := run(t, env, "pm2", `{"process":"mailer"}`); r.OK || r.Message != "PM2 has no process mailer" {
		t.Errorf("missing: %+v", r)
	}
	env.Run = fakeRun(t, map[string]answer{"pm2 jlist": {"oops", nil}})
	if r := run(t, env, "pm2", `{"process":"api"}`); r.OK || r.Message != "pm2 jlist did not answer JSON" {
		t.Errorf("garbage: %+v", r)
	}
	env.Run = fakeRun(t, map[string]answer{"pm2 jlist": {"", &exec.Error{Name: "pm2", Err: exec.ErrNotFound}}})
	if r := run(t, env, "pm2", `{"process":"api"}`); r.OK || r.Message != "pm2 is not installed where Eyrie runs" {
		t.Errorf("not installed: %+v", r)
	}
	if _, _, err := Parse(env, "pm2", []byte(`{"process":" "}`)); err == nil {
		t.Error("empty process")
	}
	for _, typ := range []string{"pm2", "system-service", "tailscale-ping"} {
		if f := Describe(typ, []byte(`{"process":"api","service":"nginx","host":"nas"}`)); len(f) != 1 {
			t.Errorf("%s describe %v", typ, f)
		}
	}
}

func TestRunProgram(t *testing.T) {
	out, err := runProgram(context.Background(), "eyrie-no-such-program")
	if err == nil || len(out) != 0 {
		t.Errorf("%s %v", out, err)
	}
}
