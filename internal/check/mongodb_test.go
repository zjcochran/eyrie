// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMongoDB(t *testing.T) {
	env := testEnv()
	answer := []byte(`{"ok":1,"set":"rs0","members":[{"name":"db1","health":1}]}`)
	check := func(settings string, ans []byte, err error) Result {
		t.Helper()
		_, c, perr := Parse(env, "mongodb", []byte(settings))
		if perr != nil {
			t.Fatal(perr)
		}
		mc := c.(*mongoChecker)
		mc.run = func(context.Context) ([]byte, error) { return ans, err }
		return mc.Check(context.Background())
	}
	if r := check(`{"connection_string":"mongodb://db.example.com"}`, answer, nil); !r.OK || !strings.HasPrefix(r.Message, `OK: {"ok":1`) {
		t.Errorf("ping: %+v", r)
	}
	if r := check(`{"connection_string":"x","command":"{\"replSetGetStatus\":1}","json_path":"members[0].health","expected":"1"}`, answer, nil); !r.OK ||
		r.Message != "members[0].health is 1" {
		t.Errorf("query: %+v", r)
	}
	if r := check(`{"connection_string":"x","json_path":"set","operator":"!=","expected":"rs0"}`, answer, nil); r.OK || r.Message != "set is rs0, not != rs0" {
		t.Errorf("mismatch: %+v", r)
	}
	if r := check(`{"connection_string":"x","json_path":"nope"}`, answer, nil); r.OK {
		t.Errorf("missing: %+v", r)
	}
	if r := check(`{"connection_string":"x"}`, nil, errors.New("(Unauthorized) command requires authentication")); r.OK ||
		r.Message != "(Unauthorized) command requires authentication" {
		t.Errorf("error: %+v", r)
	}
	for _, s := range []string{`{}`, `{"connection_string":"x","command":"[1]"}`, `{"connection_string":"x","command":"{}"}`,
		`{"connection_string":"x","json_path":"a[x"}`, `{"connection_string":"x","json_path":"a","operator":"~"}`} {
		if _, _, err := Parse(env, "mongodb", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if _, err := Prepare(env, "mongodb", []byte(`{"connection_string":"postgres://db.example.com"}`), nil, nil); err == nil {
		t.Error("not a MongoDB URI")
	}
	if f := Describe("mongodb", []byte(`{"connection_string":"x","command":"{\"ping\":1}","json_path":"ok","expected":"1"}`)); len(f) != 3 {
		t.Errorf("describe %v", f)
	}
	for uri, db := range map[string]string{"mongodb://db.example.com/shop?tls=true": "shop", "mongodb://db.example.com": "admin", "%zz": "admin"} {
		if got := mongoDatabase(uri); got != db {
			t.Errorf("%s: %s", uri, got)
		}
	}
}

// TestMongoDBDials checks that the driver dials through the Env.
func TestMongoDBDials(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = func(_ context.Context, network, addr string) (net.Conn, error) {
		dialed = network + " " + addr
		return nil, errors.New("no route to host")
	}
	_, c, _ := Parse(env, "mongodb", []byte(`{"connection_string":"mongodb://alice:`+"pw"+`@db.example.com/shop"}`))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if r := c.Check(ctx); r.OK || dialed != "tcp db.example.com:27017" {
		t.Errorf("%+v %s", r, dialed)
	}
}

func TestKafka(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = func(_ context.Context, network, addr string) (net.Conn, error) {
		dialed = network + " " + addr
		return nil, errors.New("no route to host")
	}
	for _, sasl := range []string{"none", "plain", "scram-sha-256", "scram-sha-512"} {
		_, c, err := Parse(env, "kafka-producer", []byte(`{"brokers":["kafka.example.com:9092"],"topic":"checks","sasl":"`+sasl+
			`","username":"alice","password":"pw","allow_auto_topic_creation":true}`))
		if err != nil {
			t.Fatal(err)
		}
		if m := c.(*kafkaChecker).mechanism(); (m == nil) != (sasl == "none") {
			t.Errorf("%s: %v", sasl, m)
		}
		if sasl != "plain" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if r := c.Check(ctx); r.OK || dialed != "tcp kafka.example.com:9092" {
			t.Errorf("%+v %s", r, dialed)
		}
		cancel()
	}
	cert := testCert(t, "kafka.example.com", time.Now().Add(time.Hour))
	env.Dial = pipeDial(func(c net.Conn) { tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}}).Handshake() }, nil)
	for insecure, ok := range map[bool]bool{true: true, false: false} {
		kc := &kafkaChecker{k: &Kafka{TLS: true, IgnoreTLS: insecure}, env: env}
		conn, err := kc.dial(context.Background(), "tcp", "kafka.example.com:9093")
		if (err == nil) != ok {
			t.Errorf("TLS, ignoring errors %v: %v", insecure, err)
		}
		if conn != nil {
			conn.Close()
		}
	}
	for _, s := range []string{`{"topic":"t"}`, `{"brokers":["k"],"topic":"t"}`, `{"brokers":["k:99999"],"topic":"t"}`,
		`{"brokers":["k:9092"]}`, `{"brokers":["k:9092"],"topic":"a b"}`, `{"brokers":["k:9092"],"topic":"t","sasl":"gssapi"}`,
		`{"brokers":["k:9092"],"topic":"t","sasl":"plain"}`, `{"brokers":["k:9092"],"topic":"t","message":"` + strings.Repeat("m", 10001) + `"}`} {
		if _, _, err := Parse(env, "kafka-producer", []byte(s)); err == nil {
			t.Errorf("%.60s accepted", s)
		}
	}
	if f := Describe("kafka-producer", []byte(`{"brokers":["k:9092","l:9092"],"topic":"t","sasl":"none"}`)); f[0].Value != "k:9092, l:9092" || f[2].Value != "none" {
		t.Errorf("describe %v", f)
	}
}
