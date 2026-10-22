package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var app = fstest.MapFS{
	"index.html":           {Data: []byte(`<div id="app"></div>`)},
	"assets/index-abc.js":  {Data: []byte(`console.log(1)`)},
	"favicon.svg":          {Data: []byte(`<svg/>`)},
	"assets/fonts/x.woff2": {Data: []byte(`font`)},
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func TestServe(t *testing.T) {
	h := Handler(app)
	for _, c := range []struct {
		path, body, cache string
		code              int
	}{
		{"/", `<div id="app">`, "no-cache", 200},
		{"/index.html", `<div id="app">`, "no-cache", 200},
		{"/monitors/12", `<div id="app">`, "no-cache", 200}, // an app route
		{"/assets/index-abc.js", "console.log", "immutable", 200},
		{"/favicon.svg", "<svg", "no-cache", 200},
		{"/assets/", `<div id="app">`, "no-cache", 200}, // a folder is not served
		{"/missing.js", "not found", "", 404},
		{"/../../etc/passwd", `<div id="app">`, "no-cache", 200}, // cleaned to an app route
	} {
		w := get(t, h, http.MethodGet, c.path)
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.body) || !strings.Contains(w.Header().Get("Cache-Control"), c.cache) {
			t.Errorf("%s: %d %q cache %q", c.path, w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
		}
	}
	if w := get(t, h, http.MethodHead, "/"); w.Code != 200 {
		t.Errorf("HEAD: %d", w.Code)
	}
	if w := get(t, h, http.MethodPost, "/"); w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestNotBuilt(t *testing.T) {
	w := get(t, Handler(Files()), http.MethodGet, "/")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "no web interface") {
		t.Errorf("stub: %d %s", w.Code, w.Body.String())
	}
}

type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

func TestNoIndex(t *testing.T) {
	if w := get(t, Handler(fstest.MapFS{}), http.MethodGet, "/"); w.Code != http.StatusNotFound {
		t.Errorf("missing index: %d", w.Code)
	}
	if w := get(t, Handler(brokenFS{}), http.MethodGet, "/"); w.Code != http.StatusInternalServerError {
		t.Errorf("unreadable index: %d", w.Code)
	}
}
