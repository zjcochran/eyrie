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
	"testing"

	"github.com/zjcochran/eyrie/internal/analysis"
	"github.com/zjcochran/eyrie/internal/notify"
)

func TestAnalysisSettings(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v analysisView
	if admin.call("GET", "/api/v1/settings/analysis", nil, &v); v.Enabled || v.Provider != "api" || v.TimeoutS != 60 ||
		v.DefaultModel != analysis.DefaultModel || v.BaseURL != "" || v.HasAPIKey || v.HasLocalAPIKey {
		t.Errorf("defaults %+v", v)
	}
	for body, want := range map[string]string{
		`{"enabled": true, "provider": "api", "timeout_s": 60}`:                                "needs an API key",
		`{"enabled": true, "provider": "api", "timeout_s": 60, "api_key": "sk-ant-oat01-abc"}`: "subscription token",
		`{"provider": "nope", "timeout_s": 60}`:                                                "Unknown provider",
		`{"provider": "api", "timeout_s": 1}`:                                                  "from 5 to 300",
		`{"enabled": true, "provider": "local", "timeout_s": 60, "model": "llama3.2"}`:         "server URL and the model",
		`{"provider": "local", "timeout_s": 60, "base_url": "192.0.2.20:11434"}`:               "not a server URL",
	} {
		var b map[string]any
		json.Unmarshal([]byte(body), &b)
		if code, msg := admin.errorOf("PUT", "/api/v1/settings/analysis", b); code != http.StatusUnprocessableEntity || !strings.Contains(msg, want) {
			t.Errorf("%s: %d %q", body, code, msg)
		}
	}
}

func TestAnalysisKeyKept(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v analysisView
	key := map[string]any{"enabled": true, "provider": "api", "timeout_s": 30, "model": "claude-sonnet-5-5", "api_key": "sk-ant-api03-s3cret"}
	if code := admin.call("PUT", "/api/v1/settings/analysis", key, &v); code != http.StatusOK || !v.HasAPIKey || !v.Enabled ||
		v.Model != "claude-sonnet-5-5" {
		t.Errorf("save: %d %+v", code, v)
	}
	var raw map[string]any
	if _, err := e.st.GetSetting(context.Background(), analysis.SettingKey, &raw); err != nil ||
		strings.Contains(fmt.Sprint(raw), "s3cret") {
		t.Errorf("stored %v %v", raw, err)
	}
	if s := e.analyzer.Settings(); !s.Enabled || s.TimeoutS != 30 {
		t.Errorf("not applied %+v", s)
	}
	// Turning it off keeps the key; clear_api_key removes it.
	off := map[string]any{"provider": "api", "timeout_s": 30}
	if admin.call("PUT", "/api/v1/settings/analysis", off, &v); v.Enabled || !v.HasAPIKey {
		t.Errorf("off %+v", v)
	}
	// A local server's key is its own: the Anthropic key stays.
	local := map[string]any{"enabled": true, "provider": "local", "timeout_s": 30, "model": "llama3.2",
		"base_url": "http://192.0.2.20:11434/v1/", "api_key": "l0cal"}
	if admin.call("PUT", "/api/v1/settings/analysis", local, &v); !v.HasLocalAPIKey || !v.HasAPIKey ||
		v.BaseURL != "http://192.0.2.20:11434/v1" {
		t.Errorf("local %+v", v)
	}
	if s := e.analyzer.Settings(); s.LocalKey == "" || s.LocalKey == "l0cal" || s.APIKey == "" {
		t.Errorf("local keys %+v", s)
	}
	local["api_key"], local["clear_api_key"] = "", true
	if admin.call("PUT", "/api/v1/settings/analysis", local, &v); v.HasLocalAPIKey || !v.HasAPIKey {
		t.Errorf("local cleared %+v", v)
	}
	off["clear_api_key"] = true
	if admin.call("PUT", "/api/v1/settings/analysis", off, &v); v.HasAPIKey {
		t.Errorf("cleared %+v", v)
	}
}

func TestAnalysisSettingsAdminOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, _ := e.addUser(admin, "bob", "viewer")
	if code := bob.call("GET", "/api/v1/settings/analysis", nil, nil); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}
}

func TestAnalysisTest(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var res ssoTest
	local := map[string]any{"provider": "local", "timeout_s": 10, "model": "llama3.2", "base_url": e.model.URL + "/v1"}
	if admin.call("POST", "/api/v1/settings/analysis/test", local, &res); !res.OK ||
		!strings.Contains(res.Message, "database is likely stopped") {
		t.Errorf("local %+v", res)
	}
	if p := <-e.prompts; !strings.Contains(p, "Shop website") {
		t.Errorf("prompt %q", p)
	}
	e.answer.Store(`{"error":{"message":"model \"llama3.2\" not found, try pulling it first"}}`)
	if admin.call("POST", "/api/v1/settings/analysis/test", local, &res); res.OK || !strings.Contains(res.Message, "not found") {
		t.Errorf("no model %+v", res)
	}
	<-e.prompts
	// The API with the saved key: the form leaves the key blank.
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		io.WriteString(w, `{"content":[{"type":"text","text":"Upstream down."}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	e.analyzer.APIURL = srv.URL
	admin.call("PUT", "/api/v1/settings/analysis", map[string]any{"provider": "api", "timeout_s": 10, "api_key": "sk-ant-api03-k"}, nil)
	if admin.call("POST", "/api/v1/settings/analysis/test", map[string]any{"provider": "api", "timeout_s": 10}, &res); !res.OK ||
		res.Message != "Upstream down." || gotKey != "sk-ant-api03-k" {
		t.Errorf("api %+v %q", res, gotKey)
	}
	if code, _ := admin.errorOf("POST", "/api/v1/settings/analysis/test", map[string]any{"provider": "api", "timeout_s": 10, "clear_api_key": true}); code != http.StatusUnprocessableEntity {
		t.Errorf("no key: %d", code)
	}
}

func TestAlertCarriesAnalysis(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	h := newHook(t)
	admin.call("POST", "/api/v1/channels", webhookBody(h, map[string]any{"default": true}), nil)
	admin.call("PUT", "/api/v1/settings/analysis", map[string]any{"enabled": true, "provider": "local", "timeout_s": 10,
		"model": "llama3.2", "base_url": e.model.URL + "/v1"}, nil)
	m := create(t, admin, portMonitor("DB"))
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	e.up.Store(false)
	admin.call("POST", fmt.Sprintf("/api/v1/monitors/%d/check", m.ID), nil, nil)
	eventually(t, "the down notification", func() bool { return len(h.messages()) == 1 })
	var msg notify.Message
	json.Unmarshal([]byte(h.messages()[0]), &msg)
	if msg.Event != notify.EventDown || msg.Analysis != "The port is closed: the database is likely stopped." {
		t.Errorf("message %+v", msg)
	}
	if p := <-e.prompts; !strings.Contains(p, "Monitor: DB (TCP port)") || !strings.Contains(p, "Address: db.example.com:5432") {
		t.Errorf("prompt %s", p)
	}
	_, metrics := e.client().do("GET", "/metrics", nil, "Authorization", "Bearer m3trics")
	if !strings.Contains(string(metrics), `eyrie_alert_analyses_total{result="ok"} 1`) {
		t.Errorf("metrics %s", metrics)
	}
}

func TestAnalysisModels(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var res modelsAnswer
	if admin.call("POST", "/api/v1/settings/analysis/models", map[string]any{"provider": "local", "timeout_s": 60,
		"base_url": e.model.URL + "/v1"}, &res); res.Error != "" || strings.Join(res.Models, ",") != "gemma3:12b,qwen3:14b" {
		t.Errorf("local %+v", res)
	}
	if admin.call("POST", "/api/v1/settings/analysis/models", map[string]any{"provider": "api", "timeout_s": 60}, &res); res.Error != "Enter the API key first." ||
		res.Models == nil {
		t.Errorf("no key %+v", res)
	}
	if code, _ := admin.errorOf("POST", "/api/v1/settings/analysis/models", map[string]any{"provider": "local", "timeout_s": 60,
		"base_url": "192.0.2.20"}); code != http.StatusUnprocessableEntity {
		t.Errorf("bad URL: %d", code)
	}
}

func TestAnalysisClaudeCode(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v analysisView
	if admin.call("GET", "/api/v1/settings/analysis", nil, &v); v.ClaudeCode != nil {
		t.Errorf("off %+v", v.ClaudeCode)
	}
	cc := map[string]any{"enabled": true, "provider": "claude-code", "timeout_s": 60}
	if code, msg := admin.errorOf("PUT", "/api/v1/settings/analysis", cc); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "EYRIE_CLAUDE_CODE=true") {
		t.Errorf("off: %d %q", code, msg)
	}
	// On, still downloading.
	e.analyzer.Claude = &analysis.ClaudeCode{Dir: t.TempDir()}
	if code := admin.call("PUT", "/api/v1/settings/analysis", cc, &v); code != http.StatusOK || v.Provider != "claude-code" ||
		v.ClaudeCode == nil || v.ClaudeCode.State != "installing" {
		t.Errorf("on: %d %+v %+v", code, v, v.ClaudeCode)
	}
	var res ssoTest
	if admin.call("POST", "/api/v1/settings/analysis/test", cc, &res); res.OK || !strings.Contains(res.Message, "not finished") {
		t.Errorf("test %+v", res)
	}
}
