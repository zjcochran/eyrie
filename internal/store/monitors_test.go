// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func newMonitor(name, typ string) Monitor {
	return Monitor{Name: name, Type: typ, Active: true, Interval: time.Minute, RetryInterval: 30 * time.Second,
		Retries: 1, ResendEvery: 0, Timeout: 48 * time.Second, Config: json.RawMessage(`{"url":"https://example.com"}`)}
}

func TestCreateMonitor(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		m, err := s.CreateMonitor(ctx, newMonitor("Example", "http"), t0)
		if err != nil {
			t.Fatal(err)
		}
		want := newMonitor("Example", "http")
		want.ID, want.CreatedAt, want.UpdatedAt = m.ID, t0, t0
		if m.ID == 0 || m.Interval != want.Interval || m.RetryInterval != want.RetryInterval || m.Timeout != want.Timeout ||
			string(m.Config) != string(want.Config) || !m.CreatedAt.Equal(t0) || m.ParentID != 0 || m.PushToken != "" {
			t.Fatalf("created %+v", m)
		}
		m.Name, m.UpsideDown, m.Description = "Example site", true, "the home page"
		later := t0.Add(time.Hour)
		if m, err = s.UpdateMonitor(ctx, m, later); err != nil || m.Name != "Example site" || !m.UpsideDown || !m.UpdatedAt.Equal(later) {
			t.Fatalf("updated %+v %v", m, err)
		}
		other := m
		other.Type = "port"
		if _, err := s.UpdateMonitor(ctx, other, later); err == nil {
			t.Error("changed the type")
		}
		other.ID = 9999
		if _, err := s.UpdateMonitor(ctx, other, later); !errors.Is(err, ErrNotFound) {
			t.Errorf("update unknown: %v", err)
		}
	})
}

func TestPauseListDelete(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		m, _ := s.CreateMonitor(ctx, newMonitor("Example", "http"), t0)
		s.CreateMonitor(ctx, newMonitor("Another", "http"), t0)
		if err := s.SetMonitorActive(ctx, m.ID, false, t0); err != nil {
			t.Fatal(err)
		}
		if m, _ := s.MonitorByID(ctx, m.ID); m.Active {
			t.Error("not paused")
		}
		if err := s.SetMonitorActive(ctx, 9999, false, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("pause unknown: %v", err)
		}
		list, err := s.Monitors(ctx)
		if err != nil || len(list) != 2 || list[0].Name != "Another" || list[1].Name != "Example" {
			t.Fatalf("list %+v %v", list, err)
		}
		if err := s.DeleteMonitor(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteMonitor(ctx, m.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete twice: %v", err)
		}
		if _, err := s.MonitorByID(ctx, m.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted: %v", err)
		}
	})
}

func TestPushTokens(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		push := newMonitor("Backup job", "push")
		push.PushToken = "tok-1"
		p, err := s.CreateMonitor(ctx, push, t0)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.MonitorByPushToken(ctx, "tok-1"); err != nil || got.ID != p.ID {
			t.Errorf("by token: %+v %v", got, err)
		}
		for _, tok := range []string{"", "nope"} {
			if _, err := s.MonitorByPushToken(ctx, tok); !errors.Is(err, ErrNotFound) {
				t.Errorf("token %q: %v", tok, err)
			}
		}
		var bad *InvalidError
		if _, err := s.CreateMonitor(ctx, push, t0); !errors.As(err, &bad) {
			t.Errorf("a push token twice: %v", err)
		}
		other := push
		other.PushToken = "tok-2"
		other, _ = s.CreateMonitor(ctx, other, t0)
		other.PushToken = "tok-1"
		if _, err := s.UpdateMonitor(ctx, other, t0); !errors.As(err, &bad) {
			t.Errorf("a push token twice on update: %v", err)
		}
	})
}

func TestGroups(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		outer, _ := s.CreateMonitor(ctx, newMonitor("Outer", "group"), t0)
		inner := newMonitor("Inner", "group")
		inner.ParentID = outer.ID
		inner, err := s.CreateMonitor(ctx, inner, t0)
		if err != nil || inner.ParentID != outer.ID {
			t.Fatalf("inner %+v %v", inner, err)
		}
		leaf := newMonitor("Leaf", "http")
		leaf.ParentID = inner.ID
		leaf, _ = s.CreateMonitor(ctx, leaf, t0)

		var bad *InvalidError
		for name, parent := range map[string]int64{"missing": 9999, "not a group": leaf.ID} {
			m := newMonitor("x", "http")
			m.ParentID = parent
			if _, err := s.CreateMonitor(ctx, m, t0); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// A cycle: outer into its own member.
		outer.ParentID = inner.ID
		if _, err := s.UpdateMonitor(ctx, outer, t0); !errors.As(err, &bad) {
			t.Errorf("cycle: %v", err)
		}
		outer.ParentID = outer.ID
		if _, err := s.UpdateMonitor(ctx, outer, t0); !errors.As(err, &bad) {
			t.Errorf("itself: %v", err)
		}
		// Deleting a group keeps its members, outside any group.
		if err := s.DeleteMonitor(ctx, inner.ID); err != nil {
			t.Fatal(err)
		}
		if l, _ := s.MonitorByID(ctx, leaf.ID); l.ParentID != 0 {
			t.Errorf("leaf still in %d", l.ParentID)
		}
	})
}

func beat(id int64, at time.Time, status string, latency time.Duration) Record {
	return Record{
		Beat:  Beat{MonitorID: id, At: at, Status: status, Latency: latency, Message: status, Important: status == "down"},
		State: MonitorState{MonitorID: id, Status: status, Since: t0, LastCheck: at, Latency: latency, Message: status},
	}
}

// seed makes monitors A and B: six checks of A 20 minutes apart (the
// fourth down), and one pending check of B with a certificate.
func seed(t *testing.T, s *Store) (a, b Monitor) {
	t.Helper()
	ctx := context.Background()
	a, _ = s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
	b, _ = s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
	if err := s.Save(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var recs []Record
	for i := range 6 {
		status := "up"
		if i == 3 {
			status = "down"
		}
		recs = append(recs, beat(a.ID, t0.Add(time.Duration(i)*20*time.Minute), status, time.Duration(100+i)*time.Millisecond))
	}
	last := beat(b.ID, t0, "pending", -1)
	last.State.CertExpires = t0.Add(30 * 24 * time.Hour)
	recs = append(recs, last, beat(9999, t0, "up", 1)) // the last: a deleted monitor
	if err := s.Save(ctx, recs); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestHeartbeats(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, b := seed(t, s)
		beats, err := s.Heartbeats(ctx, a.ID, t0.Add(30*time.Minute), 2)
		if err != nil || len(beats) != 2 || !beats[0].At.Equal(t0.Add(100*time.Minute)) || beats[0].Latency != 105*time.Millisecond {
			t.Fatalf("heartbeats %+v %v", beats, err)
		}
		recent, err := s.RecentBeats(ctx, 3)
		if err != nil || len(recent[a.ID]) != 3 || recent[a.ID][0].Status != "down" || len(recent[b.ID]) != 1 || recent[b.ID][0].Latency != -1 {
			t.Fatalf("recent %+v %v", recent, err)
		}
		states, err := s.States(ctx)
		if err != nil || states[a.ID].Status != "up" || !states[a.ID].LastCheck.Equal(t0.Add(100*time.Minute)) {
			t.Fatalf("states %+v %v", states, err)
		}
		if st := states[b.ID]; !st.CertExpires.Equal(t0.Add(30*24*time.Hour)) || st.Latency != -1 {
			t.Errorf("state of B %+v", st)
		}
	})
}

func TestStats(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, b := seed(t, s)
		hours, err := s.HourlyTotals(ctx, t0)
		if err != nil {
			t.Fatal(err)
		}
		if got := hours[a.ID]; got.Up != 5 || got.Down != 1 || got.LatencyCount != 6 || got.AvgLatency() != 102*time.Millisecond {
			t.Errorf("hourly totals %+v", got)
		}
		if got := hours[b.ID]; got.Uptime() != 1 || got.AvgLatency() != -1 {
			t.Errorf("pending counts as up: %+v", got)
		}
		days, _ := s.DailyTotals(ctx, t0)
		if u := days[a.ID].Uptime(); u < 0.83 || u > 0.84 {
			t.Errorf("daily uptime %v", u)
		}
		if (Totals{}).Uptime() != -1 {
			t.Error("uptime without checks")
		}
		buckets, err := s.Hours(ctx, a.ID, t0)
		if err != nil || len(buckets) < 2 || buckets[0].Start.After(t0) {
			t.Errorf("hours %+v %v", buckets, err)
		}
	})
}

func TestPrune(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := seed(t, s)
		if err := s.Prune(ctx, t0.Add(50*time.Minute), t0.Add(48*time.Hour), t0.Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if beats, _ := s.Heartbeats(ctx, a.ID, time.Time{}, 100); len(beats) != 3 {
			t.Errorf("after pruning: %d beats", len(beats))
		}
		if hours, _ := s.HourlyTotals(ctx, time.Time{}); len(hours) != 0 {
			t.Errorf("hourly stats left: %+v", hours)
		}
		if days, _ := s.DailyTotals(ctx, time.Time{}); len(days) != 2 {
			t.Errorf("daily stats: %+v", days)
		}
		// Deleting a monitor removes its history.
		s.DeleteMonitor(ctx, a.ID)
		if beats, _ := s.Heartbeats(ctx, a.ID, time.Time{}, 100); len(beats) != 0 {
			t.Error("heartbeats survived the delete")
		}
	})
}

func TestMonitorsClosed(t *testing.T) {
	s := openSQLite(t)
	ctx := context.Background()
	s.Close()
	checks := map[string]error{}
	_, checks["create"] = s.CreateMonitor(ctx, newMonitor("x", "http"), t0)
	_, checks["update"] = s.UpdateMonitor(ctx, newMonitor("x", "http"), t0)
	checks["pause"] = s.SetMonitorActive(ctx, 1, false, t0)
	checks["delete"] = s.DeleteMonitor(ctx, 1)
	_, checks["list"] = s.Monitors(ctx)
	checks["save"] = s.Save(ctx, []Record{beat(1, t0, "up", 1)})
	_, checks["states"] = s.States(ctx)
	_, checks["beats"] = s.Heartbeats(ctx, 1, t0, 1)
	_, checks["recent"] = s.RecentBeats(ctx, 1)
	_, checks["totals"] = s.HourlyTotals(ctx, t0)
	_, checks["hours"] = s.Hours(ctx, 1, t0)
	checks["prune"] = s.Prune(ctx, t0, t0, t0)
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s worked on a closed database", name)
		}
	}
}
