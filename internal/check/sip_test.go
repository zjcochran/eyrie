// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// fakeSIP answers OPTIONS with 100 Trying, then status. stream reads and
// writes as over TCP.
func fakeSIP(status string, stream bool, cert *tls.Certificate) func(net.Conn) {
	return func(c net.Conn) {
		if cert != nil {
			c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{*cert}})
		}
		var tp *textproto.Reader
		if stream {
			tp = textproto.NewReader(bufio.NewReader(c))
		} else {
			buf := make([]byte, 65535)
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			tp = textproto.NewReader(bufio.NewReader(strings.NewReader(string(buf[:n]))))
		}
		if _, err := tp.ReadLine(); err != nil {
			return
		}
		h, _ := tp.ReadMIMEHeader()
		answer := func(status, callID string, body string) {
			c.Write([]byte("SIP/2.0 " + status + "\r\nVia: " + h.Get("Via") + "\r\nCall-ID: " + callID + "\r\nCSeq: 1 OPTIONS\r\n" +
				"Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body))
		}
		answer("200 OK", "other@example.com", "")
		answer("100 Trying", h.Get("Call-ID"), "")
		answer(status, h.Get("Call-ID"), "v=0")
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n%10))) }

func TestSIP(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = pipeDial(fakeSIP("200 OK", false, nil), &dialed)
	r := run(t, env, "sip-options", `{"host":"pbx.example.com"}`)
	if !r.OK || r.Message != "200 OK" || dialed != "udp pbx.example.com:5060" {
		t.Errorf("udp: %+v %s", r, dialed)
	}
	env.Dial = pipeDial(fakeSIP("404 Not Found", true, nil), &dialed)
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com","transport":"tcp"}`); r.OK || r.Message != "Status 404 Not Found" ||
		dialed != "tcp pbx.example.com:5060" {
		t.Errorf("tcp: %+v %s", r, dialed)
	}
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com","transport":"tcp","accepted_status_codes":["200-299","404"]}`); !r.OK {
		t.Errorf("accepted 404: %+v", r)
	}
	cert := testCert(t, "pbx.example.com", time.Now().Add(time.Hour))
	env.Dial = pipeDial(fakeSIP("200 OK", true, &cert), &dialed)
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com","transport":"tls","ignore_tls":true}`); !r.OK || r.CertExpires.IsZero() ||
		dialed != "tcp pbx.example.com:5061" {
		t.Errorf("tls: %+v %s", r, dialed)
	}
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com","transport":"tls"}`); r.OK {
		t.Errorf("untrusted: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 2000)); c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")) }, nil)
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com"}`); r.OK || r.Message != "The answer is not SIP" {
		t.Errorf("not SIP: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 2000)) }, nil)
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com","transport":"tcp"}`); r.OK {
		t.Errorf("closed: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "sip-options", `{"host":"pbx.example.com"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{"host":""}`, `{"host":"pbx.example.com","transport":"sctp"}`, `{"host":"pbx.example.com","accepted_status_codes":["x"]}`} {
		if _, _, err := Parse(env, "sip-options", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}
