package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct{ err error }

func (f fakeStore) Ping(context.Context) error { return f.err }

func newServer(err error) *httptest.Server {
	s := &Server{
		Store:   fakeStore{err},
		Version: "v1.2.3",
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		UI: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("the app"))
		}),
	}
	return httptest.NewServer(s.Handler())
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestHealthz(t *testing.T) {
	srv := newServer(nil)
	defer srv.Close()
	if resp, body := get(t, srv, "/healthz"); resp.StatusCode != http.StatusOK || body != "ok\n" {
		t.Errorf("healthy: %d %q", resp.StatusCode, body)
	}
	down := newServer(errors.New("gone"))
	defer down.Close()
	if resp, body := get(t, down, "/healthz"); resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "database") {
		t.Errorf("unhealthy: %d %q", resp.StatusCode, body)
	}
}

func TestInfo(t *testing.T) {
	srv := newServer(nil)
	defer srv.Close()
	resp, body := get(t, srv, "/api/v1/info")
	var info Info
	if err := json.Unmarshal([]byte(body), &info); err != nil || resp.StatusCode != http.StatusOK ||
		info != (Info{Name: "Eyrie", Version: "v1.2.3"}) || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("info: %d %q %v", resp.StatusCode, body, err)
	}
}

func TestRouting(t *testing.T) {
	srv := newServer(nil)
	defer srv.Close()
	if resp, body := get(t, srv, "/api/v1/nothing"); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown API route: %d %q", resp.StatusCode, body)
	}
	post, err := srv.Client().Post(srv.URL+"/api/v1/info", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusNotFound {
		t.Errorf("POST /api/v1/info: %d", post.StatusCode)
	}
	resp, body := get(t, srv, "/monitors")
	if resp.StatusCode != http.StatusOK || body != "the app" {
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
