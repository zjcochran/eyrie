// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

func apiSettings() Settings {
	return Settings{Enabled: true, Provider: ProviderAPI, APIKey: "sealed:sk-ant-api03-test", TimeoutS: 5}
}

func TestAnalyzeWithTheAPI(t *testing.T) {
	a := newAnalyzer(t)
	m := seed(t, a)
	f := newFakeAPI(t, http.StatusOK, reply("  The upstream behind the proxy is failing.  "))
	a.APIURL = f.srv.URL
	if got := a.Analyze(m, store.Beat{At: t0, Status: "down"}); got != "" {
		t.Errorf("off, but got %q", got)
	}
	var log bytes.Buffer
	a.Log = slog.New(slog.NewTextHandler(&log, nil))
	a.SetSettings(apiSettings())
	if got := a.Analyze(m, store.Beat{At: t0, Status: "down", Message: "Status 502"}); got != "The upstream behind the proxy is failing." {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(log.String(), `level=INFO msg="asking the model to analyse the alert" monitor=`) {
		t.Errorf("log %s", log.String())
	}
	bodies, headers := f.requests()
	b, h := bodies[0], headers[0]
	if b.Model != DefaultModel || b.Fallbacks != "default" || b.System != system || b.MaxTokens != 16000 ||
		len(b.Messages) != 1 || !strings.Contains(b.Messages[0].Content, "Status 502") {
		t.Errorf("body %+v", b)
	}
	if h.Get("X-Api-Key") != "sk-ant-api03-test" || h.Get("Anthropic-Version") != "2023-06-01" ||
		h.Get("Anthropic-Beta") != "server-side-fallback-2026-07-01" || h.Get("User-Agent") != "Eyrie/test" {
		t.Errorf("headers %v", h)
	}
	// A model without server-side fallback gets neither the field nor the beta.
	s := apiSettings()
	s.Model = "claude-haiku-5-5"
	a.SetSettings(s)
	a.Analyze(m, store.Beat{At: t0, Status: "down"})
	bodies, headers = f.requests()
	if bodies[1].Model != "claude-haiku-5-5" || bodies[1].Fallbacks != "" || headers[1].Get("Anthropic-Beta") != "" {
		t.Errorf("haiku %+v %v", bodies[1], headers[1])
	}
	if ok, failed := a.Counts(); ok != 2 || failed != 0 {
		t.Errorf("counts %d %d", ok, failed)
	}
}

func TestAPIErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			"answered 401 Unauthorized: invalid x-api-key"},
		{http.StatusBadGateway, `<html>`, "answered 502 Bad Gateway"},
		{http.StatusTeapot, `{}`, "answered 418"},
		{http.StatusOK, `{"content":[],"stop_reason":"refusal"}`, "declined"},
		{http.StatusOK, `{"content":[{"type":"text","text":"  "}],"stop_reason":"end_turn"}`, "empty"},
	}
	for _, c := range cases {
		a := newAnalyzer(t)
		a.APIURL = newFakeAPI(t, c.status, c.body).srv.URL
		if _, err := a.Test(context.Background(), Settings{Provider: ProviderAPI, APIKey: "k", TimeoutS: 5}); err == nil ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
	a := newAnalyzer(t)
	a.APIURL = "http://[::1]:namedport"
	if _, err := a.Test(context.Background(), Settings{Provider: ProviderAPI, TimeoutS: 5}); err == nil {
		t.Error("bad URL")
	}
	a.APIURL, a.Client = "", &http.Client{Transport: failing{}}
	if _, err := a.Test(context.Background(), Settings{Provider: ProviderAPI, TimeoutS: 5}); err == nil || !strings.Contains(err.Error(), "api.anthropic.com") {
		t.Errorf("default URL: %v", err)
	}
}

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }

func TestAnalyzeFailures(t *testing.T) {
	a := newAnalyzer(t)
	m := seed(t, a)
	f := newFakeAPI(t, http.StatusOK, reply("late"))
	f.wait = 10 * time.Second
	a.APIURL, a.second = f.srv.URL, 10*time.Millisecond
	s := apiSettings()
	a.SetSettings(s)
	start := time.Now()
	if got := a.Analyze(m, store.Beat{At: t0, Status: "down"}); got != "" || time.Since(start) > 5*time.Second {
		t.Errorf("slow answer %q after %s", got, time.Since(start))
	}
	if _, err := a.Test(context.Background(), s); err == nil || !strings.Contains(err.Error(), "within 5 s") {
		t.Errorf("test timeout: %v", err)
	}
	// A key that does not open fails before any request.
	s.APIKey = "plain"
	a.SetSettings(s)
	if a.Analyze(m, store.Beat{At: t0}) != "" {
		t.Error("bad key")
	}
	if ok, failed := a.Counts(); ok != 0 || failed != 2 {
		t.Errorf("counts %d %d", ok, failed)
	}
}

func TestAnalyzeAtMostFourAtOnce(t *testing.T) {
	a := newAnalyzer(t)
	m := seed(t, a)
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(maxRunning)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started.Done()
		<-release
		io.WriteString(w, reply("ok"))
	}))
	defer srv.Close()
	a.APIURL = srv.URL
	a.SetSettings(apiSettings())
	results := make(chan string, maxRunning)
	for range maxRunning {
		go func() { results <- a.Analyze(m, store.Beat{At: t0}) }()
	}
	started.Wait()
	if got := a.Analyze(m, store.Beat{At: t0}); got != "" {
		t.Errorf("fifth ran: %q", got)
	}
	close(release)
	for range maxRunning {
		if r := <-results; r != "ok" {
			t.Errorf("result %q", r)
		}
	}
}

// fakeServer is an OpenAI-compatible server answering with status and
// body, and recording the last request.
func fakeServer(t *testing.T, status int, body string) (*httptest.Server, *chatRequest, *http.Request) {
	var got chatRequest
	var req http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		req = *r
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got, &req
}

func chatReply(content, finish string) string {
	b, _ := json.Marshal(map[string]any{"choices": []map[string]any{
		{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": finish}}})
	return string(b)
}

func TestLocal(t *testing.T) {
	a := newAnalyzer(t)
	m := seed(t, a)
	srv, got, req := fakeServer(t, http.StatusOK, chatReply("<think>\nThe proxy...\n</think>\n\nCheck the proxy.", "stop"))
	a.SetSettings(Settings{Enabled: true, Provider: ProviderLocal, BaseURL: srv.URL + "/v1", Model: "llama3.2",
		LocalKey: "sealed:l0cal", TimeoutS: 5})
	if text := a.Analyze(m, store.Beat{At: t0, Status: "down", Message: "Status 502"}); text != "Check the proxy." {
		t.Fatalf("%q", text)
	}
	if req.URL.Path != "/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer l0cal" ||
		req.Header.Get("User-Agent") != "Eyrie/test" {
		t.Errorf("request %s %v", req.URL, req.Header)
	}
	if got.Model != "llama3.2" || got.Stream || len(got.Messages) != 2 || got.Messages[0].Role != "system" ||
		got.Messages[0].Content != system || !strings.Contains(got.Messages[1].Content, "Status 502") {
		t.Errorf("body %+v", got)
	}
	// Without a key, no Authorization header.
	text, err := a.Test(context.Background(), Settings{Provider: ProviderLocal, BaseURL: srv.URL, Model: "llama3.2", TimeoutS: 5})
	if err != nil || text != "Check the proxy." || req.Header.Get("Authorization") != "" ||
		!strings.Contains(got.Messages[1].Content, "Status 502 Bad Gateway") {
		t.Errorf("test %q %v %v", text, err, req.Header)
	}
}

func TestLocalErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusNotFound, `{"error":{"message":"model \"llama3.2\" not found, try pulling it first","type":"api_error"}}`,
			`answered 404 Not Found: model "llama3.2" not found`},
		{http.StatusInternalServerError, `{"error":"out of memory"}`, "answered 500 Internal Server Error: out of memory"},
		{http.StatusBadGateway, `<html>`, "answered 502 Bad Gateway"},
		{http.StatusOK, `{"choices":[]}`, "answered 200 OK"},
		{http.StatusOK, chatReply("", "content_filter"), "declined"},
		{http.StatusOK, chatReply("<think>hmm</think>  ", "stop"), "empty"},
	}
	for _, c := range cases {
		a := newAnalyzer(t)
		srv, _, _ := fakeServer(t, c.status, c.body)
		if _, err := a.Test(context.Background(), Settings{Provider: ProviderLocal, BaseURL: srv.URL, Model: "m", TimeoutS: 5}); err == nil ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
	a := newAnalyzer(t)
	a.Client = &http.Client{Transport: failing{}}
	if _, err := a.Test(context.Background(), Settings{Provider: ProviderLocal, BaseURL: "http://192.0.2.20:11434/v1", Model: "m", TimeoutS: 5}); err == nil ||
		!strings.Contains(err.Error(), "192.0.2.20:11434/v1/chat/completions") {
		t.Errorf("offline: %v", err)
	}
}
