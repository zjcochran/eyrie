// Package api is Eyrie's HTTP interface: the JSON API under /api/, single
// sign-on under /auth/, the health check, and the web app for every other
// path.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/zjcochran/eyrie/internal/secrets"
	"github.com/zjcochran/eyrie/internal/sso"
	"github.com/zjcochran/eyrie/internal/store"
)

// Server answers HTTP requests. Set its fields, then call Handler once.
type Server struct {
	Store   *store.Store
	Secrets *secrets.Box // seals the two-factor secrets and the single sign-on client secret
	Version string
	Log     *slog.Logger
	UI      http.Handler // the web app, served for every path not handled here
	// BaseURL is how browsers reach Eyrie (EYRIE_BASE_URL); empty: the
	// address of each request.
	BaseURL string
	// TrustedProxies are reverse proxies whose X-Forwarded-* headers are
	// believed.
	TrustedProxies []netip.Prefix
	// ForceLocal lets passwords work even when single sign-on is the only
	// way in (EYRIE_LOCAL_LOGIN=force).
	ForceLocal bool
	// HTTP fetches profile pictures from the single sign-on provider; nil:
	// http.DefaultClient.
	HTTP *http.Client

	now     func() time.Time
	limiter *limiter
	codes   *pendingCodes
	sso     *sso.Client
}

// Handler returns the routes, wrapped in cross-origin protection and the
// security headers.
func (s *Server) Handler() http.Handler {
	if s.now == nil {
		s.now = time.Now
	}
	s.limiter = newLimiter(5, 15*time.Minute)
	s.codes = &pendingCodes{m: map[string]*pendingCode{}}
	s.sso = sso.New(s.Log)
	if s.HTTP == nil {
		s.HTTP = http.DefaultClient
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/info", s.info)

	mux.HandleFunc("GET /api/v1/session", s.getSession)
	mux.HandleFunc("POST /api/v1/setup", s.setup)
	mux.HandleFunc("POST /api/v1/login", s.login)
	mux.HandleFunc("POST /api/v1/login/code", s.loginCode)
	mux.HandleFunc("POST /api/v1/logout", s.logout)
	mux.HandleFunc("GET /auth/oidc/login", s.ssoLogin)
	mux.HandleFunc("GET /auth/oidc/callback", s.ssoCallback)

	mux.Handle("GET /api/v1/account", s.user(s.account))
	mux.Handle("PUT /api/v1/account/password", s.user(s.changePassword))
	mux.Handle("POST /api/v1/account/two-factor/setup", s.user(s.startTwoFactor))
	mux.Handle("POST /api/v1/account/two-factor/enable", s.user(s.enableTwoFactor))
	mux.Handle("POST /api/v1/account/two-factor/recovery-codes", s.user(s.newRecoveryCodes))
	mux.Handle("POST /api/v1/account/two-factor/disable", s.user(s.disableTwoFactor))
	mux.Handle("PUT /api/v1/account/picture", s.user(s.uploadPicture))
	mux.Handle("DELETE /api/v1/account/picture", s.user(s.removePicture))
	mux.Handle("POST /api/v1/account/picture/sso", s.user(s.useSSOPicture))
	mux.Handle("DELETE /api/v1/account/sso", s.user(s.unlinkSSO))
	mux.Handle("GET /api/v1/users/{id}/picture", s.user(s.userPicture))

	mux.Handle("GET /api/v1/users", s.admin(s.listUsers))
	mux.Handle("POST /api/v1/users", s.admin(s.createUser))
	mux.Handle("PATCH /api/v1/users/{id}", s.admin(s.updateUser))
	mux.Handle("PUT /api/v1/users/{id}/password", s.admin(s.resetPassword))
	mux.Handle("DELETE /api/v1/users/{id}/two-factor", s.admin(s.resetTwoFactor))
	mux.Handle("DELETE /api/v1/users/{id}", s.admin(s.deleteUser))

	mux.Handle("GET /api/v1/settings/sso", s.admin(s.getSSOSettings))
	mux.Handle("PUT /api/v1/settings/sso", s.admin(s.saveSSOSettings))
	mux.Handle("POST /api/v1/settings/sso/test", s.admin(s.testSSO))

	mux.Handle("GET /api/v1/tokens", s.admin(s.listTokens))
	mux.Handle("POST /api/v1/tokens", s.admin(s.createToken))
	mux.Handle("DELETE /api/v1/tokens/{id}", s.admin(s.deleteToken))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, errorBody{"no such API route"})
	})
	mux.Handle("/", s.UI)

	cop := http.NewCrossOriginProtection()
	if s.BaseURL != "" {
		// A proxy that rewrites Host would otherwise make the browser's
		// Origin look foreign. config.Load checked the URL.
		cop.AddTrustedOrigin(s.BaseURL) //nolint:errcheck,gosec // checked by config.Load
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, errorBody{"cross-origin request refused"})
	}))
	return securityHeaders(cop.Handler(mux))
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
	json.NewEncoder(w).Encode(v) //nolint:errcheck,gosec // fails only when the client has gone
}

// maxJSONBody is the largest JSON request body read.
const maxJSONBody = 64 << 10

// readJSON decodes the request body into v, answering 400 when it is not
// JSON of the right shape.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(v); err != nil {
		invalid(w, http.StatusBadRequest, "The request body must be JSON: "+err.Error())
		return false
	}
	return true
}

// invalid answers a request the client got wrong, with a message for people.
func invalid(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errorBody{msg})
}

// fail logs an unexpected error and answers 500.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{"Something went wrong: " + err.Error()})
}
