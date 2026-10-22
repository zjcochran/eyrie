package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/totp"
)

func cookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestBeforeSetup(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	var v sessionView
	want := sessionView{Setup: true, SSO: ssoButton{false, "SSO"}, PasswordSignIn: true, Version: "v1.2.3"}
	if code := c.call("GET", "/api/v1/session", nil, &v); code != http.StatusOK || v != want {
		t.Fatalf("before setup: %d %+v", code, v)
	}
	if code, msg := c.errorOf("POST", "/api/v1/setup", credentials{"alice", "short"}); code != http.StatusUnprocessableEntity ||
		msg != "The password must be at least 12 characters." {
		t.Errorf("short password: %d %q", code, msg)
	}
}

func TestSetup(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	resp, body := c.do("POST", "/api/v1/setup", credentials{" alice ", pw})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: %d %s", resp.StatusCode, body)
	}
	ck := cookie(resp, sessionCookie)
	if ck == nil || !ck.HttpOnly || ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != 7*24*3600 {
		t.Errorf("session cookie %+v", ck)
	}
	var v sessionView
	if code := c.call("GET", "/api/v1/session", nil, &v); code != http.StatusOK || v.Setup || v.User == nil || v.CSRF == "" {
		t.Fatalf("after setup: %+v", v)
	}
	if u := v.User; u.Username != "alice" || u.Role != "admin" || !u.Password {
		t.Errorf("admin %+v", u)
	}
	if !strings.Contains(e.log.String(), "first admin created") {
		t.Error("setup not logged")
	}
}

func TestSetupOnce(t *testing.T) {
	e := newEnv(t)
	e.setup()
	if code, msg := e.client().errorOf("POST", "/api/v1/setup", credentials{"mallory", pw}); code != http.StatusForbidden ||
		!strings.Contains(msg, "already done") {
		t.Errorf("second setup: %d %q", code, msg)
	}
	var v sessionView
	if code := e.client().call("GET", "/api/v1/session", nil, &v); code != http.StatusOK || v.Setup || v.User != nil {
		t.Errorf("another browser: %+v", v)
	}
}

func TestSignIn(t *testing.T) {
	e := newEnv(t)
	e.setup()
	c := e.client()
	if code, msg := c.errorOf("POST", "/api/v1/login", credentials{"alice", "wrong horse battery"}); code != http.StatusUnauthorized ||
		msg != "Wrong username or password." {
		t.Errorf("wrong password: %d %q", code, msg)
	}
	var v sessionView
	if code := c.call("POST", "/api/v1/login", credentials{"ALICE", pw}, &v); code != http.StatusOK || v.User == nil ||
		v.User.Username != "alice" || v.CSRF == "" || v.User.LastLogin == nil {
		t.Fatalf("sign in: %d %+v", code, v)
	}
	c.keep(v)
	if code := c.call("GET", "/api/v1/account", nil, nil); code != http.StatusOK {
		t.Errorf("account: %d", code)
	}
	resp, _ := c.do("POST", "/api/v1/logout", nil)
	if ck := cookie(resp, sessionCookie); resp.StatusCode != http.StatusNoContent || ck == nil || ck.MaxAge >= 0 {
		t.Errorf("sign out: %d %+v", resp.StatusCode, ck)
	}
	if code := c.call("GET", "/api/v1/account", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("after signing out: %d", code)
	}
	if resp, _ := e.client().do("POST", "/api/v1/logout", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("sign out without a session: %d", resp.StatusCode)
	}
	// An unknown cookie is no session.
	if code := e.client().call("GET", "/api/v1/session", nil, &v); code != http.StatusOK || v.User != nil {
		t.Errorf("no session: %+v", v)
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	e.setup()
	c := e.client()
	for range 5 {
		c.call("POST", "/api/v1/login", credentials{"alice", "wrong horse battery"}, nil)
	}
	if code, msg := c.errorOf("POST", "/api/v1/login", credentials{"alice", pw}); code != http.StatusTooManyRequests ||
		!strings.Contains(msg, "15 minutes") {
		t.Errorf("after 5 failures: %d %q", code, msg)
	}
	e.advance(16 * time.Minute)
	if code := c.call("POST", "/api/v1/login", credentials{"alice", pw}, nil); code != http.StatusOK {
		t.Errorf("after the window: %d", code)
	}
	if !strings.Contains(e.log.String(), "failed sign-in") {
		t.Error("failures not logged")
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, time.Minute)
	now := time.Now()
	l.fail(now, "a", "b")
	l.fail(now.Add(time.Second), "a")
	if !l.blocked(now.Add(2*time.Second), "a") || l.blocked(now.Add(2*time.Second), "b") {
		t.Error("blocked")
	}
	if l.blocked(now.Add(2*time.Minute), "a") || len(l.fails) != 1 {
		t.Errorf("old failures still count: %v", l.fails)
	}
	l.reset("a", "b")
	if len(l.fails) != 0 {
		t.Error("reset")
	}
}

// enableTwoFactor turns on two-factor sign-in for c's user, whose password
// is pw unless another is given, and returns the secret and the recovery
// codes.
func enableTwoFactor(t *testing.T, e *env, c *client, password ...string) (string, []string) {
	t.Helper()
	var setup twoFactorSetup
	if code := c.call("POST", "/api/v1/account/two-factor/setup", nil, &setup); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	code, _ := totp.Code(setup.Secret, totp.Step(e.clock()))
	password = append(password, pw)
	var rc recoveryCodes
	if status := c.call("POST", "/api/v1/account/two-factor/enable",
		map[string]string{"sealed": setup.Sealed, "code": code, "password": password[0]}, &rc); status != http.StatusOK {
		t.Fatalf("enable: %d", status)
	}
	return setup.Secret, rc.Codes
}

// passwordStep signs in with alice's password, which needs a code next.
func (c *client) passwordStep() {
	c.e.t.Helper()
	resp, body := c.do("POST", "/api/v1/login", credentials{"alice", pw})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"two_factor":true`) {
		c.e.t.Fatalf("password step: %d %s", resp.StatusCode, body)
	}
}

func (c *client) code(code string) (int, string) {
	return c.errorOf("POST", "/api/v1/login/code", map[string]string{"code": code})
}

func TestTwoFactorSignIn(t *testing.T) {
	e := newEnv(t)
	secret, codes := enableTwoFactor(t, e, e.setup())
	e.advance(time.Minute) // the setup code's step is used

	c := e.client()
	resp, _ := c.do("POST", "/api/v1/login", credentials{"alice", pw})
	ck := cookie(resp, codeCookie)
	if ck == nil || ck.Path != codePath || ck.SameSite != http.SameSiteStrictMode || cookie(resp, sessionCookie) != nil {
		t.Fatalf("password step: %+v", ck)
	}
	if code, msg := c.code("000000"); code != http.StatusUnauthorized || msg != "That code is not right." {
		t.Errorf("wrong code: %d %q", code, msg)
	}
	code, _ := totp.Code(secret, totp.Step(e.clock()))
	var v sessionView
	if status := c.call("POST", "/api/v1/login/code", map[string]string{"code": code}, &v); status != http.StatusOK ||
		v.User == nil || !v.User.TwoFactor {
		t.Fatalf("code: %d %+v", status, v)
	}
	// The same code does not work twice; a recovery code works.
	c2 := e.client()
	c2.passwordStep()
	if status, _ := c2.code(code); status != http.StatusUnauthorized {
		t.Errorf("a code used twice: %d", status)
	}
	if status, _ := c2.code(codes[0]); status != http.StatusOK {
		t.Errorf("recovery code: %d", status)
	}
	if !strings.Contains(e.log.String(), "recovery code used") {
		t.Error("recovery code not logged")
	}
}

func TestTwoFactorStepEnds(t *testing.T) {
	e := newEnv(t)
	_, codes := enableTwoFactor(t, e, e.setup())
	// Without the password step, or after it expired.
	if status, msg := e.client().code(codes[1]); status != http.StatusUnauthorized || !strings.Contains(msg, "expired") {
		t.Errorf("no pending sign-in: %d %q", status, msg)
	}
	c := e.client()
	c.passwordStep()
	e.advance(codeTTL + time.Second)
	if status, _ := c.code(codes[1]); status != http.StatusUnauthorized {
		t.Errorf("expired: %d", status)
	}
	// Five tries per password step.
	c = e.client()
	c.passwordStep()
	for range codeAttempts {
		c.code("000000")
	}
	if status, msg := c.code(codes[1]); status != http.StatusUnauthorized || !strings.Contains(msg, "expired") {
		t.Errorf("sixth try: %d %q", status, msg)
	}
	// The failures count toward the rate limit.
	if status, _ := c.errorOf("POST", "/api/v1/login", credentials{"alice", pw}); status != http.StatusTooManyRequests {
		t.Errorf("rate limit after wrong codes: %d", status)
	}
	e.s.limiter.reset("user:alice", "ip:127.0.0.1")
	c = e.client()
	c.passwordStep()
	e.s.limiter.fail(e.clock(), "user:alice", "user:alice", "user:alice", "user:alice", "user:alice")
	if status, _ := c.code(codes[1]); status != http.StatusTooManyRequests {
		t.Errorf("code step while limited: %d", status)
	}
}

func TestPendingCodes(t *testing.T) {
	p := &pendingCodes{m: map[string]*pendingCode{}}
	now := time.Now()
	old := p.add(&pendingCode{expires: now.Add(-time.Second)}, now)
	p.add(&pendingCode{expires: now.Add(time.Minute)}, now)
	if _, ok := p.m[old]; ok || len(p.m) != 1 {
		t.Errorf("expired entries are not dropped: %d", len(p.m))
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	csrf := c.csrf
	c.csrf = ""
	if code, msg := c.errorOf("PUT", "/api/v1/account/password", map[string]string{"current": pw, "new": "another long password"}); code != http.StatusForbidden ||
		!strings.Contains(msg, "CSRF") {
		t.Errorf("without the token: %d %q", code, msg)
	}
	c.csrf = "wrong"
	if code, _ := c.errorOf("POST", "/api/v1/tokens", map[string]string{"name": "ci"}); code != http.StatusForbidden {
		t.Errorf("wrong token: %d", code)
	}
	c.csrf = csrf
	if code := c.call("POST", "/api/v1/tokens", map[string]string{"name": "ci"}, nil); code != http.StatusCreated {
		t.Errorf("with the token: %d", code)
	}
	// Reading needs no token.
	c.csrf = ""
	if code := c.call("GET", "/api/v1/tokens", nil, nil); code != http.StatusOK {
		t.Errorf("GET without the token: %d", code)
	}
}

func TestRead(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var tok newToken
	admin.call("POST", "/api/v1/tokens", map[string]string{"name": "ci"}, &tok)
	h := e.s.read(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("read")) })
	srv := httptest.NewServer(h)
	defer srv.Close()
	c := e.client()
	for _, tc := range []struct {
		method, auth string
		want         int
	}{
		{"GET", "Bearer " + tok.Token, http.StatusOK},
		{"GET", "Bearer eyt_unknown", http.StatusUnauthorized},
		{"POST", "Bearer " + tok.Token, http.StatusForbidden},
		{"GET", "", http.StatusUnauthorized},
	} {
		h := []string{}
		if tc.auth != "" {
			h = []string{"Authorization", tc.auth}
		}
		if resp, _ := c.do(tc.method, srv.URL+"/", nil, h...); resp.StatusCode != tc.want {
			t.Errorf("%s %q: %d", tc.method, tc.auth, resp.StatusCode)
		}
	}
	e.st.Close()
	if resp, _ := c.do("GET", srv.URL+"/", nil, "Authorization", "Bearer "+tok.Token); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("closed database: %d", resp.StatusCode)
	}
}

func TestProxies(t *testing.T) {
	s := &Server{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}
	req := func(remote string, h ...string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://eyrie.example.com/", nil)
		r.RemoteAddr = remote
		for i := 0; i+1 < len(h); i += 2 {
			r.Header.Add(h[i], h[i+1])
		}
		return r
	}
	for _, tc := range []struct {
		r    *http.Request
		want string
	}{
		{req("203.0.113.9:1234", "X-Forwarded-For", "198.51.100.1"), "203.0.113.9"},
		{req("192.0.2.10:1234", "X-Forwarded-For", "198.51.100.1, 203.0.113.7"), "203.0.113.7"},
		{req("192.0.2.10:1234", "X-Forwarded-For", "198.51.100.1, 192.0.2.11"), "198.51.100.1"},
		{req("192.0.2.10:1234", "X-Forwarded-For", "junk, 192.0.2.11", "X-Real-IP", "198.51.100.4"), "198.51.100.4"},
		{req("192.0.2.10:1234"), "192.0.2.10"},
		{req("[::ffff:203.0.113.9]:1234"), "203.0.113.9"},
		{req("not an address"), "not an address"},
	} {
		if got := s.clientIP(tc.r); got != tc.want {
			t.Errorf("%s %v: %s", tc.r.RemoteAddr, tc.r.Header, got)
		}
	}
	if s.secure(req("203.0.113.9:1", "X-Forwarded-Proto", "https")) || !s.secure(req("192.0.2.1:1", "X-Forwarded-Proto", "HTTPS")) {
		t.Error("X-Forwarded-Proto is believed only from trusted proxies")
	}
	tlsReq := req("203.0.113.9:1")
	tlsReq.TLS = &tls.ConnectionState{}
	if !s.secure(tlsReq) {
		t.Error("TLS is secure")
	}
	if got := s.externalURL(req("192.0.2.1:1", "X-Forwarded-Proto", "https", "X-Forwarded-Host", "eyrie.example.net")); got != "https://eyrie.example.net" {
		t.Errorf("behind a proxy: %s", got)
	}
	if got := s.externalURL(req("203.0.113.9:1", "X-Forwarded-Host", "evil.example.net")); got != "http://eyrie.example.com" {
		t.Errorf("direct: %s", got)
	}
	s.BaseURL = "https://eyrie.example.org"
	if got := s.externalURL(req("203.0.113.9:1")); got != s.BaseURL || !s.secure(req("203.0.113.9:1")) {
		t.Errorf("base URL: %s", got)
	}
}
