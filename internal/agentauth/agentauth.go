// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agentauth is the protocol between Eyrie and its agents: Ed25519
// keys, signed requests and the messages agents send.
//
// Agents call Eyrie; Eyrie never calls an agent, and sends it nothing to
// do. Every request after enrollment is signed with the agent's key, so
// nothing between them, a reverse proxy included, can report for an agent.
package agentauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/workload"
)

// Timings both sides keep to.
const (
	// MaxSkew is how far a request's time may be from Eyrie's clock.
	MaxSkew = 5 * time.Minute
	// NonceTTL is how long Eyrie remembers a request's nonce.
	NonceTTL = 2 * MaxSkew
	// ReportEvery is how often an agent reports.
	ReportEvery = 30 * time.Second
	// OfflineAfter is how long without a report before an agent is offline.
	OfflineAfter = 90 * time.Second
	// RotateEvery is the age at which an agent replaces its key.
	RotateEvery = 30 * 24 * time.Hour
	// MaxKeyAge is the oldest key Eyrie accepts.
	MaxKeyAge = 90 * 24 * time.Hour
	// EnrollFor is how long an enrollment token is valid.
	EnrollFor = time.Hour
)

// MaxBody is the largest request an agent may send.
const MaxBody = 4 << 20

// Prefixes of encoded keys.
const (
	publicPrefix  = "ed25519:"
	privatePrefix = "ed25519-private:"
)

var b64 = base64.RawURLEncoding

// Key is an Ed25519 private key.
type Key struct{ priv ed25519.PrivateKey }

// NewKey makes a new random key.
func NewKey() (Key, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return Key{priv}, err
}

// ParseKey reads a key written by Key.String.
func ParseKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	seed, err := b64.DecodeString(strings.TrimPrefix(s, privatePrefix))
	if err != nil || !strings.HasPrefix(s, privatePrefix) || len(seed) != ed25519.SeedSize {
		return Key{}, errors.New("not an ed25519-private: key")
	}
	return Key{ed25519.NewKeyFromSeed(seed)}, nil
}

// String encodes the key as "ed25519-private:<base64url seed>". It is secret.
func (k Key) String() string { return privatePrefix + b64.EncodeToString(k.priv.Seed()) }

// Public returns the key's public half.
func (k Key) Public() PublicKey { return PublicKey(k.priv.Public().(ed25519.PublicKey)) }

// Sign signs msg.
func (k Key) Sign(msg []byte) string { return b64.EncodeToString(ed25519.Sign(k.priv, msg)) }

// PublicKey is an Ed25519 public key.
type PublicKey ed25519.PublicKey

// ParsePublicKey reads a key written by PublicKey.String.
func ParsePublicKey(s string) (PublicKey, error) {
	s = strings.TrimSpace(s)
	raw, err := b64.DecodeString(strings.TrimPrefix(s, publicPrefix))
	if err != nil || !strings.HasPrefix(s, publicPrefix) || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not an ed25519: public key")
	}
	return PublicKey(raw), nil
}

// String encodes the key as "ed25519:<base64url>".
func (p PublicKey) String() string { return publicPrefix + b64.EncodeToString(p) }

// Fingerprint is a short form of the key for people to compare: the first
// 16 hex digits of its SHA-256.
func (p PublicKey) Fingerprint() string {
	sum := sha256.Sum256(p)
	return hex.EncodeToString(sum[:8])
}

// Verify checks sig over msg.
func (p PublicKey) Verify(msg []byte, sig string) bool {
	raw, err := b64.DecodeString(sig)
	return err == nil && len(p) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(p), msg, raw)
}

// Equal reports whether two keys are the same.
func (p PublicKey) Equal(q PublicKey) bool { return string(p) == string(q) }

// Scheme is the Authorization scheme of agent requests.
const Scheme = "Eyrie-Agent"

// Auth is the parsed Authorization header of an agent request.
type Auth struct {
	Agent int64
	TS    int64
	Nonce string
	Sig   string
}

// requestText is what an agent signs for a request.
func requestText(method, pathQuery string, agent, ts int64, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return fmt.Appendf(nil, "%s\n%s\n%d\n%d\n%s\n%s", method, pathQuery, agent, ts, nonce, hex.EncodeToString(sum[:]))
}

// SignRequest returns the Authorization header for a request.
func SignRequest(k Key, agent int64, method, pathQuery string, body []byte, now time.Time) (string, error) {
	n := make([]byte, 16)
	if _, err := rand.Read(n); err != nil {
		return "", err
	}
	nonce := b64.EncodeToString(n)
	ts := now.Unix()
	sig := k.Sign(requestText(method, pathQuery, agent, ts, nonce, body))
	return fmt.Sprintf("%s agent=%d, ts=%d, nonce=%s, sig=%s", Scheme, agent, ts, nonce, sig), nil
}

// ParseAuth reads an Authorization header.
func ParseAuth(header string) (Auth, error) {
	rest, ok := strings.CutPrefix(header, Scheme+" ")
	if !ok {
		return Auth{}, errors.New("not an " + Scheme + " header")
	}
	var a Auth
	var err error
	seen := map[string]bool{}
	for part := range strings.SplitSeq(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || seen[k] {
			return Auth{}, errors.New("malformed " + Scheme + " header")
		}
		seen[k] = true
		switch k {
		case "agent":
			a.Agent, err = strconv.ParseInt(v, 10, 64)
		case "ts":
			a.TS, err = strconv.ParseInt(v, 10, 64)
		case "nonce":
			a.Nonce = v
		case "sig":
			a.Sig = v
		default:
			err = errors.New("unknown field")
		}
		if err != nil {
			return Auth{}, errors.New("malformed " + Scheme + " header")
		}
	}
	if len(seen) != 4 || a.Nonce == "" || a.Sig == "" {
		return Auth{}, errors.New("incomplete " + Scheme + " header")
	}
	return a, nil
}

// Verify checks a request's signature and time against one of keys, and
// returns the key that signed it. Nonces are checked separately (Nonces).
func (a Auth) Verify(keys []PublicKey, method, pathQuery string, body []byte, now time.Time) (PublicKey, error) {
	if d := now.Sub(time.Unix(a.TS, 0)); d > MaxSkew || d < -MaxSkew {
		return nil, fmt.Errorf("the request's time is %s from Eyrie's; check both clocks", d.Round(time.Second))
	}
	msg := requestText(method, pathQuery, a.Agent, a.TS, a.Nonce, body)
	for _, k := range keys {
		if k != nil && k.Verify(msg, a.Sig) {
			return k, nil
		}
	}
	return nil, errors.New("bad signature")
}

// Nonces remembers recent request nonces, so a request cannot be replayed.
type Nonces struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// Use records a nonce, and reports false if it was seen within NonceTTL.
func (n *Nonces) Use(nonce string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen == nil {
		n.seen = map[string]time.Time{}
	}
	for k, t := range n.seen {
		if now.Sub(t) > NonceTTL {
			delete(n.seen, k)
		}
	}
	if _, ok := n.seen[nonce]; ok {
		return false
	}
	n.seen[nonce] = now
	return true
}

// RotationProof is what a new key signs to show the agent holds it.
func RotationProof(agent int64, newKey PublicKey, ts int64) []byte {
	return fmt.Appendf(nil, "rotate\n%d\n%s\n%d", agent, newKey, ts)
}

// Paths of Eyrie's agent API.
const (
	PathEnroll = "/api/agent/v1/enroll"
	PathReport = "/api/agent/v1/report"
	PathRotate = "/api/agent/v1/rotate"
)

// EnrollRequest is an agent's first call: it trades a one-time token for
// its identity, and gives Eyrie its public key.
type EnrollRequest struct {
	Token     string `json:"token"`
	PublicKey string `json:"public_key"`
	Hostname  string `json:"hostname"`
	Platform  string `json:"platform"`
	Version   string `json:"version"`
}

// EnrollResponse tells an enrolled agent who it is.
type EnrollResponse struct {
	Agent int64  `json:"agent"`
	Name  string `json:"name"`
}

// Report is what an agent sees: every workload, or why it cannot list them.
type Report struct {
	Version   string              `json:"version"`
	Platform  string              `json:"platform"`
	Hostname  string              `json:"hostname"`
	Error     string              `json:"error,omitempty"`
	Workloads []workload.Workload `json:"workloads"`
}

// RotateRequest gives Eyrie an agent's new key. The request is signed with
// the current key; Proof is the new key's signature over RotationProof,
// showing the agent holds it.
type RotateRequest struct {
	NewKey string `json:"new_key"`
	TS     int64  `json:"ts"`
	Proof  string `json:"proof"`
}
