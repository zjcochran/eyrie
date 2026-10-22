// Package ssotest is a fake OpenID Connect provider for tests: discovery,
// keys, an authorize endpoint that approves at once, a token endpoint that
// checks the client and PKCE, userinfo, and a picture.
package ssotest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Client is the client ID and secret the provider accepts.
const (
	ClientID     = "eyrie"
	ClientSecret = "s3cret-for-tests-0123456789abcdef"
	AccessToken  = "access-token"
)

// Provider is a running fake provider. Change its fields under Lock.
type Provider struct {
	URL string

	mu      sync.Mutex
	srv     *httptest.Server
	signer  jose.Signer
	key     *rsa.PrivateKey
	codes   map[string]authReq
	claims  map[string]any
	scope   string
	refuse  bool
	scopes  []string
	algs    []string
	hsKey   []byte
	picture []byte
	groups  []string // in userinfo only
	n       int
}

type authReq struct{ nonce, challenge string }

// New starts a provider; Close stops it. The first sign-in is alice, with
// subject user-1.
func New() *Provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
	if err != nil {
		panic(err)
	}
	p := &Provider{signer: signer, key: key, codes: map[string]authReq{}, algs: []string{"RS256"},
		claims: map[string]any{"sub": "user-1", "preferred_username": "alice", "email": "alice@example.com"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	mux.HandleFunc("/userinfo", p.userinfo)
	mux.HandleFunc("/picture.png", p.servePicture)
	p.srv = httptest.NewServer(mux)
	p.URL = p.srv.URL
	return p
}

// Close stops the provider.
func (p *Provider) Close() { p.srv.Close() }

// SetClaims sets the claims of the next ID tokens (sub, preferred_username, …).
func (p *Provider) SetClaims(c map[string]any) {
	p.mu.Lock()
	p.claims = c
	p.mu.Unlock()
}

// SetScopes sets scopes_supported in discovery; nil leaves it out.
func (p *Provider) SetScopes(s []string) {
	p.mu.Lock()
	p.scopes = s
	p.mu.Unlock()
}

// SignHS256 signs ID tokens with HS256 and key, and lists HS256 in discovery.
func (p *Provider) SignHS256(key []byte) {
	p.mu.Lock()
	p.hsKey, p.algs = key, []string{"RS256", "HS256"}
	p.mu.Unlock()
}

// SetPicture serves data at /picture.png to holders of the access token.
func (p *Provider) SetPicture(data []byte) {
	p.mu.Lock()
	p.picture = data
	p.mu.Unlock()
}

// SetUserinfoGroups puts groups in the userinfo answer.
func (p *Provider) SetUserinfoGroups(g []string) {
	p.mu.Lock()
	p.groups = g
	p.mu.Unlock()
}

// Refuse makes the authorize endpoint send the browser back with an error.
func (p *Provider) Refuse(on bool) {
	p.mu.Lock()
	p.refuse = on
	p.mu.Unlock()
}

// Scope is the scope the last sign-in asked for.
func (p *Provider) Scope() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scope
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	doc := map[string]any{
		"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize",
		"token_endpoint": p.URL + "/token", "jwks_uri": p.URL + "/jwks",
		"userinfo_endpoint": p.URL + "/userinfo", "id_token_signing_alg_values_supported": p.algs,
	}
	if p.scopes != nil {
		doc["scopes_supported"] = p.scopes
	}
	json.NewEncoder(w).Encode(doc) //nolint:errcheck,gosec // test server
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{ //nolint:errcheck,gosec // test server
		{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "PKCE required", http.StatusBadRequest)
		return
	}
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.n++
	code := fmt.Sprintf("code-%d", p.n)
	p.codes[code] = authReq{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	p.scope = q.Get("scope")
	refuse := p.refuse
	p.mu.Unlock()
	v := url.Values{"state": {q.Get("state")}}
	if refuse {
		v.Set("error", "access_denied")
		v.Set("error_description", "no")
	} else {
		v.Set("code", code)
	}
	back.RawQuery = v.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound) //nolint:gosec // G710: a provider sends the browser back to redirect_uri
}

type hmacSigner struct{ key []byte }

func (h hmacSigner) Sign(payload []byte) (*jose.JSONWebSignature, error) {
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: h.key}, nil)
	if err != nil {
		return nil, err
	}
	return s.Sign(payload)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	p.mu.Lock()
	req, found := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	claims := map[string]any{}
	for k, v := range p.claims {
		claims[k] = v
	}
	var signer interface {
		Sign([]byte) (*jose.JSONWebSignature, error)
	} = p.signer
	if p.hsKey != nil {
		signer = hmacSigner{p.hsKey}
	}
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || id != ClientID || secret != ClientSecret || base64.RawURLEncoding.EncodeToString(sum[:]) != req.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`)) //nolint:errcheck,gosec // test server
		return
	}
	claims["iss"], claims["aud"], claims["nonce"] = p.URL, ClientID, req.nonce
	claims["iat"], claims["exp"] = time.Now().Unix(), time.Now().Add(time.Hour).Unix()
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tok, _ := jws.CompactSerialize()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck,gosec // test server
		"access_token": AccessToken, "token_type": "Bearer", "expires_in": 3600, "id_token": tok})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+AccessToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sub": p.claims["sub"], "groups": p.groups}) //nolint:errcheck,gosec // test server
}

func (p *Provider) servePicture(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+AccessToken || p.picture == nil {
		http.NotFound(w, r)
		return
	}
	w.Write(p.picture) //nolint:errcheck,gosec // test server
}

// Approve plays the browser at the provider: it opens authURL and returns
// the callback URL the provider sends the browser back to.
func Approve(authURL string) (*url.URL, error) {
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(authURL) //nolint:noctx // test helper
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return nil, errors.New("the provider did not redirect: " + resp.Status)
	}
	return url.Parse(resp.Header.Get("Location"))
}
