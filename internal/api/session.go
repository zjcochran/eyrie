// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

const sessionCookie = "eyrie_session"

type ctxKey struct{}

// sessionFrom is the session the user middleware found.
func sessionFrom(r *http.Request) *store.Session {
	sess, _ := r.Context().Value(ctxKey{}).(*store.Session)
	return sess
}

// loadSession returns the request's session, or nil when it has none.
func (s *Server) loadSession(r *http.Request) (*store.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if errors.Is(err, http.ErrNoCookie) || (err == nil && c.Value == "") {
		return nil, nil
	}
	sess, ok, err := s.Store.SessionByToken(r.Context(), c.Value, s.now())
	if err != nil || !ok {
		return nil, err
	}
	return &sess, nil
}

// user requires a signed-in user. Requests that change something must also
// carry the session's CSRF token in the X-CSRF-Token header.
func (s *Server) user(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.loadSession(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if sess == nil {
			invalid(w, http.StatusUnauthorized, "Sign in first.")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.CSRF)) != 1 {
			invalid(w, http.StatusForbidden, "Invalid or missing CSRF token; reload the page.")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	})
}

// admin is user plus the admin role.
func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return s.user(func(w http.ResponseWriter, r *http.Request) {
		if !sessionFrom(r).User.IsAdmin() {
			invalid(w, http.StatusForbidden, "Only admins can do this.")
			return
		}
		h(w, r)
	})
}

// read lets a signed-in user or an API token (Authorization: Bearer) read:
// it accepts only GET and HEAD requests. A request without a token goes
// through user.
func (s *Server) read(h http.HandlerFunc) http.Handler {
	session := s.user(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			session.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			invalid(w, http.StatusForbidden, "API tokens can only read.")
			return
		}
		if _, ok, err := s.Store.UseAPIToken(r.Context(), strings.TrimSpace(token), s.now()); err != nil {
			s.fail(w, r, err)
			return
		} else if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="eyrie"`)
			invalid(w, http.StatusUnauthorized, "Unknown or revoked API token.")
			return
		}
		h(w, r)
	})
}

// startSession signs the browser in as u.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) (*store.Session, error) {
	token, err := s.Store.CreateSession(r.Context(), u.ID, s.clientIP(r), r.UserAgent(), s.now())
	if err != nil {
		return nil, err
	}
	// Secure whenever the browser uses HTTPS; plain-HTTP setups need the cookie too.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is set from s.secure(r)
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.secure(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(store.SessionTTL.Seconds()),
	})
	s.Log.Info("signed in", "user", u.Username, "ip", s.clientIP(r))
	sess, _, err := s.Store.SessionByToken(r.Context(), token, s.now())
	return &sess, err
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is set from s.secure(r)
		Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

// ---- sign-in rate limit ----

// limiter counts failed sign-ins per key in a sliding window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{max: limit, window: window, fails: map[string][]time.Time{}}
}

func (l *limiter) blocked(now time.Time, keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if len(l.recent(now, k)) >= l.max {
			return true
		}
	}
	return false
}

func (l *limiter) fail(now time.Time, keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		l.fails[k] = append(l.recent(now, k), now)
	}
}

func (l *limiter) reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.fails, k)
	}
}

func (l *limiter) recent(now time.Time, k string) []time.Time {
	ts := l.fails[k]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > l.window {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, k)
	}
	return ts
}

// ---- reverse proxies ----

func (s *Server) trusted(ip netip.Addr) bool {
	for _, p := range s.TrustedProxies {
		if p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

// remoteAddr is the address of the direct peer.
func remoteAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	return ip.Unmap(), err == nil
}

// fromProxy reports whether the request came through a trusted proxy, whose
// forwarding headers can be believed.
func (s *Server) fromProxy(r *http.Request) bool {
	ip, ok := remoteAddr(r)
	return ok && s.trusted(ip)
}

// clientIP is the visitor's address. Behind trusted proxies it walks
// X-Forwarded-For from the right, skipping trusted hops, so a client cannot
// choose it by sending its own header. Otherwise it is the direct peer.
func (s *Server) clientIP(r *http.Request) string {
	peer, ok := remoteAddr(r)
	if !ok {
		return r.RemoteAddr
	}
	if !s.trusted(peer) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !s.trusted(ip) {
			return ip.Unmap().String()
		}
	}
	if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return ip.Unmap().String()
	}
	return peer.String()
}

// secure reports whether the browser reached Eyrie over HTTPS: directly,
// through a trusted proxy, or per the configured external URL.
func (s *Server) secure(r *http.Request) bool {
	if r.TLS != nil || strings.HasPrefix(s.BaseURL, "https://") {
		return true
	}
	return s.fromProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// externalURL is how browsers reach Eyrie, for the single sign-on callback.
func (s *Server) externalURL(r *http.Request) string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	scheme := "http"
	if s.secure(r) {
		scheme = "https"
	}
	host := r.Host
	if s.fromProxy(r) && r.Header.Get("X-Forwarded-Host") != "" {
		host = r.Header.Get("X-Forwarded-Host")
	}
	return scheme + "://" + host
}
