package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zjcochran/eyrie/internal/sso"
	"github.com/zjcochran/eyrie/internal/sso/ssotest"
)

func ssoBody(p *ssotest.Provider, extra map[string]any) map[string]any {
	b := map[string]any{"enabled": true, "issuer": p.URL + "/", "client_id": ssotest.ClientID,
		"client_secret": ssotest.ClientSecret, "auto_provision": true}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

// configure turns single sign-on on through the settings API.
func configure(t *testing.T, admin *client, p *ssotest.Provider, extra map[string]any) ssoSettingsView {
	t.Helper()
	var v ssoSettingsView
	if code := admin.call("PUT", "/api/v1/settings/sso", ssoBody(p, extra), &v); code != http.StatusOK {
		t.Fatalf("save sso: %d", code)
	}
	return v
}

// ssoSignIn runs a whole sign-in from path (/auth/oidc/login?…): Eyrie,
// the provider, Eyrie's callback. It returns the callback's redirect.
func (c *client) ssoSignIn(path string) *url.URL {
	c.e.t.Helper()
	resp, body := c.do("GET", path, nil)
	if resp.StatusCode != http.StatusFound {
		loc, _ := resp.Location()
		if loc != nil {
			return loc // refused before the provider
		}
		c.e.t.Fatalf("start: %d %s", resp.StatusCode, body)
	}
	back, err := ssotest.Approve(resp.Header.Get("Location"))
	if err != nil {
		c.e.t.Fatal(err)
	}
	resp, body = c.do("GET", back.String(), nil)
	loc, err := resp.Location()
	if resp.StatusCode != http.StatusSeeOther || err != nil {
		c.e.t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	return loc
}

// session reads the browser's session and keeps its CSRF token.
func (c *client) session() sessionView {
	c.e.t.Helper()
	var v sessionView
	c.call("GET", "/api/v1/session", nil, &v)
	return c.keep(v)
}

func TestSSOSettings(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	var v ssoSettingsView
	if code := admin.call("GET", "/api/v1/settings/sso", nil, &v); code != http.StatusOK || v.Enabled || !v.AutoProvision ||
		v.CallbackURL != e.srv.URL+"/auth/oidc/callback" {
		t.Fatalf("defaults: %d %+v", code, v)
	}
	for _, bad := range []map[string]any{{"issuer": "id.example.com"}, {"client_id": ""}} {
		if code, msg := admin.errorOf("PUT", "/api/v1/settings/sso", ssoBody(p, bad)); code != http.StatusUnprocessableEntity || msg == "" {
			t.Errorf("%v: %d %q", bad, code, msg)
		}
	}
	v = configure(t, admin, p, map[string]any{"display_name": " Example ID "})
	if !v.Enabled || v.Issuer != p.URL || !v.HasSecret || v.DisplayName != "Example ID" {
		t.Errorf("saved %+v", v)
	}
	var s sessionView
	if e.client().call("GET", "/api/v1/session", nil, &s); !s.SSO.Enabled || s.SSO.Name != "Example ID" {
		t.Errorf("sign-in page: %+v", s.SSO)
	}
	var raw map[string]any
	e.st.GetSetting(context.Background(), sso.SettingKey, &raw)
	if s, _ := raw["client_secret"].(string); !strings.HasPrefix(s, "v1:") || strings.Contains(s, ssotest.ClientSecret) {
		t.Errorf("client secret stored as %q", s)
	}
	// An empty secret keeps the saved one; clear_secret removes it.
	if v = configure(t, admin, p, map[string]any{"client_secret": ""}); !v.HasSecret {
		t.Error("the secret was lost")
	}
	if v = configure(t, admin, p, map[string]any{"client_secret": "", "clear_secret": true}); v.HasSecret {
		t.Error("the secret was not cleared")
	}
}

func TestSSOTest(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	var res ssoTest
	if code := admin.call("POST", "/api/v1/settings/sso/test", map[string]string{"issuer": p.URL + "/"}, &res); code != http.StatusOK ||
		!res.OK || !strings.Contains(res.Message, p.URL+"/authorize") {
		t.Errorf("test: %d %+v", code, res)
	}
	if admin.call("POST", "/api/v1/settings/sso/test", map[string]string{"issuer": "nope"}, &res); res.OK {
		t.Errorf("test a bad issuer: %+v", res)
	}

	// Turning off passwords needs the admin's own account linked.
	if code, msg := admin.errorOf("PUT", "/api/v1/settings/sso", ssoBody(p, map[string]any{"sso_only": true})); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "Link your own account") {
		t.Errorf("sso only, unlinked: %d %q", code, msg)
	}
}

func TestSSOSignIn(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	p.SetClaims(map[string]any{"sub": "user-1", "preferred_username": "bob", "email": "bob@example.com",
		"picture": p.URL + "/picture.png", "groups": []string{"Eyrie-Admins"}})
	p.SetPicture(png)
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, map[string]any{"admin_group": "eyrie-admins"})

	c := e.client()
	if loc := c.ssoSignIn("/auth/oidc/login?next=/monitors"); loc.Path != "/monitors" {
		t.Fatalf("redirected to %s", loc)
	}
	v := c.session()
	if v.User == nil || v.User.Username != "bob" || v.User.Role != "admin" || v.User.Password || !v.User.SSO ||
		v.User.Picture == "" || v.User.LastLogin == nil || !v.User.RoleFromGroup {
		t.Fatalf("provisioned %+v", v.User)
	}
	// The admin group decides bob's role: removed from it, he is a viewer
	// at his next sign-in, and admins cannot change it.
	if code, msg := admin.errorOf("PATCH", fmt.Sprintf("/api/v1/users/%d", v.User.ID), map[string]string{"role": "viewer"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "admin group") {
		t.Errorf("role of an SSO account: %d %q", code, msg)
	}
	p.SetClaims(map[string]any{"sub": "user-1", "preferred_username": "bob"})
	c = e.client()
	if loc := c.ssoSignIn("/auth/oidc/login?next=//evil.example.net"); loc.String() != e.srv.URL+"/" {
		t.Errorf("an off-site next: %s", loc)
	}
	if v = c.session(); v.User.Role != "viewer" || v.User.Picture != "" {
		t.Errorf("after leaving the group: %+v", v.User)
	}
}

func TestSSORefusals(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, nil)
	sso := e.client()
	sso.ssoSignIn("/auth/oidc/login")
	id := sso.session().User.ID

	// A disabled account is refused.
	if code, msg := admin.errorOf("PATCH", fmt.Sprintf("/api/v1/users/%d", id), map[string]bool{"disabled": true}); code != http.StatusOK {
		t.Fatalf("disable: %d %s", code, msg)
	}
	if loc := e.client().ssoSignIn("/auth/oidc/login"); loc.Path != loginPage || loc.Query().Get("error") != "Your account is disabled." {
		t.Errorf("disabled: %s", loc)
	}
	// Without account creation, an unknown identity is refused.
	configure(t, admin, p, map[string]any{"auto_provision": false})
	p.SetClaims(map[string]any{"sub": "user-3", "preferred_username": "carol"})
	if loc := e.client().ssoSignIn("/auth/oidc/login"); !strings.Contains(loc.Query().Get("error"), "No Eyrie account is linked") {
		t.Errorf("unknown identity: %s", loc)
	}
	// The provider refusing.
	p.Refuse(true)
	if loc := e.client().ssoSignIn("/auth/oidc/login"); !strings.Contains(loc.Query().Get("error"), "refused the sign-in") {
		t.Errorf("provider refusal: %s", loc)
	}
	p.Refuse(false)

	// Single sign-on off: no routes.
	configure(t, admin, p, map[string]any{"enabled": false})
	for _, path := range []string{"/auth/oidc/login", "/auth/oidc/callback"} {
		if code := e.client().call("GET", path, nil, nil); code != http.StatusNotFound {
			t.Errorf("%s while off: %d", path, code)
		}
	}
}

func TestSSOProviderDown(t *testing.T) {
	p := ssotest.New()
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, nil)
	c := e.client()
	resp, _ := c.do("GET", "/auth/oidc/login", nil)
	back, _ := ssotest.Approve(resp.Header.Get("Location"))
	p.Close()
	resp, _ = c.do("GET", back.String(), nil)
	if loc, _ := resp.Location(); loc == nil || loc.Path != loginPage || loc.Query().Get("error") == "" {
		t.Errorf("callback with the provider gone: %d %v", resp.StatusCode, loc)
	}
	e.s.sso.Forget()
	resp, _ = e.client().do("GET", "/auth/oidc/login", nil)
	if loc, _ := resp.Location(); loc == nil || !strings.Contains(loc.Query().Get("error"), "Cannot reach") {
		t.Errorf("start with the provider gone: %d %v", resp.StatusCode, loc)
	}
}

func TestSSOLink(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, map[string]any{"display_name": "Example ID"})
	enableTwoFactor(t, e, admin)

	// Linking needs a session.
	if loc := e.client().ssoSignIn("/auth/oidc/login?link=1"); loc.Path != loginPage {
		t.Errorf("link signed out: %s", loc)
	}
	loc := admin.ssoSignIn("/auth/oidc/login?link=1")
	if loc.Path != accountPage || loc.Query().Get("message") != "Example ID account linked. Two-factor sign-in is now off: use Example ID's." {
		t.Fatalf("link: %s", loc)
	}
	var a accountView
	admin.call("GET", "/api/v1/account", nil, &a)
	if len(a.Identities) != 1 || a.Identities[0].Email != "alice@example.com" || a.TwoFactor.On || a.TwoFactor.Available {
		t.Errorf("after linking: %+v", a)
	}
	// alice can now sign in either way.
	c := e.client()
	c.ssoSignIn("/auth/oidc/login")
	if v := c.session(); v.User == nil || v.User.Username != "alice" {
		t.Errorf("sso sign-in as alice: %+v", v.User)
	}
	// Another user cannot link the same identity.
	bob, _ := e.addUser(admin, "bob", "viewer")
	if loc := bob.ssoSignIn("/auth/oidc/login?link=1"); loc.Path != accountPage || !strings.Contains(loc.Query().Get("error"), "already linked to alice") {
		t.Errorf("link someone else's: %s", loc)
	}
	// Signed out between start and callback.
	resp, _ := bob.do("GET", "/auth/oidc/login?link=1", nil)
	back, _ := ssotest.Approve(resp.Header.Get("Location"))
	bob.do("POST", "/api/v1/logout", nil)
	resp, _ = bob.do("GET", back.String(), nil)
	if loc, _ := resp.Location(); loc == nil || !strings.Contains(loc.Query().Get("error"), "Sign in again") {
		t.Errorf("link after signing out: %v", loc)
	}
}

func TestSSOOnly(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, map[string]any{"display_name": "Example ID"})
	admin.ssoSignIn("/auth/oidc/login?link=1")

	// SSO only: passwords are refused, unless forced.
	configure(t, admin, p, map[string]any{"sso_only": true, "display_name": "Example ID"})
	if code, msg := e.client().errorOf("POST", "/api/v1/login", credentials{"alice", pw}); code != http.StatusForbidden ||
		!strings.Contains(msg, "sign in with Example ID") {
		t.Errorf("password while SSO only: %d %q", code, msg)
	}
	var s sessionView
	if e.client().call("GET", "/api/v1/session", nil, &s); s.PasswordSignIn {
		t.Error("the sign-in page offers passwords")
	}
	e.s.ForceLocal = true
	if code := e.client().call("POST", "/api/v1/login", credentials{"alice", pw}, nil); code != http.StatusOK {
		t.Errorf("forced local sign-in: %d", code)
	}
	e.s.ForceLocal = false
}

func TestSSOUnlink(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, nil)
	admin.ssoSignIn("/auth/oidc/login?link=1")
	if code := admin.call("DELETE", "/api/v1/account/sso", nil, nil); code != http.StatusNoContent {
		t.Errorf("unlink: %d", code)
	}
	// An account without a password cannot unlink.
	p.SetClaims(map[string]any{"sub": "user-9", "preferred_username": "dave"})
	dave := e.client()
	dave.ssoSignIn("/auth/oidc/login")
	daveID := dave.session().User.ID
	if code, msg := dave.errorOf("DELETE", "/api/v1/account/sso", nil); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "password") {
		t.Errorf("unlink the only way in: %d %q", code, msg)
	}
	if code, msg := dave.errorOf("PUT", "/api/v1/account/password", map[string]string{"current": "", "new": pw}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "single sign-on only") {
		t.Errorf("password of an SSO-only account: %d %q", code, msg)
	}
	if code, msg := admin.errorOf("PUT", fmt.Sprintf("/api/v1/users/%d/password", daveID), passwordBody{pw}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "single sign-on only") {
		t.Errorf("reset the password of an SSO-only account: %d %q", code, msg)
	}
}

func TestSSOPicture(t *testing.T) {
	p := ssotest.New()
	defer p.Close()
	e := newEnv(t)
	admin := e.setup()
	configure(t, admin, p, nil)
	l := sso.Login{Issuer: p.URL, AccessToken: ssotest.AccessToken}
	ctx := context.Background()
	for _, bad := range []string{"ftp://example.com/x.png", "%zz", p.URL + "/picture.png"} { // the last: not served yet
		l.Claims.Picture = bad
		if err := e.s.fetchSSOPicture(ctx, 1, l); err == nil {
			t.Errorf("fetched %q", bad)
		}
	}
	p.SetPicture([]byte("<svg/>"))
	if err := e.s.fetchSSOPicture(ctx, 1, l); err == nil {
		t.Error("stored an SVG")
	}
	p.SetPicture(gif)
	if err := e.s.fetchSSOPicture(ctx, 1, l); err != nil {
		t.Fatal(err)
	}
	if pic, ok, _ := e.st.UserPicture(ctx, 1); !ok || pic.Source != "sso" || pic.ContentType != "image/gif" {
		t.Errorf("picture %+v", pic)
	}
	// Another host never gets the access token.
	l.Issuer = "https://id.example.net"
	if err := e.s.fetchSSOPicture(ctx, 1, l); err == nil {
		t.Error("the access token went to another host")
	}
	if err := e.s.fetchSSOPicture(ctx, 999, l); err == nil {
		t.Error("unknown user")
	}
	e.s.refreshSSOPicture(ctx, 999, l)
	if !strings.Contains(e.log.String(), "cannot fetch the single sign-on profile picture") {
		t.Error("not logged")
	}
}
