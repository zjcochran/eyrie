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

// RabbitMQ is the settings of the rabbitmq type: a node of a RabbitMQ
// cluster must answer its management API's health check with no alarms.
type RabbitMQ struct {
	// Nodes are the management API's URLs, tried in order until one
	// answers: http://rabbit.example.com:15672.
	Nodes     []string `json:"nodes"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	IgnoreTLS bool     `json:"ignore_tls"`
}

func (q *RabbitMQ) secrets() map[string]*string { return map[string]*string{"password": &q.Password} }

func (q *RabbitMQ) normalize() error {
	if len(q.Nodes) == 0 || len(q.Nodes) > 20 {
		return errors.New("give 1 to 20 node URLs")
	}
	for i, n := range q.Nodes {
		n = strings.TrimRight(strings.TrimSpace(n), "/")
		u, err := url.Parse(n)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return fmt.Errorf("%q is not an http:// or https:// URL without a user", n)
		}
		q.Nodes[i] = n
	}
	if strings.TrimSpace(q.Username) == "" {
		return errors.New("the user name is required")
	}
	return nil
}

func (q *RabbitMQ) checker(env *Env) Checker { return &rabbitChecker{q: q, env: env} }

func (q *RabbitMQ) describe() []Fact {
	nodes := make([]string, len(q.Nodes))
	for i, n := range q.Nodes {
		nodes[i] = publicURL(n)
	}
	return []Fact{{"RabbitMQ management API", strings.Join(nodes, ", ")}, {"User", q.Username}}
}

type rabbitChecker struct {
	q   *RabbitMQ
	env *Env
}

func (c *rabbitChecker) Check(ctx context.Context) Result {
	var errs []string
	for _, n := range c.q.Nodes {
		r, err := c.node(ctx, n)
		if err == nil {
			return r
		}
		errs = append(errs, publicURL(n)+": "+err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return fail("No node answered: %s", strings.Join(errs, "; "))
}

// node asks one node; an error means it did not answer.
func (c *rabbitChecker) node(ctx context.Context, base string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/health/checks/alarms", nil)
	if err != nil {
		return Result{}, err
	}
	req.SetBasicAuth(c.q.Username, c.q.Password)
	req.Header.Set("User-Agent", c.env.UserAgent)
	start := time.Now()
	resp, err := (&http.Client{Transport: c.env.Transport(c.q.IgnoreTLS)}).Do(req)
	if err != nil {
		return Result{}, errors.New(requestError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	r := Result{Latency: time.Since(start)}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		r.CertExpires = resp.TLS.PeerCertificates[0].NotAfter
	}
	var health struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	json.Unmarshal(body, &health) //nolint:errcheck,gosec // the status code says enough without it
	switch {
	case resp.StatusCode == http.StatusOK:
		r.OK, r.Message = true, "No alarms on "+publicURL(base)
	case resp.StatusCode == http.StatusServiceUnavailable && health.Reason != "":
		r.Message = sentence(health.Reason)
	case resp.StatusCode == http.StatusUnauthorized:
		r.Message = "Wrong user name or password"
	default:
		r.Message = "Status " + resp.Status
	}
	return r, nil
}
