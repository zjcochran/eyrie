// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Types that run a program on Eyrie's host.

// runError says why a program failed: its output's last line, or the
// error.
func runError(ctx context.Context, name string, out []byte, err error) string {
	if ctx.Err() != nil {
		return "Timed out"
	}
	if strings.Contains(err.Error(), "executable file not found") {
		return fmt.Sprintf("%s is not installed where Eyrie runs", name)
	}
	if line := lastLine(out); line != "" {
		return sentence(line)
	}
	return sentence(err.Error())
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Tailscale is the settings of the tailscale-ping type: a node of the
// tailnet must answer `tailscale ping`.
type Tailscale struct {
	Host string `json:"host"`
}

func (t *Tailscale) normalize() error {
	h, err := host(t.Host)
	if err != nil {
		return err
	}
	if strings.HasPrefix(h, "-") {
		return fmt.Errorf("%q is not a host name or IP address", h)
	}
	t.Host = h
	return nil
}

func (t *Tailscale) checker(env *Env) Checker { return &tailscaleChecker{t: t, env: env} }

func (t *Tailscale) describe() []Fact { return []Fact{{"Tailscale node", t.Host}} }

type tailscaleChecker struct {
	t   *Tailscale
	env *Env
}

var pongTime = regexp.MustCompile(`\bin ([0-9.]+)ms\b`)

func (c *tailscaleChecker) Check(ctx context.Context) Result {
	limit := time.Until(deadline(ctx)) - 200*time.Millisecond
	if limit < time.Second {
		limit = time.Second
	}
	out, err := c.env.Run(ctx, "tailscale", "ping", "--c", "1", "--timeout", limit.Round(time.Millisecond).String(), c.t.Host)
	if err != nil {
		return fail("%s", runError(ctx, "tailscale", out, err))
	}
	line := lastLine(out)
	r := Result{OK: strings.HasPrefix(line, "pong"), Message: sentence(line), Latency: -1}
	if m := pongTime.FindStringSubmatch(line); m != nil {
		ms, _ := strconv.ParseFloat(m[1], 64)
		r.Latency = time.Duration(ms * float64(time.Millisecond))
	}
	return r
}

// Service is the settings of the system-service type: a service of
// Eyrie's host must be running (systemd on Linux, the service manager on
// Windows).
type Service struct {
	Service string `json:"service"`
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9@._:\\-]{0,254}$`)

func (s *Service) normalize() error {
	s.Service = strings.TrimSpace(s.Service)
	if !serviceName.MatchString(s.Service) {
		return errors.New("the service must be a service's name, such as nginx or sshd.service")
	}
	return nil
}

func (s *Service) checker(env *Env) Checker { return &serviceChecker{s: s, env: env} }

func (s *Service) describe() []Fact { return []Fact{{"Service", s.Service}} }

type serviceChecker struct {
	s   *Service
	env *Env
}

func (c *serviceChecker) Check(ctx context.Context) Result {
	start := time.Now()
	switch c.env.OS {
	case "linux":
		out, err := c.env.Run(ctx, "systemctl", "is-active", "--", c.s.Service)
		state := lastLine(out)
		if err != nil && state == "" {
			return fail("%s", runError(ctx, "systemctl", out, err))
		}
		return Result{OK: err == nil && state == "active", Message: sentence(state), Latency: time.Since(start)}
	case "windows":
		out, err := c.env.Run(ctx, "sc.exe", "query", c.s.Service)
		if err != nil {
			return fail("%s", runError(ctx, "sc.exe", out, err))
		}
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "STATE" {
				fields := strings.Fields(v)
				state := fields[len(fields)-1]
				return Result{OK: state == "RUNNING", Message: sentence(strings.ToLower(state)), Latency: time.Since(start)}
			}
		}
		return fail("sc.exe did not say the service's state")
	}
	return fail("System services can be checked only on Linux and Windows, not %s", c.env.OS)
}

// PM2 is the settings of the pm2 type: a process that PM2 manages on
// Eyrie's host must be online.
type PM2 struct {
	Process string `json:"process"`
}

func (p *PM2) normalize() error {
	p.Process = strings.TrimSpace(p.Process)
	if p.Process == "" || len(p.Process) > 255 {
		return errors.New("the process must be a PM2 process's name or id")
	}
	return nil
}

func (p *PM2) checker(env *Env) Checker { return &pm2Checker{p: p, env: env} }

func (p *PM2) describe() []Fact { return []Fact{{"PM2 process", p.Process}} }

type pm2Checker struct {
	p   *PM2
	env *Env
}

func (c *pm2Checker) Check(ctx context.Context) Result {
	start := time.Now()
	out, err := c.env.Run(ctx, "pm2", "jlist")
	if err != nil {
		return fail("%s", runError(ctx, "pm2", out, err))
	}
	// pm2 may print notices, such as "[PM2] Spawning PM2 daemon", before
	// the list.
	for _, prefix := range []string{"[{", "[]"} {
		if i := strings.Index(string(out), prefix); i >= 0 {
			out = out[i:]
			break
		}
	}
	var procs []struct {
		Name string `json:"name"`
		ID   *int   `json:"pm_id"`
		Env  struct {
			Status   string `json:"status"`
			Restarts int    `json:"restart_time"`
		} `json:"pm2_env"`
		Monit struct {
			Memory int64   `json:"memory"`
			CPU    float64 `json:"cpu"`
		} `json:"monit"`
	}
	if err := json.Unmarshal(out, &procs); err != nil {
		return fail("pm2 jlist did not answer JSON")
	}
	for _, p := range procs {
		if p.Name != c.p.Process && (p.ID == nil || strconv.Itoa(*p.ID) != c.p.Process) {
			continue
		}
		msg := fmt.Sprintf("%s, %d restarts", sentence(p.Env.Status), p.Env.Restarts)
		if p.Env.Status == "online" {
			msg += fmt.Sprintf(", %.0f%% CPU, %d MB", p.Monit.CPU, p.Monit.Memory>>20)
		}
		return Result{OK: p.Env.Status == "online", Message: msg, Latency: time.Since(start)}
	}
	return fail("PM2 has no process %s", c.p.Process)
}
