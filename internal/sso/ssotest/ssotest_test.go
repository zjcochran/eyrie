// SPDX-License-Identifier: AGPL-3.0-or-later

package ssotest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func get(t *testing.T, u string, header ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	if len(header) == 2 {
		req.Header.Set(header[0], header[1])
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func exchange(t *testing.T, p *Provider, code, verifier, secret string) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(p.URL+"/token", url.Values{"code": {code}, "code_verifier": {verifier},
		"client_id": {ClientID}, "client_secret": {secret}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestDiscoveryAndKeys(t *testing.T) {
	p := New()
	defer p.Close()
	p.SetScopes([]string{"openid"})
	if _, body := get(t, p.URL+"/.well-known/openid-configuration"); !strings.Contains(body, `"scopes_supported":["openid"]`) {
		t.Errorf("discovery %s", body)
	}
	if _, body := get(t, p.URL+"/jwks"); !strings.Contains(body, `"kid":"k1"`) {
		t.Errorf("jwks %s", body)
	}
}

func TestAuthorize(t *testing.T) {
	p := New()
	defer p.Close()
	if resp, _ := get(t, p.URL+"/authorize?redirect_uri=x"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("without PKCE: %d", resp.StatusCode)
	}
	if resp, _ := get(t, p.URL+"/authorize?code_challenge_method=S256&code_challenge=c&redirect_uri=%25zz"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad redirect: %d", resp.StatusCode)
	}
	if _, err := Approve(p.URL + "/authorize"); err == nil {
		t.Error("approved without PKCE")
	}
	if _, err := Approve("http://%zz"); err == nil {
		t.Error("approved a bad URL")
	}
	p.Refuse(true)
	back, err := Approve(authURL(p))
	if err != nil || back.Query().Get("error") != "access_denied" {
		t.Errorf("refused: %v %v", back, err)
	}
	p.Refuse(false)
	back, err = Approve(authURL(p))
	if err != nil || back.Query().Get("state") != "st" || back.Query().Get("code") == "" || p.Scope() != "openid" {
		t.Fatalf("approved: %v %v", back, err)
	}
}

func authURL(p *Provider) string {
	return p.URL + "/authorize?" + url.Values{"code_challenge_method": {"S256"}, "code_challenge": {challenge("v")},
		"redirect_uri": {"https://eyrie.example.com/cb"}, "state": {"st"}, "scope": {"openid"}, "nonce": {"n"}}.Encode()
}

func TestToken(t *testing.T) {
	p := New()
	defer p.Close()
	back, _ := Approve(authURL(p))
	if status, _ := exchange(t, p, back.Query().Get("code"), "wrong", ClientSecret); status != http.StatusBadRequest {
		t.Errorf("wrong verifier: %d", status)
	}
	back, _ = Approve(authURL(p))
	status, out := exchange(t, p, back.Query().Get("code"), "v", ClientSecret)
	if status != http.StatusOK || out["access_token"] != AccessToken || out["id_token"] == "" {
		t.Errorf("token: %d %v", status, out)
	}
	p.SignHS256([]byte(ClientSecret))
	back, _ = Approve(authURL(p))
	if status, out := exchange(t, p, back.Query().Get("code"), "v", ClientSecret); status != http.StatusOK || out["id_token"] == "" {
		t.Errorf("HS256 token: %d %v", status, out)
	}
	if resp, err := http.Post(p.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader("%zz")); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad form: %v %v", resp, err)
	}
}

func TestUserinfoAndPicture(t *testing.T) {
	p := New()
	defer p.Close()
	if resp, _ := get(t, p.URL+"/userinfo"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("userinfo without a token: %d", resp.StatusCode)
	}
	p.SetUserinfoGroups([]string{"g"})
	p.SetClaims(map[string]any{"sub": "user-9"})
	if _, body := get(t, p.URL+"/userinfo", "Authorization", "Bearer "+AccessToken); !strings.Contains(body, `"groups":["g"]`) ||
		!strings.Contains(body, "user-9") {
		t.Errorf("userinfo %s", body)
	}
	if resp, _ := get(t, p.URL+"/picture.png", "Authorization", "Bearer "+AccessToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("no picture: %d", resp.StatusCode)
	}
	p.SetPicture([]byte("img"))
	if _, body := get(t, p.URL+"/picture.png", "Authorization", "Bearer "+AccessToken); body != "img" {
		t.Errorf("picture %q", body)
	}
}
