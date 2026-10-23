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

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)

func (d *Docker) normalize() error {
	d.Container = strings.TrimPrefix(strings.TrimSpace(d.Container), "/")
	if !containerName.MatchString(d.Container) {
		return errors.New("the container must be a container's name or ID")
	}
	d.DockerHost = strings.TrimSpace(d.DockerHost)
	if d.DockerHost == "" {
		d.DockerHost = "unix:///var/run/docker.sock"
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
	u, _ := url.Parse(c.d.DockerHost)
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
		State struct {
			Status   string
			Running  bool
			ExitCode int
			Health   *struct{ Status string }
		}
	}
	if err := json.Unmarshal(body, &info); err != nil {
		r.Message = "Docker's answer is not JSON"
		return r
	}
	st := info.State
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
