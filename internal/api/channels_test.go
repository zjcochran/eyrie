// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zjcochran/eyrie/internal/notify"
)

// hook records webhook requests.
type hook struct {
	srv  *httptest.Server
	mu   sync.Mutex
	got  []string
	sigs []string
}

func newHook(t *testing.T) *hook {
	h := &hook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.got = append(h.got, string(b))
		h.sigs = append(h.sigs, r.Header.Get("X-Hub-Signature-256"))
		h.mu.Unlock()
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.got...)
}

func webhookBody(h *hook, extra map[string]any) map[string]any {
	b := map[string]any{"name": "Hooks", "kind": "webhook", "url": h.srv.URL + "/secret-path", "token": "s3cret", "enabled": true}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func TestChannelsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	h := newHook(t)
	if code, msg := admin.errorOf("POST", "/api/v1/channels", map[string]any{"name": "x", "kind": "gotify", "url": "https://gotify.example.com"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "application token") {
		t.Errorf("invalid: %d %q", code, msg)
	}
	var c channelView
	if code := admin.call("POST", "/api/v1/channels", webhookBody(h, nil), &c); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if c.URLHost != h.srv.URL || !c.HasURL || !c.HasToken || c.HasUser || !c.Enabled || c.Last != nil {
		t.Errorf("view %+v", c)
	}
	raw, _ := e.st.ChannelByID(context.Background(), c.ID)
	if !strings.HasPrefix(raw.URL, "v1:") || !strings.HasPrefix(raw.Token, "v1:") || strings.Contains(raw.URL, "secret-path") {
		t.Errorf("stored %+v", raw)
	}
}

func TestChannelSecretsKept(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	h := newHook(t)
	var c channelView
	admin.call("POST", "/api/v1/channels", webhookBody(h, nil), &c)
	path := fmt.Sprintf("/api/v1/channels/%d", c.ID)
	// Empty secrets keep the saved ones; clear_token removes the token.
	if code := admin.call("PUT", path, map[string]any{"name": "Renamed", "kind": "webhook", "enabled": true}, &c); code != http.StatusOK ||
		c.Name != "Renamed" || !c.HasURL || !c.HasToken {
		t.Errorf("rename: %d %+v", code, c)
	}
	if admin.call("PUT", path, map[string]any{"name": "Renamed", "kind": "webhook", "enabled": true, "clear_token": true}, &c); c.HasToken {
		t.Errorf("clear token %+v", c)
	}
	if code, _ := admin.errorOf("PUT", path, map[string]any{"name": "Renamed", "kind": "webhook", "clear_url": true}); code != http.StatusUnprocessableEntity {
		t.Errorf("clear the URL a webhook needs: %d", code)
	}
	for _, r := range [][2]string{{"PUT", "/api/v1/channels/999"}, {"DELETE", "/api/v1/channels/999"}} {
		if code := admin.call(r[0], r[1], webhookBody(h, nil), nil); code != http.StatusNotFound {
			t.Errorf("%s unknown: %d", r[0], code)
		}
	}
	var list []channelView
	if admin.call("GET", "/api/v1/channels", nil, &list); len(list) != 1 {
		t.Errorf("list %+v", list)
	}
	var kinds []struct{ ID, Name string }
	if admin.call("GET", "/api/v1/channel-kinds", nil, &kinds); len(kinds) != 7 || kinds[0].ID != "ntfy" {
		t.Errorf("kinds %+v", kinds)
	}
	if code := admin.call("DELETE", path, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	bob, _ := e.addUser(admin, "bob", "viewer")
	if code := bob.call("GET", "/api/v1/channels", nil, nil); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}
}

func TestChannelTest(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	h := newHook(t)
	var res ssoTest
	// Unsaved.
	if admin.call("POST", "/api/v1/channels/test", webhookBody(h, nil), &res); !res.OK || res.Message != "Sent." {
		t.Errorf("test %+v", res)
	}
	var c channelView
	admin.call("POST", "/api/v1/channels", webhookBody(h, nil), &c)
	// Saved: the stored URL and secret are used.
	if admin.call("POST", "/api/v1/channels/test", map[string]any{"id": c.ID, "kind": "webhook"}, &res); !res.OK {
		t.Errorf("saved test %+v", res)
	}
	got := h.messages()
	if len(got) != 2 || !strings.Contains(got[1], `"event":"test"`) || h.sigs[1] != notify.Signature([]byte(got[1]), "s3cret") {
		t.Errorf("received %v %v", got, h.sigs)
	}
	var list []channelView
	if admin.call("GET", "/api/v1/channels", nil, &list); list[0].Last == nil || list[0].Last.Error != "" {
		t.Errorf("last %+v", list[0].Last)
	}
	h.srv.Close()
	if admin.call("POST", "/api/v1/channels/test", webhookBody(h, nil), &res); res.OK || res.Message == "" {
		t.Errorf("unreachable %+v", res)
	}
	if code := admin.call("POST", "/api/v1/channels/test", map[string]any{"id": 999, "kind": "webhook"}, nil); code != http.StatusNotFound {
		t.Errorf("unknown saved channel: %d", code)
	}
}

func TestMonitorNotifications(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	h := newHook(t)
	var ch channelView
	admin.call("POST", "/api/v1/channels", webhookBody(h, map[string]any{"default": true}), &ch)
	other := create(t, admin, map[string]any{"name": "Other", "type": "manual", "channels": []int64{}})
	m := create(t, admin, portMonitor("DB"))
	if len(m.Channels) != 1 || m.Channels[0] != ch.ID || len(other.Channels) != 0 {
		t.Fatalf("channels %v %v", m.Channels, other.Channels)
	}
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	e.up.Store(false)
	admin.call("POST", fmt.Sprintf("/api/v1/monitors/%d/check", m.ID), nil, nil)
	eventually(t, "the down notification", func() bool { return len(h.messages()) == 1 })
	var msg notify.Message
	json.Unmarshal([]byte(h.messages()[0]), &msg)
	if msg.Event != notify.EventDown || msg.Monitor.Name != "DB" || msg.URL != fmt.Sprintf("https://eyrie.example.com/monitors/%d", m.ID) {
		t.Errorf("message %+v", msg)
	}
	// Channels of a monitor can change; unknown ones are refused.
	body := portMonitor("DB")
	body["channels"] = []int64{}
	var got monitorView
	if admin.call("PUT", fmt.Sprintf("/api/v1/monitors/%d", m.ID), body, &got); len(got.Channels) != 0 {
		t.Errorf("channels removed: %v", got.Channels)
	}
	body["channels"] = []int64{999}
	if code, _ := admin.errorOf("PUT", fmt.Sprintf("/api/v1/monitors/%d", m.ID), body); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown channel: %d", code)
	}
	created := portMonitor("Bad")
	created["channels"] = []int64{999}
	if code, _ := admin.errorOf("POST", "/api/v1/monitors", created); code != http.StatusUnprocessableEntity {
		t.Errorf("create with an unknown channel: %d", code)
	}
	var list []monitorView
	if admin.call("GET", "/api/v1/monitors", nil, &list); len(list) != 2 {
		t.Errorf("the failed create left a monitor: %d", len(list))
	}
	c := e.client()
	_, metrics := c.do("GET", "/metrics", nil, "Authorization", "Bearer m3trics")
	if !strings.Contains(string(metrics), `eyrie_notifications_total{result="sent"} 1`) {
		t.Errorf("metrics %s", metrics)
	}
}

func TestNotifySettings(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v notify.Settings
	if admin.call("GET", "/api/v1/settings/notifications", nil, &v); len(v.CertExpiryDays) != 3 {
		t.Errorf("defaults %+v", v)
	}
	if code, _ := admin.errorOf("PUT", "/api/v1/settings/notifications", map[string]any{"cert_expiry_days": []int{0}}); code != http.StatusUnprocessableEntity {
		t.Errorf("zero days: %d", code)
	}
	if code := admin.call("PUT", "/api/v1/settings/notifications", map[string]any{"cert_expiry_days": []int{3, 30, 3}}, &v); code != http.StatusOK ||
		len(v.CertExpiryDays) != 2 || v.CertExpiryDays[0] != 30 {
		t.Errorf("save: %d %+v", code, v)
	}
	if d := e.notifier.CertDays(); len(d) != 2 {
		t.Errorf("not applied: %v", d)
	}
}
