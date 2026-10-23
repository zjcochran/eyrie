// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGlobalping is Globalping's API, answering measurements with result.
type fakeGlobalping struct {
	created map[string]any
	auth    string
	polls   atomic.Int32
	result  string
}

func newFakeGlobalping(t *testing.T) *fakeGlobalping {
	f := &fakeGlobalping{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/measurements":
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &f.created)
			f.auth = r.Header.Get("Authorization")
			f.polls.Store(0)
			if strings.Contains(string(b), "nowhere") {
				w.WriteHeader(http.StatusUnprocessableEntity)
				w.Write([]byte(`{"error":{"message":"No suitable probes"}}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"m1","probesCount":1}`))
		case r.URL.Path == "/v1/measurements/m1":
			if f.polls.Add(1) == 1 {
				w.Write([]byte(`{"status":"in-progress","results":[]}`))
				return
			}
			w.Write([]byte(`{"status":"finished","results":[{"probe":{"city":"Example City","country":"XX","asn":64496,"network":"Example Net"},"result":` +
				f.result + `}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := globalpingAPI
	globalpingAPI = srv.URL
	t.Cleanup(func() { globalpingAPI = old })
	return f
}

func TestGlobalping(t *testing.T) {
	f := newFakeGlobalping(t)
	env := testEnv()
	f.result = `{"status":"finished","stats":{"avg":12.5,"total":3,"rcv":3}}`
	r := run(t, env, "globalping", `{"target":"shop.example.com","location":"Europe","token":"tok"}`)
	if !r.OK || r.Message != "3 of 3 replies from shop.example.com, pinging from Example City, XX (AS64496 Example Net)" ||
		r.Latency.Milliseconds() != 12 || f.auth != "Bearer tok" || f.created["type"] != "ping" {
		t.Errorf("ping: %+v %v", r, f.created)
	}
	f.result = `{"status":"finished","statusCode":200,"statusCodeName":"OK","timings":{"total":80},"tls":{"expiresAt":"2030-01-01T00:00:00Z"}}`
	r = run(t, env, "globalping", `{"kind":"http","target":"https://shop.example.com:8443/health?full=1"}`)
	opts, _ := json.Marshal(f.created["measurementOptions"])
	if !r.OK || r.Message != "200 OK from Example City, XX (AS64496 Example Net)" || r.CertExpires.Year() != 2030 || f.auth != "" ||
		string(opts) != `{"port":8443,"protocol":"HTTPS","request":{"method":"HEAD","path":"/health","query":"full=1"}}` {
		t.Errorf("http: %+v %s", r, opts)
	}
}

func TestGlobalpingFailures(t *testing.T) {
	f := newFakeGlobalping(t)
	env := testEnv()
	f.result = `{"status":"finished","statusCode":503,"statusCodeName":"Service Unavailable","timings":{"total":80}}`
	if r := run(t, env, "globalping", `{"kind":"http","target":"http://shop.example.com"}`); r.OK || !strings.HasPrefix(r.Message, "Status 503") {
		t.Errorf("503: %+v", r)
	}
	f.result = `{"status":"failed","rawOutput":"ping: unknown host"}`
	if r := run(t, env, "globalping", `{"target":"shop.example.com"}`); r.OK || !strings.HasSuffix(r.Message, ": ping: unknown host") {
		t.Errorf("failed: %+v", r)
	}
	if r := run(t, env, "globalping", `{"target":"shop.example.com","location":"nowhere"}`); r.OK ||
		r.Message != "The Globalping API answered 422 Unprocessable Entity No suitable probes" {
		t.Errorf("no probes: %+v", r)
	}
	globalpingAPI = "http://api.example.com/%zz"
	if r := run(t, env, "globalping", `{"target":"shop.example.com"}`); r.OK {
		t.Errorf("bad API: %+v", r)
	}
}

func TestGlobalpingInvalid(t *testing.T) {
	for _, s := range []string{`{"kind":"mtr","target":"x"}`, `{"target":""}`, `{"target":"x","packets":17}`, `{"kind":"http","target":"x"}`,
		`{"kind":"http","target":"http://x","method":"POST"}`, `{"kind":"http","target":"http://x","accepted_status_codes":["x"]}`,
		`{"target":"x","location":"` + strings.Repeat("l", 201) + `"}`} {
		if _, _, err := Parse(testEnv(), "globalping", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if f := (&Globalping{Kind: "http", Target: "https://u:p@shop.example.com/?k=1", Location: "Europe"}).describe(); f[1].Value != "https://shop.example.com/" ||
		len(f) != 3 {
		t.Errorf("describe %v", f)
	}
}
