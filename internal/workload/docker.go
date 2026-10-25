// SPDX-License-Identifier: AGPL-3.0-or-later

package workload

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Docker labels the clients read.
const (
	labelProject = "com.docker.compose.project"
	labelStack   = "com.docker.stack.namespace"
	labelTask    = "com.docker.swarm.service.id"
)

// Docker lists a Docker engine's containers, over its socket or TCP.
type Docker struct {
	http *http.Client
	base string
}

// NewDocker connects to host: unix:///path or tcp://host:port.
func NewDocker(host string) (*Docker, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("docker host %q: %w", host, err)
	}
	t := &http.Transport{}
	base := "http://docker"
	switch {
	case u.Scheme == "unix" && u.Path != "":
		path := u.Path
		t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
	case (u.Scheme == "tcp" || u.Scheme == "http") && u.Host != "":
		base = "http://" + u.Host
	default:
		return nil, fmt.Errorf("docker host %q: want unix:///path or tcp://host:port", host)
	}
	return &Docker{http: &http.Client{Transport: t}, base: base}, nil
}

func (d *Docker) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Docker: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError("Docker", resp, jsonMessage)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxBody)).Decode(v); err != nil {
		return fmt.Errorf("Docker: %w", errNoBody) //nolint:staticcheck // ST1005: Docker is a name
	}
	return nil
}

func jsonMessage(b []byte) string {
	var e struct{ Message string }
	_ = json.Unmarshal(b, &e)
	return e.Message
}

// List returns every container, running or not.
func (d *Docker) List(ctx context.Context) ([]Workload, error) {
	cs, err := d.containers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Workload, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.workload())
	}
	sortWorkloads(out)
	return out, nil
}

type container struct {
	ID     string `json:"Id"`
	Names  []string
	State  string
	Status string
	Labels map[string]string
}

func (d *Docker) containers(ctx context.Context) ([]container, error) {
	var cs []container
	err := d.get(ctx, "/containers/json?all=1", &cs)
	return cs, err
}

func (c container) name() string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:min(12, len(c.ID))]
}

// The health and exit code in a container's status text, such as "Up 2
// hours (healthy)" or "Exited (137) 5 minutes ago".
var (
	healthText = regexp.MustCompile(`\((healthy|unhealthy|health: starting)\)`)
	exitText   = regexp.MustCompile(`^Exited \((-?\d+)\)`)
)

func (c container) workload() Workload {
	name := c.name()
	w := Workload{Key: name, Name: name, Kind: "container", Group: c.Labels[labelProject]}
	w.Up, w.Status = containerStatus(c.State, c.Status)
	return w
}

// containerStatus reads a container's state ("running", "exited", …) and
// status text: up while it runs, and is healthy or still starting if it
// has a health check.
func containerStatus(state, text string) (bool, string) {
	switch state {
	case "running":
		switch h := healthText.FindStringSubmatch(text); {
		case h == nil:
			return true, "Running"
		case h[1] == "healthy":
			return true, "Running, healthy"
		case h[1] == "health: starting":
			return true, "Running, health check starting"
		}
		return false, "Running, unhealthy"
	case "exited":
		if m := exitText.FindStringSubmatch(text); m != nil {
			return false, "Exited (exit code " + m[1] + ")"
		}
	case "":
		return false, "Unknown"
	}
	return false, sentence(state)
}

// Events calls changed whenever a container or service starts, stops,
// changes health or is created or removed, until ctx ends or the stream
// breaks.
func (d *Docker) Events(ctx context.Context, changed func()) error {
	filters := url.QueryEscape(`{"type":["container","service"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+"/events?filters="+filters, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError("Docker", resp, jsonMessage)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct{ Action string }
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		// health_status: healthy and the like.
		action, _, _ := strings.Cut(ev.Action, ":")
		switch action {
		case "create", "destroy", "rename", "start", "die", "stop", "kill", "pause", "unpause", "health_status", "update", "remove":
			changed()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("the Docker event stream ended")
}

// Swarm lists a swarm's services, and the standalone containers of the
// manager node it runs on.
type Swarm struct{ *Docker }

// CheckManager returns an error unless the engine is a swarm manager.
func (s Swarm) CheckManager(ctx context.Context) error {
	var info struct {
		Swarm struct {
			LocalNodeState   string
			ControlAvailable bool
		}
	}
	if err := s.get(ctx, "/info", &info); err != nil {
		return err
	}
	if info.Swarm.LocalNodeState != "active" || !info.Swarm.ControlAvailable {
		return errors.New("this Docker engine is not a swarm manager: run the agent on a manager node")
	}
	return nil
}

// List returns the services, then the containers that are not tasks of a
// service.
func (s Swarm) List(ctx context.Context) ([]Workload, error) {
	var svcs []struct {
		Spec struct {
			Name   string
			Labels map[string]string
		}
		ServiceStatus *struct {
			RunningTasks int64
			DesiredTasks int64
		}
	}
	if err := s.get(ctx, "/services?status=true", &svcs); err != nil {
		return nil, err
	}
	out := []Workload{}
	for _, v := range svcs {
		w := Workload{Key: "service/" + v.Spec.Name, Name: v.Spec.Name, Kind: "service", Group: v.Spec.Labels[labelStack]}
		if st := v.ServiceStatus; st != nil {
			w.Up, w.Status = ready(st.RunningTasks, st.DesiredTasks, "tasks running")
		} else {
			w.Status = "Unknown: Docker gave no task counts"
		}
		out = append(out, w)
	}
	cs, err := s.containers(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		if c.Labels[labelTask] == "" {
			out = append(out, c.workload())
		}
	}
	sortWorkloads(out)
	return out, nil
}

// sortWorkloads orders workloads by group, then key.
func sortWorkloads(ws []Workload) {
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].Group != ws[j].Group {
			return ws[i].Group < ws[j].Group
		}
		return ws[i].Key < ws[j].Key
	})
}
