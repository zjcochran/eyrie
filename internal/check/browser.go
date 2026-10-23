// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Browser is the settings of the real-browser type: a page must load in a
// real Chrome, which Eyrie drives over the Chrome DevTools Protocol, with
// an accepted status.
type Browser struct {
	URL string `json:"url"`
	// BrowserURL is the browser's DevTools address: ws://… or wss://…
	// (its WebSocket), or http://host:9222 (Chrome's own, which says
	// where the WebSocket is). It can hold a token, so it is a secret.
	BrowserURL string   `json:"browser_url"`
	Accepted   []string `json:"accepted_status_codes"`
	IgnoreTLS  bool     `json:"ignore_tls"`
}

func (b *Browser) secrets() map[string]*string {
	return map[string]*string{"browser_url": &b.BrowserURL}
}

func (b *Browser) normalize() error {
	b.URL = strings.TrimSpace(b.URL)
	u, err := url.Parse(b.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the URL must start with http:// or https://")
	}
	b.BrowserURL = strings.TrimSpace(b.BrowserURL)
	if b.BrowserURL == "" {
		return errors.New("the browser's address is required")
	}
	if len(b.Accepted) == 0 {
		b.Accepted = []string{"200-299"}
	}
	for _, c := range b.Accepted {
		if _, _, ok := statusRange(c); !ok {
			return fmt.Errorf("%q is not a status code or a range such as 200-299", c)
		}
	}
	return nil
}

func (b *Browser) checker(env *Env) Checker { return &browserChecker{b: b, env: env} }

func (b *Browser) describe() []Fact {
	return []Fact{{"URL", publicURL(b.URL)}, {"Loaded in", "a real browser (Chrome)"},
		{"Accepted status codes", strings.Join(b.Accepted, ", ")}}
}

type browserChecker struct {
	b   *Browser
	env *Env
}

// devtools finds the browser's DevTools WebSocket.
func (c *browserChecker) devtools(ctx context.Context) (string, error) {
	u, err := url.Parse(c.b.BrowserURL)
	if err != nil {
		return "", errors.New("the browser's address is not a URL")
	}
	switch u.Scheme {
	case "ws", "wss":
		return c.b.BrowserURL, nil
	case "http", "https":
	default:
		return "", errors.New("the browser's address must start with ws://, wss://, http:// or https://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.b.BrowserURL, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Transport: c.env.Transport(false)}).Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach the browser: %s", requestError(err))
	}
	defer resp.Body.Close()
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&v); err != nil || v.WS == "" {
		return "", errors.New("the browser did not say where its DevTools WebSocket is")
	}
	// Chrome names itself as it sees itself, often 127.0.0.1: use the host
	// Eyrie reached instead.
	ws, err := url.Parse(v.WS)
	if err != nil {
		return "", errors.New("the browser's DevTools WebSocket is not a URL")
	}
	ws.Host = u.Host
	if u.Scheme == "https" {
		ws.Scheme = "wss"
	}
	return ws.String(), nil
}

func (c *browserChecker) Check(ctx context.Context) Result {
	addr, err := c.devtools(ctx)
	if err != nil {
		return failErr(err)
	}
	ws, _, _, err := wsDial(ctx, c.env, addr, http.Header{}, false, true)
	if err != nil {
		return fail("Cannot reach the browser: %s", err)
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.conn.SetDeadline(time.Now()) }) //nolint:errcheck,gosec // a failure shows as a timeout
	defer stop()
	s := &cdpSession{ws: ws}
	r, err := c.load(s)
	if err != nil {
		return failErr(connError(ctx, err))
	}
	return r
}

// load opens the page in a new, separate browser context, and closes it
// afterwards.
func (c *browserChecker) load(s *cdpSession) (Result, error) {
	var bc struct {
		ID string `json:"browserContextId"`
	}
	if err := s.call("", "Target.createBrowserContext", map[string]any{}, &bc); err != nil {
		return Result{}, err
	}
	defer s.call("", "Target.disposeBrowserContext", map[string]any{"browserContextId": bc.ID}, nil) //nolint:errcheck // closing anyway
	var target struct {
		ID string `json:"targetId"`
	}
	if err := s.call("", "Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": bc.ID}, &target); err != nil {
		return Result{}, err
	}
	var att struct {
		Session string `json:"sessionId"`
	}
	if err := s.call("", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &att); err != nil {
		return Result{}, err
	}
	for _, m := range []string{"Network.enable", "Page.enable"} {
		if err := s.call(att.Session, m, map[string]any{}, nil); err != nil {
			return Result{}, err
		}
	}
	if c.b.IgnoreTLS {
		if err := s.call(att.Session, "Security.setIgnoreCertificateErrors", map[string]any{"ignore": true}, nil); err != nil {
			return Result{}, err
		}
	}
	start := time.Now()
	var nav struct {
		Frame  string `json:"frameId"`
		Loader string `json:"loaderId"`
		Error  string `json:"errorText"`
	}
	if err := s.call(att.Session, "Page.navigate", map[string]any{"url": c.b.URL}, &nav); err != nil {
		return Result{}, err
	}
	if nav.Error != "" {
		return Result{Latency: -1, Message: "The page did not load: " + nav.Error}, nil
	}
	return c.wait(s, att.Session, nav.Loader, start)
}

// cdpResponse is the parts of Network.responseReceived used.
type cdpResponse struct {
	Type     string `json:"type"`
	Loader   string `json:"loaderId"`
	Response struct {
		Status     int    `json:"status"`
		StatusText string `json:"statusText"`
		Security   *struct {
			ValidTo float64 `json:"validTo"`
		} `json:"securityDetails"`
	} `json:"response"`
}

// wait waits for the page's load event and returns its document's status.
func (c *browserChecker) wait(s *cdpSession, session, loader string, start time.Time) (Result, error) {
	var doc *cdpResponse
	for {
		ev, err := s.event(session)
		if err != nil {
			return Result{}, err
		}
		switch ev.Method {
		case "Network.responseReceived":
			var rr cdpResponse
			if json.Unmarshal(ev.Params, &rr) == nil && rr.Type == "Document" && rr.Loader == loader {
				doc = &rr
			}
		case "Network.loadingFailed":
			var lf struct {
				Type   string `json:"type"`
				Loader string `json:"loaderId"`
				Error  string `json:"errorText"`
			}
			if json.Unmarshal(ev.Params, &lf) == nil && lf.Type == "Document" && lf.Loader == loader {
				return Result{Latency: -1, Message: "The page did not load: " + lf.Error}, nil
			}
		case "Page.loadEventFired":
			if doc == nil {
				continue
			}
			return c.result(doc, time.Since(start)), nil
		}
	}
}

func (c *browserChecker) result(doc *cdpResponse, took time.Duration) Result {
	resp := doc.Response
	r := Result{Latency: took}
	if resp.Security != nil && resp.Security.ValidTo > 0 {
		sec, frac := math.Modf(resp.Security.ValidTo)
		r.CertExpires = time.Unix(int64(sec), int64(frac*1e9)).UTC()
	}
	status := strings.TrimSpace(fmt.Sprintf("%d %s", resp.Status, cmp.Or(resp.StatusText, http.StatusText(resp.Status))))
	for _, a := range c.b.Accepted {
		if lo, hi, _ := statusRange(a); resp.Status >= lo && resp.Status <= hi {
			r.OK = true
		}
	}
	if r.OK {
		r.Message = "Loaded: " + status
	} else {
		r.Message = "Status " + status
	}
	return r
}

// cdpSession speaks the Chrome DevTools Protocol over a WebSocket.
type cdpSession struct {
	ws     *wsConn
	nextID int
	events []cdpMessage
}

type cdpMessage struct {
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Session string          `json:"sessionId"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// call sends a command and waits for its answer, keeping the events that
// come first.
func (s *cdpSession) call(session, method string, params any, out any) error {
	s.nextID++
	id := s.nextID
	msg := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		msg["sessionId"] = session
	}
	b, _ := json.Marshal(msg)
	if err := s.ws.WriteText(b); err != nil {
		return err
	}
	for {
		m, err := s.read()
		if err != nil {
			return err
		}
		if m.ID == 0 {
			s.events = append(s.events, m)
			continue
		}
		if m.ID != id {
			continue
		}
		if m.Error != nil {
			return fmt.Errorf("the browser refused %s: %s", method, m.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	}
}

// event returns the next event of a session.
func (s *cdpSession) event(session string) (cdpMessage, error) {
	for {
		if len(s.events) > 0 {
			m := s.events[0]
			s.events = s.events[1:]
			if m.Session == session {
				return m, nil
			}
			continue
		}
		m, err := s.read()
		if err != nil {
			return m, err
		}
		if m.ID == 0 && m.Session == session {
			return m, nil
		}
	}
}

func (s *cdpSession) read() (cdpMessage, error) {
	var m cdpMessage
	b, err := s.ws.Read()
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return m, errTimeout
		}
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errors.New("the browser sent something that is not JSON")
	}
	return m, nil
}
