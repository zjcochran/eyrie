// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogRequests(t *testing.T) {
	var buf bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/push/{token}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("data: x\n\n"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
	})
	s := &Server{Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	h := s.logRequests(mux)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/push/tok123", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !rec.Flushed {
		t.Error("not flushed")
	}
	got := buf.String()
	if !strings.Contains(got, `msg=request method=POST route="POST /api/push/{token}" status=202`) ||
		!strings.Contains(got, `route="GET /events" status=200`) || strings.Contains(got, "tok123") {
		t.Errorf("log %s", got)
	}
	// Above debug: nothing.
	buf.Reset()
	s.Log = slog.New(slog.NewTextHandler(&buf, nil))
	s.logRequests(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events", nil))
	if buf.Len() != 0 {
		t.Errorf("logged at info: %s", buf.String())
	}
}
