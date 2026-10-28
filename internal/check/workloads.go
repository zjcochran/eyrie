// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The types of a platform's workloads (Swarm services, Kubernetes
// workloads, Proxmox VE guests) are checked only on an agent: they read
// what Env.Workloads lists, the platform the agent was set up for.

// AgentOnly reports whether a type is checked only on an agent.
func AgentOnly(typ string) bool {
	return typ == "docker-swarm" || typ == "kubernetes" || typ == "proxmox"
}

// Swarm is the settings of the docker-swarm type: every task a service
// wants must run.
type Swarm struct {
	Service string `json:"service"`
}

// kubeName is a Kubernetes object or namespace name.
var kubeName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// kubeKinds are the kinds of workloads agents list.
var kubeKinds = []string{"deployment", "statefulset", "daemonset"}

// Kubernetes is the settings of the kubernetes type: every replica a
// Deployment or StatefulSet wants, or every pod a DaemonSet schedules, must
// be ready.
type Kubernetes struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

// Proxmox is the settings of the proxmox type: an LXC container or VM
// must be running. VM IDs are unique in a cluster, so the ID is enough.
type Proxmox struct {
	VMID int `json:"vmid"`
}

func (s *Swarm) normalize() error {
	s.Service = strings.TrimSpace(s.Service)
	if !containerName.MatchString(s.Service) {
		return errors.New("the service must be a Swarm service's name")
	}
	return nil
}

func (k *Kubernetes) normalize() error {
	k.Namespace, k.Kind, k.Name = strings.TrimSpace(k.Namespace), strings.TrimSpace(k.Kind), strings.TrimSpace(k.Name)
	if k.Namespace == "" {
		k.Namespace = "default"
	}
	if k.Kind == "" {
		k.Kind = "deployment"
	}
	switch {
	case !kubeName.MatchString(k.Namespace):
		return errors.New("the namespace must be a Kubernetes namespace's name")
	case !slices.Contains(kubeKinds, k.Kind):
		return errors.New("the kind must be deployment, statefulset or daemonset")
	case !kubeName.MatchString(k.Name):
		return errors.New("the name must be a Kubernetes workload's name")
	}
	return nil
}

func (p *Proxmox) normalize() error {
	if p.VMID < 100 || p.VMID > 999999999 {
		return errors.New("the VM ID must be from 100 to 999999999")
	}
	return nil
}

func (s *Swarm) keys() []string      { return []string{"service/" + s.Service} }
func (k *Kubernetes) keys() []string { return []string{k.Namespace + "/" + k.Kind + "/" + k.Name} }
func (p *Proxmox) keys() []string {
	id := strconv.Itoa(p.VMID)
	return []string{"lxc/" + id, "qemu/" + id}
}

func (s *Swarm) checker(env *Env) Checker {
	return &workloadChecker{env, s.keys(), "Docker Swarm service", "No service " + s.Service}
}

func (k *Kubernetes) checker(env *Env) Checker {
	return &workloadChecker{env, k.keys(), "Kubernetes workload", fmt.Sprintf("No %s %s in %s", k.Kind, k.Name, k.Namespace)}
}

func (p *Proxmox) checker(env *Env) Checker {
	return &workloadChecker{env, p.keys(), "Proxmox VE guest", fmt.Sprintf("No LXC container or VM %d", p.VMID)}
}

func (s *Swarm) describe() []Fact { return []Fact{{"Service", s.Service}} }
func (k *Kubernetes) describe() []Fact {
	return []Fact{{"Namespace", k.Namespace}, {"Kind", k.Kind}, {"Name", k.Name}}
}
func (p *Proxmox) describe() []Fact { return []Fact{{"VM ID", strconv.Itoa(p.VMID)}} }

// workloadChecker finds a workload among those Env.Workloads lists: the
// first of keys.
type workloadChecker struct {
	env     *Env
	keys    []string
	label   string
	missing string
}

func (c *workloadChecker) Check(ctx context.Context) Result {
	if c.env.Workloads == nil {
		return fail("A %s monitor is checked on an agent", c.label)
	}
	ws, err := c.env.Workloads.List(ctx)
	if err != nil {
		return fail("Cannot list the workloads: %s", err)
	}
	for _, w := range ws {
		if slices.Contains(c.keys, w.Key) {
			return Result{OK: w.Up, Message: w.Status, Latency: -1}
		}
	}
	return fail("%s", c.missing)
}

// WorkloadKeys are the keys an agent's report names a monitor's workload
// by (a container's name, service/<name>, <namespace>/<kind>/<name>,
// lxc/<vmid> and qemu/<vmid>), so its checks can follow the report; nil
// for other types.
func WorkloadKeys(typ string, saved json.RawMessage) []string {
	_, s, err := decode(typ, saved)
	if err != nil || s.normalize() != nil {
		return nil
	}
	switch v := s.(type) {
	case *Docker:
		return []string{v.Container}
	case interface{ keys() []string }:
		return v.keys()
	}
	return nil
}
