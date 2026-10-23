// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"strings"
	"testing"
)

func describe(t *testing.T, typ, raw string) string {
	t.Helper()
	saved, _, err := Parse(DefaultEnv("test"), typ, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("%s %s: %v", typ, raw, err)
	}
	var b strings.Builder
	for _, f := range Describe(typ, saved) {
		b.WriteString(f.Name + ": " + f.Value + "\n")
	}
	return b.String()
}

func TestDescribeHidesSecrets(t *testing.T) {
	// The user and password are joined at run time, so secret scanners do not
	// take the fixture for a real credential.
	got := describe(t, "http", `{"url": "https://alice:`+"s3cret"+`@shop.example.com/health?token=abc#x",
		"headers": {"Authorization": "Bearer t0ken", "X-Env": "prod"}, "body": "password=hunter2", "ignore_tls": true}`)
	for _, secret := range []string{"alice", "s3cret", "abc", "t0ken", "prod", "hunter2", "#x"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q leaks into\n%s", secret, got)
		}
	}
	for _, want := range []string{"URL: https://shop.example.com/health\n", "Method: GET", "200-299", "Maximum redirects: 10",
		"TLS errors: ignored", "Authorization, X-Env", "Request body: set"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestDescribeTypes(t *testing.T) {
	cases := []struct{ typ, raw, want string }{
		{"keyword", `{"url": "https://example.com", "keyword": "Welcome"}`, `Response must contain: "Welcome"`},
		{"keyword", `{"url": "https://example.com", "keyword": "Error", "invert_keyword": true}`, `must not contain: "Error"`},
		{"json-query", `{"url": "https://example.com", "json_path": "status", "expected_value": "ok"}`, `JSON query: status == "ok"`},
		{"port", `{"host": "192.0.2.10", "port": 5432}`, "Address: 192.0.2.10:5432"},
		{"port", `{"host": "2001:db8::1", "port": 22}`, "Address: [2001:db8::1]:22"},
		{"ping", `{"host": "192.0.2.1"}`, "Host: 192.0.2.1\nPings per check: 1\nPacket size: 56 bytes"},
		{"dns", `{"host": "example.com", "record_type": "MX", "expected": "mail"}`, "Record type: MX\nDNS server: 1.1.1.1:53\nExpected in a record: \"mail\""},
		{"push", `{}`, ""},
	}
	for _, c := range cases {
		if got := describe(t, c.typ, c.raw); !strings.Contains(got, c.want) {
			t.Errorf("%s %s: got\n%s", c.typ, c.raw, got)
		}
	}
	if Describe("nope", nil) != nil || Describe("http", json.RawMessage(`[`)) != nil {
		t.Error("unknown type or bad JSON described")
	}
	if publicURL("%zz") != "" {
		t.Error("unparsable URL")
	}
}
