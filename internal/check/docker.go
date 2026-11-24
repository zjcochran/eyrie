// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Docker is the settings of the docker type: a container must be running,
// and healthy if it has a health check.
type Docker struct {
	Container string `json:"container"`
	// DockerHost is the Docker daemon: unix:///path (the default,
	// unix:///var/run/docker.sock), tcp://host:port or https://host:port.
	DockerHost string `json:"docker_host"`
	IgnoreTLS  bool   `json:"ignore_tls"`
}

// DefaultDockerHost is the Docker host of docker monitors that name none.
const DefaultDockerHost = "unix:///var/run/docker.sock"

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)

func (d *Docker) normalize() error {
	d.Container = strings.TrimPrefix(strings.TrimSpace(d.Container), "/")
	if !containerName.MatchString(d.Container) {
		return errors.New("the container must be a container's name or ID")
	}
	d.DockerHost = strings.TrimSpace(d.DockerHost)
	if d.DockerHost == "" {
		d.DockerHost = DefaultDockerHost
	}
	u, err := url.Parse(d.DockerHost)
	switch {
	case err != nil:
		return errors.New("the Docker host must be unix:///path, tcp://host:port or https://host:port")
	case u.Scheme == "unix" && u.Path != "":
	case (u.Scheme == "tcp" || u.Scheme == "https") && u.Host != "":
	default:
		return errors.New("the Docker host must be unix:///path, tcp://host:port or https://host:port")
	}
	return nil
}

func (d *Docker) checker(env *Env) Checker { return &dockerChecker{d: d, env: env} }

func (d *Docker) describe() []Fact {
	return []Fact{{"Container", d.Container}, {"Docker host", publicURL(d.DockerHost)}}
}

type dockerChecker struct {
	d   *Docker
	env *Env
}

// client is an HTTP client for the Docker daemon, and its base URL.
func (c *dockerChecker) client() (*http.Client, string) {
	host := c.d.DockerHost
	if host == DefaultDockerHost && c.env.DockerHost != "" {
		host = c.env.DockerHost
	}
	u, _ := url.Parse(host)
	t := &http.Transport{DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: c.d.IgnoreTLS}} //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
	base := "http://" + u.Host
	switch u.Scheme {
	case "unix":
		t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return c.env.Dial(ctx, "unix", u.Path) }
		base = "http://docker"
	case "https":
		t.DialContext = c.env.Dial
		base = "https://" + u.Host
	default:
		t.DialContext = c.env.Dial
	}
	return &http.Client{Transport: t}, base
}

func (c *dockerChecker) Check(ctx context.Context) Result {
	client, base := c.client()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/containers/"+url.PathEscape(c.d.Container)+"/json", nil)
	if err != nil {
		return failErr(err)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fail("Cannot reach Docker: %s", requestError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	r := Result{Latency: time.Since(start)}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		r.Message = "No container " + c.d.Container
		return r
	case resp.StatusCode != http.StatusOK:
		r.Message = "Docker answered " + resp.Status
		return r
	}
	var info struct {
		Name         string
		RestartCount int
		Config       struct{ Image string }
		State        struct {
			Status     string
			Running    bool
			OOMKilled  bool
			ExitCode   int
			StartedAt  time.Time
			FinishedAt time.Time
			Health     *dockerHealth
		}
	}
	if err := json.Unmarshal(body, &info); err != nil {
		r.Message = "Docker's answer is not JSON"
		return r
	}
	st := info.State
	r.Details = dockerDetails(info.Name, info.Config.Image, info.RestartCount, st.Status, st.Running, st.OOMKilled, st.ExitCode,
		st.StartedAt, st.FinishedAt)
	r.Details = append(r.Details, healthDetails(st.Health)...)
	switch {
	case st.Status == "exited" || st.Status == "dead":
		r.Message = fmt.Sprintf("%s (exit code %d)", sentence(st.Status), st.ExitCode)
	case !st.Running || st.Status == "restarting" || st.Status == "paused":
		r.Message = sentence(st.Status)
	case st.Health == nil || st.Health.Status == "" || st.Health.Status == "none":
		r.OK, r.Message = true, "Running"
	case st.Health.Status == "healthy":
		r.OK, r.Message = true, "Running, healthy"
	case st.Health.Status == "starting":
		r.OK, r.Message = true, "Running, health check starting"
	default:
		r.Message = "Running, " + st.Health.Status
	}
	return r
}

// dockerDetails are what Docker says about a container. Times are RFC
// 3339, for the page to show in the reader's time zone.
func dockerDetails(name, image string, restarts int, status string, running, oom bool, exit int, started, finished time.Time) []Fact {
	f := []Fact{{"State", sentence(status)}}
	if name != "" {
		f = append(f, Fact{"Container", strings.TrimPrefix(name, "/")})
	}
	if image != "" {
		f = append(f, Fact{"Image", image})
	}
	if running && started.Year() > 1 {
		f = append(f, Fact{"Started", started.UTC().Format(time.RFC3339)})
	}
	if !running && finished.Year() > 1 {
		f = append(f, Fact{"Stopped", finished.UTC().Format(time.RFC3339)}, Fact{"Exit code", strconv.Itoa(exit)})
	}
	if oom {
		f = append(f, Fact{"Out of memory", "Killed for running out of memory"})
	}
	return append(f, Fact{"Restarts", strconv.Itoa(restarts)})
}

// dockerHealth is a container's health check, as Docker reports it.
type dockerHealth struct {
	Status        string
	FailingStreak int
	Log           []struct{ Output string }
}

// healthDetails are what a container's health check says; nil without one.
func healthDetails(h *dockerHealth) []Fact {
	if h == nil || h.Status == "" || h.Status == "none" {
		return nil
	}
	f := []Fact{{"Health", sentence(h.Status)}}
	if h.FailingStreak > 0 {
		f = append(f, Fact{"Failed health checks in a row", strconv.Itoa(h.FailingStreak)})
	}
	if n := len(h.Log); n > 0 && strings.TrimSpace(h.Log[n-1].Output) != "" {
		f = append(f, Fact{"Last health check said", clipText(strings.TrimSpace(h.Log[n-1].Output), 300)})
	}
	return f
}
