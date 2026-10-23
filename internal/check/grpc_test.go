// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const shopProto = `syntax = "proto3";
package shop.v1;
import "google/protobuf/empty.proto";
option go_package = "example.com/shop";
/* A block
   comment. */
message CheckRequest {
  string service = 1; // a line comment
  int32 depth = 2 [deprecated = true];
  repeated string tags = 3;
  map<string, int64> limits = 4;
  Kind kind = 5;
  Inner inner = 6;
  bytes blob = 7;
  sint64 delta = 8;
  double ratio = 9;
  float f = 10;
  fixed32 fx = 11;
  fixed64 fx64 = 12;
  sfixed32 sf = 13;
  sfixed64 sf64 = 14;
  uint64 big = 15;
  bool on = 16;
  repeated int32 nums = 17;
  oneof choice { string a = 18; int32 b = 19; }
  optional sint32 small = 21;
  map<int32, Inner> by_id = 22;
  message Inner { string note = 1; }
  enum Kind { option allow_alias = true; KIND_UNSPECIFIED = 0; KIND_FULL = 1; KIND_ALL = 1 [deprecated = true]; reserved 9; }
  reserved 20;
}
message CheckResponse {
  enum Status { UNKNOWN = 0; SERVING = 1; NOT_SERVING = 2; }
  Status status = 1;
  string message_text = 2;
  repeated int32 codes = 3;
  map<string, string> labels = 4;
  .shop.v1.CheckRequest echo = 5;
  repeated double ratios = 6;
  repeated fixed32 fixed = 7;
}
service Health {
  option (shop.v1.note) = "x";
  rpc Check (CheckRequest) returns (CheckResponse);
  rpc Watch (CheckRequest) returns (stream CheckResponse) {}
  rpc Ping (google.protobuf.Empty) returns (google.protobuf.Empty);
}
`

func TestProtoRoundTrip(t *testing.T) {
	pf, err := parseProto(shopProto)
	if err != nil {
		t.Fatal(err)
	}
	if m := pf.methods["shop.v1.Health/Watch"]; !m.stream {
		t.Error("stream")
	}
	if m := pf.methods["shop.v1.Health/Ping"]; m.in != "google.protobuf.Empty" {
		t.Errorf("ping %+v", m)
	}
	in := `{"service":"orders","depth":-3,"tags":["a","b"],"limits":{"x":"5"},"kind":"KIND_FULL","inner":{"note":"hi"},` +
		`"blob":"AQI=","delta":"-7","ratio":0.5,"f":1.5,"fx":7,"fx64":"8","sf":-9,"sf64":"-10","big":"18446744073709551615",` +
		`"on":true,"nums":[1,2],"a":"x","small":-2,"byId":{"4":{"note":"four"}},"b":null}`
	b, err := pf.encodeMessage("shop.v1.CheckRequest", json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := pf.decodeMessage(pf.messages["shop.v1.CheckRequest"], b, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(out)
	want := `{"a":"x","big":"18446744073709551615","blob":"AQI=","byId":{"4":{"note":"four"}},"delta":"-7","depth":-3,"f":1.5,` +
		`"fx":7,"fx64":"8","inner":{"note":"hi"},"kind":"KIND_FULL","limits":{"x":"5"},"nums":[1,2],"on":true,"ratio":0.5,` +
		`"service":"orders","sf":-9,"sf64":"-10","small":-2,"tags":["a","b"]}`
	if string(got) != want {
		t.Errorf("round trip\n got %s\nwant %s", got, want)
	}
	// Packed repeated scalars, an unknown enum value and an unknown field.
	resp := tag(nil, 3, 2)
	resp = append(resp, 2, 5, 6)
	resp = append(tag(resp, 6, 2), 8)
	resp = binary.LittleEndian.AppendUint64(resp, 0x3ff0000000000000)
	resp = append(tag(resp, 7, 2), 4)
	resp = binary.LittleEndian.AppendUint32(resp, 9)
	resp = append(tag(resp, 1, 0), 9)
	resp = append(tag(resp, 99, 0), 1)
	out, err = pf.decodeMessage(pf.messages["shop.v1.CheckResponse"], resp, 0)
	got, _ = json.Marshal(out)
	if err != nil || string(got) != `{"codes":[5,6],"fixed":[9],"ratios":[1],"status":9}` {
		t.Errorf("packed %s %v", got, err)
	}
	for _, bad := range [][]byte{{0x80}, {0x0b}, {0x09, 1}, {0x0d, 1}, {0x12, 5, 1}, {0x08}, {0x1a, 3, 0x80}, {0x32, 1, 0}, {0x3a, 1, 0}, {0x2a, 1, 0x80}, {0x29}} {
		if _, err := pf.decodeMessage(pf.messages["shop.v1.CheckResponse"], bad, 0); err == nil {
			t.Errorf("%x decoded", bad)
		}
	}
	if _, err := pf.decodeMessage(pf.messages["shop.v1.CheckResponse"], nil, 65); err == nil {
		t.Error("too deep")
	}
}

func TestProtoErrors(t *testing.T) {
	for _, src := range []string{
		`message A { string a = 1; `, `/* open`, `message A { string a = "x; }`, `message A { string a = 0; }`,
		`message A { B b = 1; }`, `service S { rpc M (A) returns (A); }`, `message A {} service S { rpc M (A) returns (B); }`,
		`message A { map<string string> m = 1; }`, `enum E { A = x; }`, `enum E { A = 1 }`, `huh`, `message A { string a 1; }`,
		`service S { thing }`, `message A { oneof o { repeated string a = 1; } }`, `message A { string a = 1 [x = 1 }`,
		`enum E { A = 1 [x = 1 }`, `package a`, `message A { string a = 1; } service S { rpc M A) returns (A); }`, "message A { ` }",
		`enum E {`, `service S {`, `message A {} service S { rpc M (A) (A); }`, `message A { map<string, string> m = 1 }`,
		`message A {} service S { rpc M (A) returns (A) }`, `message A { message B { `, `message A { enum E { `, `message A { oneof o x`,
		`message A { option x = 1 `, `syntax = "proto3"`, `enum E { option x = 1`,
	} {
		if _, err := parseProto(src); err == nil {
			t.Errorf("%q parsed", src)
		}
	}
	pf, _ := parseProto(shopProto)
	for _, body := range []string{`[]`, `{"nope":1}`, `{"tags":"a"}`, `{"service":1}`, `{"blob":1}`, `{"blob":"!"}`, `{"on":"yes"}`,
		`{"depth":"x"}`, `{"depth":1e40}`, `{"big":-1}`, `{"kind":"KIND_NONE"}`, `{"ratio":"x"}`, `{"limits":[]}`, `{"limits":{"x":"y"}}`,
		`{"inner":{"nope":1}}`, `{"byId":{"x":{}}}`, `{"byId":{"1":{"nope":1}}}`, `{"depth":{}}`} {
		if _, err := pf.encodeMessage("shop.v1.CheckRequest", json.RawMessage(body)); err == nil {
			t.Errorf("%s encoded", body)
		}
	}
	if b, err := pf.encodeMessage("shop.v1.CheckRequest", json.RawMessage(`{"kind":1}`)); err != nil || len(b) != 2 {
		t.Errorf("enum by number %x %v", b, err)
	}
}

// grpcServer answers shop.v1.Health/Check, echoing the request.
func grpcServer(t *testing.T) http.Handler {
	pf, err := parseProto(shopProto)
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shop.v1.Health/Check" {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "12")
			w.Header().Set("Grpc-Message", "unknown%20method")
			return
		}
		body, _ := io.ReadAll(r.Body)
		req, err := pf.decodeMessage(pf.messages["shop.v1.CheckRequest"], body[5:], 0)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/grpc")
		switch req["service"] {
		case "teapot":
			http.Error(w, "no", http.StatusTeapot)
			return
		case "nostatus":
			return
		case "badstatus":
			w.Header().Set("Grpc-Status", "x")
			return
		case "squash":
			w.Write([]byte{1, 0, 0, 0, 0})
			w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
			return
		case "short":
			w.Write([]byte{0, 0, 0, 0, 9, 1})
			w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
			return
		case "garbage":
			w.Write([]byte{0, 0, 0, 0, 1, 0x80})
			w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
			return
		case "empty":
			w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
			return
		}
		echo, _ := json.Marshal(req)
		msg, err := pf.encodeMessage("shop.v1.CheckResponse", json.RawMessage(`{"status":"SERVING","message_text":"auth `+
			r.Header.Get("Authorization")+`","echo":`+string(echo)+`}`))
		if err != nil {
			t.Error(err)
		}
		w.Write(append([]byte{0}, binary.BigEndian.AppendUint32(nil, uint32(len(msg)))...))
		w.Write(msg)
		code := "0"
		if req["service"] == "down" {
			code = "14"
		}
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", code)
		w.Header().Set(http.TrailerPrefix+"Grpc-Message", "")
	})
}

func TestGRPC(t *testing.T) {
	srv := httptest.NewUnstartedServer(grpcServer(t))
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	env := testEnv()
	proto, _ := json.Marshal(shopProto)
	settings := func(extra string) string {
		return `{"address":"` + addr + `","proto":` + string(proto) + `,"service":"shop.v1.Health","method":"Check"` + extra + `}`
	}
	r := run(t, env, "grpc-keyword", settings(`,"body":"{\"service\":\"orders\"}","metadata":{"Authorization":"Bearer t"},"keyword":"SERVING"`))
	if !r.OK || r.Message != `OK, "SERVING" found` {
		t.Errorf("serving: %+v", r)
	}
	if r := run(t, env, "grpc-keyword", settings(`,"metadata":{"Authorization":"Bearer t"},"keyword":"Bearer t"`)); !r.OK {
		t.Errorf("metadata: %+v", r)
	}
	if r := run(t, env, "grpc-keyword", settings(`,"keyword":"NOT_SERVING"`)); r.OK || !strings.HasPrefix(r.Message, `"NOT_SERVING" not found in {`) {
		t.Errorf("missing: %+v", r)
	}
	if r := run(t, env, "grpc-keyword", settings(`,"keyword":"NOT_SERVING","invert_keyword":true`)); !r.OK || r.Message != `OK, "NOT_SERVING" not found` {
		t.Errorf("inverted: %+v", r)
	}
	if r := run(t, env, "grpc-keyword", settings(`,"keyword":"SERVING","invert_keyword":true`)); r.OK || r.Message != `"SERVING" found` {
		t.Errorf("inverted found: %+v", r)
	}
	if r := run(t, env, "grpc-keyword", settings(``)); !r.OK || !strings.HasPrefix(r.Message, `OK: {"echo"`) {
		t.Errorf("no keyword: %+v", r)
	}
	for svc, want := range map[string]string{
		"down": "UNAVAILABLE", "teapot": "Status 418 I'm a teapot", "nostatus": "UNKNOWN: the server sent no gRPC status",
		"badstatus": "UNKNOWN: the server's gRPC status is not valid", "squash": "The answer is compressed, which Eyrie does not read",
		"short": "The answer's message is cut short", "garbage": "The answer is not valid protobuf", "empty": "The answer has no message",
	} {
		if r := run(t, env, "grpc-keyword", settings(`,"body":"{\"service\":\"`+svc+`\"}"`)); r.OK || r.Message != want {
			t.Errorf("%s: %+v", svc, r)
		}
	}
	unknown := `{"address":"` + addr + `","proto":` + string(proto) + `,"service":"shop.v1.Health","method":"Ping"}`
	if r := run(t, env, "grpc-keyword", unknown); r.OK || r.Message != "UNIMPLEMENTED: unknown method" {
		t.Errorf("unimplemented: %+v", r)
	}
	if f := Describe("grpc-keyword", []byte(settings(`,"keyword":"x","invert_keyword":true`))); len(f) != 4 || f[1].Value != "shop.v1.Health/Check" {
		t.Errorf("describe %v", f)
	}
}

func TestGRPCTLS(t *testing.T) {
	env := testEnv()
	proto, _ := json.Marshal(shopProto)
	tlsSrv := httptest.NewUnstartedServer(grpcServer(t))
	tlsSrv.EnableHTTP2 = true
	tlsSrv.StartTLS()
	defer tlsSrv.Close()
	taddr := strings.TrimPrefix(tlsSrv.URL, "https://")
	s := `{"address":"` + taddr + `","tls":true,"ignore_tls":true,"proto":` + string(proto) + `,"service":"shop.v1.Health","method":"Check","keyword":"SERVING"}`
	if r := run(t, env, "grpc-keyword", s); !r.OK || r.CertExpires.IsZero() {
		t.Errorf("tls: %+v", r)
	}
}

func TestGRPCInvalid(t *testing.T) {
	proto, _ := json.Marshal(shopProto)
	p := string(proto)
	for _, s := range []string{
		`{"address":"x","proto":` + p + `}`,
		`{"address":"x:99999","proto":` + p + `}`,
		`{"address":"x:1","proto":"message {"}`,
		`{"address":"x:1","proto":` + p + `,"service":"shop.v1.Health","method":"Nope"}`,
		`{"address":"x:1","proto":` + p + `,"service":"shop.v1.Health","method":"Watch"}`,
		`{"address":"x:1","proto":` + p + `,"service":"shop.v1.Health","method":"Check","body":"[]"}`,
		`{"address":"x:1","proto":` + p + `,"service":"shop.v1.Health","method":"Check","metadata":{"a b":"c"}}`,
		`{"address":"x:1","proto":"` + strings.Repeat(" ", maxProto+1) + `"}`,
	} {
		if _, _, err := Parse(testEnv(), "grpc-keyword", []byte(s)); err == nil {
			t.Errorf("%.80s accepted", s)
		}
	}
}
