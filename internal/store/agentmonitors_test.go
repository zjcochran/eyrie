// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// oldAgentMonitors makes monitors of the agent type of earlier versions,
// one of each kind of workload.
func oldAgentMonitors(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for name, workload := range map[string]string{
		"Web": "web", "API": "service/api", "App": "shop/statefulset/app", "Box": "lxc/101", "VM": "qemu/102", "Odd": "lxc/x",
	} {
		m := newMonitor(name, "agent")
		m.Config = json.RawMessage(`{"agent":4,"workload":"` + workload + `"}`)
		if _, err := s.CreateMonitor(ctx, m, t0); err != nil {
			t.Fatal(err)
		}
	}
	m := newMonitor("Broken", "agent")
	m.Config = json.RawMessage(`not json`)
	if _, err := s.CreateMonitor(ctx, m, t0); err != nil {
		t.Fatal(err)
	}
}

// checkAgentMonitors checks what oldAgentMonitors became.
func checkAgentMonitors(t *testing.T, s *Store) {
	t.Helper()
	ms, err := s.Monitors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		typ, config string
		agent       int64
	}{
		"Web":    {"docker", `{"container":"web"}`, 4},
		"API":    {"docker-swarm", `{"service":"api"}`, 4},
		"App":    {"kubernetes", `{"kind":"statefulset","name":"app","namespace":"shop"}`, 4},
		"Box":    {"proxmox", `{"vmid":101}`, 4},
		"VM":     {"proxmox", `{"vmid":102}`, 4},
		"Odd":    {"docker", `{"container":"lxc/x"}`, 4},
		"Broken": {"docker", `{"container":""}`, 0},
	}
	seen := 0
	for _, m := range ms {
		w, ok := want[m.Name]
		if !ok {
			continue
		}
		seen++
		if m.Type != w.typ || string(m.Config) != w.config || m.AgentID != w.agent {
			t.Errorf("%s: %s %s on %d", m.Name, m.Type, m.Config, m.AgentID)
		}
	}
	if seen != len(want) {
		t.Errorf("%d of %d monitors", seen, len(want))
	}
}

func TestConvertAgentMonitors(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		oldAgentMonitors(t, s)
		if err := s.convertOnOpen(context.Background()); err != nil {
			t.Fatal(err)
		}
		checkAgentMonitors(t, s)
	})
}

func TestRestoreOldAgentMonitors(t *testing.T) {
	ctx := context.Background()
	src := openSQLite(t)
	if _, err := src.CreateFirstAdmin(ctx, "alice", "a long password here", t0); err != nil {
		t.Fatal(err)
	}
	oldAgentMonitors(t, src)
	snap, err := src.Export(ctx, false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Config["monitors"] {
		delete(r, "agent_id") // as a backup from before
	}
	eachDB(t, func(t *testing.T, dst *Store) {
		if err := dst.Import(ctx, roundTrip(t, snap), false); err != nil {
			t.Fatal(err)
		}
		checkAgentMonitors(t, dst)
	})
}

// A monitor keeps the agent it runs on.
func TestMonitorAgent(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		m := newMonitor("Web", "http")
		m.AgentID = 3
		m, err := s.CreateMonitor(ctx, m, t0)
		if err != nil || m.AgentID != 3 {
			t.Fatalf("created %+v %v", m, err)
		}
		m.AgentID = 0
		if m, err = s.UpdateMonitor(ctx, m, t0); err != nil || m.AgentID != 0 {
			t.Fatalf("updated %+v %v", m, err)
		}
	})
}
