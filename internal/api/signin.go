package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/sso"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/totp"
)

// userView is a user as the API shows it.
type userView struct {
	ID         int64      `json:"id"`
	Username   string     `json:"username"`
	Role       string     `json:"role"`
	Disabled   bool       `json:"disabled"`
	Password   bool       `json:"password"`    // has a password
	SSO        bool       `json:"sso"`         // has a linked single sign-on account
	TwoFactor  bool       `json:"two_factor"`  // needs a code after the password
	Picture    string     `json:"picture"`     // URL of the profile picture; "" for none
	SSOPicture bool       `json:"sso_picture"` // single sign-on may set the picture
	CreatedAt  time.Time  `json:"created_at"`
	LastLogin  *time.Time `json:"last_login"` // null: never
	// RoleFromGroup is set when the single sign-on admin group decides the
	// role (accounts without a password while an admin group is set).
	RoleFromGroup bool `json:"role_from_group"`
}

func viewUser(u store.User, sso sso.Settings) userView {
	v := userView{ID: u.ID, Username: u.Username, Role: u.Role, Disabled: u.Disabled, Password: u.HasLocal,
		SSO: u.HasSSO, TwoFactor: u.TwoFactor, SSOPicture: u.SSOPicture, CreatedAt: u.CreatedAt,
		RoleFromGroup: sso.Enabled && sso.AdminGroup != "" && !u.HasLocal}
	if !u.Picture.IsZero() {
		// The time in the URL changes with the picture, so it can be cached.
		v.Picture = fmt.Sprintf("/api/v1/users/%d/picture?v=%d", u.ID, u.Picture.UnixMilli())
	}
	if !u.LastLogin.IsZero() {
		t := u.LastLogin
		v.LastLogin = &t
	}
	return v
}

// ssoButton is what the sign-in page needs to know about single sign-on.
type ssoButton struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
}

// sessionView is the answer of GET /api/v1/session, and of a sign-in.
type sessionView struct {
	Setup          bool      `json:"setup"` // no account exists yet: the first one is made at /api/v1/setup
	User           *userView `json:"user"`  // null: not signed in
	CSRF           string    `json:"csrf,omitempty"`
	SSO            ssoButton `json:"sso"`
	PasswordSignIn bool      `json:"password_sign_in"`
	Version        string    `json:"version"`
}

func (s *Server) ssoSettings(r *http.Request) (sso.Settings, error) {
	o := sso.Defaults()
	_, err := s.Store.GetSetting(r.Context(), sso.SettingKey, &o)
	return o, err
}

// localAllowed reports whether password sign-in is on.
func (s *Server) localAllowed(o sso.Settings) bool {
	return !o.Enabled || !o.SSOOnly || s.ForceLocal
}

func (s *Server) sessionView(r *http.Request, sess *store.Session) (sessionView, error) {
	o, err := s.ssoSettings(r)
	if err != nil {
		return sessionView{}, err
	}
	v := sessionView{SSO: ssoButton{o.Enabled, o.Name()}, PasswordSignIn: s.localAllowed(o), Version: s.Version}
	if sess != nil {
		u := viewUser(sess.User, o)
		v.User, v.CSRF = &u, sess.CSRF
		return v, nil
	}
	n, err := s.Store.CountUsers(r.Context())
	v.Setup = n == 0
	return v, err
}

func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, code int, sess *store.Session) {
	v, err := s.sessionView(r, sess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, code, v)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.loadSession(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSession(w, r, http.StatusOK, sess)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setup creates the first account, an admin, and signs it in. It works only
// while there is no account.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	u, err := s.Store.CreateFirstAdmin(r.Context(), c.Username, c.Password, s.now())
	switch {
	case errors.Is(err, store.ErrSetupDone):
		invalid(w, http.StatusForbidden, "Setup is already done: sign in.")
		return
	case err != nil:
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("first admin created", "user", u.Username, "ip", s.clientIP(r))
	sess, err := s.startSession(w, r, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSession(w, r, http.StatusCreated, sess)
}

// codeNeeded is a sign-in's answer when the password was right and a
// two-factor code must follow.
type codeNeeded struct {
	TwoFactor bool `json:"two_factor"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.localAllowed(o) {
		invalid(w, http.StatusForbidden, "Password sign-in is turned off: sign in with "+o.Name()+".")
		return
	}
	user := strings.TrimSpace(c.Username)
	keys := []string{"ip:" + s.clientIP(r), "user:" + strings.ToLower(user)}
	now := s.now()
	if s.limiter.blocked(now, keys...) {
		invalid(w, http.StatusTooManyRequests, "Too many failed sign-ins. Try again in 15 minutes.")
		return
	}
	u, err := s.Store.Authenticate(r.Context(), user, c.Password, now)
	if errors.Is(err, store.ErrBadLogin) {
		s.limiter.fail(now, keys...)
		s.Log.Warn("failed sign-in", "user", user, "ip", s.clientIP(r))
		invalid(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.limiter.reset(keys...)
	if u.TwoFactor {
		token := s.codes.add(&pendingCode{user: u, expires: now.Add(codeTTL)}, now)
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is set from s.secure(r)
			Name: codeCookie, Value: token, Path: codePath, HttpOnly: true, Secure: s.secure(r),
			SameSite: http.SameSiteStrictMode, MaxAge: int(codeTTL.Seconds()),
		})
		writeJSON(w, http.StatusOK, codeNeeded{true})
		return
	}
	sess, err := s.startSession(w, r, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSession(w, r, http.StatusOK, sess)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Store.DeleteSession(r.Context(), c.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.clearCookie(w, r, sessionCookie, "/")
	w.WriteHeader(http.StatusNoContent)
}

// ---- the two-factor step ----

const (
	codeCookie   = "eyrie_2fa"
	codePath     = "/api/v1/login/code"
	codeTTL      = 5 * time.Minute
	codeAttempts = 5
)

// pendingCode is a sign-in whose password was right and that waits for its
// code.
type pendingCode struct {
	user    store.User
	expires time.Time
	tries   int
}

type pendingCodes struct {
	mu sync.Mutex
	m  map[string]*pendingCode
}

func (p *pendingCodes) add(pc *pendingCode, now time.Time) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
	p.m[token] = pc
	return token
}

// take returns the pending sign-in for a token, counting the attempt; it is
// dropped when it has expired or used up its attempts.
func (p *pendingCodes) take(token string, now time.Time) (*pendingCode, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, ok := p.m[token]
	if !ok || now.After(pc.expires) || pc.tries >= codeAttempts {
		delete(p.m, token)
		return nil, false
	}
	pc.tries++
	return pc, true
}

func (p *pendingCodes) done(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, token)
}

// loginCode checks the code of a two-factor sign-in: a code from the app,
// or a recovery code.
func (s *Server) loginCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ctx, now := r.Context(), s.now()
	c, err := r.Cookie(codeCookie)
	var pc *pendingCode
	ok := err == nil
	if ok {
		pc, ok = s.codes.take(c.Value, now)
	}
	if !ok {
		invalid(w, http.StatusUnauthorized, "The sign-in has expired. Sign in again.")
		return
	}
	keys := []string{"ip:" + s.clientIP(r), "user:" + strings.ToLower(pc.user.Username)}
	if s.limiter.blocked(now, keys...) {
		invalid(w, http.StatusTooManyRequests, "Too many failed sign-ins. Try again in 15 minutes.")
		return
	}
	used, recovery, err := s.checkCode(r, pc.user.ID, body.Code, now)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !used {
		s.limiter.fail(now, keys...)
		s.Log.Warn("failed sign-in code", "user", pc.user.Username, "ip", s.clientIP(r))
		invalid(w, http.StatusUnauthorized, "That code is not right.")
		return
	}
	s.codes.done(c.Value)
	s.clearCookie(w, r, codeCookie, codePath)
	s.limiter.reset(keys...)
	if recovery {
		left, _ := s.Store.RecoveryCodesLeft(ctx, pc.user.ID)
		s.Log.Info("recovery code used", "user", pc.user.Username, "left", left)
	}
	if err := s.Store.TouchLogin(ctx, pc.user.ID, now); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.startSession(w, r, pc.user)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSession(w, r, http.StatusOK, sess)
}

// checkCode accepts a current app code (once) or an unused recovery code.
func (s *Server) checkCode(r *http.Request, id int64, code string, now time.Time) (ok, recovery bool, err error) {
	ctx := r.Context()
	sealed, err := s.Store.TwoFactorSecret(ctx, id)
	if err != nil || sealed == "" {
		return false, false, err
	}
	secret, err := s.Secrets.Open(sealed)
	if err != nil {
		return false, false, err
	}
	if step, match := totp.Verify(secret, code, now); match {
		ok, err = s.Store.UseTOTPStep(ctx, id, step)
		return ok, false, err
	}
	ok, err = s.Store.UseRecoveryCode(ctx, id, code)
	return ok, ok, err
}

// capitalize makes a store error into the start of a sentence.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
