// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// prefix "seals" by prefixing, so tests can see what was sealed.
type prefix struct{}

func (prefix) Seal(s string) (string, error) { return "sealed:" + s, nil }
func (prefix) Open(s string) (string, error) {
	if !strings.HasPrefix(s, "sealed:") {
		return "", errors.New("not sealed")
	}
	return strings.TrimPrefix(s, "sealed:"), nil
}

var t0 = time.Date(2027, 1, 15, 8, 30, 0, 0, time.UTC)

// fakeAPI is a Claude API that answers with answer and records requests.
type fakeAPI struct {
	srv     *httptest.Server
	mu      sync.Mutex
	bodies  []messagesRequest
	headers []http.Header
	status  int
	answer  string
	wait    time.Duration
}

func newFakeAPI(t *testing.T, status int, answer string) *fakeAPI {
	f := &fakeAPI{status: status, answer: answer}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b messagesRequest
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.bodies, f.headers = append(f.bodies, b), append(f.headers, r.Header.Clone())
		wait := f.wait
		f.mu.Unlock()
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case <-time.After(wait):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.answer)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) requests() ([]messagesRequest, []http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]messagesRequest(nil), f.bodies...), append([]http.Header(nil), f.headers...)
}

func reply(text string) string {
	b, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "thinking", "text": ""}, {"type": "text", "text": text}},
		"stop_reason": "end_turn"})
	return string(b)
}

func newAnalyzer(t *testing.T) *Analyzer {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Analyzer{Store: st, Secrets: prefix{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test",
		Now: func() time.Time { return t0 }}
}

// seed makes a group and a monitor in it with a few heartbeats.
func seed(t *testing.T, a *Analyzer) store.Monitor {
	ctx := context.Background()
	g, err := a.Store.CreateMonitor(ctx, store.Monitor{Name: "Web", Type: "group", Active: true, Interval: time.Minute,
		RetryInterval: time.Minute, Timeout: time.Second, Config: json.RawMessage(`{}`)}, t0)
	if err != nil {
		t.Fatal(err)
	}
	m, err := a.Store.CreateMonitor(ctx, store.Monitor{Name: "Shop website", Type: "http", ParentID: g.ID, Active: true,
		Interval: time.Minute, RetryInterval: 30 * time.Second, Retries: 2, Timeout: 48 * time.Second, Description: "the shop",
		Config: json.RawMessage(`{"url":"https://bob:` + "pw" + `@shop.example.com/health?key=k3y","method":"GET","headers":{"Authorization":"Bearer t0ken"},` +
			`"accepted_status_codes":["200-299"],"max_redirects":10}`)}, t0)
	if err != nil {
		t.Fatal(err)
	}
	var recs []store.Record
	for i, s := range []string{"up", "up", "pending"} {
		at := t0.Add(time.Duration(i-3) * time.Minute)
		lat := time.Duration(100*(i+1)) * time.Millisecond
		if s == "pending" {
			lat = -1
		}
		recs = append(recs, store.Record{Beat: store.Beat{MonitorID: m.ID, At: at, Status: s, Latency: lat, Message: "msg " + s},
			State: store.MonitorState{MonitorID: m.ID, Status: s, Since: at, LastCheck: at, Latency: lat}})
	}
	if err := a.Store.Save(ctx, recs); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLatency(t *testing.T) {
	for d, want := range map[time.Duration]string{512 * time.Microsecond: "0.51 ms", 3250 * time.Microsecond: "3.2 ms", 150 * time.Millisecond: "150 ms"} {
		if got := latency(d); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
}

func TestPrompt(t *testing.T) {
	a := newAnalyzer(t)
	m := seed(t, a)
	p, err := a.prompt(context.Background(), m, store.Beat{At: t0, Status: "down", Message: "Status 503 Service Unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<monitor_data>\nMonitor: Shop website (HTTP(S))\nIn group: Web\nDescription: the shop\n",
		"Checked every 1m0s, timeout 48s; down after 2 failed retries, 30s apart",
		"URL: https://shop.example.com/health\n", "Request headers (values not shown): Authorization",
		"Alert: down at 2027-01-15T08:30:00Z: Status 503 Service Unavailable",
		"Last 24 hours: 100.00% of 3 checks up, average response time 150 ms",
		"Last 3 checks, newest first:\n2027-01-15T08:29:00Z pending msg pending\n2027-01-15T08:28:00Z up 200 ms msg up\n",
		"</monitor_data>"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q in\n%s", want, p)
		}
	}
	for _, secret := range []string{"bob", "pw@", "k3y", "t0ken"} {
		if strings.Contains(p, secret) {
			t.Errorf("%q leaks into\n%s", secret, p)
		}
	}
	if !strings.Contains(render(store.Monitor{Type: "nope", UpsideDown: true}, store.Beat{}, "", nil, nil, store.Totals{}), "(nope)\n") {
		t.Error("unknown type")
	}
}

func TestSettingsNormalize(t *testing.T) {
	ok := []Settings{DefaultSettings(), {Enabled: true, Provider: ProviderAPI, APIKey: "sealed:k", TimeoutS: 300},
		{Enabled: true, Provider: ProviderLocal, Model: " llama3.2 ", BaseURL: " https://llm.example.com/v1/ ", TimeoutS: 5}}
	for _, s := range ok {
		if err := s.Normalize(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	if s := ok[2]; s.Normalize() != nil || s.Model != "llama3.2" || s.BaseURL != "https://llm.example.com/v1" {
		t.Errorf("trimmed %+v", s)
	}
	bad := map[string]Settings{
		"unknown provider": {Provider: "x", TimeoutS: 60},
		"from 5 to 300":    {Provider: ProviderAPI, TimeoutS: 4},
		"not a model name": {Provider: ProviderLocal, TimeoutS: 60, Model: "-x"},
		"needs an API key": {Enabled: true, Provider: ProviderAPI, TimeoutS: 60},
		"server URL and the model": {Enabled: true, Provider: ProviderLocal, BaseURL: "http://192.0.2.20:11434/v1",
			TimeoutS: 60},
		"not a server URL": {Provider: ProviderLocal, BaseURL: "192.0.2.20:11434", TimeoutS: 60},
	}
	for _, u := range []string{"ftp://192.0.2.20/v1", "http:///v1", "http://bob:x@192.0.2.20/v1", "http://192.0.2.20/v1?k=1", "http://192.0.2.20/v1#x", "http://[::1"} {
		s := Settings{Provider: ProviderLocal, BaseURL: u, TimeoutS: 60}
		if err := s.Normalize(); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	for want, s := range bad {
		if err := s.Normalize(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v", s, err)
		}
	}
	s := Settings{Provider: ProviderLocal}
	if s.Key() != &s.LocalKey {
		t.Error("local key")
	}
	if CheckKey("sk-ant-oat01-abc") == nil || CheckKey("a b") == nil || CheckKey("sk-ant-api03-abc") != nil {
		t.Error("CheckKey")
	}
	a := newAnalyzer(t)
	if s := a.Settings(); s.Enabled || s.Provider != ProviderAPI {
		t.Errorf("default %+v", s)
	}
	loaded, err := LoadSettings(context.Background(), a.Store)
	if err != nil || loaded != DefaultSettings() {
		t.Errorf("load %+v %v", loaded, err)
	}
}

func TestShorten(t *testing.T) {
	if Shorten("héllo", 5) != "héllo" || Shorten("héllo wörld", 7) != "héllo…" {
		t.Errorf("%q", Shorten("héllo wörld", 7))
	}
}
