// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// fakeChrome answers DevTools commands; page says what loading a URL
// does: its status, or a navigation error.
type fakeChrome struct {
	status    int
	navError  string
	failLoad  string
	refuse    string // a method to refuse
	commands  []string
	ignoreTLS bool
}

func (f *fakeChrome) serve(s wsServer) {
	send := func(v any) {
		b, _ := json.Marshal(v)
		s.send(opText, true, b)
	}
	for {
		b, err := s.Read()
		if err != nil {
			return
		}
		var m struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Sess   string          `json:"sessionId"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(b, &m)
		f.commands = append(f.commands, m.Method)
		if m.Method == f.refuse {
			send(map[string]any{"id": m.ID, "error": map[string]any{"message": "nope"}})
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "Target.createBrowserContext":
			result = map[string]any{"browserContextId": "ctx1"}
		case "Target.createTarget":
			result = map[string]any{"targetId": "t1"}
		case "Target.attachToTarget":
			// An event of another session first.
			send(map[string]any{"method": "Page.loadEventFired", "sessionId": "other", "params": map[string]any{}})
			result = map[string]any{"sessionId": "s1"}
		case "Security.setIgnoreCertificateErrors":
			f.ignoreTLS = true
		case "Page.navigate":
			if f.navError != "" {
				result = map[string]any{"frameId": "f1", "errorText": f.navError}
				break
			}
			// Events can come before the answer.
			send(map[string]any{"method": "Network.responseReceived", "sessionId": "s1", "params": map[string]any{
				"type": "Image", "loaderId": "l1", "response": map[string]any{"status": 404}}})
			if f.failLoad != "" {
				send(map[string]any{"method": "Network.loadingFailed", "sessionId": "s1", "params": map[string]any{
					"type": "Document", "loaderId": "l1", "errorText": f.failLoad}})
			}
			send(map[string]any{"method": "Page.loadEventFired", "sessionId": "s1", "params": map[string]any{}})
			send(map[string]any{"method": "Network.responseReceived", "sessionId": "s1", "params": map[string]any{
				"type": "Document", "loaderId": "l1", "response": map[string]any{"status": f.status, "statusText": "",
					"securityDetails": map[string]any{"validTo": 1893456000.5}}}})
			send(map[string]any{"id": m.ID, "result": map[string]any{"frameId": "f1", "loaderId": "l1"}})
			send(map[string]any{"method": "Page.loadEventFired", "sessionId": "s1", "params": map[string]any{}})
			continue
		}
		send(map[string]any{"id": m.ID, "result": result})
	}
}

func TestBrowser(t *testing.T) {
	chrome := &fakeChrome{status: 200}
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			// Chrome names an address of its own, which Eyrie replaces.
			w.Write([]byte(`{"webSocketDebuggerUrl":"ws://192.0.2.1:9222/devtools/browser/abc"}`))
		case "/devtools/browser/abc":
			upgrade(chrome.serve, wsAccept)(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	env := testEnv()
	ws := "ws" + strings.TrimPrefix(srvURL, "http") + "/devtools/browser/abc"

	r := run(t, env, "real-browser", `{"url":"https://shop.example.com","browser_url":"`+srvURL+`","ignore_tls":true}`)
	if !r.OK || r.Message != "Loaded: 200 OK" || r.CertExpires.Unix() != 1893456000 || !chrome.ignoreTLS {
		t.Errorf("load: %+v", r)
	}
	if !strings.Contains(strings.Join(chrome.commands, " "), "Target.disposeBrowserContext") {
		t.Errorf("not cleaned up: %v", chrome.commands)
	}
	chrome.status = 503
	if r := run(t, env, "real-browser", `{"url":"https://shop.example.com","browser_url":"`+ws+`"}`); r.OK || r.Message != "Status 503 Service Unavailable" {
		t.Errorf("503: %+v", r)
	}
	chrome.navError = "net::ERR_NAME_NOT_RESOLVED"
	if r := run(t, env, "real-browser", `{"url":"https://shop.example.com","browser_url":"`+ws+`"}`); r.OK ||
		r.Message != "The page did not load: net::ERR_NAME_NOT_RESOLVED" {
		t.Errorf("nav error: %+v", r)
	}
	chrome.navError, chrome.failLoad = "", "net::ERR_CONNECTION_RESET"
	if r := run(t, env, "real-browser", `{"url":"https://shop.example.com","browser_url":"`+ws+`"}`); r.OK ||
		!strings.Contains(r.Message, "ERR_CONNECTION_RESET") {
		t.Errorf("load failed: %+v", r)
	}
	chrome.failLoad, chrome.refuse = "", "Page.enable"
	if r := run(t, env, "real-browser", `{"url":"https://shop.example.com","browser_url":"`+ws+`"}`); r.OK ||
		r.Message != "The browser refused Page.enable: nope" {
		t.Errorf("refused: %+v", r)
	}
	for _, bad := range []string{srvURL + "/nothing", "ftp://chrome.example.com", "ws://" + strings.TrimPrefix(srvURL, "http://") + "/none", "%zz"} {
		if r := run(t, env, "real-browser", `{"url":"https://shop.example.com","browser_url":"`+bad+`"}`); r.OK {
			t.Errorf("%s: %+v", bad, r)
		}
	}
	if f := (&Browser{URL: "https://shop.example.com/?k=1", Accepted: []string{"200"}}).describe(); f[0].Value != "https://shop.example.com/" {
		t.Errorf("describe %v", f)
	}
}

func TestBrowserInvalid(t *testing.T) {
	for _, s := range []string{`{"url":"ftp://example.com","browser_url":"ws://c.example.com"}`, `{"url":"https://example.com"}`,
		`{"url":"https://example.com","browser_url":"ws://c.example.com","accepted_status_codes":["9"]}`} {
		if _, _, err := Parse(testEnv(), "real-browser", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestCDPGarbage(t *testing.T) {
	srv := httptest.NewServer(upgrade(func(s wsServer) {
		s.Read()
		s.send(opText, true, []byte("not json"))
	}, wsAccept))
	defer srv.Close()
	r := run(t, testEnv(), "real-browser", `{"url":"https://shop.example.com","browser_url":"ws`+strings.TrimPrefix(srv.URL, "http")+`"}`)
	if r.OK || !strings.Contains(r.Message, "not JSON") {
		t.Errorf("garbage: %+v", r)
	}
}
