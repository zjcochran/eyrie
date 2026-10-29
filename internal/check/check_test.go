// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// testEnv reaches only what tests give it.
func testEnv() *Env {
	d := &net.Dialer{}
	return &Env{
		Dial:      d.DialContext,
		Transport: func(insecure bool) http.RoundTripper { return newTransport(d, insecure) },
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("no resolver in tests")
		},
		Ping: func(context.Context, netip.Addr, int, int) (time.Duration, error) {
			return 0, errors.New("no ping in tests")
		},
		UserAgent: "Eyrie/test",
	}
}

func run(t *testing.T, env *Env, typ, settings string) Result {
	t.Helper()
	_, c, err := Parse(env, typ, json.RawMessage(settings))
	if err != nil {
		t.Fatalf("%s %s: %v", typ, settings, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Check(ctx)
}

func TestParse(t *testing.T) {
	env := testEnv()
	out, c, err := Parse(env, "http", json.RawMessage(`{"url":" https://example.com "}`))
	if err != nil || c == nil {
		t.Fatal(err)
	}
	var h HTTP
	json.Unmarshal(out, &h)
	if h.URL != "https://example.com" || h.Method != http.MethodGet || h.AcceptedStatusCodes[0] != "200-299" || h.Headers == nil ||
		*h.MaxRedirects != 10 {
		t.Errorf("defaults %s", out)
	}
	for _, typ := range []string{"push", "manual"} {
		out, c, err := Parse(env, typ, nil)
		if err != nil || c != nil || string(out) != "{}" {
			t.Errorf("%s: %s %v %v", typ, out, c, err)
		}
		if Types[typ].Scheduled {
			t.Errorf("%s is scheduled", typ)
		}
	}
	if _, _, err := Parse(env, "telepathy", nil); err == nil {
		t.Error("unknown type")
	}
	if _, _, err := Parse(env, "http", json.RawMessage(`{"url":"https://example.com","colour":"red"}`)); err == nil {
		t.Error("unknown setting accepted")
	}
	if len(Names()) != 37 || Names()[0] != "dns" {
		t.Errorf("names %v", Names())
	}
}

func TestInvalidSettings(t *testing.T) {
	env := testEnv()
	for _, c := range []struct{ typ, settings string }{
		{"http", `{}`},
		{"http", `{"url":"ftp://example.com"}`},
		{"http", `{"url":"https://example.com","method":"BREW"}`},
		{"http", `{"url":"https://example.com","headers":{"Bad Header":"x"}}`},
		{"http", `{"url":"https://example.com","headers":{"X":"a\nb"}}`},
		{"http", `{"url":"https://example.com","accepted_status_codes":["2xx"]}`},
		{"http", `{"url":"https://example.com","accepted_status_codes":["299-200"]}`},
		{"http", `{"url":"https://example.com","accepted_status_codes":["200-abc"]}`},
		{"http", `{"url":"https://example.com","max_redirects":21}`},
		{"keyword", `{"url":"https://example.com"}`},
		{"json-query", `{"url":"https://example.com"}`},
		{"json-query", `{"url":"https://example.com","json_path":"a","json_operator":"~"}`},
		{"port", `{"host":"","port":80}`},
		{"port", `{"host":"example.com/x","port":80}`},
		{"port", `{"host":"example.com","port":70000}`},
		{"ping", `{"host":""}`},
		{"ping", `{"host":"example.com","count":11}`},
		{"ping", `{"host":"example.com","packet_size":4}`},
		{"dns", `{"host":""}`},
		{"dns", `{"host":"example.com","resolver":"a b"}`},
		{"dns", `{"host":"example.com","resolver_port":0,"record_type":"ANY"}`},
		{"dns", `{"host":"example.com","resolver_port":99999}`},
	} {
		if _, _, err := Parse(env, c.typ, json.RawMessage(c.settings)); err == nil {
			t.Errorf("%s %s accepted", c.typ, c.settings)
		}
	}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			if r.Header.Get("User-Agent") != "Eyrie/test" || r.Header.Get("X-Token") != "t" || r.Method != http.MethodPost {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"status":"green","checks":{"db.primary":{"ok":true}},"items":[{"n":3}]}`))
		case "/down":
			http.Error(w, "down", http.StatusServiceUnavailable)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		default:
			w.Write([]byte("<html>hello  world</html>"))
		}
	}))
	defer srv.Close()
	env := testEnv()
	for _, c := range []struct {
		typ, settings string
		ok            bool
		msg           string
	}{
		{"http", `{"url":"` + srv.URL + `/ok","method":"post","headers":{"X-Token":"t"}}`, true, "200 OK"},
		{"http", `{"url":"` + srv.URL + `/down"}`, false, "Status 503 Service Unavailable"},
		{"http", `{"url":"` + srv.URL + `/down","accepted_status_codes":["200-299","503"]}`, true, "503"},
		{"http", `{"url":"` + srv.URL + `/loop","max_redirects":2}`, false, "more than 2 redirects"},
		{"http", `{"url":"` + srv.URL + `/loop","max_redirects":0}`, false, "more than 0 redirects"},
		{"keyword", `{"url":"` + srv.URL + `/","keyword":"hello"}`, true, `Keyword "hello" found`},
		{"keyword", `{"url":"` + srv.URL + `/","keyword":"Hello"}`, false, `not found in "<html>hello world</html>"`},
		{"keyword", `{"url":"` + srv.URL + `/","keyword":"error","invert_keyword":true}`, true, "not found, as wanted"},
		{"keyword", `{"url":"` + srv.URL + `/","keyword":"hello","invert_keyword":true}`, false, "must not be"},
		{"json-query", `{"url":"` + srv.URL + `/ok","method":"POST","headers":{"X-Token":"t"},"json_path":"$.status","expected_value":"green"}`, true, "$.status is green"},
		{"json-query", `{"url":"` + srv.URL + `/ok","method":"POST","headers":{"X-Token":"t"},"json_path":"items[0].n","json_operator":">","expected_value":"5"}`, false, "items[0].n is 3, not > 5"},
		{"json-query", `{"url":"` + srv.URL + `/","json_path":"a","expected_value":"1"}`, false, "not JSON"},
	} {
		r := run(t, env, c.typ, c.settings)
		if r.OK != c.ok || !strings.Contains(r.Message, c.msg) {
			t.Errorf("%s %s: %+v", c.typ, c.settings, r)
		}
		if strings.Contains(c.settings, "/ok") && r.Latency <= 0 {
			t.Errorf("no latency: %+v", r)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, c, _ := Parse(env, "http", json.RawMessage(`{"url":"`+srv.URL+`/slow"}`))
	if r := c.Check(ctx); r.OK || r.Message != "Timed out" || r.Latency != -1 {
		t.Errorf("timeout: %+v", r)
	}
}

func TestHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	env := testEnv()
	if r := run(t, env, "http", `{"url":"`+srv.URL+`"}`); r.OK || !strings.Contains(r.Message, "certificate") {
		t.Errorf("untrusted certificate: %+v", r)
	}
	r := run(t, env, "http", `{"url":"`+srv.URL+`","ignore_tls":true}`)
	if !r.OK || r.CertExpires.IsZero() {
		t.Errorf("ignore TLS: %+v", r)
	}
	env.Transport = func(bool) http.RoundTripper { return srv.Client().Transport }
	if r := run(t, env, "http", `{"url":"`+srv.URL+`"}`); !r.OK || !r.CertExpires.Equal(srv.Certificate().NotAfter) {
		t.Errorf("trusted: %+v", r)
	}
}

func TestQuery(t *testing.T) {
	doc := []byte(`{"a":{"b":[10,{"c":"x"}]},"d.e":true,"n":null,"s":"text"}`)
	for path, want := range map[string]string{
		"a.b[0]": "10", "$.a.b[1].c": "x", `["d.e"]`: "true", "n": "null", "$.a.b": `[10,{"c":"x"}]`, "s": "text",
	} {
		if got, err := Query(doc, path); err != nil || got != want {
			t.Errorf("%s: %q %v", path, got, err)
		}
	}
	for _, path := range []string{"zz", "a.b[5]", "a.b[0].c", "s.x", "a[0]", "a.b.c"} {
		if _, err := Query(doc, path); err == nil {
			t.Errorf("%s found something", path)
		}
	}
	for _, bad := range []string{"", "$", "a..b", "a.", `["x`, "a[x]", "a[-1]", "a[1"} {
		if _, err := parsePath(bad); err == nil {
			t.Errorf("path %q accepted", bad)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		got, op, want string
		ok            bool
	}{
		{"10", "==", "10.0", true}, {"10", "!=", "9", true}, {"9", "<", "10", true}, {"9", "<=", "9", true},
		{"11", ">", "10", true}, {"10", ">=", "11", false}, {"b", ">", "a", true}, {"abc", "contains", "b", true},
		{"green", "==", "red", false}, {"2", ">", "10", false},
	} {
		if compare(c.got, c.op, c.want) != c.ok {
			t.Errorf("%s %s %s", c.got, c.op, c.want)
		}
	}
}
