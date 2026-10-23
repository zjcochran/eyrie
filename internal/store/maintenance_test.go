// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestWindows(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		tag, _ := s.CreateTag(ctx, Tag{Name: "prod", Color: "#000000"}, t0)
		sched := json.RawMessage(`{"strategy":"manual"}`)
		w, err := s.CreateWindow(ctx, Window{Title: "Upgrade", Description: "d", Enabled: true, Schedule: sched,
			MonitorIDs: []int64{b.ID, a.ID, a.ID}, TagIDs: []int64{tag.ID}}, t0)
		if err != nil || w.Title != "Upgrade" || !w.Enabled || string(w.Schedule) != string(sched) ||
			!slices.Equal(w.MonitorIDs, []int64{a.ID, b.ID}) || !slices.Equal(w.TagIDs, []int64{tag.ID}) || !w.CreatedAt.Equal(t0) {
			t.Fatalf("create %+v %v", w, err)
		}
		all, _ := s.CreateWindow(ctx, Window{Title: "all", AllMonitors: true, Schedule: sched}, t0)
		later := t0.Add(time.Hour)
		w.Title, w.Enabled, w.MonitorIDs, w.TagIDs = "Upgrade 2", false, []int64{b.ID}, nil
		if w, err = s.UpdateWindow(ctx, w, later); err != nil || w.Title != "Upgrade 2" || w.Enabled ||
			!slices.Equal(w.MonitorIDs, []int64{b.ID}) || len(w.TagIDs) != 0 || !w.UpdatedAt.Equal(later) {
			t.Fatalf("update %+v %v", w, err)
		}
		// Deleting a monitor takes it out of windows.
		s.DeleteMonitor(ctx, b.ID)
		list, err := s.Windows(ctx)
		if err != nil || len(list) != 2 || list[0].ID != all.ID || !list[0].AllMonitors || len(list[1].MonitorIDs) != 0 {
			t.Fatalf("list %+v %v", list, err)
		}
	})
}

func TestWindowErrors(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		var bad *InvalidError
		sched := json.RawMessage(`{"strategy":"manual"}`)
		for name, w := range map[string]Window{
			"no such monitor": {Title: "x", Schedule: sched, MonitorIDs: []int64{999}},
			"no such tag":     {Title: "x", Schedule: sched, TagIDs: []int64{999}},
		} {
			if _, err := s.CreateWindow(ctx, w, t0); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := s.UpdateWindow(ctx, Window{ID: 999, Schedule: sched}, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("update missing: %v", err)
		}
		w, _ := s.CreateWindow(ctx, Window{Title: "x", Schedule: sched}, t0)
		if _, err := s.UpdateWindow(ctx, Window{ID: w.ID, Title: "x", Schedule: sched, TagIDs: []int64{999}}, t0); !errors.As(err, &bad) {
			t.Errorf("update bad tag: %v", err)
		}
		// Deleted, it is gone.
		s.DeleteWindow(ctx, w.ID)
		if err := s.DeleteWindow(ctx, w.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete again: %v", err)
		}
		if _, err := s.WindowByID(ctx, w.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("by id: %v", err)
		}
	})
}

func TestMaintenanceBeats(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		m, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		at := t0.Truncate(time.Hour)
		recs := []Record{
			{Beat: Beat{MonitorID: m.ID, At: at, Status: "up", Latency: 100 * time.Millisecond},
				State: MonitorState{MonitorID: m.ID, Status: "up", Since: at, LastCheck: at}},
			{Beat: Beat{MonitorID: m.ID, At: at.Add(time.Minute), Status: "maintenance", Latency: 900 * time.Millisecond},
				State: MonitorState{MonitorID: m.ID, Status: "maintenance", Since: at.Add(time.Minute), LastCheck: at.Add(time.Minute),
					BeforeStatus: "up", BeforeSince: at}},
		}
		if err := s.Save(ctx, recs); err != nil {
			t.Fatal(err)
		}
		st, err := s.States(ctx)
		if err != nil || st[m.ID].BeforeStatus != "up" || !st[m.ID].BeforeSince.Equal(at) {
			t.Fatalf("state %+v %v", st[m.ID], err)
		}
		// Only the up check counts: maintenance is neither up nor down, and
		// its response time is left out.
		totals, _ := s.HourlyTotals(ctx, at)
		if tt := totals[m.ID]; tt.Up != 1 || tt.Down != 0 || tt.AvgLatency() != 100*time.Millisecond {
			t.Errorf("totals %+v", tt)
		}
		beats, _ := s.Heartbeats(ctx, m.ID, at, 10)
		if len(beats) != 2 || beats[0].Status != "maintenance" {
			t.Errorf("beats %+v", beats)
		}
	})
}
