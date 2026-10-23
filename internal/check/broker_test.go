// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRedis answers RESP commands; the password is "pw" (user alice or
// default).
func fakeRedis(cert *tls.Certificate) func(net.Conn) {
	return func(c net.Conn) {
		if cert != nil {
			c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{*cert}})
		}
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
			var args []string
			for range n {
				r.ReadString('\n')
				a, _ := r.ReadString('\n')
				args = append(args, strings.TrimSpace(a))
			}
			switch {
			case args[0] == "AUTH" && args[len(args)-1] == "pw":
				c.Write([]byte("+OK\r\n"))
			case args[0] == "AUTH":
				c.Write([]byte("-WRONGPASS invalid username-password pair\r\n"))
			case args[0] == "SELECT" && args[1] == "3":
				c.Write([]byte("$2\r\nOK\r\n"))
			case args[0] == "SELECT" && args[1] == "4":
				c.Write([]byte(":1\r\n"))
			case args[0] == "SELECT" && args[1] == "5":
				c.Write([]byte("$-1\r\n"))
			case args[0] == "SELECT" && args[1] == "6":
				c.Write([]byte("?\r\n"))
			case args[0] == "SELECT":
				c.Write([]byte("\r\n"))
			case args[0] == "PING":
				c.Write([]byte("+PONG\r\n"))
			}
		}
	}
}

func TestRedis(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = pipeDial(fakeRedis(nil), &dialed)
	if r := run(t, env, "redis", `{"connection_string":"redis://alice:`+"pw"+`@cache.example.com/3"}`); !r.OK || r.Message != "PONG" ||
		dialed != "tcp cache.example.com:6379" {
		t.Errorf("redis: %+v %s", r, dialed)
	}
	if r := run(t, env, "redis", `{"connection_string":"redis://:`+"pw"+`@cache.example.com:6380/4"}`); !r.OK || dialed != "tcp cache.example.com:6380" {
		t.Errorf("password only: %+v %s", r, dialed)
	}
	if r := run(t, env, "redis", `{"connection_string":"redis://:`+"pw"+`@cache.example.com/5"}`); !r.OK {
		t.Errorf("nil bulk: %+v", r)
	}
	if r := run(t, env, "redis", `{"connection_string":"redis://alice:`+"wrong"+`@cache.example.com"}`); r.OK || r.Message != "WRONGPASS invalid username-password pair" {
		t.Errorf("wrong password: %+v", r)
	}
	for _, db := range []string{"6", "7"} {
		if r := run(t, env, "redis", `{"connection_string":"redis://cache.example.com/`+db+`"}`); r.OK || r.Message != "The server's answer is not RESP" {
			t.Errorf("garbage %s: %+v", db, r)
		}
	}
	cert := testCert(t, "cache.example.com", time.Now().Add(time.Hour))
	env.Dial = pipeDial(fakeRedis(&cert), nil)
	if r := run(t, env, "redis", `{"connection_string":"rediss://cache.example.com","ignore_tls":true}`); !r.OK || r.CertExpires.IsZero() {
		t.Errorf("TLS: %+v", r)
	}
	if r := run(t, env, "redis", `{"connection_string":"rediss://cache.example.com"}`); r.OK {
		t.Errorf("untrusted: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "redis", `{"connection_string":"redis://cache.example.com"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{}`, `{"connection_string":"http://cache.example.com"}`, `{"connection_string":"redis://cache.example.com/x"}`} {
		if _, err := Prepare(env, "redis", []byte(s), nil, nil); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	c := &redisChecker{r: &Redis{ConnectionString: "nope"}, env: env}
	if r := c.Check(context.Background()); r.OK {
		t.Errorf("bad saved URL: %+v", r)
	}
}

// fakeBroker is an MQTT broker that sends payload on any subscription.
type fakeBroker struct {
	rc      byte // CONNACK's return code
	suback  byte
	payload string
	qos     byte
	silent  bool
	// seen gets, once the broker is done, the user who signed in and
	// whether the message was acknowledged.
	seen chan string
}

// dial is a Dial whose connections each reach a copy of the broker as it
// is now, so a test can change it for the next check.
func (f *fakeBroker) dial(dialed *string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		cp := *f
		return pipeDial(cp.serve, dialed)(ctx, network, addr)
	}
}

func (f *fakeBroker) serve(c net.Conn) {
	user, puback := "", false
	defer func() {
		if f.seen != nil {
			f.seen <- fmt.Sprintf("%s %v", user, puback)
		}
	}()
	r := bufio.NewReader(c)
	typ, _, body, err := readMQTT(r)
	if err != nil || typ != mqttConnect {
		return
	}
	if body[7]&0x80 != 0 {
		id := int(binary.BigEndian.Uint16(body[10:]))
		n := int(binary.BigEndian.Uint16(body[12+id:]))
		user = string(body[14+id : 14+id+n])
	}
	c.Write(mqttPacket(mqttConnack, 0, []byte{0, f.rc}))
	if typ, _, body, err = readMQTT(r); err != nil || typ != mqttSubscribe {
		return
	}
	c.Write(mqttPacket(mqttSuback, 0, []byte{body[0], body[1], f.suback}))
	if f.silent {
		r.ReadByte()
		return
	}
	msg := mqttString(nil, "shop/status")
	if f.qos > 0 {
		msg = append(msg, 0, 7)
	}
	c.Write(mqttPacket(mqttPublish, f.qos<<1|1, append(msg, f.payload...)))
	if typ, _, _, err := readMQTT(r); err == nil && typ == mqttPuback {
		puback = true
	}
}

func TestMQTT(t *testing.T) {
	env := testEnv()
	b := &fakeBroker{payload: `{"state":"open","load":3}`, qos: 1, seen: make(chan string, 1)}
	var dialed string
	env.Dial = b.dial(&dialed)
	r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/#","keyword":"open","username":"alice","password":"pw"}`)
	if !r.OK || r.Message != `Message: {"state":"open","load":3}` || dialed != "tcp mq.example.com:1883" {
		t.Errorf("keyword: %+v %s", r, dialed)
	}
	if seen := <-b.seen; seen != "alice true" {
		t.Errorf("broker saw %s", seen)
	}
	b.seen = nil
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status","keyword":"closed"}`); r.OK || !strings.HasPrefix(r.Message, `"closed" not found in`) {
		t.Errorf("missing keyword: %+v", r)
	}
	b.qos = 0
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status","check":"json-query","json_path":"load","operator":"<","expected":"5"}`); !r.OK ||
		r.Message != "load is 3" {
		t.Errorf("json: %+v", r)
	}
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status","check":"json-query","json_path":"state","expected":"closed"}`); r.OK ||
		r.Message != "state is open, not == closed" {
		t.Errorf("json mismatch: %+v", r)
	}
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status","check":"json-query","json_path":"nope"}`); r.OK {
		t.Errorf("json missing: %+v", r)
	}
	b.rc = 5
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status"}`); r.OK || r.Message != "The broker refused: not authorized" {
		t.Errorf("refused: %+v", r)
	}
	b.rc, b.suback = 0, 0x80
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status"}`); r.OK || r.Message != "The broker refused the subscription to shop/status" {
		t.Errorf("subscription: %+v", r)
	}
	b.suback, b.silent = 0, true
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, c, _ := Parse(env, "mqtt", []byte(`{"host":"mq.example.com","topic":"shop/status"}`))
	if r := c.Check(ctx); r.OK || r.Message != "No message on shop/status in time" {
		t.Errorf("silent: %+v", r)
	}
}

func TestMQTTTLS(t *testing.T) {
	env := testEnv()
	b := &fakeBroker{payload: "up"}
	var dialed string
	cert := testCert(t, "mq.example.com", time.Now().Add(time.Hour))
	env.Dial = pipeDial(func(c net.Conn) { b.serve(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})) }, &dialed)
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status","tls":true,"ignore_tls":true}`); !r.OK || r.CertExpires.IsZero() ||
		dialed != "tcp mq.example.com:8883" {
		t.Errorf("TLS: %+v %s", r, dialed)
	}
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"shop/status","tls":true}`); r.OK {
		t.Errorf("untrusted: %+v", r)
	}
}

func TestMQTTPackets(t *testing.T) {
	env := testEnv()
	for _, reply := range [][]byte{{0x20, 1, 0}, {0x30, 0}, {0x20, 0xff, 0xff, 0xff, 0xff}, {0x20, 0xff, 0xff, 0xff, 0x7f}} {
		env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 100)); c.Write(reply) }, nil)
		if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"t"}`); r.OK {
			t.Errorf("%x: %+v", reply, r)
		}
	}
	if p := mqttPacket(1, 0, make([]byte, 200)); p[1] != 0xc8 || p[2] != 1 {
		t.Errorf("length %x", p[:3])
	}
	for _, body := range [][]byte{{0}, {0, 9, 1}, {0, 1, 'a', 0}} {
		if _, err := publishPayload(nil, 2, body); err == nil {
			t.Errorf("%x read", body)
		}
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "mqtt", `{"host":"mq.example.com","topic":"t"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{"host":"","topic":"t"}`, `{"host":"h"}`, `{"host":"h","topic":"t","check":"regex"}`,
		`{"host":"h","topic":"t","check":"json-query","json_path":"a[x"}`, `{"host":"h","topic":"t","check":"json-query","json_path":"a","operator":"~"}`,
		`{"host":"h","topic":"t","username":"` + strings.Repeat("u", 65536) + `"}`} {
		if _, _, err := Parse(env, "mqtt", []byte(s)); err == nil {
			t.Errorf("%.60s accepted", s)
		}
	}
	for raw, n := range map[string]int{`{"host":"h","topic":"t","keyword":"k"}`: 3, `{"host":"h","topic":"t","check":"json-query","json_path":"a"}`: 3,
		`{"host":"h","topic":"t"}`: 2} {
		if f := Describe("mqtt", []byte(raw)); len(f) != n {
			t.Errorf("describe %s: %v", raw, f)
		}
	}
}

func TestRabbitMQ(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		auth = u + ":" + p
		switch {
		case p != "pw":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/alarm/api/health/checks/alarms":
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"failed","reason":"resource alarm(s) in effect: memory"}`))
		case r.URL.Path == "/odd/api/health/checks/alarms":
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == "/api/health/checks/alarms":
			w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	env := testEnv()
	inner := env.Transport(false)
	env.Transport = func(bool) http.RoundTripper {
		return roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "down.example.com" {
				return nil, errors.New("connection refused")
			}
			return inner.RoundTrip(r)
		})
	}
	nodes := func(n ...string) string {
		return `{"nodes":["` + strings.Join(n, `","`) + `"],"username":"alice","password":"pw"}`
	}
	if r := run(t, env, "rabbitmq", nodes("http://down.example.com", srv.URL+"/")); !r.OK || r.Message != "No alarms on "+srv.URL || auth != "alice:pw" {
		t.Errorf("second node: %+v %s", r, auth)
	}
	if r := run(t, env, "rabbitmq", nodes(srv.URL+"/alarm")); r.OK || r.Message != "Resource alarm(s) in effect: memory" {
		t.Errorf("alarm: %+v", r)
	}
	if r := run(t, env, "rabbitmq", nodes(srv.URL+"/odd")); r.OK || r.Message != "Status 500 Internal Server Error" {
		t.Errorf("odd: %+v", r)
	}
	if r := run(t, env, "rabbitmq", `{"nodes":["`+srv.URL+`"],"username":"alice","password":"no"}`); r.OK || r.Message != "Wrong user name or password" {
		t.Errorf("unauthorized: %+v", r)
	}
	if r := run(t, env, "rabbitmq", nodes("http://down.example.com")); r.OK ||
		!strings.HasPrefix(r.Message, "No node answered: http://down.example.com: connection refused") {
		t.Errorf("none: %+v", r)
	}
	for _, s := range []string{`{"nodes":[],"username":"a"}`, `{"nodes":["ftp://x"],"username":"a"}`, `{"nodes":["http://u:p@x"],"username":"a"}`,
		`{"nodes":["http://x"]}`} {
		if _, _, err := Parse(env, "rabbitmq", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if f := Describe("rabbitmq", []byte(nodes("http://a.example.com", "http://b.example.com"))); f[0].Value != "http://a.example.com, http://b.example.com" {
		t.Errorf("describe %v", f)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
