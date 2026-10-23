// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GRPC is the settings of the grpc-keyword type: a gRPC method, called
// with a JSON body, must answer OK, with a keyword in its answer (as JSON).
type GRPC struct {
	// Address is host:port.
	Address   string `json:"address"`
	TLS       bool   `json:"tls"`
	IgnoreTLS bool   `json:"ignore_tls"`
	// Proto is the .proto definition of the service and its messages.
	Proto string `json:"proto"`
	// Service is the service's full name, such as shop.v1.Orders.
	Service string `json:"service"`
	Method  string `json:"method"`
	// Body is the request, as JSON; {} by default.
	Body string `json:"body"`
	// Metadata are sent as headers.
	Metadata      map[string]string `json:"metadata"`
	Keyword       string            `json:"keyword"`
	InvertKeyword bool              `json:"invert_keyword"`

	file   *protoFile
	method protoMethod
}

// maxProto is the largest .proto definition accepted.
const maxProto = 256 << 10

func (g *GRPC) normalize() error {
	g.Address = strings.TrimSpace(g.Address)
	h, p, err := net.SplitHostPort(g.Address)
	if err != nil || h == "" {
		return errors.New("the address must be host:port")
	}
	if port, err := strconv.Atoi(p); err != nil || checkPort(port) != nil {
		return errors.New("the address must be host:port")
	}
	if len(g.Proto) > maxProto {
		return errors.New("the protobuf definition is at most 256 KiB")
	}
	if g.file, err = parseProto(g.Proto); err != nil {
		return err
	}
	g.Service, g.Method = strings.TrimSpace(g.Service), strings.TrimSpace(g.Method)
	m, ok := g.file.methods[g.Service+"/"+g.Method]
	if !ok {
		return fmt.Errorf("the protobuf definition has no method %s in a service %s", g.Method, g.Service)
	}
	if m.stream {
		return errors.New("streaming methods cannot be checked")
	}
	g.method = m
	if strings.TrimSpace(g.Body) == "" {
		g.Body = "{}"
	}
	if _, err := g.file.encodeMessage(m.in, json.RawMessage(g.Body)); err != nil {
		return fmt.Errorf("the body does not fit %s: %w", m.in, err)
	}
	if g.Metadata == nil {
		g.Metadata = map[string]string{}
	}
	for k, v := range g.Metadata {
		if k == "" || strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("the metadata %q is not valid", k)
		}
	}
	return nil
}

func (g *GRPC) checker(env *Env) Checker { return &grpcChecker{g: g, env: env} }

func (g *GRPC) describe() []Fact {
	f := []Fact{{"Address", g.Address}, {"Method", g.Service + "/" + g.Method}, {"TLS", strconv.FormatBool(g.TLS)}}
	if g.Keyword != "" {
		want := "must contain"
		if g.InvertKeyword {
			want = "must not contain"
		}
		f = append(f, Fact{"Answer " + want, strconv.Quote(g.Keyword)})
	}
	return f
}

type grpcChecker struct {
	g   *GRPC
	env *Env
}

// grpcCodes are gRPC's status codes.
var grpcCodes = []string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS",
	"PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL",
	"UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}

func (c *grpcChecker) transport() *http.Transport {
	t := &http.Transport{DialContext: c.env.Dial, DisableKeepAlives: true, Protocols: new(http.Protocols),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: c.g.IgnoreTLS}} //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
	if c.g.TLS {
		t.Protocols.SetHTTP2(true)
	} else {
		t.Protocols.SetUnencryptedHTTP2(true)
	}
	return t
}

func (c *grpcChecker) Check(ctx context.Context) Result {
	g := c.g
	msg, err := g.file.encodeMessage(g.method.in, json.RawMessage(g.Body))
	if err != nil {
		return failErr(err)
	}
	frame := append([]byte{0}, binary.BigEndian.AppendUint32(nil, uint32(len(msg)))...) //nolint:gosec // G115: small bodies
	scheme := "http"
	if g.TLS {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: g.Address, Path: "/" + g.Service + "/" + g.Method}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(append(frame, msg...)))
	if err != nil {
		return failErr(err)
	}
	for k, v := range g.Metadata {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("User-Agent", c.env.UserAgent)
	if d, ok := ctx.Deadline(); ok {
		req.Header.Set("Grpc-Timeout", strconv.FormatInt(max(time.Until(d).Milliseconds(), 1), 10)+"m")
	}
	start := time.Now()
	t := c.transport()
	defer t.CloseIdleConnections()
	resp, err := (&http.Client{Transport: t}).Do(req)
	if err != nil {
		return fail("%s", requestError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	r := Result{Latency: time.Since(start)}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		r.CertExpires = resp.TLS.PeerCertificates[0].NotAfter
	}
	if err != nil {
		r.Message = "Reading the answer: " + sentence(err.Error())
		return r
	}
	if resp.StatusCode != http.StatusOK {
		r.Message = "Status " + resp.Status
		return r
	}
	if code, text := grpcStatus(resp); code != 0 {
		name := strconv.Itoa(code)
		if code < len(grpcCodes) {
			name = grpcCodes[code]
		}
		r.Message = strings.TrimSuffix(name+": "+text, ": ")
		return r
	}
	answer, err := c.answer(body)
	if err != nil {
		r.Message = sentence(err.Error())
		return r
	}
	r.OK, r.Message = c.keyword(answer)
	return r
}

// grpcStatus reads the status from the trailers, or from the headers of a
// trailers-only answer.
func grpcStatus(resp *http.Response) (int, string) {
	s, m := resp.Trailer.Get("Grpc-Status"), resp.Trailer.Get("Grpc-Message")
	if s == "" {
		s, m = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	if s == "" {
		return 2, "the server sent no gRPC status"
	}
	code, err := strconv.Atoi(s)
	if err != nil {
		return 2, "the server's gRPC status is not valid"
	}
	if text, err := url.PathUnescape(m); err == nil {
		m = text
	}
	return code, m
}

// answer decodes the first message of the answer, as JSON.
func (c *grpcChecker) answer(body []byte) (string, error) {
	if len(body) < 5 {
		return "", errors.New("the answer has no message")
	}
	if body[0] != 0 {
		return "", errors.New("the answer is compressed, which Eyrie does not read")
	}
	n := binary.BigEndian.Uint32(body[1:])
	if uint64(n) > uint64(len(body)-5) { //nolint:gosec // G115: at least 5 bytes, checked above
		return "", errors.New("the answer's message is cut short")
	}
	m, err := c.g.file.decodeMessage(c.g.file.messages[c.g.method.out], body[5:5+n], 0)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(m)
	return string(out), err
}

func (c *grpcChecker) keyword(answer string) (bool, string) {
	g := c.g
	if g.Keyword == "" {
		return true, "OK: " + snippet([]byte(answer))
	}
	found := strings.Contains(answer, g.Keyword)
	switch {
	case found && !g.InvertKeyword:
		return true, fmt.Sprintf("OK, %q found", g.Keyword)
	case !found && g.InvertKeyword:
		return true, fmt.Sprintf("OK, %q not found", g.Keyword)
	case found:
		return false, fmt.Sprintf("%q found", g.Keyword)
	}
	return false, fmt.Sprintf("%q not found in %s", g.Keyword, snippet([]byte(answer)))
}
