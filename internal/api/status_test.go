// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// shopPage makes a status page with a section of the monitors tagged
// prod and one naming db, and returns it.
func shopPage(t *testing.T, admin *client, published bool, tag int64, db int64) pageView {
	t.Helper()
	var p pageView
	body := map[string]any{"slug": "shop", "title": "Shop status", "published": published, "show_tags": true,
		"sections": []map[string]any{{"title": "Web", "tag_id": tag}, {"title": "Data", "monitors": []int64{db}}}}
	if code := admin.call("POST", "/api/v1/status-pages", body, &p); code != http.StatusCreated {
		t.Fatalf("create page: %d %+v", code, p)
	}
	return p
}

// setupShop makes a tag, two monitors (Web tagged prod, DB not) and the
// page; it waits for both monitors' first checks.
func setupShop(t *testing.T, admin *client, published bool) (pageView, monitorView, monitorView) {
	t.Helper()
	var tag tagView
	admin.call("POST", "/api/v1/tags", map[string]any{"name": "prod", "color": "#1f5fbf"}, &tag)
	web := portMonitor("Web")
	web["tags"] = []map[string]any{{"id": tag.ID, "value": "eu"}}
	wm := create(t, admin, web)
	db := create(t, admin, portMonitor("DB"))
	// Up, and saved: heartbeats are written about once a second.
	eventually(t, "the first checks", func() bool {
		var ms []monitorView
		admin.call("GET", "/api/v1/monitors", nil, &ms)
		for _, m := range ms {
			if m.Status.Status != "up" || m.Uptime.Day == nil {
				return false
			}
		}
		return len(ms) == 2
	})
	return shopPage(t, admin, published, tag.ID, db.ID), wm, db
}

func TestStatusPagesAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	p, _, db := setupShop(t, admin, false)
	if p.Slug != "shop" || p.Theme != "auto" || len(p.Sections) != 2 || p.Sections[0].TagID == nil || p.Sections[1].Monitors[0] != db.ID {
		t.Fatalf("page %+v", p)
	}
	if code, msg := admin.errorOf("POST", "/api/v1/status-pages", map[string]any{"slug": "shop", "title": "x"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "address") {
		t.Errorf("same address %d %q", code, msg)
	}
	body := map[string]any{"slug": "shop", "title": "Shop", "published": true, "theme": "dark", "accent": "#12a150"}
	if code := admin.call("PUT", fmt.Sprintf("/api/v1/status-pages/%d", p.ID), body, &p); code != http.StatusOK || !p.Published ||
		p.Theme != "dark" || len(p.Sections) != 0 {
		t.Errorf("update %d %+v", code, p)
	}
	if code := admin.call("PUT", "/api/v1/status-pages/999", body, nil); code != http.StatusNotFound {
		t.Errorf("update missing %d", code)
	}
	var list []pageView
	if admin.call("GET", "/api/v1/status-pages", nil, &list); len(list) != 1 {
		t.Errorf("list %+v", list)
	}
	bob, _ := e.addUser(admin, "bob", "viewer")
	if code := bob.call("GET", fmt.Sprintf("/api/v1/status-pages/%d", p.ID), nil, nil); code != http.StatusOK {
		t.Errorf("viewer get %d", code)
	}
	for _, r := range [][2]string{{"POST", "/api/v1/status-pages"}, {"PUT", fmt.Sprintf("/api/v1/status-pages/%d", p.ID)},
		{"DELETE", fmt.Sprintf("/api/v1/status-pages/%d", p.ID)}, {"POST", fmt.Sprintf("/api/v1/status-pages/%d/incidents", p.ID)}} {
		if code := bob.call(r[0], r[1], map[string]any{}, nil); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: %d", r[0], r[1], code)
		}
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/status-pages/%d", p.ID), nil, nil); code != http.StatusNoContent {
		t.Errorf("delete %d", code)
	}
	if code := admin.call("GET", fmt.Sprintf("/api/v1/status-pages/%d", p.ID), nil, nil); code != http.StatusNotFound {
		t.Errorf("after delete %d", code)
	}
}

func TestPublicStatus(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	p, web, _ := setupShop(t, admin, false)
	anon := e.client()
	// Not published: only signed-in users see it.
	if code := anon.call("GET", "/api/v1/status/shop", nil, nil); code != http.StatusNotFound {
		t.Errorf("draft for anyone %d", code)
	}
	var out publicPage
	if code := admin.call("GET", "/api/v1/status/shop", nil, &out); code != http.StatusOK || out.Published {
		t.Errorf("preview %d %+v", code, out)
	}
	p.Published = true
	admin.call("PUT", fmt.Sprintf("/api/v1/status-pages/%d", p.ID), p, nil)
	resp, raw := anon.do("GET", "/api/v1/status/shop", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("public %d %s", resp.StatusCode, raw)
	}
	// Names and statuses only: no check messages or settings.
	for _, secret := range []string{"Connected", "db.example.com", "5432", "alice"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%q in the public page: %s", secret, raw)
		}
	}
	anon.call("GET", "/api/v1/status/shop", nil, &out)
	if out.Status != "up" || len(out.Sections) != 2 || out.Sections[0].Monitors[0].Name != "Web" || out.Sections[1].Monitors[0].Name != "DB" {
		t.Fatalf("public %+v", out)
	}
	m := out.Sections[0].Monitors[0]
	if m.Status != "up" || len(m.Days) != statusDays || m.Days[statusDays-1].Uptime == nil || m.Uptime.Day == nil ||
		len(m.Tags) != 1 || m.Tags[0].Value != "eu" || m.ID != web.ID {
		t.Errorf("monitor %+v", m)
	}
	if code := anon.call("GET", "/api/v1/status/nope", nil, nil); code != http.StatusNotFound {
		t.Errorf("unknown page %d", code)
	}
}

func TestStatusIncidentsAndMaintenance(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	p, web, _ := setupShop(t, admin, true)
	var in incidentView
	if code := admin.call("POST", fmt.Sprintf("/api/v1/status-pages/%d/incidents", p.ID), map[string]any{"title": "Checkout errors",
		"status": "investigating", "message": "Payments fail."}, &in); code != http.StatusCreated || in.Updates[0].Author != "alice" {
		t.Fatalf("open %d %+v", code, in)
	}
	admin.call("POST", "/api/v1/maintenance", map[string]any{"title": "Upgrade", "schedule": map[string]any{"strategy": "manual"},
		"monitors": []int64{web.ID}}, nil)
	var out publicPage
	e.client().call("GET", "/api/v1/status/shop", nil, &out)
	if len(out.Incidents) != 1 || out.Incidents[0].Updates[0].Author != "" || out.Incidents[0].Updates[0].Message != "Payments fail." {
		t.Errorf("incidents %+v", out.Incidents)
	}
	if out.Status != "maintenance" || len(out.Maintenance) != 1 || out.Maintenance[0].Title != "Upgrade" || out.Maintenance[0].State != "on" {
		t.Errorf("maintenance %s %+v", out.Status, out.Maintenance)
	}
	if m := out.Sections[0].Monitors[0]; m.Status != "maintenance" {
		t.Errorf("monitor %+v", m)
	}
}

func TestIncidentsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var p pageView
	admin.call("POST", "/api/v1/status-pages", map[string]any{"slug": "shop", "title": "Shop"}, &p)
	path := fmt.Sprintf("/api/v1/status-pages/%d/incidents", p.ID)
	var in incidentView
	admin.call("POST", path, map[string]any{"title": "Slow", "status": "investigating", "message": "Looking."}, &in)
	if code := admin.call("POST", fmt.Sprintf("/api/v1/incidents/%d/updates", in.ID), map[string]any{"status": "resolved",
		"message": "Fixed."}, &in); code != http.StatusOK || in.Status != "resolved" || in.ResolvedAt == nil || len(in.Updates) != 2 {
		t.Errorf("resolve %d %+v", code, in)
	}
	if code := admin.call("PUT", fmt.Sprintf("/api/v1/incidents/%d", in.ID), map[string]any{"title": "Slow pages"}, &in); code != http.StatusOK ||
		in.Title != "Slow pages" {
		t.Errorf("rename %d %+v", code, in)
	}
	var list []incidentView
	if admin.call("GET", path, nil, &list); len(list) != 1 || list[0].Updates[0].Author != "alice" {
		t.Errorf("list %+v", list)
	}
	if code, msg := admin.errorOf("POST", path, map[string]any{"title": "x", "status": "fixed", "message": "m"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "status") {
		t.Errorf("bad status %d %q", code, msg)
	}
	for _, r := range [][2]string{{"POST", "/api/v1/incidents/999/updates"}, {"PUT", "/api/v1/incidents/999"},
		{"DELETE", "/api/v1/incidents/999"}, {"POST", "/api/v1/status-pages/999/incidents"}, {"GET", "/api/v1/status-pages/999/incidents"}} {
		if code := admin.call(r[0], r[1], map[string]any{"title": "x", "status": "resolved", "message": "m"}, nil); code != http.StatusNotFound {
			t.Errorf("%s %s: %d", r[0], r[1], code)
		}
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/incidents/%d", in.ID), nil, nil); code != http.StatusNoContent {
		t.Errorf("delete %d", code)
	}
}

func TestBadges(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	p, web, db := setupShop(t, admin, false)
	anon := e.client()
	badge := func(path string) (int, string) {
		resp, b := anon.do("GET", path, nil)
		return resp.StatusCode, string(b)
	}
	// Not on a published page: no badge.
	if code, _ := badge(fmt.Sprintf("/api/badge/%d/status", web.ID)); code != http.StatusNotFound {
		t.Errorf("unpublished %d", code)
	}
	p.Published = true
	admin.call("PUT", fmt.Sprintf("/api/v1/status-pages/%d", p.ID), p, nil)
	for path, want := range map[string]string{
		fmt.Sprintf("/api/badge/%d/status", web.ID):            "Web: up",
		fmt.Sprintf("/api/badge/%d/status?label=Shop", db.ID):  "Shop: up",
		fmt.Sprintf("/api/badge/%d/uptime", web.ID):            "uptime 24h: 100%",
		fmt.Sprintf("/api/badge/%d/uptime?period=30d", web.ID): "uptime 30d: 100%",
		fmt.Sprintf("/api/badge/%d/uptime?period=1y", web.ID):  "uptime 1y: 100%",
		fmt.Sprintf("/api/badge/%d/response", web.ID):          "response: ",
		fmt.Sprintf("/api/badge/%d/cert", web.ID):              "certificate: none",
	} {
		code, svg := badge(path)
		if code != http.StatusOK || !strings.Contains(svg, "<title>"+want) {
			t.Errorf("%s: %d %s", path, code, svg)
		}
	}
	for _, path := range []string{"/api/badge/x/status", "/api/badge/999/status", fmt.Sprintf("/api/badge/%d/nope", web.ID),
		fmt.Sprintf("/api/badge/%d/uptime?period=2w", web.ID)} {
		if code, _ := badge(path); code != http.StatusNotFound {
			t.Errorf("%s: %d", path, code)
		}
	}
	if percent(0.99996) != "99.99%" || percent(1) != "100%" {
		t.Error("percent")
	}
	if millis(0.512) != "0.51 ms" || millis(3.25) != "3.2 ms" || millis(123.4) != "123 ms" {
		t.Error("millis")
	}
}

func TestPageLogo(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var p pageView
	admin.call("POST", "/api/v1/status-pages", map[string]any{"slug": "shop", "title": "Shop", "published": true}, &p)
	path := fmt.Sprintf("/api/v1/status-pages/%d/logo", p.ID)
	if code := admin.call("PUT", path, png, &p); code != http.StatusOK || !strings.HasPrefix(p.LogoURL, "/api/v1/status/shop/logo?v=") {
		t.Fatalf("upload %d %+v", code, p)
	}
	resp, _ := e.client().do("GET", p.LogoURL, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("logo %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if code, _ := admin.errorOf("PUT", path, []byte("not an image")); code != http.StatusUnprocessableEntity {
		t.Errorf("bad image %d", code)
	}
	if code := admin.call("PUT", "/api/v1/status-pages/999/logo", png, nil); code != http.StatusNotFound {
		t.Errorf("missing page %d", code)
	}
	if code := admin.call("DELETE", path, nil, &p); code != http.StatusOK || p.LogoURL != "" {
		t.Errorf("remove %d %+v", code, p)
	}
	if resp, _ := e.client().do("GET", "/api/v1/status/shop/logo", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("after remove %d", resp.StatusCode)
	}
}
