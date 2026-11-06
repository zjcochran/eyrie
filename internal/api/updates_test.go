// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/update"
)

type available struct {
	Current string          `json:"current"`
	Release *update.Release `json:"release"`
}

func TestUpdateSettings(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()

	var v updatesView
	if code := admin.call("GET", "/api/v1/settings/updates", nil, &v); code != http.StatusOK || v.Enabled || v.IntervalHours != 24 ||
		v.Current != "v1.2.3" || v.Repo != "example/eyrie" || !v.Status.Checked.IsZero() || v.Available != nil || v.Channels == nil {
		t.Fatalf("defaults %d %+v", code, v)
	}
	var c channelView
	admin.call("POST", "/api/v1/channels", map[string]any{"name": "Phone", "kind": "webhook", "url": "https://hooks.example.com/x", "enabled": true}, &c)
	ch := c.ID
	body := map[string]any{"enabled": true, "interval_hours": 12, "proxy": "http://proxy.example.com:3128", "proxy_user": "alice",
		"proxy_password": "s3cret", "notify": true, "channels": []int64{ch}}
	if code := admin.call("PUT", "/api/v1/settings/updates", body, &v); code != http.StatusOK || !v.Enabled || v.IntervalHours != 12 ||
		!v.HasProxyPassword || v.ProxyUser != "alice" || len(v.Channels) != 1 {
		t.Fatalf("saved %d %+v", code, v)
	}
	saved, _ := update.LoadSettings(t.Context(), e.st)
	if saved.ProxyPassword == "s3cret" || saved.ProxyPassword == "" {
		t.Errorf("proxy password not sealed: %q", saved.ProxyPassword)
	}
	// An empty password keeps the saved one; clearing removes it.
	delete(body, "proxy_password")
	if admin.call("PUT", "/api/v1/settings/updates", body, &v); !v.HasProxyPassword {
		t.Error("password lost")
	}
	body["clear_proxy_password"] = true
	if admin.call("PUT", "/api/v1/settings/updates", body, &v); v.HasProxyPassword {
		t.Error("password kept")
	}
	if !strings.Contains(e.log.String(), "update settings saved") {
		t.Error("not audited")
	}
}

func TestUpdateSettingsRefused(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	viewer, _ := e.addUser(admin, "bob", "viewer")
	for name, bad := range map[string]map[string]any{
		"interval": {"interval_hours": 0},
		"proxy":    {"interval_hours": 24, "proxy": "ftp://proxy.example.com"},
		"channel":  {"interval_hours": 24, "channels": []int64{999}},
	} {
		if code, msg := admin.errorOf("PUT", "/api/v1/settings/updates", bad); code != http.StatusUnprocessableEntity || msg == "" {
			t.Errorf("%s: %d %q", name, code, msg)
		}
	}
	if code, _ := viewer.errorOf("GET", "/api/v1/settings/updates", nil); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}
}

func TestUpdateReminder(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var a available
	// Off: nothing to remind of, even after a check.
	var v updatesView
	if admin.call("POST", "/api/v1/settings/updates/check", nil, &v); v.Available == nil || v.Available.Version != "v1.3.0" || v.Status.Error != "" {
		t.Fatalf("checked %+v", v)
	}
	if admin.call("GET", "/api/v1/updates", nil, &a); a.Release != nil || a.Current != "v1.2.3" {
		t.Errorf("reminded while off %+v", a)
	}
	admin.call("PUT", "/api/v1/settings/updates", map[string]any{"enabled": true, "interval_hours": 24}, nil)
	if admin.call("GET", "/api/v1/updates", nil, &a); a.Release == nil || a.Release.Version != "v1.3.0" || a.Release.Notes != "- Faster" {
		t.Fatalf("not reminded %+v", a)
	}

	// Later: not for a day.
	if code := admin.call("PUT", "/api/v1/updates/reminder", map[string]string{"version": "v1.3.0", "choice": "later"}, nil); code != http.StatusNoContent {
		t.Fatalf("later: %d", code)
	}
	if admin.call("GET", "/api/v1/updates", nil, &a); a.Release != nil {
		t.Error("reminded too soon")
	}
	e.advance(25 * time.Hour)
	if admin.call("GET", "/api/v1/updates", nil, &a); a.Release == nil {
		t.Error("not reminded a day later")
	}

	// Never: not for this version, but for the next.
	admin.call("PUT", "/api/v1/updates/reminder", map[string]string{"version": "v1.3.0", "choice": "never"}, nil)
	e.advance(25 * time.Hour)
	if admin.call("GET", "/api/v1/updates", nil, &a); a.Release != nil {
		t.Error("reminded after never")
	}
	e.tag.Store("v1.4.0")
	admin.call("POST", "/api/v1/settings/updates/check", nil, nil)
	if admin.call("GET", "/api/v1/updates", nil, &a); a.Release == nil || a.Release.Version != "v1.4.0" {
		t.Errorf("next release not reminded %+v", a)
	}

	for _, bad := range []map[string]string{{"version": "v1.4.0", "choice": "soon"}, {"choice": "never"}} {
		if code, _ := admin.errorOf("PUT", "/api/v1/updates/reminder", bad); code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", bad, code)
		}
	}
	viewer, _ := e.addUser(admin, "bob", "viewer")
	if code, _ := viewer.errorOf("GET", "/api/v1/updates", nil); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}
}

func TestWhatsNew(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	viewer, _ := e.addUser(admin, "bob", "viewer")
	type news struct {
		Upgrade *update.Upgrade `json:"upgrade"`
		URL     string          `json:"url"`
	}
	var n news
	// Not updated: nothing to show.
	if code := viewer.call("GET", "/api/v1/updates/news", nil, &n); code != http.StatusOK || n.Upgrade != nil ||
		n.URL != "https://github.com/example/eyrie/releases" {
		t.Fatalf("not updated %d %+v", code, n)
	}
	e.advance(time.Hour)
	if err := e.st.SetSetting(t.Context(), "running_version", "v1.2.2", e.clock()); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Updates.Started(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Hour)
	late, _ := e.addUser(admin, "carol", "viewer")
	for _, c := range []*client{admin, viewer} {
		if c.call("GET", "/api/v1/updates/news", nil, &n); n.Upgrade == nil || n.Upgrade.From != "v1.2.2" || n.Upgrade.To != "v1.2.3" ||
			n.Upgrade.Releases == nil {
			t.Fatalf("updated %+v", n)
		}
	}
	// A newer account never ran the old version.
	if late.call("GET", "/api/v1/updates/news", nil, &n); n.Upgrade != nil {
		t.Errorf("shown to a newer account %+v", n)
	}
	// Once per user.
	if code := viewer.call("PUT", "/api/v1/updates/news", map[string]string{"version": "v1.2.3"}, nil); code != http.StatusNoContent {
		t.Fatalf("seen: %d", code)
	}
	if viewer.call("GET", "/api/v1/updates/news", nil, &n); n.Upgrade != nil {
		t.Error("shown again")
	}
	if admin.call("GET", "/api/v1/updates/news", nil, &n); n.Upgrade == nil {
		t.Error("seen for everyone")
	}
	if code, _ := viewer.errorOf("PUT", "/api/v1/updates/news", map[string]string{}); code != http.StatusUnprocessableEntity {
		t.Errorf("no version: %d", code)
	}
	if code, _ := e.client().errorOf("GET", "/api/v1/updates/news", nil); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
}
