// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"context"
	"errors"
	"net/http"
	"slices"
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
	a.SetSettings(apiSettings())
	if got := a.Analyze(m, store.Beat{At: t0, Status: "down", Message: "Status 502"}); got != "The upstream behind the proxy is failing." {
		t.Errorf("got %q", got)
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
	a.Run = func(context.Context, string, []byte, string, ...string) ([]byte, error) {
		started.Done()
		<-release
		return []byte(`{"is_error":false,"result":"ok"}`), nil
	}
	a.SetSettings(Settings{Enabled: true, Provider: ProviderClaudeCode, TimeoutS: 60})
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

func TestClaudeCode(t *testing.T) {
	a := newAnalyzer(t)
	var gotDir, gotName string
	var gotArgs []string
	var gotStdin []byte
	a.Command = "/opt/claude/bin/claude"
	a.Run = func(_ context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error) {
		gotDir, gotStdin, gotName, gotArgs = dir, stdin, name, args
		return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Check the proxy."}` + "\n"), nil
	}
	text, err := a.Test(context.Background(), Settings{Provider: ProviderClaudeCode, Model: "opus", TimeoutS: 5})
	if err != nil || text != "Check the proxy." {
		t.Fatalf("%q %v", text, err)
	}
	if gotName != "/opt/claude/bin/claude" || gotDir == "" || !strings.Contains(string(gotStdin), "Status 502 Bad Gateway") {
		t.Errorf("ran %s in %q with %q", gotName, gotDir, gotStdin)
	}
	want := []string{"-p", "--output-format", "json", "--max-turns", "1", "--tools", "", "--strict-mcp-config",
		"--disable-slash-commands", "--no-session-persistence", "--system-prompt", system, "--model", "opus"}
	if !slices.Equal(gotArgs, want) {
		t.Errorf("args %q", gotArgs)
	}
}

func TestClaudeCodeErrors(t *testing.T) {
	cases := []struct {
		out  string
		err  error
		want string
	}{
		{`{"is_error":true,"result":"Not logged in · Please run /login"}`, errors.New("exit status 1"), "Not logged in"},
		{``, errors.New(`exec: "claude": executable file not found in $PATH`), "not found"},
		{`not json`, nil, "did not answer in JSON"},
		{`{"is_error":false,"result":""}`, errors.New("exit status 2"), "exit status 2"},
		{`{"is_error":false,"result":""}`, nil, "empty"},
	}
	for _, c := range cases {
		a := newAnalyzer(t)
		a.Run = func(context.Context, string, []byte, string, ...string) ([]byte, error) { return []byte(c.out), c.err }
		if _, err := a.Test(context.Background(), Settings{Provider: ProviderClaudeCode, TimeoutS: 5}); err == nil ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %v: %v", c.out, c.err, err)
		}
	}
}

func TestRunCommand(t *testing.T) {
	out, err := runCommand(context.Background(), t.TempDir(), []byte("hello"), "cat")
	if err != nil || string(out) != "hello" {
		t.Errorf("%q %v", out, err)
	}
	if _, err := runCommand(context.Background(), t.TempDir(), nil, "sh", "-c", "echo oops >&2; exit 3"); err == nil ||
		!strings.Contains(err.Error(), "oops") {
		t.Errorf("stderr: %v", err)
	}
	a := &Analyzer{Command: "sh"}
	if p, err := a.ClaudeCode(); err != nil || !strings.HasSuffix(p, "/sh") {
		t.Errorf("look up %q %v", p, err)
	}
	if (&Analyzer{}).command() != "claude" {
		t.Error("default command")
	}
}
