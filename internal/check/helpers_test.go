// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// pipeDial is a Dial whose connections reach serve, in memory. It records
// the last network and address dialed.
func pipeDial(serve func(net.Conn), dialed *string) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, network, addr string) (net.Conn, error) {
		if dialed != nil {
			*dialed = network + " " + addr
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			serve(server)
		}()
		return client, nil
	}
}

// listenDial is a Dial whose connections reach serve over loopback TCP,
// for protocols where both ends write first.
func listenDial(t *testing.T, serve func(net.Conn), dialed *string) func(context.Context, string, string) (net.Conn, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				serve(c)
			}()
		}
	}()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if dialed != nil {
			*dialed = network + " " + addr
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", l.Addr().String())
	}
}

// testCert is a self-signed certificate for host, expiring at notAfter.
func testCert(t *testing.T, host string, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// sealer is a fake Sealer: sealing adds a prefix.
type sealer struct{}

func (sealer) Seal(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	return "sealed:" + s, nil
}

func (sealer) Open(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	plain, ok := strings.CutPrefix(s, "sealed:")
	if !ok {
		return "", errNotSealed
	}
	return plain, nil
}

var errNotSealed = &net.AddrError{Err: "not sealed"}
