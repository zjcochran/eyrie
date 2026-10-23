// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// globalpingAPI is Globalping's API.
var globalpingAPI = "https://api.globalping.io"

// Globalping is the settings of the globalping type: a ping or an HTTP
// request from one of Globalping's probes around the world.
type Globalping struct {
	Kind     string   `json:"kind"`     // ping (the default) or http
	Target   string   `json:"target"`   // a host to ping, or a URL
	Location string   `json:"location"` // where the probe is, as Globalping's "magic" field: a country, city, network, ASN…
	Packets  int      `json:"packets"`  // ping: 1 to 16, 3 by default
	Method   string   `json:"method"`   // http: HEAD (the default), GET or OPTIONS
	Accepted []string `json:"accepted_status_codes"`
	// Token is a Globalping API token, for more measurements an hour.
	Token string `json:"token"`
}

func (g *Globalping) secrets() map[string]*string { return map[string]*string{"token": &g.Token} }

func (g *Globalping) normalize() error {
	if g.Kind == "" {
		g.Kind = "ping"
	}
	g.Target = strings.TrimSpace(g.Target)
	g.Location = strings.TrimSpace(g.Location)
	if len(g.Location) > 200 {
		return errors.New("the location must be at most 200 characters")
	}
	switch g.Kind {
	case "ping":
		h, err := host(g.Target)
		if err != nil {
			return err
		}
		g.Target = h
		if g.Packets == 0 {
			g.Packets = 3
		}
		if g.Packets < 1 || g.Packets > 16 {
			return errors.New("the packets must be from 1 to 16")
		}
	case "http":
		u, err := url.Parse(g.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return errors.New("the target must be a URL starting with http:// or https://")
		}
		g.Method = strings.ToUpper(g.Method)
		if g.Method == "" {
			g.Method = "HEAD"
		}
		if !slices.Contains([]string{"HEAD", "GET", "OPTIONS"}, g.Method) {
			return errors.New("the method must be HEAD, GET or OPTIONS")
		}
		if len(g.Accepted) == 0 {
			g.Accepted = []string{"200-299"}
		}
		for _, c := range g.Accepted {
			if _, _, ok := statusRange(c); !ok {
				return fmt.Errorf("%q is not a status code or a range such as 200-299", c)
			}
		}
	default:
		return errors.New("the measurement must be ping or http")
	}
	return nil
}

func (g *Globalping) checker(env *Env) Checker {
	return &globalpingChecker{g: g, env: env, api: globalpingAPI}
}

func (g *Globalping) describe() []Fact {
	target := g.Target
	if g.Kind == "http" {
		target = publicURL(g.Target)
	}
	f := []Fact{{"Measurement", "Globalping " + g.Kind}, {"Target", target}}
	if g.Location != "" {
		f = append(f, Fact{"Probe location", g.Location})
	}
	return f
}

type globalpingChecker struct {
	g   *Globalping
	env *Env
	api string
}

// measurement is what is sent to create one.
func (c *globalpingChecker) measurement() map[string]any {
	g := c.g
	m := map[string]any{"type": g.Kind, "limit": 1}
	if g.Location != "" {
		m["locations"] = []map[string]any{{"magic": g.Location, "limit": 1}}
	}
	if g.Kind == "ping" {
		m["target"] = g.Target
		m["measurementOptions"] = map[string]any{"packets": g.Packets}
		return m
	}
	u, _ := url.Parse(g.Target)
	m["target"] = u.Hostname()
	req := map[string]any{"method": g.Method, "path": cmp.Or(u.EscapedPath(), "/")}
	if u.RawQuery != "" {
		req["query"] = u.RawQuery
	}
	opts := map[string]any{"request": req, "protocol": strings.ToUpper(u.Scheme)}
	if u.Port() != "" {
		opts["port"], _ = strconv.Atoi(u.Port())
	}
	m["measurementOptions"] = opts
	return m
}

func (c *globalpingChecker) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.env.UserAgent)
	req.Header.Set("Content-Type", "application/json")
	if c.g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.g.Token)
	}
	resp, err := (&http.Client{Transport: c.env.Transport(false)}).Do(req)
	if err != nil {
		return errors.New(requestError(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.Unmarshal(data, &e) //nolint:errcheck,gosec // the status is enough without it
		return fmt.Errorf("the Globalping API answered %s %s", resp.Status, e.Error.Message)
	}
	return json.Unmarshal(data, out)
}

// gpResult is one probe's result.
type gpResult struct {
	Probe struct {
		City, Country, Network string
		ASN                    int `json:"asn"`
	} `json:"probe"`
	Result struct {
		Status    string `json:"status"`
		RawOutput string `json:"rawOutput"`
		Stats     struct {
			Avg   *float64 `json:"avg"`
			Total int      `json:"total"`
			Rcv   int      `json:"rcv"`
		} `json:"stats"`
		StatusCode     int    `json:"statusCode"`
		StatusCodeName string `json:"statusCodeName"`
		Timings        json.RawMessage
		TLS            *struct {
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"tls"`
	} `json:"result"`
}

func (c *globalpingChecker) Check(ctx context.Context) Result {
	var created struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/v1/measurements", c.measurement(), &created); err != nil {
		return failErr(err)
	}
	var m struct {
		Status  string     `json:"status"`
		Results []gpResult `json:"results"`
	}
	for {
		if err := c.call(ctx, http.MethodGet, "/v1/measurements/"+url.PathEscape(created.ID), nil, &m); err != nil {
			return failErr(err)
		}
		if m.Status != "in-progress" {
			break
		}
		select {
		case <-ctx.Done():
			return fail("Timed out waiting for Globalping")
		case <-time.After(500 * time.Millisecond):
		}
	}
	if len(m.Results) == 0 {
		return fail("No Globalping probe matches the location")
	}
	return c.result(m.Results[0])
}

func (c *globalpingChecker) result(res gpResult) Result {
	p := res.Probe
	where := fmt.Sprintf("%s, %s (AS%d %s)", p.City, p.Country, p.ASN, p.Network)
	rr := res.Result
	if rr.Status != "finished" {
		return fail("Failed from %s: %s", where, lastLine([]byte(rr.RawOutput)))
	}
	if c.g.Kind == "ping" {
		r := Result{Latency: -1, OK: rr.Stats.Rcv > 0,
			Message: fmt.Sprintf("%d of %d replies from %s, pinging from %s", rr.Stats.Rcv, rr.Stats.Total, c.g.Target, where)}
		if rr.Stats.Avg != nil {
			r.Latency = time.Duration(*rr.Stats.Avg * float64(time.Millisecond))
		}
		return r
	}
	var t struct {
		Total float64 `json:"total"`
	}
	json.Unmarshal(rr.Timings, &t) //nolint:errcheck,gosec // no timing: no latency
	r := Result{Latency: time.Duration(t.Total * float64(time.Millisecond))}
	if rr.TLS != nil {
		r.CertExpires = rr.TLS.ExpiresAt
	}
	status := fmt.Sprintf("%d %s", rr.StatusCode, rr.StatusCodeName)
	for _, a := range c.g.Accepted {
		if lo, hi, _ := statusRange(a); rr.StatusCode >= lo && rr.StatusCode <= hi {
			r.OK = true
		}
	}
	if r.OK {
		r.Message = status + " from " + where
	} else {
		r.Message = "Status " + status + " from " + where
	}
	return r
}
