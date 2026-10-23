// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"slices"
	"strconv"
	"time"
)

// SMTP is the settings of the smtp type: a mail server must greet Eyrie
// and answer EHLO, over TLS as asked.
type SMTP struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Security is "starttls" (the default: STARTTLS must work), "tls" (TLS
	// from the start, as on port 465) or "none" (plain, STARTTLS not tried).
	Security  string `json:"security"`
	IgnoreTLS bool   `json:"ignore_tls"`
}

var smtpSecurity = []string{"starttls", "tls", "none"}

func (s *SMTP) normalize() error {
	h, err := host(s.Host)
	if err != nil {
		return err
	}
	s.Host = h
	if s.Security == "" {
		s.Security = "starttls"
	}
	if !slices.Contains(smtpSecurity, s.Security) {
		return errors.New("the security must be starttls, tls or none")
	}
	if s.Port == 0 {
		s.Port = 25
		if s.Security == "tls" {
			s.Port = 465
		}
	}
	return checkPort(s.Port)
}

// checkPort checks a port number.
func checkPort(p int) error {
	if p < 1 || p > 65535 {
		return errors.New("the port must be from 1 to 65535")
	}
	return nil
}

func (s *SMTP) checker(env *Env) Checker { return &smtpChecker{s: s, env: env} }

func (s *SMTP) describe() []Fact {
	return []Fact{{"Address", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}, {"Security", s.Security}}
}

type smtpChecker struct {
	s   *SMTP
	env *Env
}

func (c *smtpChecker) Check(ctx context.Context) Result {
	start := time.Now()
	r, err := c.talk(ctx)
	if err != nil {
		return failErr(connError(ctx, err))
	}
	r.OK, r.Latency = true, time.Since(start)
	return r
}

func (c *smtpChecker) talk(ctx context.Context) (Result, error) {
	var r Result
	conn, err := c.env.Dial(ctx, "tcp", net.JoinHostPort(c.s.Host, strconv.Itoa(c.s.Port)))
	if err != nil {
		return r, errors.New(dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx))                                         //nolint:errcheck,gosec // a failure shows as a timeout
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) }) //nolint:errcheck,gosec // a failure shows as a timeout
	defer stop()
	cfg := &tls.Config{ServerName: c.s.Host, InsecureSkipVerify: c.s.IgnoreTLS} //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
	if c.s.Security == "tls" {
		conn = tls.Client(conn, cfg)
	}
	client, err := smtp.NewClient(conn, c.s.Host)
	if err != nil {
		return r, fmt.Errorf("no greeting: %w", err)
	}
	if err := client.Hello("eyrie.invalid"); err != nil {
		return r, fmt.Errorf("EHLO: %w", err)
	}
	r.Message = "Ready"
	switch c.s.Security {
	case "starttls":
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return r, errors.New("the server does not offer STARTTLS")
		}
		if err := client.StartTLS(cfg); err != nil {
			return r, fmt.Errorf("STARTTLS: %w", err)
		}
		r.Message = "Ready, STARTTLS works"
	case "tls":
		r.Message = "Ready over TLS"
	}
	if st, ok := client.TLSConnectionState(); ok && len(st.PeerCertificates) > 0 {
		r.CertExpires = st.PeerCertificates[0].NotAfter
	}
	client.Quit() //nolint:errcheck,gosec // the check is done
	return r, nil
}
