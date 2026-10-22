package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestViewerCannotAdminister(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, _ := e.addUser(admin, "bob", "viewer")
	for _, r := range [][2]string{{"GET", "/api/v1/users"}, {"POST", "/api/v1/users"}, {"PATCH", "/api/v1/users/1"},
		{"DELETE", "/api/v1/users/1"}, {"GET", "/api/v1/tokens"}, {"POST", "/api/v1/tokens"}, {"GET", "/api/v1/settings/sso"},
		{"PUT", "/api/v1/settings/sso"}} {
		if code, msg := bob.errorOf(r[0], r[1], map[string]any{}); code != http.StatusForbidden || msg != "Only admins can do this." {
			t.Errorf("viewer %s %s: %d %q", r[0], r[1], code, msg)
		}
		if code := e.client().call(r[0], r[1], map[string]any{}, nil); code != http.StatusUnauthorized {
			t.Errorf("signed out %s %s: %d", r[0], r[1], code)
		}
	}
}

func TestCreateUser(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	for _, body := range []map[string]string{
		{"username": "", "password": pw, "role": "viewer"},
		{"username": "bob", "password": "short", "role": "viewer"},
		{"username": "bob", "password": pw, "role": "root"},
		{"username": "ALICE", "password": pw, "role": "viewer"},
	} {
		if code, msg := admin.errorOf("POST", "/api/v1/users", body); code != http.StatusUnprocessableEntity || msg == "" {
			t.Errorf("create %v: %d %q", body, code, msg)
		}
	}
	e.addUser(admin, "bob", "viewer")
	var list []userView
	if code := admin.call("GET", "/api/v1/users", nil, &list); code != http.StatusOK || len(list) != 2 ||
		list[0].Username != "alice" || list[1].Username != "bob" || list[1].Role != "viewer" {
		t.Fatalf("list: %d %+v", code, list)
	}
}

func TestUpdateUser(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, bobUser := e.addUser(admin, "bob", "viewer")
	path := fmt.Sprintf("/api/v1/users/%d", bobUser.ID)
	var u userView
	if code := admin.call("PATCH", path, map[string]string{"role": "admin"}, &u); code != http.StatusOK || u.Role != "admin" {
		t.Errorf("promote: %d %+v", code, u)
	}
	if code, _ := admin.errorOf("PATCH", path, map[string]string{"role": "root"}); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown role: %d", code)
	}
	if code := admin.call("PATCH", path, map[string]bool{"disabled": true}, &u); code != http.StatusOK || !u.Disabled {
		t.Errorf("disable: %d %+v", code, u)
	}
	if code := bob.call("GET", "/api/v1/account", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("a disabled user's session: %d", code)
	}
	if code, _ := e.client().errorOf("POST", "/api/v1/login", credentials{"bob", pw}); code != http.StatusUnauthorized {
		t.Errorf("a disabled user signed in: %d", code)
	}
	if code := admin.call("PATCH", path, map[string]bool{"disabled": false}, &u); code != http.StatusOK || u.Disabled {
		t.Errorf("enable: %d %+v", code, u)
	}
	if code, msg := admin.errorOf("PATCH", "/api/v1/users/1", map[string]string{"role": "viewer"}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "yourself") {
		t.Errorf("change myself: %d %q", code, msg)
	}
	if code := admin.call("PATCH", "/api/v1/users/999", map[string]string{"role": "viewer"}, nil); code != http.StatusNotFound {
		t.Errorf("unknown user: %d", code)
	}
	// Another admin can disable alice, the first admin.
	carol, _ := e.addUser(admin, "carol", "admin")
	if code := carol.call("PATCH", "/api/v1/users/1", map[string]bool{"disabled": true}, nil); code != http.StatusOK {
		t.Fatalf("disable alice: %d", code)
	}
	if code := admin.call("GET", "/api/v1/users", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("alice disabled: %d", code)
	}
}

func TestResetUser(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	_, bobUser := e.addUser(admin, "bob", "viewer")
	path := fmt.Sprintf("/api/v1/users/%d", bobUser.ID)
	if code := admin.call("PUT", path+"/password", passwordBody{"short"}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("short password: %d", code)
	}
	if code := admin.call("PUT", path+"/password", passwordBody{"another long password"}, nil); code != http.StatusNoContent {
		t.Errorf("reset password: %d", code)
	}
	bob := e.signIn("bob", "another long password")
	enableTwoFactor(t, e, bob, "another long password")
	for range 2 {
		if code := admin.call("DELETE", path+"/two-factor", nil, nil); code != http.StatusNoContent {
			t.Errorf("turn off 2FA: %d", code)
		}
	}
	if code := admin.call("DELETE", "/api/v1/users/999/two-factor", nil, nil); code != http.StatusNotFound {
		t.Errorf("2FA of unknown: %d", code)
	}
	if code := admin.call("PUT", "/api/v1/users/999/password", passwordBody{pw}, nil); code != http.StatusNotFound {
		t.Errorf("password of unknown: %d", code)
	}
	for _, line := range []string{"user created", "password reset", "two-factor sign-in off"} {
		if !strings.Contains(e.log.String(), line) {
			t.Errorf("%q not logged", line)
		}
	}
}

func TestDeleteUser(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	_, bobUser := e.addUser(admin, "bob", "viewer")
	path := fmt.Sprintf("/api/v1/users/%d", bobUser.ID)
	if code, msg := admin.errorOf("DELETE", "/api/v1/users/1", nil); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "yourself") {
		t.Errorf("delete myself: %d %q", code, msg)
	}
	if code := admin.call("DELETE", path, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code := admin.call("DELETE", path, nil, nil); code != http.StatusNotFound {
		t.Errorf("delete twice: %d", code)
	}
	if !strings.Contains(e.log.String(), "user deleted") {
		t.Error("not logged")
	}
}

func TestTokens(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	if code, msg := admin.errorOf("POST", "/api/v1/tokens", map[string]string{"name": " "}); code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "1 to 64") {
		t.Errorf("no name: %d %q", code, msg)
	}
	var tok newToken
	if code := admin.call("POST", "/api/v1/tokens", map[string]string{"name": "ci"}, &tok); code != http.StatusCreated ||
		!strings.HasPrefix(tok.Token, "eyt_") || tok.Name != "ci" || tok.CreatedBy != "alice" || tok.LastUsed != nil {
		t.Fatalf("create: %d %+v", code, tok)
	}
	var list []tokenView
	if code := admin.call("GET", "/api/v1/tokens", nil, &list); code != http.StatusOK || len(list) != 1 || list[0].ID != tok.ID {
		t.Fatalf("list: %d %+v", code, list)
	}
	e.st.UseAPIToken(t.Context(), tok.Token, e.clock())
	if admin.call("GET", "/api/v1/tokens", nil, &list); list[0].LastUsed == nil {
		t.Error("last use not shown")
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/tokens/%d", tok.ID), nil, nil); code != http.StatusNoContent {
		t.Errorf("revoke: %d", code)
	}
	if code := admin.call("DELETE", fmt.Sprintf("/api/v1/tokens/%d", tok.ID), nil, nil); code != http.StatusNotFound {
		t.Errorf("revoke twice: %d", code)
	}
	if !strings.Contains(e.log.String(), "api token revoked") {
		t.Error("not logged")
	}
}
