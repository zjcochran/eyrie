// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeSFTP is an SSH server with SFTP for alice, password "pw" or the
// key; /data exists.
type fakeSFTP struct {
	hostKey ssh.Signer
	userKey ssh.PublicKey
	noSFTP  bool
}

func newFakeSFTP(t *testing.T) (*fakeSFTP, string) {
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	host, _ := ssh.NewSignerFromKey(hk)
	_, uk, _ := ed25519.GenerateKey(rand.Reader)
	user, _ := ssh.NewSignerFromKey(uk)
	block, err := ssh.MarshalPrivateKey(uk, "")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSFTP{hostKey: host, userKey: user.PublicKey()}, string(pem.EncodeToMemory(block))
}

func (f *fakeSFTP) serve(c net.Conn) {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(m ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if m.User() == "alice" && string(pw) == "pw" {
				return nil, nil
			}
			return nil, errNotSealed
		},
		PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if m.User() == "alice" && string(k.Marshal()) == string(f.userKey.Marshal()) {
				return nil, nil
			}
			return nil, errNotSealed
		},
	}
	cfg.AddHostKey(f.hostKey)
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range creqs {
				ok := r.Type == "subsystem" && !f.noSFTP
				r.Reply(ok, nil)
				if ok {
					go f.sftp(ch)
				}
			}
		}()
	}
}

func (f *fakeSFTP) sftp(ch ssh.Channel) {
	defer ch.Close()
	for {
		typ, body, err := fxpRead(ch)
		if err != nil {
			return
		}
		switch typ {
		case fxpInit:
			fxpWrite(ch, fxpVersion, binary.BigEndian.AppendUint32(nil, 3))
		case fxpStat:
			id := body[:4]
			path := string(body[8:])
			switch path {
			case "/data":
				fxpWrite(ch, fxpAttrs, append(id, 0, 0, 0, 0))
			case "/odd":
				fxpWrite(ch, 42, id)
			case "/locked":
				fxpWrite(ch, fxpStatus, append(id, 0, 0, 0, 3))
			default:
				fxpWrite(ch, fxpStatus, append(id, 0, 0, 0, 2))
			}
		}
	}
}

func TestSFTP(t *testing.T) {
	f, key := newFakeSFTP(t)
	env := testEnv()
	var dialed string
	env.Dial = listenDial(t, f.serve, &dialed)
	fp := ssh.FingerprintSHA256(f.hostKey.PublicKey())
	r := run(t, env, "sftp", `{"host":"files.example.com","username":"alice","password":"pw","path":"/data"}`)
	if !r.OK || r.Message != "Signed in, SFTP version 3; /data exists; host key "+fp || dialed != "tcp files.example.com:22" {
		t.Errorf("password: %+v %s", r, dialed)
	}
	keyJSON := strings.ReplaceAll(key, "\n", `\n`)
	if r := run(t, env, "sftp", `{"host":"files.example.com","username":"alice","private_key":"`+keyJSON+`","host_key":"`+fp+`"}`); !r.OK ||
		r.Message != "Signed in, SFTP version 3" {
		t.Errorf("key: %+v", r)
	}
	for _, c := range []struct{ settings, want string }{
		{`"password":"pw","path":"/nope"`, "/nope: no such file"},
		{`"password":"pw","path":"/locked"`, "/locked: permission denied"},
		{`"password":"pw","path":"/odd"`, "The server's SFTP answer is not valid"},
		{`"password":"wrong"`, "unable to authenticate"},
		{`"password":"pw","host_key":"SHA256:abc"`, "is " + fp + ", not SHA256:abc"},
		{`"private_key":"junk"`, "The private key cannot be read"},
		{`"private_key":"junk","passphrase":"x"`, "The private key cannot be read"},
	} {
		r := run(t, env, "sftp", `{"host":"files.example.com","username":"alice",`+c.settings+`}`)
		if r.OK || !strings.Contains(r.Message, c.want) {
			t.Errorf("%s: %+v", c.settings, r)
		}
	}
	f.noSFTP = true
	if r := run(t, env, "sftp", `{"host":"files.example.com","username":"alice","password":"pw"}`); r.OK ||
		!strings.HasPrefix(r.Message, "The server has no SFTP") {
		t.Errorf("no SFTP: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "sftp", `{"host":"files.example.com","username":"alice","password":"pw"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{"host":"","username":"a","password":"p"}`, `{"host":"h","password":"p"}`, `{"host":"h","username":"a"}`,
		`{"host":"h","username":"a","password":"p","host_key":"MD5:x"}`, `{"host":"h","username":"a","password":"p","path":"` + strings.Repeat("p", 4097) + `"}`} {
		if _, _, err := Parse(env, "sftp", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if f := (&SFTP{Host: "h", Port: 22, Username: "a", Path: "/data"}).describe(); len(f) != 3 {
		t.Errorf("describe %v", f)
	}
	if _, _, err := fxpRead(strings.NewReader("\x00\x00\x00\x00\x01")); err == nil {
		t.Error("empty packet")
	}
}
