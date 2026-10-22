// Package api is Eyrie's HTTP interface: the JSON API under /api/, the
// health check, and the web app for every other path.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Pinger is the part of the store the health check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server answers HTTP requests. Set its fields, then call Handler.
type Server struct {
	Store   Pinger
	Version string
	Log     *slog.Logger
	UI      http.Handler // the web app, served for every path not handled here
}

// Handler returns the routes, wrapped in the security headers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/info", s.info)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, errorBody{"no such API route"})
	})
	mux.Handle("/", s.UI)
	return securityHeaders(mux)
}

// securityHeaders are on every response. The app has no inline scripts, so
// scripts may come only from Eyrie itself; Svelte sets inline styles.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}

// healthz answers 200 "ok" while the database answers within 2 seconds,
// 503 otherwise. Docker's health check calls it.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.Store.Ping(ctx); err != nil {
		s.Log.Warn("health check: the database does not answer", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("database unavailable\n"))
		return
	}
	w.Write([]byte("ok\n"))
}

// Info is the answer of GET /api/v1/info.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Info{Name: "Eyrie", Version: s.Version})
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // fails only when the client has gone
}
