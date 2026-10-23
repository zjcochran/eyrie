// SPDX-License-Identifier: AGPL-3.0-or-later

package check

// The fake server speaks RADIUS, which is built on MD5.
// nosemgrep
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// fakeRadius answers Access-Requests for alice/pw with the code; the
// shared secret is "s3cret".
type fakeRadius struct {
	code      byte
	reply     string
	secret    string // the secret it answers with; "" for s3cret
	msgAuth   bool   // add a Message-Authenticator
	badAuth   bool   // and get it wrong
	password  string // the password it saw
	badLength bool
}

func (f *fakeRadius) serve(c net.Conn) {
	req := make([]byte, 4096)
	n, err := c.Read(req)
	if err != nil {
		return
	}
	req = req[:n]
	secret := "s3cret"
	// Decode the password, and check the request's Message-Authenticator.
	for a := req[20:]; len(a) >= 2; a = a[a[1]:] {
		if a[0] == radUserPassword {
			h := md5.New()
			h.Write([]byte(secret))
			h.Write(req[4:20])
			sum := h.Sum(nil)
			p := make([]byte, 16)
			for i := range 16 {
				p[i] = a[2+i] ^ sum[i]
			}
			f.password = string(bytes.TrimRight(p, "\x00"))
		}
	}
	// Something else first: an answer to another request.
	c.Write([]byte{radAccessAccept, req[1] + 1, 0, 20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	resp := []byte{f.code, req[1], 0, 0}
	resp = append(resp, req[4:20]...)
	if f.reply != "" {
		resp = radiusAttr(resp, radReplyMessage, []byte(f.reply))
	}
	off := len(resp)
	if f.msgAuth {
		resp = radiusAttr(resp, radMessageAuth, make([]byte, 16))
	}
	binary.BigEndian.PutUint16(resp[2:], uint16(len(resp)))
	if f.badLength {
		binary.BigEndian.PutUint16(resp[2:], 2000)
	}
	if f.secret != "" {
		secret = f.secret
	}
	if f.msgAuth {
		mac := hmac.New(md5.New, []byte(secret))
		mac.Write(resp)
		sum := mac.Sum(nil)
		if f.badAuth {
			sum[0] ^= 1
		}
		copy(resp[off+2:], sum)
	}
	h := md5.New()
	h.Write(resp)
	h.Write([]byte(secret))
	copy(resp[4:20], h.Sum(nil))
	c.Write(resp)
}

func TestRadius(t *testing.T) {
	env := testEnv()
	settings := `{"host":"radius.example.com","secret":"s3cret","username":"alice","password":"pw","called_station_id":"ap1","calling_station_id":"02-00-00-00-00-01"}`
	f := &fakeRadius{code: radAccessAccept, msgAuth: true}
	var dialed string
	env.Dial = pipeDial(f.serve, &dialed)
	r := run(t, env, "radius", settings)
	if !r.OK || r.Message != "Access-Accept" || f.password != "pw" || dialed != "udp radius.example.com:1812" {
		t.Errorf("accept: %+v %q %s", r, f.password, dialed)
	}
	for _, c := range []struct {
		f    *fakeRadius
		want string
	}{
		{&fakeRadius{code: radAccessReject, reply: "Bad password"}, "Access-Reject: Bad password"},
		{&fakeRadius{code: radAccessChallenge}, "Access-Challenge: the server asks for more than a password"},
		{&fakeRadius{code: 42}, "Unexpected RADIUS code 42"},
		{&fakeRadius{code: radAccessAccept, secret: "other"}, "The answer's authenticator is wrong: is the shared secret right?"},
		{&fakeRadius{code: radAccessAccept, msgAuth: true, badAuth: true}, "The answer's Message-Authenticator is wrong"},
		{&fakeRadius{code: radAccessAccept, badLength: true}, "The answer is not a RADIUS packet"},
	} {
		env.Dial = pipeDial(c.f.serve, nil)
		if r := run(t, env, "radius", settings); r.OK || r.Message != c.want {
			t.Errorf("%+v: %+v", c.f, r)
		}
	}
	env.Dial = pipeDial(func(net.Conn) {}, nil)
	if r := run(t, env, "radius", settings); r.OK {
		t.Errorf("no answer: %+v", r)
	}
	long := `{"host":"radius.example.com","secret":"s","username":"alice","password":"` + strings.Repeat("x", 129) + `"}`
	if r := run(t, env, "radius", long); r.OK || !strings.Contains(r.Message, "longer") {
		t.Errorf("long password: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "radius", settings); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	if len(papPassword("", "s", make([]byte, 16))) != 16 || len(papPassword(strings.Repeat("x", 17), "s", make([]byte, 16))) != 32 {
		t.Error("padding")
	}
	ch := &radiusChecker{r: &Radius{Secret: "s"}}
	if r := ch.answer([]byte{2, 1, 0, 23, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 18, 9, 0}, make([]byte, 16)); r.OK {
		t.Errorf("bad attributes: %+v", r)
	}
	for _, s := range []string{`{"host":""}`, `{"host":"r.example.com","username":"a"}`, `{"host":"r.example.com","secret":"s"}`,
		`{"host":"r.example.com","secret":"s","username":"a","nas_identifier":"` + strings.Repeat("n", 254) + `"}`} {
		if _, _, err := Parse(env, "radius", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}
