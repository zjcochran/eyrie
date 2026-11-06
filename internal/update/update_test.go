// SPDX-License-Identifier: AGPL-3.0-or-later

package update

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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

var t0 = time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC)

// prefix "seals" by adding a prefix.
type prefix struct{}

func (prefix) Open(s string) (string, error) {
	if !strings.HasPrefix(s, "sealed:") {
		return "", errors.New("not sealed")
	}
	return strings.TrimPrefix(s, "sealed:"), nil
}

// github is a fake GitHub API with one latest release, and list as its
// list of releases.
type github struct {
	mu    sync.Mutex
	tag   string
	list  string
	code  int
	calls int
	agent string
}

func (g *github) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	g.agent = r.Header.Get("User-Agent")
	if r.URL.Path == "/repos/example/eyrie/releases" && g.code == 0 {
		fmt.Fprint(w, g.list)
		return
	}
	if r.URL.Path != "/repos/example/eyrie/releases/latest" {
		http.NotFound(w, r)
		return
	}
	if g.code != 0 {
		w.WriteHeader(g.code)
		return
	}
	fmt.Fprintf(w, `{"tag_name":%q,"name":"Eyrie %s","body":"## Changes\n\n- Faster checks","html_url":"https://github.com/example/eyrie/releases/tag/%s","published_at":"2027-01-14T10:00:00Z"}`,
		g.tag, g.tag, g.tag)
}

type sent struct {
	channels []int64
	current  string
	release  Release
}

func newChecker(t *testing.T, gh http.Handler) (*Checker, *[]sent) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	var mu sync.Mutex
	var got []sent
	c := &Checker{Store: st, Secrets: prefix{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "v1.0.0",
		Repo: "example/eyrie", API: srv.URL, Transport: srv.Client().Transport, Now: func() time.Time { return t0 },
		Notify: func(ch []int64, cur string, r Release) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, sent{ch, cur, r})
		}}
	return c, &got
}

func TestCheck(t *testing.T) {
	gh := &github{tag: "v1.2.0"}
	c, got := newChecker(t, gh)
	ctx := context.Background()
	s := Settings{Enabled: true, IntervalHours: 24, Notify: true, Channels: []int64{3}}
	st := c.Check(ctx, s)
	if st.Error != "" || st.Latest == nil || st.Latest.Version != "v1.2.0" || st.Latest.Notes != "## Changes\n\n- Faster checks" ||
		!st.Checked.Equal(t0) || !st.Latest.Published.Equal(time.Date(2027, 1, 14, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("status %+v %+v", st, st.Latest)
	}
	if gh.agent != "Eyrie/v1.0.0" {
		t.Errorf("user agent %q", gh.agent)
	}
	if a := c.Available(ctx); a == nil || a.Version != "v1.2.0" {
		t.Errorf("available %+v", a)
	}
	if len(*got) != 1 || (*got)[0].channels[0] != 3 || (*got)[0].current != "v1.0.0" || (*got)[0].release.Version != "v1.2.0" {
		t.Errorf("notified %+v", *got)
	}
	// Once per release.
	c.Check(ctx, s)
	if len(*got) != 1 {
		t.Errorf("notified again %+v", *got)
	}
	gh.tag = "v1.3.0"
	c.Check(ctx, s)
	if len(*got) != 2 {
		t.Errorf("new release not notified %+v", *got)
	}

	// Kept across restarts.
	again := &Checker{Store: c.Store, Log: c.Log, Version: "v1.3.0"}
	if st := again.Status(ctx); st.Latest == nil || st.Latest.Version != "v1.3.0" || st.Notified != "v1.3.0" {
		t.Errorf("reloaded %+v", st)
	}
	if again.Available(ctx) != nil {
		t.Error("the running release is available")
	}
}

func TestCheckFails(t *testing.T) {
	gh := &github{tag: "v1.2.0"}
	c, got := newChecker(t, gh)
	ctx := context.Background()
	c.Check(ctx, DefaultSettings())
	gh.code = http.StatusForbidden
	st := c.Check(ctx, Settings{IntervalHours: 24, Notify: true, Channels: []int64{1}})
	if st.Error != "GitHub answered 403 Forbidden" || st.Latest == nil || st.Latest.Version != "v1.2.0" {
		t.Errorf("failed check %+v", st)
	}
	if len(*got) != 0 {
		t.Errorf("notified without notify %+v", *got)
	}
	c.Repo = ""
	if st := c.Check(ctx, DefaultSettings()); !strings.Contains(st.Error, "not a GitHub repository") {
		t.Errorf("no repo %+v", st)
	}
	c.Repo, c.API = "example/eyrie", "" // the real API, which the fake transport stands in for
	c.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("no route") })
	if st := c.Check(ctx, DefaultSettings()); !strings.Contains(st.Error, "cannot reach GitHub") {
		t.Errorf("unreachable %+v", st)
	}
	c.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":""}`))}, nil
	})
	if st := c.Check(ctx, DefaultSettings()); st.Error != "GitHub's answer has no release" {
		t.Errorf("empty %+v", st)
	}
	c.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{`))}, nil
	})
	if st := c.Check(ctx, DefaultSettings()); !strings.Contains(st.Error, "cannot read GitHub's answer") {
		t.Errorf("bad JSON %+v", st)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The check goes through the proxy set, with its username and password.
func TestProxy(t *testing.T) {
	var mu sync.Mutex
	var seen, auth string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen, auth = r.URL.String(), r.Header.Get("Proxy-Authorization")
		mu.Unlock()
		fmt.Fprint(w, `{"tag_name":"v2.0.0","html_url":"https://github.com/example/eyrie/releases/tag/v2.0.0"}`)
	}))
	defer proxy.Close()
	c, _ := newChecker(t, &github{})
	c.API = "http://api.example.com" // only the proxy can answer for it
	s := Settings{IntervalHours: 24, Proxy: proxy.URL, ProxyUser: "alice", ProxyPassword: "sealed:s3cret"}
	if st := c.Check(context.Background(), s); st.Error != "" || st.Latest.Version != "v2.0.0" {
		t.Fatalf("through the proxy %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if seen != "http://api.example.com/repos/example/eyrie/releases/latest" || auth != want {
		t.Errorf("proxy saw %q with %q", seen, auth)
	}
	s.ProxyPassword = "not sealed"
	if st := c.Check(context.Background(), s); !strings.Contains(st.Error, "cannot open the proxy password") {
		t.Errorf("bad password %+v", st)
	}
	s.Proxy = "http://[::1"
	if st := c.Check(context.Background(), s); st.Error == "" {
		t.Error("bad proxy URL accepted")
	}
}

func TestRun(t *testing.T) {
	gh := &github{tag: "v1.1.0"}
	c, _ := newChecker(t, gh)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	// Off: never checked.
	time.Sleep(50 * time.Millisecond)
	gh.mu.Lock()
	calls := gh.calls
	gh.mu.Unlock()
	cancel()
	<-done
	if calls != 0 {
		t.Fatalf("checked while off: %d", calls)
	}

	if err := c.Store.SetSetting(context.Background(), SettingKey, Settings{Enabled: true, IntervalHours: 6}, t0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for c.Status(context.Background()).Latest == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if st := c.Status(context.Background()); st.Latest == nil || st.Latest.Version != "v1.1.0" {
		t.Errorf("not checked when on: %+v", st)
	}
}

const releases = `[
	{"tag_name":"v1.3.0","body":"- Later"},
	{"tag_name":"v1.2.0","name":"Eyrie v1.2.0","body":"- Pages","html_url":"https://github.com/example/eyrie/releases/tag/v1.2.0"},
	{"tag_name":"v1.2.1-rc.1","draft":true},
	{"tag_name":"v1.1.0","body":"- Faster"},
	{"tag_name":"v1.0.0","body":"- First"}]`

// upgraded starts c on v1.0.0, then on v1.2.0.
func upgraded(t *testing.T, c *Checker) *Upgrade {
	t.Helper()
	ctx := context.Background()
	for _, v := range []string{"v1.0.0", "v1.0.0", "v1.2.0"} {
		u, err := c.Upgrade(ctx)
		if err != nil || u != nil {
			t.Fatalf("before %s: %v %+v", v, err, u)
		}
		c.Version = v
		if err := c.Started(ctx); err != nil {
			t.Fatal(err)
		}
	}
	u, err := c.Upgrade(ctx)
	if err != nil || u == nil {
		t.Fatalf("no upgrade: %v", err)
	}
	return u
}

// A first start is no upgrade, nor is starting the same version again.
func TestUpgrade(t *testing.T) {
	c, _ := newChecker(t, &github{})
	if u := upgraded(t, c); u.From != "v1.0.0" || u.To != "v1.2.0" || !u.At.Equal(t0) || u.Fetched || len(u.Releases) != 0 {
		t.Fatalf("upgrade %+v", u)
	}
}

func TestUpgradeNotes(t *testing.T) {
	gh := &github{list: releases}
	c, _ := newChecker(t, gh)
	ctx := context.Background()
	upgraded(t, c)
	// Off: no notes fetched.
	s := Settings{IntervalHours: 24}
	c.fetchNotes(ctx, s)
	if gh.calls != 0 {
		t.Fatalf("fetched while off: %d", gh.calls)
	}
	// A failure is tried again an hour later.
	s.Enabled = true
	gh.code = http.StatusForbidden
	c.fetchNotes(ctx, s)
	gh.code = 0
	c.fetchNotes(ctx, s)
	if u, _ := c.Upgrade(ctx); gh.calls != 1 || u.Fetched {
		t.Fatalf("retried at once: %d", gh.calls)
	}
	c.Now = func() time.Time { return t0.Add(time.Hour) }
	c.fetchNotes(ctx, s)
	u, _ := c.Upgrade(ctx)
	if !u.Fetched || len(u.Releases) != 2 || u.Releases[0].Version != "v1.2.0" || u.Releases[0].Notes != "- Pages" ||
		u.Releases[1].Version != "v1.1.0" {
		t.Fatalf("notes %+v", u)
	}
	c.fetchNotes(ctx, s)
	if gh.calls != 2 {
		t.Errorf("fetched again: %d", gh.calls)
	}
}

// A downgrade records no upgrade, and the last one was not to it.
func TestDowngrade(t *testing.T) {
	c, _ := newChecker(t, &github{})
	ctx := context.Background()
	for _, v := range []string{"v1.0.0", "v1.2.0", "v1.1.0"} {
		c.Version = v
		if err := c.Started(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if u, err := c.Upgrade(ctx); err != nil || u != nil {
		t.Errorf("downgrade %v %+v", err, u)
	}
}

func TestSettings(t *testing.T) {
	ok := Settings{IntervalHours: 12, Proxy: " http://proxy.example.com:3128 ", ProxyUser: " alice ", Channels: []int64{3, 1, 3}}
	if err := ok.Normalize(); err != nil || ok.Proxy != "http://proxy.example.com:3128" || ok.ProxyUser != "alice" ||
		fmt.Sprint(ok.Channels) != "[1 3]" {
		t.Errorf("normalized %+v %v", ok, err)
	}
	none := DefaultSettings()
	if err := none.Normalize(); err != nil || none.Channels == nil {
		t.Errorf("default %+v %v", none, err)
	}
	for _, s := range []Settings{
		{IntervalHours: 0},
		{IntervalHours: 721},
		{IntervalHours: 1, Proxy: "proxy.example.com"},
		{IntervalHours: 1, Proxy: "ftp://proxy.example.com"},
		{IntervalHours: 1, Proxy: "http://alice:pw@proxy.example.com"},
		{IntervalHours: 1, ProxyUser: "alice"},
	} {
		if err := s.Normalize(); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
	c, _ := newChecker(t, &github{})
	if s, err := LoadSettings(context.Background(), c.Store); err != nil || s.Enabled || s.IntervalHours != 24 || s.Channels == nil {
		t.Errorf("loaded %+v %v", s, err)
	}
}

func TestRepoFrom(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/example/eyrie":      "example/eyrie",
		"https://github.com/example/eyrie.git/": "example/eyrie",
		"https://git.example.com/example/eyrie": "",
		"https://github.com/example":            "",
		"https://github.com/example/eyrie/tree": "",
		"":                                      "",
		"://":                                   "",
	} {
		if got := RepoFrom(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.10.0", "v1.9.0", true},
		{"v2.0.0", "1.9.9", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.1.0", "v1.2.0", false},
		{"v1.2.0", "dev", false},
		{"v1.2", "v1.0.0", false},
		{"v1.2.x", "v1.0.0", false},
		{"v1.3.0-rc.1", "v1.2.0", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
