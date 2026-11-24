// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxBody is the most of a response body read, for keywords and JSON.
const MaxBody = 1 << 20

// HTTP is the settings of the http type, which keyword and json-query
// share.
type HTTP struct {
	URL                 string            `json:"url"`
	Method              string            `json:"method"`
	Headers             map[string]string `json:"headers"`
	Body                string            `json:"body"`
	AcceptedStatusCodes []string          `json:"accepted_status_codes"`
	MaxRedirects        *int              `json:"max_redirects"` // nil: 10
	IgnoreTLS           bool              `json:"ignore_tls"`
	// NotifyCertExpiry sends warnings before the server's certificate
	// expires, at the days in the notification settings.
	NotifyCertExpiry bool `json:"notify_cert_expiry"`
}

// NotifiesCertExpiry reports whether a monitor's settings ask for
// certificate expiry warnings.
func NotifiesCertExpiry(settings []byte) bool {
	var s struct {
		NotifyCertExpiry bool `json:"notify_cert_expiry"`
	}
	return json.Unmarshal(settings, &s) == nil && s.NotifyCertExpiry
}

var methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

func (h *HTTP) normalize() error {
	h.URL = strings.TrimSpace(h.URL)
	u, err := url.Parse(h.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the URL must start with http:// or https://")
	}
	h.Method = strings.ToUpper(strings.TrimSpace(h.Method))
	if h.Method == "" {
		h.Method = "GET"
	}
	if !slices.Contains(methods, h.Method) {
		return fmt.Errorf("the method must be one of %s", strings.Join(methods, ", "))
	}
	if h.Headers == nil {
		h.Headers = map[string]string{}
	}
	for k := range h.Headers {
		if k == "" || strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(h.Headers[k], "\r\n") {
			return fmt.Errorf("the header %q is not valid", k)
		}
	}
	if len(h.AcceptedStatusCodes) == 0 {
		h.AcceptedStatusCodes = []string{"200-299"}
	}
	for _, c := range h.AcceptedStatusCodes {
		if _, _, ok := statusRange(c); !ok {
			return fmt.Errorf("%q is not a status code or a range such as 200-299", c)
		}
	}
	if h.MaxRedirects == nil {
		ten := 10
		h.MaxRedirects = &ten
	}
	if *h.MaxRedirects < 0 || *h.MaxRedirects > 20 {
		return errors.New("the maximum number of redirects is from 0 to 20")
	}
	return nil
}

// statusRange reads "200" or "200-299".
func statusRange(s string) (lo, hi int, ok bool) {
	a, b, isRange := strings.Cut(strings.TrimSpace(s), "-")
	lo, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	hi = lo
	if isRange {
		if hi, err = strconv.Atoi(b); err != nil {
			return 0, 0, false
		}
	}
	return lo, hi, lo >= 100 && hi <= 599 && lo <= hi
}

func (h *HTTP) accepted(code int) bool {
	for _, c := range h.AcceptedStatusCodes {
		if lo, hi, _ := statusRange(c); code >= lo && code <= hi {
			return true
		}
	}
	return false
}

func (h *HTTP) checker(env *Env) Checker { return &httpChecker{h: h, env: env} }

// newTransport is an HTTP transport without connection reuse between
// checks, so each check measures a fresh connection.
func newTransport(d *net.Dialer, insecure bool) http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = d.DialContext
	t.DisableKeepAlives = true
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
	return t
}

type httpChecker struct {
	h   *HTTP
	env *Env
	// inspect looks at the body of an accepted response, and says what it
	// found; nil accepts it.
	inspect func(body []byte) (ok bool, msg string, found []Fact)
}

func (c *httpChecker) Check(ctx context.Context) Result {
	h := c.h
	var body io.Reader
	if h.Body != "" {
		body = strings.NewReader(h.Body)
	}
	req, err := http.NewRequestWithContext(ctx, h.Method, h.URL, body)
	if err != nil {
		return fail("%v", err)
	}
	req.Header.Set("User-Agent", c.env.UserAgent)
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Transport: c.env.Transport(h.IgnoreTLS),
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > *h.MaxRedirects {
				return fmt.Errorf("more than %d redirects", *h.MaxRedirects)
			}
			return nil
		},
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fail("%s", requestError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	latency := time.Since(start)
	r := Result{Latency: latency, Details: responseDetails(h.URL, resp, len(data))}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		r.CertExpires = resp.TLS.PeerCertificates[0].NotAfter
	}
	status := resp.Status
	if !h.accepted(resp.StatusCode) {
		r.Message = "Status " + status
		return r
	}
	if err != nil {
		r.Message = "Reading the response: " + err.Error()
		return r
	}
	if c.inspect == nil {
		r.OK, r.Message = true, status
		return r
	}
	var found []Fact
	r.OK, r.Message, found = c.inspect(data)
	r.Details = append(found, r.Details...)
	return r
}

// responseDetails are what an HTTP response says about the server: its
// status, where redirects ended, the protocol, the body's type and size,
// and the certificate.
func responseDetails(asked string, resp *http.Response, size int) []Fact {
	f := []Fact{{"Status", resp.Status}}
	if resp.Request != nil && resp.Request.URL.String() != asked {
		f = append(f, Fact{"Redirected to", publicURL(resp.Request.URL.String())})
	}
	f = append(f, Fact{"Protocol", resp.Proto})
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		f = append(f, Fact{"Content type", ct})
	}
	f = append(f, Fact{"Body", byteSize(size)})
	if srv := resp.Header.Get("Server"); srv != "" {
		f = append(f, Fact{"Server", srv})
	}
	return append(f, tlsDetails(resp.TLS)...)
}

// tlsDetails are a TLS connection's version and its certificate's subject
// and issuer; nil without TLS.
func tlsDetails(st *tls.ConnectionState) []Fact {
	if st == nil {
		return nil
	}
	f := []Fact{{"TLS", tls.VersionName(st.Version)}}
	if len(st.PeerCertificates) > 0 {
		c := st.PeerCertificates[0]
		subject := c.Subject.CommonName
		if len(c.DNSNames) > 0 {
			subject = strings.Join(c.DNSNames, ", ")
		}
		if subject != "" {
			f = append(f, Fact{"Certificate for", clipText(subject, 200)})
		}
		issuer := c.Issuer.CommonName
		if len(c.Issuer.Organization) > 0 {
			issuer = strings.TrimSpace(c.Issuer.Organization[0] + " " + c.Issuer.CommonName)
		}
		if issuer != "" {
			f = append(f, Fact{"Issued by", issuer})
		}
	}
	return f
}

// byteSize is a size in bytes as people read it.
func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// clipText cuts s to at most n bytes, on a character's boundary.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// requestError says what went wrong with a request, without Go's
// `Get "https://…":` prefix.
func requestError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Timed out"
	}
	return err.Error()
}

// Keyword is the settings of the keyword type: an HTTP check whose response
// must contain a word, or must not with InvertKeyword.
type Keyword struct {
	HTTP
	Keyword       string `json:"keyword"`
	InvertKeyword bool   `json:"invert_keyword"`
}

func (k *Keyword) normalize() error {
	if k.Keyword == "" {
		return errors.New("the keyword is required")
	}
	return k.HTTP.normalize()
}

func (k *Keyword) checker(env *Env) Checker {
	return &httpChecker{h: &k.HTTP, env: env, inspect: func(body []byte) (bool, string, []Fact) {
		found := strings.Contains(string(body), k.Keyword)
		switch {
		case found && !k.InvertKeyword:
			return true, fmt.Sprintf("Keyword %q found", k.Keyword), []Fact{{"Keyword", "Found"}}
		case !found && k.InvertKeyword:
			return true, fmt.Sprintf("Keyword %q not found, as wanted", k.Keyword), []Fact{{"Keyword", "Not found, as wanted"}}
		case found:
			return false, fmt.Sprintf("Keyword %q found, but it must not be", k.Keyword), []Fact{{"Keyword", "Found, but it must not be"}}
		}
		return false, fmt.Sprintf("Keyword %q not found in %q", k.Keyword, snippet(body)), []Fact{{"Keyword", "Not found"}}
	}}
}

// snippet is the start of a body, for messages.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if r := []rune(s); len(r) > 50 {
		return string(r[:50]) + "…"
	}
	return s
}

// JSONQuery is the settings of the json-query type: an HTTP check whose
// JSON response must have a value at a path (see Query) that compares as
// wanted with ExpectedValue.
type JSONQuery struct {
	HTTP
	JSONPath      string `json:"json_path"`
	JSONOperator  string `json:"json_operator"`
	ExpectedValue string `json:"expected_value"`
}

func (j *JSONQuery) normalize() error {
	if _, err := parsePath(j.JSONPath); err != nil {
		return err
	}
	if j.JSONOperator == "" {
		j.JSONOperator = "=="
	}
	if !slices.Contains(operators, j.JSONOperator) {
		return fmt.Errorf("the operator must be one of %s", strings.Join(operators, " "))
	}
	return j.HTTP.normalize()
}

func (j *JSONQuery) checker(env *Env) Checker {
	return &httpChecker{h: &j.HTTP, env: env, inspect: func(body []byte) (bool, string, []Fact) {
		got, err := Query(body, j.JSONPath)
		if err != nil {
			return false, err.Error(), nil
		}
		found := []Fact{{"Value", clipText(got, 300)}, {"Expected", j.JSONOperator + " " + j.ExpectedValue}}
		if compare(got, j.JSONOperator, j.ExpectedValue) {
			return true, fmt.Sprintf("%s is %s", j.JSONPath, got), found
		}
		return false, fmt.Sprintf("%s is %s, not %s %s", j.JSONPath, got, j.JSONOperator, j.ExpectedValue), found
	}}
}
