// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/totp"
)

// A PNG and a GIF, as far as content sniffing is concerned.
var (
	png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	gif = append([]byte("GIF89a"), make([]byte, 32)...)
)

func TestAccount(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	var a accountView
	if code := c.call("GET", "/api/v1/account", nil, &a); code != http.StatusOK || a.User.Username != "alice" ||
		len(a.Identities) != 0 || !a.TwoFactor.Available || a.TwoFactor.On || a.SSO.Enabled {
		t.Fatalf("account: %d %+v", code, a)
	}
	if code := e.client().call("GET", "/api/v1/account", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	other := e.signIn("alice", pw)
	if code, msg := c.errorOf("PUT", "/api/v1/account/password", map[string]string{"current": "wrong horse battery", "new": "another long password"}); code != http.StatusUnprocessableEntity ||
		msg != "Your current password is not right." {
		t.Errorf("wrong current: %d %q", code, msg)
	}
	if code, msg := c.errorOf("PUT", "/api/v1/account/password", map[string]string{"current": pw, "new": "short"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "12 characters") {
		t.Errorf("short new: %d %q", code, msg)
	}
	var v sessionView
	if code := c.call("PUT", "/api/v1/account/password", map[string]string{"current": pw, "new": "another long password"}, &v); code != http.StatusOK ||
		v.CSRF == "" || v.CSRF == c.csrf {
		t.Fatalf("change: %d %+v", code, v)
	}
	c.keep(v)
	if code := c.call("GET", "/api/v1/account", nil, nil); code != http.StatusOK {
		t.Errorf("this browser was signed out: %d", code)
	}
	if code := other.call("GET", "/api/v1/account", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("another session survived: %d", code)
	}
	e.signIn("alice", "another long password")
}

func TestTwoFactorSetup(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	var setup twoFactorSetup
	if code := c.call("POST", "/api/v1/account/two-factor/setup", nil, &setup); code != http.StatusOK || len(setup.Secret) != 32 {
		t.Fatalf("setup: %d %+v", code, setup)
	}
	if !strings.HasPrefix(setup.QR, "data:image/png;base64,") || !strings.HasPrefix(setup.URI, "otpauth://totp/Eyrie:alice?") ||
		!strings.HasPrefix(setup.Sealed, "v1:") {
		t.Errorf("setup %+v", setup)
	}
	code, _ := totp.Code(setup.Secret, totp.Step(e.clock()))
	for _, tc := range []struct{ sealed, code, password, want string }{
		{setup.Sealed, code, "wrong horse battery", "password"},
		{"v1:junk", code, pw, "expired"},
		{setup.Sealed, "000000", pw, "not right"},
	} {
		status, msg := c.errorOf("POST", "/api/v1/account/two-factor/enable", map[string]string{"sealed": tc.sealed, "code": tc.code, "password": tc.password})
		if status != http.StatusUnprocessableEntity || !strings.Contains(msg, tc.want) {
			t.Errorf("%+v: %d %q", tc, status, msg)
		}
	}
	var rc recoveryCodes
	if status := c.call("POST", "/api/v1/account/two-factor/enable",
		map[string]string{"sealed": setup.Sealed, "code": code, "password": pw}, &rc); status != http.StatusOK || len(rc.Codes) != 10 {
		t.Fatalf("enable: %d %+v", status, rc)
	}
	var a accountView
	c.call("GET", "/api/v1/account", nil, &a)
	if !a.TwoFactor.On || a.TwoFactor.CodesLeft != 10 || !a.User.TwoFactor {
		t.Errorf("after enabling: %+v", a)
	}
	if status, _ := c.errorOf("POST", "/api/v1/account/two-factor/setup", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("setup while on: %d", status)
	}
	if status, _ := c.errorOf("POST", "/api/v1/account/two-factor/enable", map[string]string{"sealed": setup.Sealed, "code": code, "password": pw}); status != http.StatusUnprocessableEntity {
		t.Errorf("enable while on: %d", status)
	}
}

func TestTwoFactorManage(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	_, codes := enableTwoFactor(t, e, c)
	if status, _ := c.errorOf("POST", "/api/v1/account/two-factor/recovery-codes", passwordBody{"wrong horse battery"}); status != http.StatusUnprocessableEntity {
		t.Errorf("new codes, wrong password: %d", status)
	}
	var fresh recoveryCodes
	if status := c.call("POST", "/api/v1/account/two-factor/recovery-codes", passwordBody{pw}, &fresh); status != http.StatusOK ||
		len(fresh.Codes) != 10 || fresh.Codes[0] == codes[0] {
		t.Errorf("new codes: %d", status)
	}
	if status, _ := c.errorOf("POST", "/api/v1/account/two-factor/disable", passwordBody{"wrong horse battery"}); status != http.StatusUnprocessableEntity {
		t.Errorf("disable, wrong password: %d", status)
	}
	for range 2 {
		if status := c.call("POST", "/api/v1/account/two-factor/disable", passwordBody{pw}, nil); status != http.StatusNoContent {
			t.Errorf("disable: %d", status)
		}
	}
	if status, msg := c.errorOf("POST", "/api/v1/account/two-factor/recovery-codes", passwordBody{pw}); status != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "off") {
		t.Errorf("new codes while off: %d %q", status, msg)
	}
	if !strings.Contains(e.log.String(), "two-factor sign-in off") {
		t.Error("not logged")
	}
}

func TestUploadPicture(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, _ := e.addUser(admin, "bob", "viewer")
	var u userView
	if code := bob.call("PUT", "/api/v1/account/picture", png, &u); code != http.StatusOK || !strings.HasPrefix(u.Picture, "/api/v1/users/") {
		t.Fatalf("upload: %d %+v", code, u)
	}
	for _, tc := range []struct {
		body []byte
		want string
	}{{[]byte("<svg></svg>"), "PNG, JPEG, GIF or WebP"}, {append(gif, make([]byte, MaxPictureBytes)...), "larger"}} {
		if code, msg := bob.errorOf("PUT", "/api/v1/account/picture", tc.body); code != http.StatusUnprocessableEntity || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %d %q", tc.want, code, msg)
		}
	}
	for who, c := range map[string]*client{"owner": bob, "admin": admin} {
		resp, body := c.do("GET", u.Picture, nil)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, png) ||
			!strings.Contains(resp.Header.Get("Cache-Control"), "private") {
			t.Errorf("%s: %d %s", who, resp.StatusCode, resp.Header)
		}
	}
	carol, _ := e.addUser(admin, "carol", "viewer")
	if code := carol.call("GET", u.Picture, nil, nil); code != http.StatusNotFound {
		t.Errorf("another viewer: %d", code)
	}
	if code := admin.call("GET", "/api/v1/users/1/picture", nil, nil); code != http.StatusNotFound {
		t.Errorf("no picture: %d", code)
	}
}

func TestRemovePicture(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, bobUser := e.addUser(admin, "bob", "viewer")
	var u userView
	bob.call("PUT", "/api/v1/account/picture", png, &u)
	if code := bob.call("DELETE", "/api/v1/account/picture", nil, &u); code != http.StatusOK || u.Picture != "" || !u.SSOPicture {
		t.Errorf("remove: %d %+v", code, u)
	}
	// Removing a picture from single sign-on stops it coming back, until
	// the user asks for it again.
	e.st.SetUserPicture(context.Background(), bobUser.ID, "image/png", png, store.PictureSSO, e.clock())
	if bob.call("DELETE", "/api/v1/account/picture", nil, &u); u.SSOPicture {
		t.Errorf("single sign-on picture still allowed: %+v", u)
	}
	if code := bob.call("POST", "/api/v1/account/picture/sso", nil, &u); code != http.StatusOK || !u.SSOPicture {
		t.Errorf("use it again: %d %+v", code, u)
	}
}

func TestReadPicture(t *testing.T) {
	if ct, _, err := readPicture(bytes.NewReader(gif)); ct != "image/gif" || err != nil {
		t.Errorf("gif: %q %v", ct, err)
	}
	if _, _, err := readPicture(failingReader{}); err == nil {
		t.Error("a read error was ignored")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, http.ErrBodyNotAllowed }
