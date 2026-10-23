// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/zjcochran/eyrie/internal/store"
)

func storeWindow(schedule string) store.Window {
	return store.Window{Title: "Old", Enabled: true, Schedule: json.RawMessage(schedule), AllMonitors: true}
}

func TestTagsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var prod, db tagView
	if code := admin.call("POST", "/api/v1/tags", map[string]any{"name": "prod", "color": "#1F5FBF"}, &prod); code != http.StatusCreated ||
		prod.Color != "#1f5fbf" {
		t.Fatalf("create %d %+v", code, prod)
	}
	admin.call("POST", "/api/v1/tags", map[string]any{"name": "db", "color": "#12a150"}, &db)
	if code, msg := admin.errorOf("POST", "/api/v1/tags", map[string]any{"name": "Prod", "color": "#000000"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "already a tag") {
		t.Errorf("duplicate %d %q", code, msg)
	}
	body := portMonitor("DB")
	body["tags"] = []map[string]any{{"id": prod.ID, "value": "eu"}, {"id": db.ID}}
	m := create(t, admin, body)
	if len(m.Tags) != 2 || m.Tags[0].Name != "db" || m.Tags[1].Value != "eu" || m.Tags[1].Color != "#1f5fbf" {
		t.Errorf("monitor tags %+v", m.Tags)
	}
	// Left out, the tags stay; a bad tag is refused.
	body["tags"] = nil
	delete(body, "tags")
	admin.call("PUT", fmt.Sprintf("/api/v1/monitors/%d", m.ID), body, &m)
	if len(m.Tags) != 2 {
		t.Errorf("kept %+v", m.Tags)
	}
	body["tags"] = []map[string]any{{"id": 999}}
	if code, _ := admin.errorOf("PUT", fmt.Sprintf("/api/v1/monitors/%d", m.ID), body); code != http.StatusUnprocessableEntity {
		t.Errorf("bad tag: %d", code)
	}
	bad := portMonitor("Bad")
	bad["tags"] = []map[string]any{{"id": 999}}
	if code, _ := admin.errorOf("POST", "/api/v1/monitors", bad); code != http.StatusUnprocessableEntity {
		t.Errorf("create with a bad tag: %d", code)
	}
	var tags []tagView
	if admin.call("GET", "/api/v1/tags", nil, &tags); len(tags) != 2 || tags[0].Name != "db" || tags[1].Monitors != 1 {
		t.Errorf("list %+v", tags)
	}
}

func TestTagRenameDelete(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var prod, db tagView
	admin.call("POST", "/api/v1/tags", map[string]any{"name": "prod", "color": "#1f5fbf"}, &prod)
	admin.call("POST", "/api/v1/tags", map[string]any{"name": "db", "color": "#12a150"}, &db)
	body := portMonitor("DB")
	body["tags"] = []map[string]any{{"id": prod.ID}, {"id": db.ID}}
	m := create(t, admin, body)
	if code := admin.call("PUT", fmt.Sprintf("/api/v1/tags/%d", db.ID), map[string]any{"name": "database", "color": "#12a150"}, &db); code != http.StatusOK ||
		db.Name != "database" {
		t.Errorf("rename %d %+v", code, db)
	}
	if code := admin.call("PUT", "/api/v1/tags/999", map[string]any{"name": "x", "color": "#000000"}, nil); code != http.StatusNotFound {
		t.Errorf("rename missing: %d", code)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/tags/%d", db.ID), nil, nil); code != http.StatusNoContent {
		t.Errorf("delete %d", code)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/tags/%d", db.ID), nil, nil); code != http.StatusNotFound {
		t.Errorf("delete again %d", code)
	}
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &m)
	if len(m.Tags) != 1 || m.Tags[0].ID != prod.ID {
		t.Errorf("after delete %+v", m.Tags)
	}
}

func TestTagsAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, _ := e.addUser(admin, "bob", "viewer")
	if code := bob.call("GET", "/api/v1/tags", nil, nil); code != http.StatusOK {
		t.Errorf("viewer list: %d", code)
	}
	if code := bob.call("GET", "/api/v1/maintenance", nil, nil); code != http.StatusOK {
		t.Errorf("viewer windows: %d", code)
	}
	for _, r := range [][2]string{{"POST", "/api/v1/tags"}, {"PUT", "/api/v1/tags/1"}, {"DELETE", "/api/v1/tags/1"},
		{"POST", "/api/v1/maintenance"}, {"PUT", "/api/v1/maintenance/1"}, {"DELETE", "/api/v1/maintenance/1"}} {
		if code := bob.call(r[0], r[1], map[string]any{}, nil); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: %d", r[0], r[1], code)
		}
	}
}

func TestMaintenanceAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, portMonitor("DB"))
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	manual := map[string]any{"strategy": "manual"}
	var w windowView
	if code := admin.call("POST", "/api/v1/maintenance", map[string]any{"title": " Upgrade ", "schedule": manual,
		"monitors": []int64{m.ID}}, &w); code != http.StatusCreated || w.Title != "Upgrade" || !w.Enabled || w.State != "on" ||
		!slices.Equal(w.Monitors, []int64{m.ID}) || w.Schedule.Strategy != "manual" {
		t.Fatalf("create %d %+v", code, w)
	}
	var mv monitorView
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &mv)
	if mv.Status.Status != "maintenance" || mv.Status.Maintenance != "Upgrade" {
		t.Errorf("monitor %+v", mv.Status)
	}
	_, metrics := e.client().do("GET", "/metrics", nil, "Authorization", "Bearer m3trics")
	if !strings.Contains(string(metrics), fmt.Sprintf(`eyrie_monitor_status{id="%d",monitor="DB",type="port"} 3`, m.ID)) {
		t.Errorf("metrics %s", metrics)
	}
	// Turned off: the monitor comes back.
	if code := admin.call("PUT", fmt.Sprintf("/api/v1/maintenance/%d", w.ID), map[string]any{"title": "Upgrade", "enabled": false,
		"schedule": manual, "all_monitors": true, "monitors": []int64{m.ID}}, &w); code != http.StatusOK || w.State != "paused" ||
		!w.AllMonitors || len(w.Monitors) != 0 {
		t.Fatalf("update %d %+v", code, w)
	}
	if s := statusOf(admin, m.ID); s != "up" {
		t.Errorf("after %s", s)
	}
	var list []windowView
	if admin.call("GET", "/api/v1/maintenance", nil, &list); len(list) != 1 {
		t.Errorf("list %+v", list)
	}
	var one windowView
	if code := admin.call("GET", fmt.Sprintf("/api/v1/maintenance/%d", w.ID), nil, &one); code != http.StatusOK || one.Title != "Upgrade" {
		t.Errorf("get %d %+v", code, one)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/maintenance/%d", w.ID), nil, nil); code != http.StatusNoContent {
		t.Errorf("delete %d", code)
	}
}

func TestMaintenanceStates(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	for want, sched := range map[string]map[string]any{
		"scheduled": {"strategy": "once", "start": "2099-01-15T22:00", "end": "2099-01-16T02:00"},
		"ended":     {"strategy": "once", "start": "2020-01-15T22:00", "end": "2020-01-16T02:00"},
	} {
		var w windowView
		admin.call("POST", "/api/v1/maintenance", map[string]any{"title": want, "schedule": sched, "all_monitors": true}, &w)
		if w.State != want || (want == "scheduled") != (w.Change != nil) {
			t.Errorf("%s: %+v", want, w)
		}
	}
	// A schedule saved before it stopped compiling is never on.
	w, _ := e.st.CreateWindow(t.Context(), storeWindow(`{"strategy":"hourly"}`), e.clock())
	if v := e.s.viewWindow(w); v.State != "ended" {
		t.Errorf("broken %+v", v)
	}
}

func TestMaintenanceValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	manual := map[string]any{"strategy": "manual"}
	long := strings.Repeat("x", 2001)
	for want, body := range map[string]map[string]any{
		"title must be":       {"title": " ", "schedule": manual, "all_monitors": true},
		"description must be": {"title": "x", "description": long, "schedule": manual, "all_monitors": true},
		"choose what":         {"title": "x", "schedule": manual},
		"unknown schedule":    {"title": "x", "schedule": map[string]any{"strategy": "hourly"}, "all_monitors": true},
		"no monitor 999":      {"title": "x", "schedule": manual, "monitors": []int64{999}},
	} {
		if code, msg := admin.errorOf("POST", "/api/v1/maintenance", body); code != http.StatusUnprocessableEntity ||
			!strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
			t.Errorf("%s: %d %q", want, code, msg)
		}
	}
	for _, r := range [][2]string{{"GET", "/api/v1/maintenance/99"}, {"DELETE", "/api/v1/maintenance/99"}, {"PUT", "/api/v1/maintenance/99"}} {
		body := map[string]any{"title": "x", "schedule": manual, "all_monitors": true}
		if code := admin.call(r[0], r[1], body, nil); code != http.StatusNotFound {
			t.Errorf("%s %s: %d", r[0], r[1], code)
		}
	}
}
