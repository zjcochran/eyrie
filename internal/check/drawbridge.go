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
	State      string     `json:"state"`
	StateLabel string     `json:"state_label"`
	Since      time.Time  `json:"since"`
	CurrentISP *struct {
		Name string `json:"name"`
	} `json:"current_isp"`
	PrimaryISP *struct {
		Name string `json:"name"`
	} `json:"primary_isp"`
	FailoverTestUntil *time.Time `json:"failover_test_until"`
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
	if failedOver && s.FailoverTestUntil != nil {
		return Result{OK: true, Latency: latency,
			Message: fmt.Sprintf("%s (a failover test, until %s)", msg, s.FailoverTestUntil.UTC().Format("15:04 UTC"))}
	}
	if failedOver && s.PrimaryISP != nil && s.PrimaryISP.Name != "" {
		msg += " (primary: " + s.PrimaryISP.Name + ")"
	}
	return Result{OK: !failedOver, Message: msg, Latency: latency}
}
