// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sso signs people in with an OpenID Connect provider: the
// authorization code flow with PKCE, ID-token verification, and the group
// rules. What to do with a verified sign-in (sign in, create or link an
// account) is up to the caller.
package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

// SettingKey is the setting that holds Settings as JSON.
const SettingKey = "sso"

// Settings configure single sign-on. ClientSecret is sealed with the
// instance key.
type Settings struct {
	Enabled       bool   `json:"enabled"`
	Issuer        string `json:"issuer"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	DisplayName   string `json:"display_name"`
	AutoProvision bool   `json:"auto_provision"`
	AllowedGroup  string `json:"allowed_group"`
	AdminGroup    string `json:"admin_group"`
	SSOOnly       bool   `json:"sso_only"`
}

// Defaults are the settings before any are saved.
func Defaults() Settings { return Settings{AutoProvision: true} }

// Name is the provider's name on the sign-in button and in messages:
// DisplayName, or "SSO".
func (s Settings) Name() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return "SSO"
}

// Validate checks the settings that must be right before single sign-on is
// turned on.
func (s Settings) Validate() error {
	if !s.Enabled {
		return nil
	}
	if err := checkIssuer(s.Issuer); err != nil {
		return err
	}
	if s.ClientID == "" {
		return errors.New("the client ID is required")
	}
	return nil
}

func checkIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("the issuer must be a URL such as https://id.example.com")
	}
	return nil
}

// LoginTTL is how long a sign-in may take at the provider.
const LoginTTL = 10 * time.Minute

// discoveryTimeout bounds loading the provider's discovery document.
const discoveryTimeout = 10 * time.Second

// Client runs sign-ins. The zero value is not usable: call New.
type Client struct {
	log *slog.Logger
	now func() time.Time

	mu       sync.Mutex
	issuer   string
	provider *oidc.Provider
	meta     providerMeta
	keys     oidc.KeySet
	pending  map[string]pending
}

// New returns a Client that logs to log.
func New(log *slog.Logger) *Client {
	return &Client{log: log, now: time.Now, pending: map[string]pending{}}
}

// pending is a sign-in waiting for the provider to send the browser back.
type pending struct {
	verifier, nonce string
	next            string
	linkUser        int64
	expires         time.Time
}

// providerMeta is the part of the discovery document Eyrie adapts to.
type providerMeta struct {
	JWKSURI string   `json:"jwks_uri"`
	Scopes  []string `json:"scopes_supported"`
	Algs    []string `json:"id_token_signing_alg_values_supported"`
}

// scopes are the scopes to request. "groups" is not a standard scope:
// providers that list their scopes without it refuse a sign-in that asks for
// it, so it is requested only if listed, or if the provider does not list
// its scopes at all.
func (m providerMeta) scopes() []string {
	sc := []string{oidc.ScopeOpenID, "profile", "email"}
	if len(m.Scopes) == 0 || slices.Contains(m.Scopes, "groups") {
		sc = append(sc, "groups")
	}
	return sc
}

// signingAlgs are the ID token algorithms Eyrie accepts.
var signingAlgs = map[string]bool{
	oidc.RS256: true, oidc.RS384: true, oidc.RS512: true, oidc.ES256: true, oidc.ES384: true, oidc.ES512: true,
	oidc.PS256: true, oidc.PS384: true, oidc.PS512: true, oidc.EdDSA: true, string(jose.HS256): true,
}

// minHMACSecret is the shortest client secret usable as an HS256 key
// (RFC 7518, section 3.2).
const minHMACSecret = 32

// hmacKeySet verifies ID tokens signed with HS256, whose key is the client
// secret (OIDC Core 10.1). Other algorithms go to the provider's published
// keys. The published keys are never used as an HMAC key: that confusion
// would let anyone forge tokens.
type hmacKeySet struct {
	secret []byte
	keys   oidc.KeySet
}

func (k hmacKeySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return k.keys.VerifySignature(ctx, raw) // not HS256
	}
	return jws.Verify(k.secret)
}

// discover loads the provider's discovery document, cached until the issuer
// changes or Forget is called.
func (c *Client) discover(ctx context.Context, issuer string) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil && c.issuer == issuer {
		return c.provider, nil
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", issuer, err)
	}
	var meta providerMeta
	if err := p.Claims(&meta); err != nil {
		return nil, fmt.Errorf("cannot read the discovery document of %s: %w", issuer, err)
	}
	// The key set fetches keys with this context's HTTP client, later, so it
	// must not be the request's.
	c.issuer, c.provider, c.meta = issuer, p, meta
	c.keys = oidc.NewRemoteKeySet(context.WithoutCancel(ctx), meta.JWKSURI)
	return p, nil
}

// Forget drops the cached discovery document, after the settings change.
func (c *Client) Forget() {
	c.mu.Lock()
	c.provider = nil
	c.mu.Unlock()
}

// Test loads the discovery document of issuer, without the cache, and
// returns the provider's sign-in URL.
func (c *Client) Test(ctx context.Context, issuer string) (string, error) {
	if err := checkIssuer(issuer); err != nil {
		return "", err
	}
	c.Forget()
	p, err := c.discover(ctx, issuer)
	if err != nil {
		return "", err
	}
	return p.Endpoint().AuthURL, nil
}

// verifier checks ID tokens. HS256 is accepted only when the provider lists
// it and a long enough client secret is saved; otherwise the library's
// defaults apply (the provider's asymmetric algorithms).
func (c *Client) verifier(p *oidc.Provider, clientID, secret string) *oidc.IDTokenVerifier {
	c.mu.Lock()
	issuer, meta, keys := c.issuer, c.meta, c.keys
	c.mu.Unlock()
	if len(secret) < minHMACSecret || !slices.Contains(meta.Algs, string(jose.HS256)) {
		return p.Verifier(&oidc.Config{ClientID: clientID})
	}
	var algs []string
	for _, a := range meta.Algs {
		if signingAlgs[a] {
			algs = append(algs, a)
		}
	}
	return oidc.NewVerifier(issuer, hmacKeySet{secret: []byte(secret), keys: keys},
		&oidc.Config{ClientID: clientID, SupportedSigningAlgs: algs})
}

func (c *Client) config(ctx context.Context, s Settings, secret, redirect string) (*oidc.Provider, oauth2.Config, error) {
	p, err := c.discover(ctx, s.Issuer)
	if err != nil {
		return nil, oauth2.Config{}, err
	}
	c.mu.Lock()
	scopes := c.meta.scopes()
	c.mu.Unlock()
	return p, oauth2.Config{
		ClientID: s.ClientID, ClientSecret: secret, Endpoint: p.Endpoint(), RedirectURL: redirect, Scopes: scopes,
	}, nil
}

func randomString() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start begins a sign-in and returns the provider's URL to send the browser
// to. secret is the client secret in clear; redirect is Eyrie's callback
// URL. next is where to go afterwards; linkUser, when not 0, is the
// signed-in user who is linking their account.
func (c *Client) Start(ctx context.Context, s Settings, secret, redirect, next string, linkUser int64) (string, error) {
	_, cfg, err := c.config(ctx, s, secret, redirect)
	if err != nil {
		return "", err
	}
	p := pending{verifier: oauth2.GenerateVerifier(), nonce: randomString(), next: next, linkUser: linkUser,
		expires: c.now().Add(LoginTTL)}
	state := randomString()
	c.mu.Lock()
	for k, v := range c.pending {
		if c.now().After(v.expires) {
			delete(c.pending, k)
		}
	}
	c.pending[state] = p
	c.mu.Unlock()
	return cfg.AuthCodeURL(state, oidc.Nonce(p.nonce), oauth2.S256ChallengeOption(p.verifier)), nil
}

// Claims are the ID token claims Eyrie reads.
type Claims struct {
	Subject           string   `json:"sub"`
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	Picture           string   `json:"picture"`
	Nonce             string   `json:"nonce"`
}

// Username is the name for a new account: preferred_username, else the
// email address, else the subject.
func (c Claims) Username() string {
	switch {
	case c.PreferredUsername != "":
		return c.PreferredUsername
	case c.Email != "":
		return c.Email
	}
	return c.Subject
}

// Login is a verified sign-in.
type Login struct {
	Claims          Claims
	Issuer, Subject string
	AccessToken     string
	Next            string // where to go afterwards
	LinkUser        int64  // the user linking their account; 0 for a sign-in
}

// Refused is a sign-in the provider or Eyrie turned down. Its message is
// meant for the person signing in.
type Refused struct{ Message string }

func (r *Refused) Error() string { return r.Message }

func refuse(format string, args ...any) error { return &Refused{fmt.Sprintf(format, args...)} }

// Finish completes a sign-in from the callback's query: it checks the
// state, exchanges the code with the PKCE verifier, verifies the ID token
// and its nonce, and applies the allowed group. Errors are *Refused, with a
// message for the person signing in, or problems reaching the provider.
func (c *Client) Finish(ctx context.Context, s Settings, secret, redirect string, q url.Values) (Login, error) {
	if e := q.Get("error"); e != "" {
		return Login{}, refuse("%s refused the sign-in: %s %s", s.Name(), e, q.Get("error_description"))
	}
	c.mu.Lock()
	p, ok := c.pending[q.Get("state")]
	delete(c.pending, q.Get("state"))
	c.mu.Unlock()
	if !ok || c.now().After(p.expires) {
		return Login{}, refuse("That sign-in link expired or was already used. Try again.")
	}
	provider, cfg, err := c.config(ctx, s, secret, redirect)
	if err != nil {
		return Login{}, err
	}
	tok, err := cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(p.verifier))
	if err != nil {
		c.log.Warn("sso code exchange failed", "err", err)
		return Login{}, refuse("Could not complete the sign-in with %s. Check the client ID and secret.", s.Name())
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := c.verifier(provider, s.ClientID, secret).Verify(ctx, raw)
	if err != nil {
		c.log.Warn("sso id token rejected", "err", err)
		return Login{}, refuse("The sign-in token from %s was not valid.", s.Name())
	}
	var cl Claims
	if err := idt.Claims(&cl); err != nil || cl.Nonce != p.nonce {
		return Login{}, refuse("The sign-in token from %s was not valid.", s.Name())
	}
	if cl.Groups == nil && (s.AllowedGroup != "" || s.AdminGroup != "") {
		// Some providers only put groups in the userinfo response.
		if ui, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var extra Claims
			if ui.Claims(&extra) == nil {
				cl.Groups = extra.Groups
			}
		}
	}
	c.log.Info("sso sign-in", "user", cl.Username(), "groups", cl.Groups)
	if s.AllowedGroup != "" && !InGroup(cl.Groups, s.AllowedGroup) {
		c.log.Warn("sso sign-in refused: not in the allowed group", "user", cl.Username(), "group", s.AllowedGroup)
		return Login{}, refuse("Your %s account is not in the %q group.", s.Name(), s.AllowedGroup)
	}
	return Login{Claims: cl, Issuer: idt.Issuer, Subject: idt.Subject, AccessToken: tok.AccessToken,
		Next: p.next, LinkUser: p.linkUser}, nil
}

// InGroup reports whether group is in groups, ignoring case: providers may
// send a group's lower-case name while people type its display name.
func InGroup(groups []string, group string) bool {
	return slices.ContainsFunc(groups, func(g string) bool { return strings.EqualFold(g, group) })
}
