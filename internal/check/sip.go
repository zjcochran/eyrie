// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SIP is the settings of the sip-options type: a SIP server must answer
// OPTIONS with an accepted status.
type SIP struct {
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Transport string   `json:"transport"` // udp (the default), tcp or tls
	IgnoreTLS bool     `json:"ignore_tls"`
	Accepted  []string `json:"accepted_status_codes"`
}

var sipTransports = []string{"udp", "tcp", "tls"}

func (s *SIP) normalize() error {
	h, err := host(s.Host)
	if err != nil {
		return err
	}
	s.Host = h
	if s.Transport == "" {
		s.Transport = "udp"
	}
	if !slices.Contains(sipTransports, s.Transport) {
		return errors.New("the transport must be udp, tcp or tls")
	}
	if s.Port == 0 {
		s.Port = 5060
		if s.Transport == "tls" {
			s.Port = 5061
		}
	}
	if len(s.Accepted) == 0 {
		s.Accepted = []string{"200-299"}
	}
	for _, c := range s.Accepted {
		if _, _, ok := statusRange(c); !ok {
			return fmt.Errorf("%q is not a status code or a range such as 200-299", c)
		}
	}
	return checkPort(s.Port)
}

func (s *SIP) checker(env *Env) Checker { return &sipChecker{s: s, env: env} }

func (s *SIP) describe() []Fact {
	return []Fact{{"Server", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}, {"Transport", s.Transport},
		{"Accepted status codes", strings.Join(s.Accepted, ", ")}}
}

type sipChecker struct {
	s   *SIP
	env *Env
}

func token() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *sipChecker) request(local net.Addr, callID string) []byte {
	s := c.s
	target := "sip:" + net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	if s.Transport == "tls" {
		target = "sips:" + net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	}
	lines := []string{
		"OPTIONS " + target + " SIP/2.0",
		"Via: SIP/2.0/" + strings.ToUpper(s.Transport) + " " + local.String() + ";branch=z9hG4bK" + token() + ";rport",
		"Max-Forwards: 70",
		"From: <sip:eyrie@eyrie.invalid>;tag=" + token(),
		"To: <" + target + ">",
		"Call-ID: " + callID,
		"CSeq: 1 OPTIONS",
		"Accept: application/sdp",
		"User-Agent: " + c.env.UserAgent,
		"Content-Length: 0",
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n\r\n")
}

func (c *sipChecker) Check(ctx context.Context) Result {
	s := c.s
	network := "tcp"
	if s.Transport == "udp" {
		network = "udp"
	}
	conn, err := c.env.Dial(ctx, network, net.JoinHostPort(s.Host, strconv.Itoa(s.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	var certExpires time.Time
	if s.Transport == "tls" {
		tc := tls.Client(conn, &tls.Config{ServerName: s.Host, InsecureSkipVerify: s.IgnoreTLS}) //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
		if err := tc.HandshakeContext(ctx); err != nil {
			return failErr(connError(ctx, err))
		}
		if st := tc.ConnectionState(); len(st.PeerCertificates) > 0 {
			certExpires = st.PeerCertificates[0].NotAfter
		}
		conn = tc
	}
	callID := token() + "@eyrie.invalid"
	start := time.Now()
	if _, err := conn.Write(c.request(conn.LocalAddr(), callID)); err != nil {
		return failErr(connError(ctx, err))
	}
	code, status, err := c.answer(conn, network, callID)
	if err != nil {
		return failErr(connError(ctx, err))
	}
	r := Result{Latency: time.Since(start), CertExpires: certExpires, Message: status}
	for _, a := range s.Accepted {
		if lo, hi, _ := statusRange(a); code >= lo && code <= hi {
			r.OK = true
		}
	}
	if !r.OK {
		r.Message = "Status " + status
	}
	return r
}

// answer reads answers until the final one for callID, and returns its
// status code and status line ("200 OK").
func (c *sipChecker) answer(conn net.Conn, network, callID string) (int, string, error) {
	br := bufio.NewReader(conn)
	for {
		var tp *textproto.Reader
		if network == "udp" {
			buf := make([]byte, 65535)
			n, err := conn.Read(buf)
			if err != nil {
				return 0, "", err
			}
			tp = textproto.NewReader(bufio.NewReader(bytes.NewReader(buf[:n])))
		} else {
			tp = textproto.NewReader(br)
		}
		line, err := tp.ReadLine()
		if err != nil {
			return 0, "", err
		}
		h, err := tp.ReadMIMEHeader()
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, "", fmt.Errorf("the answer is not SIP: %w", err)
		}
		if network != "udp" {
			n, _ := strconv.Atoi(h.Get("Content-Length"))
			if _, err := io.CopyN(io.Discard, br, int64(n)); err != nil {
				return 0, "", err
			}
		}
		version, status, ok := strings.Cut(line, " ")
		code, err := strconv.Atoi(strings.SplitN(status, " ", 2)[0])
		if !ok || version != "SIP/2.0" || err != nil {
			return 0, "", errors.New("the answer is not SIP")
		}
		callIDs := append(h.Values("Call-ID"), h.Values("I")...)
		if !slices.Contains(callIDs, callID) || code < 200 {
			continue
		}
		return code, status, nil
	}
}
