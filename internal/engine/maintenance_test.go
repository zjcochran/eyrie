// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// window saves a maintenance window; t0 is 2027-01-15T08:00Z.
func (h *harness) window(w store.Window) store.Window {
	h.t.Helper()
	if w.Title == "" {
		w.Title = "Upgrade"
	}
	w, err := h.st.CreateWindow(context.Background(), w, t0)
	if err != nil {
		h.t.Fatal(err)
	}
	if h.cancel != nil {
		if err := h.e.ReloadMaintenance(context.Background()); err != nil {
			h.t.Fatal(err)
		}
	}
	return w
}

func once(from, to string) json.RawMessage {
	return json.RawMessage(`{"strategy":"once","start":"2027-01-15T` + from + `","end":"2027-01-15T` + to + `"}`)
}

// until waits for the heartbeat with this message, skipping others.
func (h *harness) until(msg string) store.Beat {
	h.t.Helper()
	for {
		if b := h.next(); b.Message == msg {
			return b
		}
	}
}

// noNotes checks that nothing was notified.
func (h *harness) noNotes() {
	h.t.Helper()
	select {
	case n := <-h.notes:
		h.t.Fatalf("notified %+v", n)
	default:
	}
}

func TestMaintenanceWindow(t *testing.T) {
	h := newHarness(t)
	h.start()
	m := h.add(monitor("DB", "port", portConfig))
	if b := h.next(); b.Status != "up" {
		t.Fatalf("first %+v", b)
	}
	h.window(store.Window{Enabled: true, Schedule: once("08:10", "08:30"), MonitorIDs: []int64{m.ID}})
	h.none()
	// 08:10: it starts, as a check is due; the port is closed by then.
	h.up.Store(false)
	h.clock.Advance(10 * time.Minute)
	b := h.next()
	if b.Status != "maintenance" || !b.Important || b.Message != "Maintenance: Upgrade" {
		t.Fatalf("start %+v", b)
	}
	if s := h.e.Statuses()[m.ID]; s.Status != state.Maintenance || s.Maintenance != "Upgrade" {
		t.Errorf("status %+v", s)
	}
	// A failing check is recorded and notifies nobody.
	for range 2 {
		if b := h.next(); b.Status != "maintenance" || b.Important || b.Message != "connection refused" {
			t.Fatalf("check in maintenance %+v", b)
		}
		h.clock.Advance(time.Minute)
	}
	h.noNotes()
	// 08:30: it ends; the status before comes back, then the check at once
	// finds it down.
	h.clock.Advance(30*time.Minute - 12*time.Minute)
	if b = h.until("Maintenance ended"); b.Status != "up" || !b.Important {
		t.Fatalf("end %+v", b)
	}
	if b := h.next(); b.Status != "down" {
		t.Fatalf("after %+v", b)
	}
	if n := <-h.notes; n.Status != "down" {
		t.Errorf("notification %+v", n)
	}
	if s := h.e.Statuses()[m.ID]; s.Maintenance != "" || s.Status != state.Down || s.Up != 2 || s.Down != 1 { // the first check and the end
		t.Errorf("after %+v", s)
	}
	if log := h.logged(); !strings.Contains(log, "maintenance started") {
		t.Errorf("log %s", log)
	}
}

func TestMaintenanceCoverage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	outer, _ := h.st.CreateGroup(ctx, store.Group{Name: "Web", Alert: store.AlertNone}, t0)
	group, _ := h.st.CreateGroup(ctx, store.Group{Name: "Shop", ParentID: outer.ID, Alert: store.AlertNone}, t0)
	member := monitor("Shop", "port", portConfig)
	member.GroupID = group.ID
	member = h.add(member)
	tagged := h.add(monitor("Tagged", "manual", `{}`))
	other := h.add(monitor("Other", "manual", `{}`))
	tag, _ := h.st.CreateTag(ctx, store.Tag{Name: "prod", Color: "#000000"}, t0)
	h.st.SetMonitorTags(ctx, tagged.ID, []store.MonitorTag{{TagID: tag.ID}})
	manual := json.RawMessage(`{"strategy":"manual"}`)
	h.window(store.Window{Title: "Group", Enabled: true, Schedule: manual, GroupIDs: []int64{outer.ID}})
	h.window(store.Window{Title: "Tag", Enabled: true, Schedule: manual, TagIDs: []int64{tag.ID}})
	h.window(store.Window{Title: "Off", Enabled: false, Schedule: manual, AllMonitors: true})
	h.window(store.Window{Title: "Broken", Enabled: true, Schedule: json.RawMessage(`{"strategy":"hourly"}`), AllMonitors: true})
	h.start()
	st := h.e.Statuses()
	for id, want := range map[int64]string{member.ID: "Group", tagged.ID: "Tag", other.ID: ""} {
		if st[id].Maintenance != want {
			t.Errorf("monitor %d in %q, want %q", id, st[id].Maintenance, want)
		}
	}
	if !strings.Contains(h.logged(), "bad schedule") {
		t.Errorf("broken window not logged: %s", h.logged())
	}
	// Moving the member out of the group takes it out of maintenance.
	member.GroupID = 0
	h.e.Upsert(ctx, member)
	if s := h.e.Statuses()[member.ID]; s.Maintenance != "" {
		t.Errorf("left the group %+v", s)
	}
	// Taking the tag off ends the tagged monitor's maintenance; it was
	// never set, so it is unknown again.
	h.st.SetMonitorTags(ctx, tagged.ID, nil)
	h.e.ReloadMaintenance(ctx)
	if s := h.e.Statuses()[tagged.ID]; s.Maintenance != "" || s.Status != state.Unknown {
		t.Errorf("untagged %+v", s)
	}
}

func TestMaintenanceManualAndPush(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	man := h.add(monitor("Switch", "manual", `{}`))
	push := h.add(monitor("Backup", "push", `{}`))
	h.start()
	h.e.SetManual(ctx, man.ID, state.Up, "")
	h.next()
	w := h.window(store.Window{Enabled: true, Schedule: json.RawMessage(`{"strategy":"manual"}`), AllMonitors: true})
	h.next()
	h.next()
	// Set during maintenance: kept for its end.
	h.e.SetManual(ctx, man.ID, state.Down, "outage")
	if b := h.next(); b.Status != "maintenance" || b.Message != "outage (after the maintenance)" {
		t.Fatalf("manual %+v", b)
	}
	if err := h.e.Push(ctx, push.ID, false, "disk full", -1); err != nil {
		t.Fatal(err)
	}
	if b := h.next(); b.Status != "maintenance" || b.Message != "disk full" {
		t.Fatalf("push %+v", b)
	}
	h.noNotes()
	// Turned off: the manual monitor is down, without a notification; the
	// push monitor, never pushed before, has an interval to push.
	w.Enabled = false
	h.st.UpdateWindow(ctx, w, t0)
	h.e.ReloadMaintenance(ctx)
	if b := h.next(); b.MonitorID != man.ID || b.Status != "down" || b.Message != "Maintenance ended" {
		t.Fatalf("manual after %+v", b)
	}
	h.noNotes()
	if s := h.e.Statuses()[push.ID]; s.Status != state.Unknown {
		t.Errorf("push after %+v", s)
	}
	h.clock.Advance(time.Minute)
	if b := h.next(); b.MonitorID != push.ID || b.Status != "down" {
		t.Fatalf("missed push %+v", b)
	}
}

func TestMaintenanceRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m := h.add(monitor("DB", "port", portConfig))
	h.st.Save(ctx, []store.Record{{
		Beat: store.Beat{MonitorID: m.ID, At: t0, Status: "maintenance", Latency: -1},
		State: store.MonitorState{MonitorID: m.ID, Status: "maintenance", Since: t0, LastCheck: t0, Latency: -1,
			BeforeStatus: "down", BeforeSince: t0.Add(-time.Hour)},
	}})
	// The window is gone: the status before comes back, still down since an
	// hour ago, and nobody is notified; the port is still closed.
	h.up.Store(false)
	h.start()
	b := h.next()
	if b.Status != "down" || b.Message != "Maintenance ended" {
		t.Fatalf("restart %+v", b)
	}
	if b := h.next(); b.Status != "down" || b.Important {
		t.Fatalf("check after %+v", b)
	}
	if s := h.e.Statuses()[m.ID]; !s.Since.Equal(t0.Add(-time.Hour)) {
		t.Errorf("since %v", s.Since)
	}
	h.noNotes()

	// Still in a window after a restart: it stays in maintenance, and keeps
	// what it had before.
	h2 := newHarness(t)
	m2 := h2.add(monitor("DB", "port", portConfig))
	h2.window(store.Window{Title: "Night", Enabled: true, Schedule: once("07:00", "09:00"), AllMonitors: true})
	h2.st.Save(ctx, []store.Record{{
		Beat: store.Beat{MonitorID: m2.ID, At: t0, Status: "maintenance", Latency: -1},
		State: store.MonitorState{MonitorID: m2.ID, Status: "maintenance", Since: t0, LastCheck: t0, Latency: -1,
			BeforeStatus: "up", BeforeSince: t0.Add(-time.Hour)},
	}})
	h2.start()
	if s := h2.e.Statuses()[m2.ID]; s.Maintenance != "Night" || s.Status != state.Maintenance {
		t.Errorf("still in %+v", s)
	}
	h2.clock.Advance(time.Hour)
	if b := h2.until("Maintenance ended"); b.Status != "up" {
		t.Errorf("ended %+v", b)
	}
}

func TestReloadMaintenanceFails(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.st.Close()
	if err := h.e.ReloadMaintenance(context.Background()); err == nil {
		t.Error("reloaded without a database")
	}
}
