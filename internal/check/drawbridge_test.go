// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeDrawbridge is Drawbridge's /api/status, answering status with code.
func fakeDrawbridge(t *testing.T, code int, status string) (srv *httptest.Server, auth *string) {
	auth = new(string)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		*auth = r.Header.Get("Authorization")
		w.WriteHeader(code)
		w.Write([]byte(status))
	}))
	t.Cleanup(srv.Close)
	return srv, auth
}

func drawbridgeSettings(url string) string {
	b, _ := json.Marshal(map[string]string{"url": url, "token": "dbt_test"})
	return string(b)
}

func TestDrawbridge(t *testing.T) {
	env := testEnv()
	cases := []struct {
		status string
		ok     bool
		msg    string
	}{
		{`{"state":"primary","state_label":"On primary","since":"2026-10-14T15:17:34Z","current_isp":{"name":"EXAMPLENET"},"primary_isp":{"name":"EXAMPLENET"}}`,
			true, "On primary, on EXAMPLENET since 2026-10-14 15:17 UTC"},
		{`{"state":"failover","state_label":"Failover","since":"2026-10-14T15:17:34Z","current_isp":{"name":"SKYLINK"},"primary_isp":{"name":"EXAMPLENET"},"failover_test_until":null}`,
			false, "Failover, on SKYLINK since 2026-10-14 15:17 UTC (primary: EXAMPLENET)"},
		{`{"state":"failback_pending","state_label":"Failback pending","since":"2026-10-14T16:00:00Z","current_isp":{"name":"EXAMPLENET"},"primary_isp":null}`,
			false, "Failback pending, on EXAMPLENET since 2026-10-14 16:00 UTC"},
		{`{"state":"failover","state_label":"Failover","since":"2026-10-14T15:17:34Z","current_isp":{"name":"SKYLINK"},"failover_test_until":"2026-10-14T15:27:34Z"}`,
			true, "Failover, on SKYLINK since 2026-10-14 15:17 UTC (a failover test, until 15:27 UTC)"},
		{`{"state":"failover_pending","state_label":"Failover pending","since":"2026-10-14T15:17:34Z","current_isp":{"name":"SKYLINK"}}`,
			true, "Failover pending, on SKYLINK since 2026-10-14 15:17 UTC"},
		{`{"state":"learning","current_isp":null}`, true, "learning"},
	}
	for _, c := range cases {
		srv, auth := fakeDrawbridge(t, http.StatusOK, c.status)
		r := run(t, env, "drawbridge", drawbridgeSettings(srv.URL+"/"))
		if r.OK != c.ok || r.Message != c.msg || r.Latency < 0 || *auth != "Bearer dbt_test" {
			t.Errorf("%s: %+v %q", c.status, r, *auth)
		}
	}
}

func TestDrawbridgeFailures(t *testing.T) {
	env := testEnv()
	for _, c := range []struct {
		code   int
		status string
		msg    string
	}{
		{http.StatusUnauthorized, ``, "Drawbridge refused the API token (401 Unauthorized)"},
		{http.StatusInternalServerError, ``, "Drawbridge answered 500 Internal Server Error"},
		{http.StatusOK, `<html>`, "Drawbridge's answer is not its status"},
		{http.StatusOK, `{}`, "Drawbridge's answer is not its status"},
	} {
		srv, _ := fakeDrawbridge(t, c.code, c.status)
		if r := run(t, env, "drawbridge", drawbridgeSettings(srv.URL+"/api/status")); r.OK || r.Message != c.msg {
			t.Errorf("%d %s: %+v", c.code, c.status, r)
		}
	}
	srv, _ := fakeDrawbridge(t, http.StatusOK, `{}`)
	srv.Close()
	if r := run(t, env, "drawbridge", drawbridgeSettings(srv.URL)); r.OK || r.Message == "" {
		t.Errorf("closed: %+v", r)
	}
}

func TestDrawbridgeSettings(t *testing.T) {
	env := testEnv()
	out, err := Prepare(env, "drawbridge", json.RawMessage(`{"url":" http://drawbridge.example.com:8080/api/status ","token":"dbt_x"}`), nil, nil)
	if err != nil || string(out) != `{"url":"http://drawbridge.example.com:8080","token":"dbt_x","ignore_tls":false}` {
		t.Errorf("%s %v", out, err)
	}
	for _, raw := range []string{`{"url":"drawbridge:8080","token":"dbt_x"}`, `{"url":"http://drawbridge.example.com"}`} {
		if _, err := Prepare(env, "drawbridge", json.RawMessage(raw), nil, nil); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
	if got := describe(t, "drawbridge", `{"url":"http://alice:pw@drawbridge.example.com","token":"dbt_x"}`); got != "Drawbridge: http://drawbridge.example.com\n" {
		t.Errorf("describe %q", got)
	}
}
