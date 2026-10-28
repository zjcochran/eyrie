// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestGroupsCRUD(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		ch, _ := s.CreateChannel(ctx, Channel{Name: "ntfy", Kind: "ntfy", URL: "u", Enabled: true}, false, t0)
		off, _ := s.CreateChannel(ctx, Channel{Name: "off", Kind: "ntfy", URL: "u"}, false, t0)
		outer, err := s.CreateGroup(ctx, Group{Name: "Outer", Alert: AlertAny, Channels: []int64{ch.ID, off.ID, ch.ID}}, t0)
		if err != nil || outer.Position != 0 || len(outer.Channels) != 2 || !outer.CreatedAt.Equal(t0) {
			t.Fatalf("outer %+v %v", outer, err)
		}
		if cs, err := s.ChannelsForGroup(ctx, outer.ID); err != nil || len(cs) != 1 || cs[0].ID != ch.ID {
			t.Errorf("enabled channels %+v %v", cs, err)
		}
		inner, err := s.CreateGroup(ctx, Group{Name: "Inner", ParentID: outer.ID, Alert: AlertNone}, t0)
		if err != nil || inner.ParentID != outer.ID {
			t.Fatalf("inner %+v %v", inner, err)
		}
		leaf := newMonitor("Leaf", "http")
		leaf.GroupID = inner.ID
		leaf, _ = s.CreateMonitor(ctx, leaf, t0)
		if leaf.Position != 0 {
			t.Errorf("leaf at %d", leaf.Position)
		}
		root, _ := s.CreateGroup(ctx, Group{Name: "Second", Alert: AlertNone}, t0)
		if root.Position != 1 {
			t.Errorf("second at %d", root.Position)
		}

		inner.Name, inner.Alert, inner.AlertMonitors = "Inside", AlertSelected, []int64{leaf.ID}
		later := t0.Add(time.Hour)
		if inner, err = s.UpdateGroup(ctx, inner, later); err != nil || inner.Name != "Inside" || len(inner.AlertMonitors) != 1 ||
			!inner.UpdatedAt.Equal(later) || inner.Position != 0 {
			t.Errorf("updated %+v %v", inner, err)
		}
		// Out to the top: after Outer and Second.
		inner.ParentID = 0
		if inner, err = s.UpdateGroup(ctx, inner, later); err != nil || inner.Position != 2 {
			t.Errorf("moved %+v %v", inner, err)
		}
	})
}

func TestDeleteGroup(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		outer, _ := s.CreateGroup(ctx, Group{Name: "Outer", Alert: AlertNone}, t0)
		s.CreateGroup(ctx, Group{Name: "Second", Alert: AlertNone}, t0)
		inner, _ := s.CreateGroup(ctx, Group{Name: "Inner", ParentID: outer.ID, Alert: AlertNone}, t0)
		leaf := newMonitor("Leaf", "http")
		leaf.GroupID = inner.ID
		leaf, _ = s.CreateMonitor(ctx, leaf, t0)
		// Deleting Outer moves Inner to the top, after what is there.
		if err := s.DeleteGroup(ctx, outer.ID); err != nil {
			t.Fatal(err)
		}
		if g, err := s.GroupByID(ctx, inner.ID); err != nil || g.ParentID != 0 || g.Position < 2 {
			t.Errorf("inner after delete %+v %v", g, err)
		}
		if err := s.DeleteGroup(ctx, outer.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete again: %v", err)
		}
		if _, err := s.GroupByID(ctx, outer.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted: %v", err)
		}
		if l, _ := s.MonitorByID(ctx, leaf.ID); l.GroupID != inner.ID {
			t.Errorf("leaf in %d", l.GroupID)
		}
	})
}

func TestGroupsRefused(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		outer, _ := s.CreateGroup(ctx, Group{Name: "Outer", Alert: AlertNone}, t0)
		inner, _ := s.CreateGroup(ctx, Group{Name: "Inner", ParentID: outer.ID, Alert: AlertNone}, t0)
		var bad *InvalidError
		for name, g := range map[string]Group{
			"missing parent": {Name: "x", ParentID: 9999, Alert: AlertNone},
			"bad alert":      {Name: "x", Alert: "sometimes"},
			"no channel":     {Name: "x", Alert: AlertAny, Channels: []int64{9999}},
			"no monitor":     {Name: "x", Alert: AlertSelected, AlertMonitors: []int64{9999}},
		} {
			if _, err := s.CreateGroup(ctx, g, t0); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
		outer.ParentID = inner.ID
		if _, err := s.UpdateGroup(ctx, outer, t0); !errors.As(err, &bad) {
			t.Errorf("cycle: %v", err)
		}
		outer.ParentID = outer.ID
		if _, err := s.UpdateGroup(ctx, outer, t0); !errors.As(err, &bad) {
			t.Errorf("itself: %v", err)
		}
		if _, err := s.UpdateGroup(ctx, Group{ID: 9999, Name: "x", Alert: AlertNone}, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("update missing: %v", err)
		}
		if _, err := s.UpdateGroup(ctx, Group{ID: inner.ID, Name: "x", Alert: "?"}, t0); !errors.As(err, &bad) {
			t.Errorf("update bad alert: %v", err)
		}
	})
}

// withoutGroups makes a snapshot look like a backup from before groups:
// none of their tables or columns.
func withoutGroups(snap Snapshot) {
	for _, table := range []string{"monitor_groups", "group_channels", "group_alert_monitors", "maintenance_groups"} {
		delete(snap.Config, table)
	}
	for _, r := range snap.Config["monitors"] {
		delete(r, "group_id")
		delete(r, "position")
	}
}

func TestArrange(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateGroup(ctx, Group{Name: "A", Alert: AlertNone}, t0)
		b, _ := s.CreateGroup(ctx, Group{Name: "B", Alert: AlertNone}, t0)
		m1, _ := s.CreateMonitor(ctx, newMonitor("M1", "http"), t0)
		m2, _ := s.CreateMonitor(ctx, newMonitor("M2", "http"), t0)
		// B into A, after M2; M1 at the top, before A.
		err := s.Arrange(ctx, []Place{{ID: m1.ID}, {Group: true, ID: a.ID}, {ID: m2.ID, GroupID: a.ID}, {Group: true, ID: b.ID, GroupID: a.ID}}, t0)
		if err != nil {
			t.Fatal(err)
		}
		gs, _ := s.Groups(ctx)
		ms, _ := s.Monitors(ctx)
		if gs[0].ID != a.ID || gs[0].Position != 1 || gs[1].ParentID != a.ID || gs[1].Position != 1 ||
			ms[0].ID != m1.ID || ms[0].Position != 0 || ms[1].GroupID != a.ID || ms[1].Position != 0 {
			t.Errorf("groups %+v\\nmonitors %+v", gs, ms)
		}
		var bad *InvalidError
		for name, places := range map[string][]Place{
			"cycle":      {{Group: true, ID: a.ID, GroupID: b.ID}},
			"itself":     {{Group: true, ID: a.ID, GroupID: a.ID}},
			"no group":   {{Group: true, ID: 9999}},
			"no parent":  {{ID: m1.ID, GroupID: 9999}},
			"no monitor": {{ID: 9999}},
		} {
			if err := s.Arrange(ctx, places, t0); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
}

// oldGroups makes group monitors as earlier versions saved them: Site with
// a channel and a window, Inner in it, and Web in Inner.
func oldGroups(t *testing.T, s *Store) (site, inner, web Monitor, ch Channel) {
	t.Helper()
	ctx := context.Background()
	ch, _ = s.CreateChannel(ctx, Channel{Name: "ntfy", Kind: "ntfy", URL: "u", Enabled: true}, false, t0)
	group := func(name string) Monitor {
		m, err := s.CreateMonitor(ctx, Monitor{Name: name, Type: "group", Active: true, Interval: time.Minute,
			RetryInterval: time.Minute, Timeout: time.Second, Config: json.RawMessage(`{}`)}, t0)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	site, inner = group("Site"), group("Inner")
	web, _ = s.CreateMonitor(ctx, newMonitor("Web", "http"), t0)
	for child, parent := range map[int64]int64{inner.ID: site.ID, web.ID: inner.ID} {
		if _, err := s.db.ExecContext(ctx, s.q(`UPDATE monitors SET parent_id = ? WHERE id = ?`), parent, child); err != nil {
			t.Fatal(err)
		}
	}
	s.SetMonitorChannels(ctx, site.ID, []int64{ch.ID})
	if _, err := s.CreatePage(ctx, Page{Slug: "shop", Title: "Shop", Theme: "auto",
		Sections: []Section{{Title: "Core", MonitorIDs: []int64{site.ID}}}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWindow(ctx, Window{Title: "Patch", Enabled: true, Schedule: json.RawMessage(`{"strategy":"manual"}`),
		MonitorIDs: []int64{site.ID, web.ID}}, t0); err != nil {
		t.Fatal(err)
	}
	return site, inner, web, ch
}

func checkConverted(t *testing.T, s *Store, site, inner, web Monitor, ch Channel) {
	t.Helper()
	ctx := context.Background()
	gs, err := s.Groups(ctx)
	if err != nil || len(gs) != 2 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	byID := map[int64]Group{gs[0].ID: gs[0], gs[1].ID: gs[1]}
	g, in := byID[site.ID], byID[inner.ID]
	if g.Name != "Site" || g.ParentID != 0 || g.Alert != AlertAny || len(g.Channels) != 1 || g.Channels[0] != ch.ID ||
		in.Name != "Inner" || in.ParentID != site.ID || in.Alert != AlertNone {
		t.Errorf("site %+v inner %+v", g, in)
	}
	checkConvertedRest(t, s, site, inner, web)
}

func checkConvertedRest(t *testing.T, s *Store, site, inner, web Monitor) {
	t.Helper()
	ctx := context.Background()
	ms, _ := s.Monitors(ctx)
	if len(ms) != 1 || ms[0].ID != web.ID || ms[0].GroupID != inner.ID {
		t.Errorf("monitors %+v", ms)
	}
	if ps, _ := s.Pages(ctx); len(ps) != 1 || len(ps[0].Sections[0].MonitorIDs) != 1 || ps[0].Sections[0].MonitorIDs[0] != web.ID {
		t.Errorf("pages %+v", ps)
	}
	ws, _ := s.Windows(ctx)
	if len(ws) != 1 || len(ws[0].MonitorIDs) != 1 || ws[0].MonitorIDs[0] != web.ID || len(ws[0].GroupIDs) != 1 || ws[0].GroupIDs[0] != site.ID {
		t.Errorf("window %+v", ws)
	}
	// New groups get new ids.
	n, err := s.CreateGroup(ctx, Group{Name: "New", Alert: AlertNone}, t0)
	if err != nil || n.ID <= inner.ID || n.ID <= site.ID {
		t.Errorf("new %+v %v", n, err)
	}
}

func TestConvertGroupMonitors(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		site, inner, web, ch := oldGroups(t, s)
		if err := s.convertOnOpen(context.Background()); err != nil {
			t.Fatal(err)
		}
		checkConverted(t, s, site, inner, web, ch)
		// Once done, it does nothing.
		if err := s.convertOnOpen(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRestoreOldGroups(t *testing.T) {
	ctx := context.Background()
	src := openSQLite(t)
	if _, err := src.CreateFirstAdmin(ctx, "alice", "a long password here", t0); err != nil {
		t.Fatal(err)
	}
	site, inner, web, ch := oldGroups(t, src)
	snap, err := src.Export(ctx, true, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	withoutGroups(snap) // as a backup from before groups
	eachDB(t, func(t *testing.T, dst *Store) {
		fill(t, dst) // replaced by the restore
		if err := dst.Import(ctx, roundTrip(t, snap), false); err != nil {
			t.Fatal(err)
		}
		checkConverted(t, dst, site, inner, web, ch)
	})
}
