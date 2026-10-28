// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/zjcochran/eyrie/internal/notify"
)

func newGroup(t *testing.T, admin *client, body map[string]any) groupView {
	t.Helper()
	var g groupView
	if code := admin.call("POST", "/api/v1/groups", body, &g); code != http.StatusCreated {
		t.Fatalf("create group %v: %d", body, code)
	}
	return g
}

func TestGroupsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	site := newGroup(t, admin, map[string]any{"name": " Site "})
	if site.Name != "Site" || site.Alert != "none" || site.ParentID != nil || len(site.Channels) != 0 {
		t.Errorf("site %+v", site)
	}
	shop := newGroup(t, admin, map[string]any{"name": "Shop", "parent_id": site.ID})
	m := create(t, admin, map[string]any{"name": "Cart", "type": "manual", "group_id": shop.ID})
	for body, want := range map[string]string{
		`{"name": ""}`:                        "name",
		`{"name": "x", "alert": "selected"}`:  "choose the members",
		`{"name": "x", "alert": "sometimes"}`: "unknown alert",
		`{"name": "x", "parent_id": 9999}`:    "no group",
	} {
		var b map[string]any
		json.Unmarshal([]byte(body), &b)
		if code, msg := admin.errorOf("POST", "/api/v1/groups", b); code != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(msg), want) {
			t.Errorf("%s: %d %q", body, code, msg)
		}
	}
	path := fmt.Sprintf("/api/v1/groups/%d", shop.ID)
	var got groupView
	if code := admin.call("PUT", path, map[string]any{"name": "Shop", "parent_id": site.ID, "alert": "selected",
		"alert_monitors": []int64{m.ID}}, &got); code != http.StatusOK || got.Alert != "selected" || len(got.AlertMonitors) != 1 {
		t.Errorf("update %d %+v", code, got)
	}
	if code, _ := admin.errorOf("PUT", fmt.Sprintf("/api/v1/groups/%d", site.ID), map[string]any{"name": "Site", "parent_id": shop.ID}); code != http.StatusUnprocessableEntity {
		t.Errorf("cycle: %d", code)
	}
	if code, _ := admin.errorOf("PUT", "/api/v1/groups/9999", map[string]any{"name": "x"}); code != http.StatusNotFound {
		t.Errorf("update missing: %d", code)
	}
}

func TestGroupsAPIAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	site := newGroup(t, admin, map[string]any{"name": "Site"})
	shop := newGroup(t, admin, map[string]any{"name": "Shop", "parent_id": site.ID})
	m := create(t, admin, map[string]any{"name": "Cart", "type": "manual", "group_id": shop.ID})
	path := fmt.Sprintf("/api/v1/groups/%d", shop.ID)
	var got groupView
	// Anyone signed in sees groups; only admins change them.
	viewer, _ := e.addUser(admin, "bob", "viewer")
	var list []groupView
	if code := viewer.call("GET", "/api/v1/groups", nil, &list); code != http.StatusOK || len(list) != 2 {
		t.Errorf("viewer list %d %+v", code, list)
	}
	if code := viewer.call("GET", path, nil, &got); code != http.StatusOK || got.ID != shop.ID {
		t.Errorf("viewer get %d", code)
	}
	if code, _ := viewer.errorOf("POST", "/api/v1/groups", map[string]any{"name": "x"}); code != http.StatusForbidden {
		t.Errorf("viewer create %d", code)
	}
	// Deleting Shop moves Cart up into Site.
	if code := admin.call("DELETE", path, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete %d", code)
	}
	if code := admin.call("DELETE", path, nil, nil); code != http.StatusNotFound {
		t.Errorf("delete again %d", code)
	}
	var after monitorView
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &after)
	if after.GroupID == nil || *after.GroupID != site.ID {
		t.Errorf("cart in %v", after.GroupID)
	}
	if !strings.Contains(e.log.String(), "group deleted") {
		t.Error("not audited")
	}
}

func TestLayout(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	g := newGroup(t, admin, map[string]any{"name": "Site"})
	a := create(t, admin, map[string]any{"name": "A", "type": "manual"})
	b := create(t, admin, map[string]any{"name": "B", "type": "manual"})
	layout := map[string]any{"items": []map[string]any{
		{"type": "monitor", "id": b.ID},
		{"type": "group", "id": g.ID},
		{"type": "monitor", "id": a.ID, "group_id": g.ID},
	}}
	if code := admin.call("PUT", "/api/v1/layout", layout, nil); code != http.StatusNoContent {
		t.Fatalf("layout %d", code)
	}
	var ma, mb monitorView
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", a.ID), nil, &ma)
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", b.ID), nil, &mb)
	if mb.Position != 0 || mb.GroupID != nil || ma.Position != 0 || ma.GroupID == nil || *ma.GroupID != g.ID {
		t.Errorf("a %+v\nb %+v", ma, mb)
	}
	var gs []groupView
	admin.call("GET", "/api/v1/groups", nil, &gs)
	if gs[0].Position != 1 {
		t.Errorf("group %+v", gs)
	}
	for name, items := range map[string][]map[string]any{
		"type":    {{"type": "folder", "id": g.ID}},
		"missing": {{"type": "monitor", "id": 9999}},
		"itself":  {{"type": "group", "id": g.ID, "group_id": g.ID}},
	} {
		if code, _ := admin.errorOf("PUT", "/api/v1/layout", map[string]any{"items": items}); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := admin.errorOf("PUT", "/api/v1/layout", map[string]any{"items": make([]map[string]any, maxLayout+1)}); code != http.StatusUnprocessableEntity {
		t.Errorf("too many: %d", code)
	}
}

func TestGroupAlertReachesChannel(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	h := newHook(t)
	var c struct {
		ID int64 `json:"id"`
	}
	admin.call("POST", "/api/v1/channels", webhookBody(h, nil), &c)
	g := newGroup(t, admin, map[string]any{"name": "Databases", "alert": "any", "channels": []int64{c.ID}})
	body := portMonitor("DB")
	body["group_id"], body["channels"] = g.ID, []int64{}
	m := create(t, admin, body)
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	e.up.Store(false)
	admin.call("POST", fmt.Sprintf("/api/v1/monitors/%d/check", m.ID), nil, nil)
	eventually(t, "the group alert", func() bool { return len(h.messages()) == 1 })
	var msg notify.Message
	json.Unmarshal([]byte(h.messages()[0]), &msg)
	if msg.Event != notify.EventGroupDown || msg.Title != "Databases is down" || msg.Text != "Down: DB" {
		t.Errorf("message %+v", msg)
	}
}

func TestMaintenanceByGroup(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	g := newGroup(t, admin, map[string]any{"name": "Site"})
	m := create(t, admin, map[string]any{"name": "A", "type": "manual", "group_id": g.ID})
	var w windowView
	if code := admin.call("POST", "/api/v1/maintenance", map[string]any{"title": "Upgrade", "schedule": map[string]any{"strategy": "manual"},
		"groups": []int64{g.ID}}, &w); code != http.StatusCreated || len(w.Groups) != 1 {
		t.Fatalf("window %d %+v", code, w)
	}
	eventually(t, "maintenance", func() bool { return statusOf(admin, m.ID) == "maintenance" })
	if code, msg := admin.errorOf("POST", "/api/v1/maintenance", map[string]any{"title": "x", "schedule": map[string]any{"strategy": "manual"},
		"groups": []int64{9999}}); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "no group") {
		t.Errorf("missing group: %d %q", code, msg)
	}
}
