package sso

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/sso/ssotest"
)

const callback = "https://eyrie.example.com/auth/oidc/callback"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func settings(p *ssotest.Provider) Settings {
	return Settings{Enabled: true, Issuer: p.URL, ClientID: ssotest.ClientID, AutoProvision: true}
}

// signIn runs a sign-in through the fake provider.
func signIn(t *testing.T, c *Client, s Settings, secret, next string) (Login, error) {
	t.Helper()
	auth, err := c.Start(context.Background(), s, secret, callback, next, 0)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ssotest.Approve(auth)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(back.String(), callback+"?") {
		t.Fatalf("sent back to %s", back)
	}
	return c.Finish(context.Background(), s, secret, callback, back.Query())
}

func refusal(t *testing.T, err error) string {
	t.Helper()
	var r *Refused
	if !errors.As(err, &r) {
		t.Fatalf("not refused: %v", err)
	}
	return r.Error()
}

func TestSettings(t *testing.T) {
	d := Defaults()
	if !d.AutoProvision || d.Enabled || d.Name() != "SSO" || d.Validate() != nil {
		t.Errorf("defaults %+v", d)
	}
	s := Settings{Enabled: true, Issuer: "https://id.example.com", ClientID: "eyrie", DisplayName: "Example ID"}
	if s.Name() != "Example ID" || s.Validate() != nil {
		t.Errorf("valid settings: %v", s.Validate())
	}
	for _, bad := range []Settings{
		{Enabled: true, Issuer: "id.example.com", ClientID: "x"},
		{Enabled: true, Issuer: "ftp://id.example.com", ClientID: "x"},
		{Enabled: true, Issuer: "https://", ClientID: "x"},
		{Enabled: true, Issuer: "https://id.example.com"},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestClaims(t *testing.T) {
	for want, c := range map[string]Claims{
		"alice":             {Subject: "s", PreferredUsername: "alice", Email: "a@example.com"},
		"alice@example.com": {Subject: "s", Email: "alice@example.com"},
		"s":                 {Subject: "s"},
	} {
		if got := c.Username(); got != want {
			t.Errorf("%+v: %q", c, got)
		}
	}
	if !InGroup([]string{"Eyrie-Admins"}, "eyrie-admins") || InGroup(nil, "x") {
		t.Error("InGroup")
	}
}

func TestSignIn(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	p.SetPicture([]byte("png"))
	c := New(quiet())
	l, err := signIn(t, c, settings(p), ssotest.ClientSecret, "/monitors")
	if err != nil {
		t.Fatal(err)
	}
	if l.Issuer != p.URL || l.Subject != "user-1" || l.Claims.Username() != "alice" || l.Claims.Email != "alice@example.com" ||
		l.AccessToken != ssotest.AccessToken || l.Next != "/monitors" || l.LinkUser != 0 {
		t.Fatalf("login %+v", l)
	}
	// Without scopes_supported, groups is requested.
	if p.Scope() != "openid profile email groups" {
		t.Errorf("scope %q", p.Scope())
	}
	// A state works once.
	auth, _ := c.Start(context.Background(), settings(p), ssotest.ClientSecret, callback, "/", 7)
	back, _ := ssotest.Approve(auth)
	if l, err := c.Finish(context.Background(), settings(p), ssotest.ClientSecret, callback, back.Query()); err != nil || l.LinkUser != 7 {
		t.Fatalf("link: %+v %v", l, err)
	}
	_, err = c.Finish(context.Background(), settings(p), ssotest.ClientSecret, callback, back.Query())
	if msg := refusal(t, err); !strings.Contains(msg, "expired or was already used") {
		t.Errorf("reused state: %s", msg)
	}
}

func TestScopes(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	p.SetScopes([]string{"openid", "profile", "email"})
	c := New(quiet())
	if _, err := signIn(t, c, settings(p), ssotest.ClientSecret, "/"); err != nil {
		t.Fatal(err)
	}
	if p.Scope() != "openid profile email" {
		t.Errorf("groups asked of a provider that does not list it: %q", p.Scope())
	}
	p.SetScopes([]string{"openid", "groups"})
	c.Forget()
	if _, err := signIn(t, c, settings(p), ssotest.ClientSecret, "/"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p.Scope(), " groups") {
		t.Errorf("groups not asked: %q", p.Scope())
	}
}

func TestRefusals(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	c := New(quiet())
	s := settings(p)
	s.DisplayName = "Example ID"

	p.Refuse(true)
	_, err := signIn(t, c, s, ssotest.ClientSecret, "/")
	if msg := refusal(t, err); msg != "Example ID refused the sign-in: access_denied no" {
		t.Errorf("provider refusal: %q", msg)
	}
	p.Refuse(false)

	_, err = signIn(t, c, s, "wrong secret", "/")
	if msg := refusal(t, err); !strings.Contains(msg, "Check the client ID and secret") {
		t.Errorf("wrong secret: %q", msg)
	}

	// An expired sign-in.
	auth, _ := c.Start(context.Background(), s, ssotest.ClientSecret, callback, "/", 0)
	back, _ := ssotest.Approve(auth)
	c.now = func() time.Time { return time.Now().Add(LoginTTL + time.Minute) }
	_, err = c.Finish(context.Background(), s, ssotest.ClientSecret, callback, back.Query())
	if msg := refusal(t, err); !strings.Contains(msg, "expired") {
		t.Errorf("expired: %q", msg)
	}
	// Starting another sign-in clears the expired one.
	c.Start(context.Background(), s, ssotest.ClientSecret, callback, "/", 0)
	c.now = time.Now
	if len(c.pending) != 1 {
		t.Errorf("%d sign-ins pending", len(c.pending))
	}

	// The allowed group, from the token or from userinfo, ignoring case.
	s.AllowedGroup = "Eyrie-Users"
	_, err = signIn(t, c, s, ssotest.ClientSecret, "/")
	if msg := refusal(t, err); msg != `Your Example ID account is not in the "Eyrie-Users" group.` {
		t.Errorf("group: %q", msg)
	}
	p.SetUserinfoGroups([]string{"eyrie-users"})
	if l, err := signIn(t, c, s, ssotest.ClientSecret, "/"); err != nil || len(l.Claims.Groups) != 1 {
		t.Errorf("groups from userinfo: %+v %v", l, err)
	}
	p.SetClaims(map[string]any{"sub": "user-2", "groups": []string{"EYRIE-USERS"}})
	if l, err := signIn(t, c, s, ssotest.ClientSecret, "/"); err != nil || l.Subject != "user-2" {
		t.Errorf("groups from the token: %+v %v", l, err)
	}
}

func TestHS256(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	c := New(quiet())
	p.SignHS256([]byte(ssotest.ClientSecret))
	if _, err := signIn(t, c, settings(p), ssotest.ClientSecret, "/"); err != nil {
		t.Fatalf("HS256 with the client secret: %v", err)
	}
	// Signed with another key: refused.
	p.SignHS256([]byte("another key that is long enough!!"))
	_, err := signIn(t, c, settings(p), ssotest.ClientSecret, "/")
	if msg := refusal(t, err); !strings.Contains(msg, "not valid") {
		t.Errorf("forged HS256: %q", msg)
	}
	// A secret too short for HS256 never verifies one.
	p.SignHS256([]byte("short"))
	if v := c.verifier(c.provider, ssotest.ClientID, "short"); v == nil {
		t.Fatal("no verifier")
	}
	if _, err := (hmacKeySet{secret: []byte(ssotest.ClientSecret), keys: c.keys}).VerifySignature(context.Background(), "not a token"); err == nil {
		t.Error("verified garbage")
	}
}

func TestDiscovery(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	c := New(quiet())
	if auth, err := c.Test(context.Background(), p.URL); err != nil || auth != p.URL+"/authorize" {
		t.Errorf("test: %q %v", auth, err)
	}
	if _, err := c.Test(context.Background(), "not a url"); err == nil {
		t.Error("tested a bad issuer")
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer broken.Close()
	if _, err := c.Test(context.Background(), broken.URL); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("broken provider: %v", err)
	}
	s := settings(p)
	s.Issuer = broken.URL
	if _, err := c.Start(context.Background(), s, "", callback, "/", 0); err == nil {
		t.Error("started a sign-in with a broken provider")
	}
	// Finish with a valid state but a provider that has gone away.
	s.Issuer = p.URL
	auth, _ := c.Start(context.Background(), s, ssotest.ClientSecret, callback, "/", 0)
	back, _ := ssotest.Approve(auth)
	c.Forget()
	s.Issuer = broken.URL
	if _, err := c.Finish(context.Background(), s, ssotest.ClientSecret, callback, back.Query()); err == nil {
		t.Error("finished with a broken provider")
	}
	// Discovery that is not JSON for the metadata.
	odd := httptest.NewServer(nil)
	defer odd.Close()
	odd.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"issuer":"` + odd.URL + `","jwks_uri":5}`))
	})
	if _, err := c.Test(context.Background(), odd.URL); err == nil {
		t.Error("accepted a broken discovery document")
	}
}
