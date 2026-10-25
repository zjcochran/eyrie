// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/workload"
)

func TestAgents(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, token, err := s.AddAgent(ctx, " edge ", t0, t0.Add(time.Hour))
		if err != nil || a.Name != "edge" || !strings.HasPrefix(token, AgentTokenPrefix) || !a.TokenExpires.Equal(t0.Add(time.Hour)) || a.PublicKey != "" || !a.LastSeen.IsZero() {
			t.Fatalf("%+v %s %v", a, token, err)
		}
		var bad *InvalidError
		if _, _, err := s.AddAgent(ctx, "edge", t0, t0); !errors.As(err, &bad) {
			t.Errorf("a second edge: %v", err)
		}
		if _, _, err := s.AddAgent(ctx, " ", t0, t0); !errors.As(err, &bad) {
			t.Errorf("no name: %v", err)
		}

		r := AgentReport{Seen: t0.Add(time.Minute), Address: "192.0.2.5", Version: "v1.0.0", Platform: "docker", Hostname: "node1"}
		if _, err := s.EnrollAgent(ctx, token, "ed25519:k1", AgentReport{Seen: t0.Add(2 * time.Hour)}); !errors.Is(err, ErrNotFound) {
			t.Errorf("expired token: %v", err)
		}
		if _, err := s.EnrollAgent(ctx, "eyt_"+token[4:], "ed25519:k1", r); !errors.Is(err, ErrNotFound) {
			t.Errorf("wrong prefix: %v", err)
		}
		a, err = s.EnrollAgent(ctx, token, "ed25519:k1", r)
		if err != nil || a.PublicKey != "ed25519:k1" || !a.KeyCreated.Equal(r.Seen) || !a.TokenExpires.IsZero() || a.Hostname != "node1" || a.LastAddress != "192.0.2.5" {
			t.Fatalf("enrolled %+v %v", a, err)
		}
		if _, err := s.EnrollAgent(ctx, token, "ed25519:k2", r); !errors.Is(err, ErrNotFound) {
			t.Errorf("a used token: %v", err)
		}
	})
}

// enrolledAgent adds and enrolls the agent edge.
func enrolledAgent(t *testing.T, s *Store) (Agent, AgentReport) {
	t.Helper()
	ctx := context.Background()
	_, token, err := s.AddAgent(ctx, "edge", t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	r := AgentReport{Seen: t0.Add(time.Minute), Address: "192.0.2.5", Version: "v1.0.0", Platform: "docker", Hostname: "node1"}
	a, err := s.EnrollAgent(ctx, token, "ed25519:k1", r)
	if err != nil {
		t.Fatal(err)
	}
	return a, r
}

func TestAgentReports(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, r := enrolledAgent(t, s)
		ws := []workload.Workload{{Key: "web", Name: "web", Kind: "container", Up: true, Status: "Running"}}
		r.Seen, r.Workloads = t0.Add(2*time.Minute), ws
		if err := s.SaveAgentReport(ctx, a.ID, r); err != nil {
			t.Fatal(err)
		}
		r.Seen, r.Workloads, r.Error = t0.Add(3*time.Minute), nil, "cannot reach Docker"
		if err := s.SaveAgentReport(ctx, a.ID, r); err != nil {
			t.Fatal(err)
		}
		a, err := s.Agent(ctx, a.ID)
		if err != nil || len(a.Workloads) != 1 || a.Workloads[0].Status != "Running" || a.Error != "cannot reach Docker" || !a.LastSeen.Equal(t0.Add(3*time.Minute)) {
			t.Fatalf("after reports %+v %v", a, err)
		}
	})
}

func TestAgentKeys(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := enrolledAgent(t, s)
		if err := s.RotateAgentKey(ctx, a.ID, "ed25519:k2", t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if a, _ = s.Agent(ctx, a.ID); a.PublicKey != "ed25519:k2" || a.PreviousKey != "ed25519:k1" || !a.KeyCreated.Equal(t0.Add(time.Hour)) {
			t.Errorf("rotated %+v", a)
		}
		if err := s.ClearAgentPreviousKey(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if a, _ = s.Agent(ctx, a.ID); a.PreviousKey != "" {
			t.Errorf("previous key kept %+v", a)
		}
	})
}

func TestAgentTokenAndDelete(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := enrolledAgent(t, s)
		token2, err := s.NewAgentToken(ctx, a.ID, t0.Add(5*time.Hour))
		if err != nil || !strings.HasPrefix(token2, AgentTokenPrefix) {
			t.Fatal(err)
		}
		if a, _ = s.Agent(ctx, a.ID); a.PublicKey != "" || !a.KeyCreated.IsZero() || !a.TokenExpires.Equal(t0.Add(5*time.Hour)) {
			t.Errorf("new token %+v", a)
		}
		if _, err := s.NewAgentToken(ctx, 999, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("no agent: %v", err)
		}

		if _, _, err := s.AddAgent(ctx, "core", t0, t0); err != nil {
			t.Fatal(err)
		}
		all, err := s.Agents(ctx)
		if err != nil || len(all) != 2 || all[0].Name != "core" {
			t.Fatalf("list %+v %v", all, err)
		}
		if name, err := s.DeleteAgent(ctx, a.ID); err != nil || name != "edge" {
			t.Errorf("delete %s %v", name, err)
		}
		if _, err := s.DeleteAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete twice: %v", err)
		}
		if _, err := s.Agent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("get deleted: %v", err)
		}
	})
}
