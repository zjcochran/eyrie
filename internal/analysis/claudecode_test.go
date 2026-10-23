// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// releases is a fake of Anthropic's Claude Code releases.
type releases struct {
	srv       *httptest.Server
	mu        sync.Mutex
	stable    string
	binary    string // the linux-x64 binary
	checksum  string // "": the binary's
	downloads atomic.Int32
}

func newReleases(t *testing.T) *releases {
	r := &releases{stable: "2.1.308", binary: "#!/bin/sh\necho claude\n"}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		sum := sha256.Sum256([]byte(r.binary))
		checksum := hex.EncodeToString(sum[:])
		if r.checksum != "" {
			checksum = r.checksum
		}
		switch req.URL.Path {
		case "/stable":
			io.WriteString(w, r.stable+"\n")
		case "/" + r.stable + "/manifest.json":
			json.NewEncoder(w).Encode(map[string]any{"version": r.stable, "platforms": map[string]any{
				"linux-x64": map[string]any{"checksum": checksum, "size": len(r.binary)}}})
		case "/" + r.stable + "/linux-x64/claude":
			r.downloads.Add(1)
			io.WriteString(w, r.binary)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *releases) set(f func(*releases)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(r)
}

func newClaudeCode(t *testing.T, r *releases) *ClaudeCode {
	return &ClaudeCode{Dir: filepath.Join(t.TempDir(), "claude-code"), BaseURL: r.srv.URL, Platform: "linux-x64",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestClaudeCodeInstall(t *testing.T) {
	r := newReleases(t)
	c := newClaudeCode(t, r)
	if s := c.Status(); s.State != "installing" {
		t.Errorf("before %+v", s)
	}
	if _, err := c.Path(); err == nil || !strings.Contains(err.Error(), "not finished") {
		t.Errorf("before: %v", err)
	}
	if err := c.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	path, err := c.Path()
	if err != nil || path != filepath.Join(c.Dir, "2.1.308", "claude") || c.Status() != (ClaudeCodeStatus{State: "ready", Version: "2.1.308"}) {
		t.Fatalf("%q %v %+v", path, err, c.Status())
	}
	if b, _ := os.ReadFile(path); string(b) != r.binary {
		t.Errorf("binary %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", fi.Mode())
	}
	// Already there: not downloaded again.
	if err := c.Install(context.Background()); err != nil || r.downloads.Load() != 1 {
		t.Errorf("again: %v, %d downloads", err, r.downloads.Load())
	}
}

func TestClaudeCodeUpdate(t *testing.T) {
	r := newReleases(t)
	c := newClaudeCode(t, r)
	if err := c.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A new stable release replaces the old one.
	r.set(func(r *releases) { r.stable, r.binary = "2.1.309", "#!/bin/sh\necho newer\n" })
	if err := c.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p, _ := c.Path(); p != filepath.Join(c.Dir, "2.1.309", "claude") {
		t.Errorf("new path %q", p)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "2.1.308")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old version kept: %v", err)
	}
	// A failed update keeps the working version.
	r.set(func(r *releases) { r.stable, r.checksum = "2.1.310", strings.Repeat("0", 64) })
	if err := c.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("bad checksum: %v", err)
	}
	if p, err := c.Path(); err != nil || c.Status().Version != "2.1.309" || !strings.HasSuffix(p, "2.1.309/claude") {
		t.Errorf("kept %q %v %+v", p, err, c.Status())
	}
	entries, _ := os.ReadDir(filepath.Join(c.Dir, "2.1.310"))
	if len(entries) != 0 {
		t.Errorf("partial download left: %v", entries)
	}
}

func TestClaudeCodeInstallErrors(t *testing.T) {
	for want, f := range map[string]func(*releases){
		"not a Claude Code version": func(r *releases) { r.stable = "<html>" },
		"answered 404":              func(*releases) {},
		"does not match":            func(r *releases) { r.binary = "x" + r.binary[:3]; r.checksum = strings.Repeat("a", 64) },
	} {
		r := newReleases(t)
		r.set(f)
		if want == "answered 404" {
			r.srv.Config.Handler = http.NotFoundHandler()
		}
		c := newClaudeCode(t, r)
		if err := c.Install(context.Background()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
		if _, err := c.Path(); err == nil || !strings.Contains(err.Error(), "download failed") || c.Status().State != "failed" {
			t.Errorf("%s: path %v %+v", want, err, c.Status())
		}
	}
	r := newReleases(t)
	c := newClaudeCode(t, r)
	c.Platform = "linux-arm64"
	if err := c.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "no build for linux-arm64") {
		t.Errorf("platform: %v", err)
	}
	c.BaseURL = "http://[::1"
	if err := c.Install(context.Background()); err == nil {
		t.Error("bad URL")
	}
	c.BaseURL, c.Client = "", &http.Client{Transport: failing{}}
	if err := c.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "downloads.claude.ai") {
		t.Errorf("default URL: %v", err)
	}
	// The data folder is not writable.
	c = newClaudeCode(t, r)
	os.WriteFile(filepath.Dir(c.Dir)+"/file", nil, 0o600)
	c.Dir = filepath.Dir(c.Dir) + "/file/claude-code"
	if err := c.Install(context.Background()); err == nil {
		t.Error("not a folder")
	}
}

func TestClaudeCodeRun(t *testing.T) {
	r := newReleases(t)
	c := newClaudeCode(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	for c.Status().State != "ready" {
		tick()
	}
	cancel()
	<-done
	// A failure is logged and retried later; cancelling stops it.
	r.srv.Config.Handler = http.NotFoundHandler()
	c = newClaudeCode(t, r)
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	for c.Status().State != "failed" {
		tick()
	}
	cancel()
	<-done
	// Cancelled while downloading.
	c = newClaudeCode(t, r)
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	c.Run(ctx)
}

func TestPlatform(t *testing.T) {
	no := func() bool { return false }
	yes := func() bool { return true }
	for _, c := range []struct {
		goos, goarch string
		musl         func() bool
		want         string
	}{
		{"linux", "amd64", no, "linux-x64"}, {"linux", "arm64", yes, "linux-arm64-musl"},
		{"darwin", "arm64", yes, "darwin-arm64"}, {"windows", "amd64", no, ""}, {"linux", "386", no, ""},
	} {
		got, err := platformOf(c.goos, c.goarch, c.musl)
		if got != c.want || (err != nil) != (c.want == "") {
			t.Errorf("%s/%s: %q %v", c.goos, c.goarch, got, err)
		}
	}
	if p, err := (&ClaudeCode{}).platform(); err != nil || p == "" {
		t.Errorf("this machine: %q %v", p, err)
	}
	musl()
}

// readyClaudeCode is a Claude Code whose binary is in place.
func readyClaudeCode(t *testing.T) *ClaudeCode {
	r := newReleases(t)
	c := newClaudeCode(t, r)
	if err := c.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClaudeCode(t *testing.T) {
	a := newAnalyzer(t)
	a.Claude = readyClaudeCode(t)
	var gotDir, gotName string
	var gotArgs, gotEnv []string
	var gotStdin []byte
	a.Run = func(_ context.Context, dir string, env []string, stdin []byte, name string, args ...string) ([]byte, error) {
		gotDir, gotEnv, gotStdin, gotName, gotArgs = dir, env, stdin, name, args
		return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Check the proxy."}` + "\n"), nil
	}
	text, err := a.Test(context.Background(), Settings{Provider: ProviderClaudeCode, Model: "opus", TimeoutS: 5})
	if err != nil || text != "Check the proxy." {
		t.Fatalf("%q %v", text, err)
	}
	if p, _ := a.Claude.Path(); gotName != p || gotDir == "" || !strings.Contains(string(gotStdin), "Status 502 Bad Gateway") {
		t.Errorf("ran %s in %q with %q", gotName, gotDir, gotStdin)
	}
	want := []string{"-p", "--output-format", "json", "--max-turns", "1", "--tools", "", "--strict-mcp-config",
		"--disable-slash-commands", "--no-session-persistence", "--system-prompt", system, "--model", "opus"}
	if !slices.Equal(gotArgs, want) {
		t.Errorf("args %q", gotArgs)
	}
	if !slices.Equal(gotEnv, []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(a.Claude.Dir, "config"), "DISABLE_AUTOUPDATER=1"}) {
		t.Errorf("env %q", gotEnv)
	}
	if m, err := a.Models(context.Background(), Settings{Provider: ProviderClaudeCode}); err != nil || !slices.Equal(m, []string{"opus", "sonnet", "haiku"}) {
		t.Errorf("models %v %v", m, err)
	}
}

func TestClaudeCodeErrors(t *testing.T) {
	cases := []struct {
		out  string
		err  error
		want string
	}{
		{`{"is_error":true,"result":"Not logged in · Please run /login"}`, errors.New("exit status 1"), "Not logged in"},
		{``, errors.New("fork/exec: permission denied"), "permission denied"},
		{`not json`, nil, "did not answer in JSON"},
		{`{"is_error":false,"result":""}`, errors.New("exit status 2"), "exit status 2"},
		{`{"is_error":false,"result":""}`, nil, "empty"},
	}
	c := readyClaudeCode(t)
	for _, tc := range cases {
		a := newAnalyzer(t)
		a.Claude = c
		a.Run = func(context.Context, string, []string, []byte, string, ...string) ([]byte, error) {
			return []byte(tc.out), tc.err
		}
		if _, err := a.Test(context.Background(), Settings{Provider: ProviderClaudeCode, TimeoutS: 5}); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q %v: %v", tc.out, tc.err, err)
		}
	}
	a := newAnalyzer(t)
	if _, err := a.Test(context.Background(), Settings{Provider: ProviderClaudeCode, TimeoutS: 5}); err == nil ||
		!strings.Contains(err.Error(), "EYRIE_CLAUDE_CODE=true") {
		t.Errorf("off: %v", err)
	}
	a.Claude = newClaudeCode(t, newReleases(t))
	if _, err := a.Test(context.Background(), Settings{Provider: ProviderClaudeCode, TimeoutS: 5}); err == nil ||
		!strings.Contains(err.Error(), "not finished") {
		t.Errorf("downloading: %v", err)
	}
}

func TestRunCommand(t *testing.T) {
	out, err := runCommand(context.Background(), t.TempDir(), []string{"EYRIE_TEST=from env"}, []byte("hello"),
		"sh", "-c", `cat; echo " $EYRIE_TEST"`)
	if err != nil || string(out) != "hello from env\n" {
		t.Errorf("%q %v", out, err)
	}
	if _, err := runCommand(context.Background(), t.TempDir(), nil, nil, "sh", "-c", "echo oops >&2; exit 3"); err == nil ||
		!strings.Contains(err.Error(), "oops") {
		t.Errorf("stderr: %v", err)
	}
}

func tick() { time.Sleep(time.Millisecond) }
