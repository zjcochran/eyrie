// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Drawbridge is the settings of the drawbridge type: WAN Drawbridge's
// status, read from its /api/status with an API token. Down while it has
// failed over to a backup ISP.
type Drawbridge struct {
	URL       string `json:"url"`   // where Drawbridge is, such as http://drawbridge:8080
	Token     string `json:"token"` // a Drawbridge API token (dbt_…)
	IgnoreTLS bool   `json:"ignore_tls"`
}

func (d *Drawbridge) secrets() map[string]*string { return map[string]*string{"token": &d.Token} }

func (d *Drawbridge) normalize() error {
	d.URL = strings.TrimRight(strings.TrimSpace(d.URL), "/")
	d.URL = strings.TrimSuffix(d.URL, "/api/status")
	u, err := url.Parse(d.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the URL must start with http:// or https://")
	}
	if d.Token == "" {
		return errors.New("the API token is required")
	}
	return nil
}

func (d *Drawbridge) checker(env *Env) Checker { return &drawbridgeChecker{d: d, env: env} }

func (d *Drawbridge) describe() []Fact { return []Fact{{"Drawbridge", publicURL(d.URL)}} }

type drawbridgeChecker struct {
	d   *Drawbridge
	env *Env
}

// drawbridgeStatus is the part of Drawbridge's /api/status read here.
type drawbridgeStatus struct {
	State      string    `json:"state"`
	StateLabel string    `json:"state_label"`
	Since      time.Time `json:"since"`
	CurrentISP *struct {
		Name string `json:"name"`
	} `json:"current_isp"`
	PrimaryISP *struct {
		Name string `json:"name"`
	} `json:"primary_isp"`
	PublicIP          string     `json:"public_ip"`
	LastCheck         *time.Time `json:"last_check"`
	Due               *time.Time `json:"due"`
	Paused            bool       `json:"paused"`
	FailoverTestUntil *time.Time `json:"failover_test_until"`
	Stopped           []string   `json:"stopped"`
	Version           string     `json:"version"`
}

func (c *drawbridgeChecker) Check(ctx context.Context) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.d.URL+"/api/status", nil)
	if err != nil {
		return failErr(err)
	}
	req.Header.Set("User-Agent", c.env.UserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.d.Token)
	start := time.Now()
	resp, err := (&http.Client{Transport: c.env.Transport(c.d.IgnoreTLS)}).Do(req)
	if err != nil {
		return fail("%s", sentence(requestError(err)))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	latency := time.Since(start)
	if err != nil {
		return failErr(err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fail("Drawbridge refused the API token (401 Unauthorized)")
	case resp.StatusCode != http.StatusOK:
		return fail("Drawbridge answered %s", resp.Status)
	}
	var s drawbridgeStatus
	if err := json.Unmarshal(data, &s); err != nil || s.State == "" {
		return fail("Drawbridge's answer is not its status")
	}
	return c.result(s, latency)
}

// result is down while Drawbridge has failed over, as its own health
// check counts it: failover or failback_pending, but not during a
// failover test.
func (c *drawbridgeChecker) result(s drawbridgeStatus, latency time.Duration) Result {
	label := s.StateLabel
	if label == "" {
		label = s.State
	}
	msg := label
	if s.CurrentISP != nil && s.CurrentISP.Name != "" {
		msg += ", on " + s.CurrentISP.Name
	}
	if !s.Since.IsZero() {
		msg += " since " + s.Since.UTC().Format("2006-01-02 15:04 UTC")
	}
	failedOver := s.State == "failover" || s.State == "failback_pending"
	r := Result{OK: !failedOver, Latency: latency, Details: s.details(label)}
	switch {
	case failedOver && s.FailoverTestUntil != nil:
		r.OK, r.Message = true, fmt.Sprintf("%s (a failover test, until %s)", msg, s.FailoverTestUntil.UTC().Format("15:04 UTC"))
	case failedOver && s.PrimaryISP != nil && s.PrimaryISP.Name != "":
		r.Message = msg + " (primary: " + s.PrimaryISP.Name + ")"
	default:
		r.Message = msg
	}
	return r
}

// details are Drawbridge's status, for the monitor's page. Times are
// RFC 3339, for the page to show in the reader's time zone.
func (s drawbridgeStatus) details(label string) []Fact {
	f := []Fact{{"State", label}}
	add := func(name, v string) {
		if v != "" {
			f = append(f, Fact{name, v})
		}
	}
	at := func(t *time.Time) string {
		if t == nil || t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	if s.CurrentISP != nil {
		add("ISP in use", s.CurrentISP.Name)
	}
	if s.PrimaryISP != nil {
		add("Primary ISP", s.PrimaryISP.Name)
	}
	if !s.Since.IsZero() {
		add("Since", s.Since.UTC().Format(time.RFC3339))
	}
	add("Due", at(s.Due))
	add("Failover test until", at(s.FailoverTestUntil))
	add("Public IP", s.PublicIP)
	if s.Paused {
		add("Automation", "Paused")
	}
	if len(s.Stopped) > 0 {
		add("Stopped containers", strings.Join(s.Stopped, ", "))
	}
	add("Last WAN check", at(s.LastCheck))
	add("Drawbridge version", s.Version)
	return f
}
