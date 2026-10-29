// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/analysis"
	"github.com/zjcochran/eyrie/internal/backup"
	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/engine"
	"github.com/zjcochran/eyrie/internal/notify"
	"github.com/zjcochran/eyrie/internal/secrets"
	"github.com/zjcochran/eyrie/internal/store"
)

const pw = "correct horse battery"

// env is a server on a real SQLite store in a temp dir, with a clock the
// test sets.
type env struct {
	t   *testing.T
	st  *store.Store
	s   *Server
	srv *httptest.Server
	log *syncBuffer

	mu  sync.Mutex
	now time.Time
	up  atomic.Bool // whether port checks connect

	notifier *notify.Notifier
	analyzer *analysis.Analyzer
	// model is a fake OpenAI-compatible server: it answers with the body in
	// answer, and prompts gets what it was asked.
	model   *httptest.Server
	answer  atomic.Value
	prompts chan string
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.New("a key for tests")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, log: &syncBuffer{}, now: time.Now().UTC().Truncate(time.Millisecond)}
	e.up.Store(true)
	checkEnv := &check.Env{
		Dial: func(context.Context, string, string) (net.Conn, error) {
			if !e.up.Load() {
				return nil, errors.New("connection refused")
			}
			a, b := net.Pipe()
			b.Close()
			return a, nil
		},
		Transport: func(bool) http.RoundTripper { return http.DefaultTransport },
		Resolve:   func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("none") },
		Ping:      func(context.Context, netip.Addr, int, int) (time.Duration, error) { return 0, errors.New("none") },
		Secrets:   box,
	}
	log := slog.New(slog.NewTextHandler(e.log, nil))
	eng := engine.New(st, checkEnv, log)
	e.answer.Store(`{"choices":[{"message":{"content":"The port is closed: the database is likely stopped."}}]}`)
	e.prompts = make(chan string, 10)
	e.model = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:14b"},{"id":"gemma3:12b"}]}`)
			return
		}
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) == 2 {
			e.prompts <- body.Messages[1].Content
		}
		io.WriteString(w, e.answer.Load().(string))
	}))
	t.Cleanup(e.model.Close)
	e.analyzer = &analysis.Analyzer{Store: st, Secrets: box, Log: log, Version: "v1.2.3"}
	e.notifier = &notify.Notifier{Store: st, Secrets: box, Log: log, Version: "v1.2.3", BaseURL: "https://eyrie.example.com",
		Retry: []time.Duration{time.Millisecond}, Analyst: e.analyzer}
	eng.OnNotify, eng.OnCert, eng.CertDays = e.notifier.Monitor, e.notifier.CertExpiring, e.notifier.CertDays
	eng.OnGroup = func(a engine.GroupAlert) { e.notifier.Group(a.Group, a.Down, a.Members, a.Since, time.Now()) }
	e.s = &Server{
		Engine: eng, Env: checkEnv, MetricsToken: "m3trics", Notifier: e.notifier, Analyzer: e.analyzer,
		Backups: &backup.Backups{Store: st, Secrets: box, Dir: filepath.Join(t.TempDir(), "backups"), Version: "v1.2.3", Log: log, Now: e.clock},
		Store:   st, Secrets: box, Version: "v1.2.3", SourceURL: "https://git.example.com/eyrie",
		Log: log,
		UI: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("the app"))
		}),
		now:      e.clock,
		jobsWait: 200 * time.Millisecond,
	}
	e.srv = httptest.NewServer(e.s.Handler())
	t.Cleanup(e.srv.Close)
	eng.Publish = e.s.Publish
	eng.Agents = e.s.Agents
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()
	go e.notifier.Run(ctx)
	t.Cleanup(func() { cancel(); <-done }) // before the store closes
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

// client is a browser: it keeps cookies, does not follow redirects, and
// sends the CSRF token it was given.
type client struct {
	e    *env
	http *http.Client
	csrf string
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

// do sends a request; body is JSON-encoded unless it is []byte.
func (c *client) do(method, path string, body any, header ...string) (*http.Response, []byte) {
	c.e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	u := path
	if strings.HasPrefix(path, "/") {
		u = c.e.srv.URL + path
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// call sends a JSON request and decodes the answer into out (when not nil),
// returning the status.
func (c *client) call(method, path string, body, out any) int {
	c.e.t.Helper()
	resp, b := c.do(method, path, body)
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			c.e.t.Fatalf("%s %s: %d %s: %v", method, path, resp.StatusCode, b, err)
		}
	}
	return resp.StatusCode
}

// errorOf returns the error message of an answer.
func (c *client) errorOf(method, path string, body any) (int, string) {
	c.e.t.Helper()
	var e errorBody
	code := c.call(method, path, body, &e)
	return code, e.Error
}

// keep takes the CSRF token of a session answer.
func (c *client) keep(v sessionView) sessionView {
	c.csrf = v.CSRF
	return v
}

// setup makes the first admin, alice, and returns her browser.
func (e *env) setup() *client {
	e.t.Helper()
	c := e.client()
	var v sessionView
	if code := c.call("POST", "/api/v1/setup", credentials{"alice", pw}, &v); code != http.StatusCreated {
		e.t.Fatalf("setup: %d", code)
	}
	c.keep(v)
	return c
}

// signIn signs a new browser in with a password.
func (e *env) signIn(user, password string) *client {
	e.t.Helper()
	c := e.client()
	var v sessionView
	if code := c.call("POST", "/api/v1/login", credentials{user, password}, &v); code != http.StatusOK || v.User == nil {
		e.t.Fatalf("sign in as %s: %d %+v", user, code, v)
	}
	c.keep(v)
	return c
}

// addUser has admin create a user with a password and signs them in.
func (e *env) addUser(admin *client, name, role string) (*client, userView) {
	e.t.Helper()
	var u userView
	if code := admin.call("POST", "/api/v1/users", map[string]string{"username": name, "password": pw, "role": role}, &u); code != http.StatusCreated {
		e.t.Fatalf("add %s: %d", name, code)
	}
	return e.signIn(name, pw), u
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if resp, body := c.do("GET", "/healthz", nil); resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Errorf("healthy: %d %q", resp.StatusCode, body)
	}
	e.st.Close()
	if resp, body := c.do("GET", "/healthz", nil); resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "database") {
		t.Errorf("unhealthy: %d %q", resp.StatusCode, body)
	}
}

func TestInfo(t *testing.T) {
	e := newEnv(t)
	resp, body := e.client().do("GET", "/api/v1/info", nil)
	var info Info
	if err := json.Unmarshal(body, &info); err != nil || resp.StatusCode != http.StatusOK ||
		info != (Info{Name: "Eyrie", Version: "v1.2.3", License: "AGPL-3.0-or-later", Source: "https://git.example.com/eyrie"}) || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("info: %d %q %v", resp.StatusCode, body, err)
	}
}

func TestRouting(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if resp, body := c.do("GET", "/api/v1/nothing", nil); resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"error"`) {
		t.Errorf("unknown API route: %d %q", resp.StatusCode, body)
	}
	if resp, _ := c.do("POST", "/api/v1/info", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST /api/v1/info: %d", resp.StatusCode)
	}
	resp, body := c.do("GET", "/monitors", nil)
	if resp.StatusCode != http.StatusOK || string(body) != "the app" {
		t.Errorf("app route: %d %q", resp.StatusCode, body)
	}
	for k, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
		"Content-Security-Policy": "default-src 'self'",
	} {
		if !strings.Contains(resp.Header.Get(k), want) {
			t.Errorf("%s = %q", k, resp.Header.Get(k))
		}
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	for _, path := range []string{"/api/v1/setup", "/api/v1/login", "/api/v1/login/code", "/api/v1/users", "/api/v1/tokens",
		"/api/v1/settings/sso/test"} {
		if resp, _ := admin.do("POST", path, []byte("{")); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s with bad JSON: %d", path, resp.StatusCode)
		}
	}
	for _, r := range [][2]string{{"PUT", "/api/v1/account/password"}, {"POST", "/api/v1/account/two-factor/enable"},
		{"POST", "/api/v1/account/two-factor/recovery-codes"}, {"POST", "/api/v1/account/two-factor/disable"},
		{"PATCH", "/api/v1/users/1"}, {"PUT", "/api/v1/users/1/password"}, {"PUT", "/api/v1/settings/sso"}} {
		if resp, _ := admin.do(r[0], r[1], []byte("not json")); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s with bad JSON: %d", r[0], r[1], resp.StatusCode)
		}
	}
	for _, r := range [][2]string{{"PATCH", "/api/v1/users/x"}, {"PUT", "/api/v1/users/x/password"},
		{"DELETE", "/api/v1/users/x/two-factor"}, {"DELETE", "/api/v1/users/x"}, {"DELETE", "/api/v1/tokens/x"},
		{"GET", "/api/v1/users/x/picture"}} {
		if resp, _ := admin.do(r[0], r[1], map[string]any{}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: %d", r[0], r[1], resp.StatusCode)
		}
	}
	// A cross-site request is refused before it reaches a route.
	if code, msg := admin.errorOf("POST", "/api/v1/logout", nil); code != http.StatusOK && code != http.StatusNoContent {
		t.Errorf("same-site logout: %d %s", code, msg)
	}
	if !strings.Contains(e.log.String(), `msg="signed out" user=alice`) {
		t.Error("sign-out not logged")
	}
	resp, body := e.client().do("POST", "/api/v1/login", credentials{"alice", pw}, "Sec-Fetch-Site", "cross-site")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "cross-origin") {
		t.Errorf("cross-site sign-in: %d %s", resp.StatusCode, body)
	}
}

// TestDatabaseErrors checks that a database that stops answering gives 500,
// not a crash or a wrong answer.
func TestDatabaseErrors(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	e.st.Close()
	for _, r := range [][2]string{{"GET", "/api/v1/session"}, {"POST", "/api/v1/login"}, {"POST", "/api/v1/setup"},
		{"GET", "/api/v1/account"}, {"GET", "/auth/oidc/login"}, {"GET", "/auth/oidc/callback"}, {"POST", "/api/v1/logout"}} {
		if resp, _ := admin.do(r[0], r[1], credentials{"alice", pw}); resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s %s: %d", r[0], r[1], resp.StatusCode)
		}
	}
	if !strings.Contains(e.log.String(), "request failed") {
		t.Error("errors not logged")
	}
}
