// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP is a mail server: implicit wraps the connection in TLS first;
// starttls offers STARTTLS.
func fakeSMTP(t *testing.T, implicit, starttls bool, greeting string) func(net.Conn) {
	cert := testCert(t, "mail.example.com", time.Now().Add(48*time.Hour))
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	return func(c net.Conn) {
		if implicit {
			c = tls.Server(c, cfg)
		}
		r := bufio.NewReader(c)
		c.Write([]byte(greeting + "\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				if starttls {
					c.Write([]byte("250-mail.example.com\r\n250-STARTTLS\r\n250 OK\r\n"))
				} else {
					c.Write([]byte("250-mail.example.com\r\n250 OK\r\n"))
				}
			case cmd == "STARTTLS":
				c.Write([]byte("220 Go ahead\r\n"))
				c = tls.Server(c, cfg)
				r = bufio.NewReader(c)
				starttls = false
			case cmd == "QUIT":
				c.Write([]byte("221 Bye\r\n"))
				return
			default:
				c.Write([]byte("500 What\r\n"))
			}
		}
	}
}

func TestSMTP(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = pipeDial(fakeSMTP(t, false, true, "220 mail.example.com ESMTP"), &dialed)
	r := run(t, env, "smtp", `{"host":"mail.example.com","ignore_tls":true}`)
	if !r.OK || r.Message != "Ready, STARTTLS works" || r.CertExpires.IsZero() || dialed != "tcp mail.example.com:25" {
		t.Errorf("starttls: %+v %s", r, dialed)
	}
	if r := run(t, env, "smtp", `{"host":"mail.example.com"}`); r.OK || !strings.Contains(r.Message, "STARTTLS") {
		t.Errorf("untrusted: %+v", r)
	}
	if r := run(t, env, "smtp", `{"host":"mail.example.com","security":"none","port":587}`); !r.OK || r.Message != "Ready" ||
		dialed != "tcp mail.example.com:587" {
		t.Errorf("plain: %+v", r)
	}
	env.Dial = pipeDial(fakeSMTP(t, false, false, "220 mail.example.com ESMTP"), nil)
	if r := run(t, env, "smtp", `{"host":"mail.example.com"}`); r.OK || r.Message != "The server does not offer STARTTLS" {
		t.Errorf("no starttls: %+v", r)
	}
	env.Dial = pipeDial(fakeSMTP(t, true, false, "220 mail.example.com ESMTP"), &dialed)
	if r := run(t, env, "smtp", `{"host":"mail.example.com","security":"tls","ignore_tls":true}`); !r.OK || r.Message != "Ready over TLS" ||
		dialed != "tcp mail.example.com:465" || r.CertExpires.IsZero() {
		t.Errorf("tls: %+v %s", r, dialed)
	}
	env.Dial = pipeDial(fakeSMTP(t, false, false, "554 go away"), nil)
	if r := run(t, env, "smtp", `{"host":"mail.example.com","security":"none"}`); r.OK || !strings.HasPrefix(r.Message, "No greeting") {
		t.Errorf("refused: %+v", r)
	}
}

func TestSMTPInvalid(t *testing.T) {
	env := testEnv()
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "smtp", `{"host":"mail.example.com"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{"host":""}`, `{"host":"mail.example.com","security":"ssl"}`, `{"host":"mail.example.com","port":70000}`} {
		if _, _, err := Parse(env, "smtp", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

// fakeNTP answers a time request with stratum, leap and mode, from a
// server clock offset from the real one.
func fakeNTP(stratum, leap, mode byte, offset time.Duration, refID string) func(net.Conn) {
	return func(c net.Conn) {
		req := make([]byte, 48)
		if _, err := c.Read(req); err != nil {
			return
		}
		ts := func(t time.Time) []byte {
			d := t.Sub(ntpEpoch)
			sec := uint32(d / time.Second)
			frac := uint32((uint64(d%time.Second) << 32) / uint64(time.Second))
			return binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, sec), frac)
		}
		// Something else first: an answer to another request.
		other := make([]byte, 48)
		rand.Read(other[24:32])
		c.Write(other)
		resp := make([]byte, 48)
		resp[0] = leap<<6 | 4<<3 | mode
		resp[1] = stratum
		copy(resp[12:16], refID)
		copy(resp[24:32], req[40:48])
		now := time.Now().Add(offset)
		copy(resp[32:40], ts(now))
		copy(resp[40:48], ts(now))
		c.Write(resp)
	}
}

func TestNTP(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = pipeDial(fakeNTP(2, 0, 4, 0, ""), &dialed)
	r := run(t, env, "ntp", `{"host":"time.example.com","max_offset_ms":500}`)
	if !r.OK || !strings.HasPrefix(r.Message, "Stratum 2, offset ") || dialed != "udp time.example.com:123" {
		t.Errorf("good: %+v %s", r, dialed)
	}
	env.Dial = pipeDial(fakeNTP(2, 0, 4, 3*time.Second, ""), nil)
	if r := run(t, env, "ntp", `{"host":"time.example.com","max_offset_ms":500}`); r.OK || !strings.HasSuffix(r.Message, "more than 500 ms") {
		t.Errorf("offset: %+v", r)
	}
	env.Dial = pipeDial(fakeNTP(0, 0, 4, 0, "RATE"), nil)
	if r := run(t, env, "ntp", `{"host":"time.example.com"}`); r.OK || r.Message != `The server refused: "RATE"` {
		t.Errorf("kiss: %+v", r)
	}
	env.Dial = pipeDial(fakeNTP(16, 3, 4, 0, ""), nil)
	if r := run(t, env, "ntp", `{"host":"time.example.com"}`); r.OK || r.Message != "The server's clock is not synchronized" {
		t.Errorf("unsynchronized: %+v", r)
	}
	env.Dial = pipeDial(fakeNTP(2, 0, 3, 0, ""), nil)
	if r := run(t, env, "ntp", `{"host":"time.example.com"}`); r.OK || r.Message != "Not a server's answer" {
		t.Errorf("mode: %+v", r)
	}
	env.Dial = pipeDial(func(net.Conn) {}, nil)
	if r := run(t, env, "ntp", `{"host":"time.example.com"}`); r.OK {
		t.Errorf("closed: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "ntp", `{"host":"time.example.com"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	c := &ntpChecker{now: func() time.Time { return time.Unix(0, 0) }}
	if !c.clock().Equal(time.Unix(0, 0)) {
		t.Error("clock")
	}
	for _, s := range []string{`{"host":""}`, `{"host":"time.example.com","max_offset_ms":-1}`} {
		if _, _, err := Parse(env, "ntp", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if trimZero([]byte{0, 0}) != "" {
		t.Error("trimZero")
	}
}
