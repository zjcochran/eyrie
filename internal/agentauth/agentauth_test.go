// SPDX-License-Identifier: AGPL-3.0-or-later

package agentauth

import (
	"strings"
	"testing"
	"time"
)

func TestKeys(t *testing.T) {
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := ParseKey(k.String() + "\n")
	if err != nil || !k2.Public().Equal(k.Public()) {
		t.Fatalf("round trip: %v", err)
	}
	p, err := ParsePublicKey(" " + k.Public().String())
	if err != nil || !p.Equal(k.Public()) {
		t.Fatalf("public round trip: %v", err)
	}
	if len(p.Fingerprint()) != 16 {
		t.Errorf("fingerprint %s", p.Fingerprint())
	}
	sig := k.Sign([]byte("hello"))
	if !p.Verify([]byte("hello"), sig) || p.Verify([]byte("hullo"), sig) || p.Verify([]byte("hello"), "!!") {
		t.Error("verify")
	}
	for _, s := range []string{"", "ed25519-private:AAAA", "ed25519:" + strings.TrimPrefix(k.String(), "ed25519-private:"), "ed25519-private:!!"} {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("ParseKey(%q) worked", s)
		}
	}
	for _, s := range []string{"", "ed25519:AAAA", strings.Replace(k.Public().String(), "ed25519:", "rsa:", 1), "ed25519:!!"} {
		if _, err := ParsePublicKey(s); err == nil {
			t.Errorf("ParsePublicKey(%q) worked", s)
		}
	}
}

func TestRequests(t *testing.T) {
	k, _ := NewKey()
	other, _ := NewKey()
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"workloads":[]}`)
	h, err := SignRequest(k, 7, "POST", PathReport, body, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ParseAuth(h)
	if err != nil || a.Agent != 7 || a.TS != now.Unix() {
		t.Fatalf("%+v %v", a, err)
	}
	if got, err := a.Verify([]PublicKey{nil, other.Public(), k.Public()}, "POST", PathReport, body, now.Add(time.Minute)); err != nil || !got.Equal(k.Public()) {
		t.Errorf("verify: %v", err)
	}
	if _, err := a.Verify([]PublicKey{k.Public()}, "POST", PathReport, []byte(`{}`), now); err == nil {
		t.Error("another body verified")
	}
	if _, err := a.Verify([]PublicKey{k.Public()}, "POST", PathRotate, body, now); err == nil {
		t.Error("another path verified")
	}
	if _, err := a.Verify([]PublicKey{other.Public()}, "POST", PathReport, body, now); err == nil {
		t.Error("another key verified")
	}
	if _, err := a.Verify([]PublicKey{k.Public()}, "POST", PathReport, body, now.Add(6*time.Minute)); err == nil || !strings.Contains(err.Error(), "clocks") {
		t.Errorf("late: %v", err)
	}
	if _, err := a.Verify([]PublicKey{k.Public()}, "POST", PathReport, body, now.Add(-6*time.Minute)); err == nil {
		t.Error("early request verified")
	}
	for _, h := range []string{
		"Bearer x",
		"Eyrie-Agent agent=7, ts=1, nonce=n",
		"Eyrie-Agent agent=x, ts=1, nonce=n, sig=s",
		"Eyrie-Agent agent=7, ts=y, nonce=n, sig=s",
		"Eyrie-Agent agent=7, agent=7, nonce=n, sig=s",
		"Eyrie-Agent agent=7, ts=1, nonce=n, sig=s, extra=1",
		"Eyrie-Agent agent=7, ts=1, nonce=, sig=s",
		"Eyrie-Agent agent=7 ts=1",
	} {
		if _, err := ParseAuth(h); err == nil {
			t.Errorf("ParseAuth(%q) worked", h)
		}
	}
}

func TestNonces(t *testing.T) {
	var n Nonces
	now := time.Unix(1_800_000_000, 0)
	if !n.Use("a", now) || n.Use("a", now.Add(time.Minute)) {
		t.Error("replay")
	}
	if !n.Use("a", now.Add(NonceTTL+time.Second)) {
		t.Error("an expired nonce is still remembered")
	}
}

func TestRotationProof(t *testing.T) {
	k, _ := NewKey()
	proof := RotationProof(3, k.Public(), 100)
	if !strings.HasPrefix(string(proof), "rotate\n3\ned25519:") || !strings.HasSuffix(string(proof), "\n100") {
		t.Errorf("%q", proof)
	}
}
