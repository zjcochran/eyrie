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
			string(m.Config) != string(want.Config) || !m.CreatedAt.Equal(t0) || m.GroupID != 0 || m.PushToken != "" {
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
		if err != nil || len(list) != 2 || list[0].Name != "Example" || list[1].Name != "Another" {
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

func TestMonitorGroup(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		g, _ := s.CreateGroup(ctx, Group{Name: "Web", Alert: AlertNone}, t0)
		a := newMonitor("A", "http")
		a.GroupID = g.ID
		a, err := s.CreateMonitor(ctx, a, t0)
		b := newMonitor("B", "http")
		b.GroupID = g.ID
		b, _ = s.CreateMonitor(ctx, b, t0)
		if err != nil || a.GroupID != g.ID || a.Position != 0 || b.Position != 1 {
			t.Fatalf("a %+v b %+v %v", a, b, err)
		}
		var bad *InvalidError
		m := newMonitor("x", "http")
		m.GroupID = 9999
		if _, err := s.CreateMonitor(ctx, m, t0); !errors.As(err, &bad) {
			t.Errorf("missing group: %v", err)
		}
		// Out of the group: after everything outside any group.
		a.GroupID = 0
		if a, err = s.UpdateMonitor(ctx, a, t0); err != nil || a.GroupID != 0 || a.Position != 1 {
			t.Errorf("moved %+v %v", a, err)
		}
		// Unmoved, it keeps its place.
		b.Name = "Bee"
		if b, err = s.UpdateMonitor(ctx, b, t0); err != nil || b.Position != 1 {
			t.Errorf("kept %+v %v", b, err)
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

func TestChanges(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, b := seed(t, s)
		recs := []Record{beat(b.ID, t0.Add(time.Hour), "down", -1), beat(a.ID, t0.Add(2*time.Hour), "up", 1), beat(b.ID, t0.Add(3*time.Hour), "down", -1)}
		recs[1].Beat.Important = true
		if err := s.Save(ctx, recs); err != nil {
			t.Fatal(err)
		}
		all, err := s.Changes(ctx, 0, 10)
		if err != nil || len(all) != 4 {
			t.Fatalf("changes %+v %v", all, err)
		}
		if c := all[0]; c.MonitorID != b.ID || !c.At.Equal(t0.Add(3*time.Hour)) || c.Status != "down" || c.Latency != -1 {
			t.Errorf("newest %+v", c)
		}
		if c := all[3]; c.MonitorID != a.ID || c.Status != "down" || c.Latency != 103*time.Millisecond {
			t.Errorf("oldest %+v", c)
		}
		older, err := s.Changes(ctx, all[1].ID, 1)
		if err != nil || len(older) != 1 || older[0].ID != all[2].ID {
			t.Errorf("older %+v %v", older, err)
		}
	})
}

// Faster than a millisecond is kept, to the microsecond.
func TestFastBeats(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := seed(t, s)
		fast := beat(a.ID, t0.Add(200*time.Minute), "up", 0)
		fast.Beat.Latency, fast.State.Latency = 512345*time.Nanosecond, 512345*time.Nanosecond
		if err := s.Save(ctx, []Record{fast}); err != nil {
			t.Fatal(err)
		}
		if beats, _ := s.Heartbeats(ctx, a.ID, t0.Add(200*time.Minute), 1); len(beats) != 1 || beats[0].Latency != 512*time.Microsecond {
			t.Errorf("fast beat %+v", beats)
		}
		if states, _ := s.States(ctx); states[a.ID].Latency != 512*time.Microsecond {
			t.Errorf("fast state %+v", states[a.ID])
		}
		if hours, _ := s.Hours(ctx, a.ID, t0.Add(200*time.Minute)); len(hours) != 1 || hours[0].AvgLatency() != 512*time.Microsecond {
			t.Errorf("fast hour %+v", hours)
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
		if got := hours[a.ID]; got.Up != 5 || got.Down != 1 || got.LatencyCount != 6 || got.AvgLatency() != 102500*time.Microsecond {
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
