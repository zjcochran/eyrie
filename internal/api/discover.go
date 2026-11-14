// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zjcochran/eyrie/internal/agents"
	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/workload"
)

// Discovery: an admin asks what one source has (the containers of a
// Docker engine, the workloads an agent reports, an SNMP device or its
// storage), to add
// monitors of them. Nothing is added here: each one found carries the
// settings that start the monitor form.

// discoverTimeout is how long an SNMP device is asked for.
const discoverTimeout = 60 * time.Second

// discoverable are the types discovery finds, and what it calls their
// things.
var discoverable = map[string]string{
	"docker":       "Docker containers",
	"docker-swarm": "Docker Swarm services",
	"kubernetes":   "Kubernetes workloads",
	"proxmox":      "Proxmox VE guests",
	"snmp":         "SNMP devices",
	"snmp-storage": "NAS storage",
}

// workloadKinds are the kinds of an agent's workloads each type monitors,
// and platforms the agents that list them.
var (
	workloadKinds = map[string][]string{
		"docker": {"container"}, "docker-swarm": {"service"}, "kubernetes": {"deployment", "statefulset", "daemonset"},
		"proxmox": {"lxc", "vm"},
	}
	workloadPlatforms = map[string][]string{
		"docker": {"docker", "swarm"}, "docker-swarm": {"swarm"}, "kubernetes": {"kubernetes"}, "proxmox": {"proxmox"},
	}
)

// found is one thing discovered: a monitor that could be added.
type found struct {
	Name   string `json:"name"`   // the monitor's name
	Detail string `json:"detail"` // how it is now
	Group  string `json:"group,omitempty"`
	Up     bool   `json:"up"`
	// Settings are the monitor's settings, over those of the request.
	Settings map[string]any `json:"settings"`
}

type discovered struct {
	// Device names an SNMP device and what it says it is.
	Device string  `json:"device,omitempty"`
	Found  []found `json:"found"`
	// Monitored counts what was found but left out: a monitor checks it.
	Monitored int `json:"monitored"`
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Type     string          `json:"type"`
		AgentID  int64           `json:"agent_id"` // 0: Eyrie
		Settings json.RawMessage `json:"settings"` // snmp, snmp-storage: the device and its credentials
	}
	if !readJSON(w, r, &b) {
		return
	}
	what, ok := discoverable[b.Type]
	if !ok {
		invalid(w, http.StatusBadRequest, "Discovery finds Docker containers, Docker Swarm services, Kubernetes workloads, Proxmox VE guests, SNMP devices and NAS storage.")
		return
	}
	where := "Eyrie"
	if b.AgentID != 0 {
		a, ok := s.Agents.Get(b.AgentID)
		if !ok {
			invalid(w, http.StatusNotFound, "No such agent.")
			return
		}
		where = "agent " + a.Name
	}
	var out discovered
	var err error
	switch b.Type {
	case "snmp":
		out, err = s.discoverSNMP(r.Context(), b.AgentID, b.Settings)
	case "snmp-storage":
		out, err = s.discoverStorage(r.Context(), b.AgentID, b.Settings)
	default:
		out.Found, err = s.discoverWorkloads(r.Context(), b.Type, b.AgentID, what)
	}
	if err != nil {
		msg := capitalize(err.Error())
		if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, "?") {
			msg += "."
		}
		invalid(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if err := s.dropMonitored(r.Context(), b.Type, b.AgentID, b.Settings, &out); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "discovery", "type", b.Type, "runs_on", where, "found", len(out.Found)+out.Monitored, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, out)
}

// discoverWorkloads lists the containers of Eyrie's Docker engine, or what
// an agent last reported.
func (s *Server) discoverWorkloads(ctx context.Context, typ string, agent int64, what string) ([]found, error) {
	var ws []workload.Workload
	if agent == 0 {
		if typ != "docker" {
			return nil, fmt.Errorf("%s are found by an agent", what)
		}
		host := cmp.Or(s.Env.DockerHost, check.DefaultDockerHost)
		d, err := s.dockerLister(host)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if ws, err = d.List(ctx); err != nil {
			return nil, fmt.Errorf("Eyrie cannot list the containers of %s: %w", host, err) //nolint:staticcheck // ST1005: Eyrie is a name
		}
	} else {
		a, _ := s.Agents.Get(agent)
		switch {
		case !slices.Contains(workloadPlatforms[typ], a.Platform):
			return nil, fmt.Errorf("the agent %s does not list %s", a.Name, what)
		case !agents.Online(a, s.now()):
			return nil, fmt.Errorf("the agent %s is not reporting: start it, then try again", a.Name)
		case a.Error != "":
			return nil, fmt.Errorf("the agent %s cannot list %s: %s", a.Name, what, a.Error)
		}
		ws = a.Workloads
	}
	out := []found{}
	for _, w := range ws {
		if !slices.Contains(workloadKinds[typ], w.Kind) {
			continue
		}
		out = append(out, found{Name: w.Name, Detail: w.Status, Group: w.Group, Up: w.Up, Settings: workloadSettings(typ, w)})
	}
	return out, nil
}

// workloadSettings are the settings of a monitor of a workload.
func workloadSettings(typ string, w workload.Workload) map[string]any {
	switch typ {
	case "docker-swarm":
		return map[string]any{"service": w.Name}
	case "kubernetes":
		// <namespace>/<kind>/<name>
		parts := strings.SplitN(w.Key, "/", 3)
		if len(parts) == 3 {
			return map[string]any{"namespace": parts[0], "kind": parts[1], "name": parts[2]}
		}
		return map[string]any{"name": w.Name}
	case "proxmox":
		// lxc/<vmid> or qemu/<vmid>
		_, id, _ := strings.Cut(w.Key, "/")
		n, _ := strconv.Atoi(id)
		return map[string]any{"vmid": n}
	}
	return map[string]any{"container": w.Name}
}

// discoverSNMP asks an SNMP device, from Eyrie or an agent, for its name and
// interfaces: a monitor of the device, and one of each interface.
func (s *Server) discoverSNMP(ctx context.Context, agent int64, settings json.RawMessage) (discovered, error) {
	var d check.SNMPDevice
	ctx, cancel := context.WithTimeout(ctx, discoverTimeout+20*time.Second)
	defer cancel()
	if agent == 0 {
		var err error
		dctx, cancel := context.WithTimeout(ctx, discoverTimeout)
		defer cancel()
		if d, err = check.DiscoverSNMP(dctx, s.Env, settings); err != nil {
			return discovered{}, err
		}
	} else {
		raw, err := s.Agents.Discover(ctx, agent, "snmp", settings, discoverTimeout)
		if err != nil {
			return discovered{}, err
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return discovered{}, errors.New("the agent's answer cannot be read")
		}
	}
	var dev struct {
		Host string `json:"host"`
	}
	_ = json.Unmarshal(settings, &dev) // DiscoverSNMP read them
	name := cmp.Or(clipText(d.Name), strings.TrimSpace(dev.Host))
	descr := clipText(d.Description)
	out := discovered{Device: name, Found: []found{{
		Name: name, Detail: cmp.Or(descr, "Answers SNMP"), Up: true,
		Settings: map[string]any{"oid": check.OIDSysUpTime, "operator": "==", "expected": ""},
	}}}
	if descr != "" {
		out.Device += ": " + descr
	}
	for i, f := range d.Interfaces {
		if i == check.MaxInterfaces {
			break
		}
		detail := capitalize(clipText(f.Oper))
		if f.Admin == "down" {
			detail += ", turned off"
		}
		if alias := clipText(f.Alias); alias != "" {
			detail += ": " + alias
		}
		out.Found = append(out.Found, found{
			Name: name + " " + clipText(f.Name), Detail: detail, Up: f.Oper == "up",
			Settings: map[string]any{"oid": check.OIDIfOperStatus + "." + strconv.FormatInt(f.Index, 10), "operator": "==", "expected": "1"},
		})
	}
	return out, nil
}

// discoverStorage asks an SNMP device, from Eyrie or an agent, about its
// storage: a monitor of each volume's space and health, of its disks'
// health and temperature, of its system's health and of each command its
// snmpd runs.
func (s *Server) discoverStorage(ctx context.Context, agent int64, settings json.RawMessage) (discovered, error) {
	var d check.StorageDevice
	ctx, cancel := context.WithTimeout(ctx, discoverTimeout+20*time.Second)
	defer cancel()
	if agent == 0 {
		var err error
		dctx, cancel := context.WithTimeout(ctx, discoverTimeout)
		defer cancel()
		if d, err = check.DiscoverStorage(dctx, s.Env, settings); err != nil {
			return discovered{}, err
		}
	} else {
		raw, err := s.Agents.Discover(ctx, agent, "snmp-storage", settings, discoverTimeout)
		if err != nil {
			return discovered{}, err
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return discovered{}, errors.New("the agent's answer cannot be read")
		}
	}
	var dev struct {
		Host string `json:"host"`
	}
	_ = json.Unmarshal(settings, &dev) // DiscoverStorage read them
	name := cmp.Or(clipText(d.Name), strings.TrimSpace(dev.Host))
	out := discovered{Device: name, Found: []found{}}
	if descr := clipText(d.Description); descr != "" {
		out.Device += ": " + descr
	}
	for i, f := range d.Suggestions() {
		if i == check.MaxInterfaces {
			break
		}
		out.Found = append(out.Found, found{Name: name + " " + clipText(f.Name), Detail: clipText(f.Detail), Up: f.Up, Settings: f.Settings})
	}
	return out, nil
}

// clipText trims what a device or agent says to its first line, at most
// 200 bytes.
func clipText(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(s)
	if len(s) <= 200 {
		return s
	}
	s = s[:200]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// dropMonitored leaves out what a monitor already checks: one on the same
// agent (or Eyrie), of the same workload, of the same SNMP device and
// OID, or of the same thing on the same NAS.
func (s *Server) dropMonitored(ctx context.Context, typ string, agent int64, settings json.RawMessage, out *discovered) error {
	mons, err := s.Store.Monitors(ctx)
	if err != nil {
		return err
	}
	var dev struct {
		Host string `json:"host"`
	}
	_ = json.Unmarshal(settings, &dev)
	kept := out.Found[:0]
	for _, f := range out.Found {
		if slices.ContainsFunc(mons, func(m store.Monitor) bool {
			return m.Type == typ && m.AgentID == agent && monitors(typ, m.Config, f.Settings, dev.Host)
		}) {
			out.Monitored++
			continue
		}
		kept = append(kept, f)
	}
	out.Found = kept
	return nil
}

// monitors reports whether a monitor's saved settings check what settings
// would.
func monitors(typ string, saved json.RawMessage, settings map[string]any, host string) bool {
	switch typ {
	case "snmp":
		var m struct{ Host, OID string }
		_ = json.Unmarshal(saved, &m)
		return strings.EqualFold(m.Host, strings.TrimSpace(host)) && m.OID == settings["oid"]
	case "snmp-storage":
		var m struct{ Host, Watch, Volume, Disk, Command string }
		_ = json.Unmarshal(saved, &m)
		same := func(saved, key string) bool { v, _ := settings[key].(string); return saved == v }
		return strings.EqualFold(m.Host, strings.TrimSpace(host)) && same(m.Watch, "watch") && same(m.Volume, "volume") &&
			same(m.Disk, "disk") && same(m.Command, "command")
	}
	b, _ := json.Marshal(settings) //nolint:errchkjson // strings and numbers
	keys := check.WorkloadKeys(typ, b)
	return slices.ContainsFunc(check.WorkloadKeys(typ, saved), func(k string) bool { return slices.Contains(keys, k) })
}
