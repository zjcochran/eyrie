// SPDX-License-Identifier: AGPL-3.0-or-later

// Package check knows the monitor types: their settings, how to check them,
// and what a check found. Every check reaches the network only through an
// Env, so tests give it fakes.
package check

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Result is what one check found.
type Result struct {
	OK          bool
	Message     string
	Latency     time.Duration // negative: none
	CertExpires time.Time     // HTTPS: when the server's certificate expires
}

func fail(format string, args ...any) Result {
	return Result{Message: fmt.Sprintf(format, args...), Latency: -1}
}

// Checker checks one monitor. The context ends at the monitor's timeout.
type Checker interface {
	Check(ctx context.Context) Result
}

// Env is how checks reach the network.
type Env struct {
	// Dial opens TCP and UDP connections.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Transport makes HTTP requests; insecure skips TLS certificate checks.
	Transport func(insecure bool) http.RoundTripper
	// Resolve looks up a host name's addresses.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// Ping sends one ICMP echo request of size bytes and waits for its
	// reply.
	Ping func(ctx context.Context, addr netip.Addr, size, seq int) (time.Duration, error)
	// UserAgent is sent with HTTP requests.
	UserAgent string
}

// DefaultEnv reaches the real network.
func DefaultEnv(userAgent string) *Env {
	dialer := &net.Dialer{}
	secure := newTransport(dialer, false)
	insecure := newTransport(dialer, true)
	return &Env{
		Dial: dialer.DialContext,
		Transport: func(skip bool) http.RoundTripper {
			if skip {
				return insecure
			}
			return secure
		},
		Resolve: func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		},
		Ping:      ping,
		UserAgent: userAgent,
	}
}

// Type is a monitor type.
type Type struct {
	Name  string
	Label string
	// Scheduled types are checked at their interval by a Checker. The
	// others are run by the engine: push waits for pushes, manual is set by
	// people, group follows its members.
	Scheduled bool
	// settings returns a new, empty settings value of the type.
	settings func() settings
}

// settings are a type's own settings.
type settings interface {
	// normalize fills in defaults and checks the values.
	normalize() error
	// checker is what checks a monitor with these settings.
	checker(env *Env) Checker
}

// Types are every monitor type, by name.
var Types = map[string]Type{}

func register(t Type) { Types[t.Name] = t }

func init() {
	register(Type{"http", "HTTP(S)", true, func() settings { return &HTTP{} }})
	register(Type{"keyword", "HTTP(S) keyword", true, func() settings { return &Keyword{} }})
	register(Type{"json-query", "HTTP(S) JSON query", true, func() settings { return &JSONQuery{} }})
	register(Type{"port", "TCP port", true, func() settings { return &Port{} }})
	register(Type{"ping", "Ping", true, func() settings { return &Ping{} }})
	register(Type{"dns", "DNS", true, func() settings { return &DNS{} }})
	register(Type{"push", "Push", false, func() settings { return &none{} }})
	register(Type{"manual", "Manual", false, func() settings { return &none{} }})
	register(Type{"group", "Group", false, func() settings { return &none{} }})
}

// Names are the type names, sorted.
func Names() []string {
	var out []string
	for n := range Types {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Parse reads a type's settings from JSON, fills in defaults and checks
// them. It returns the settings as they will be saved, and their Checker
// (nil for types the engine runs). Errors are meant for people.
func Parse(env *Env, typ string, raw json.RawMessage) (json.RawMessage, Checker, error) {
	t, ok := Types[typ]
	if !ok {
		return nil, nil, fmt.Errorf("unknown monitor type %q", typ)
	}
	s := t.settings()
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(s); err != nil {
		return nil, nil, fmt.Errorf("the %s settings are not right: %w", t.Label, err)
	}
	if err := s.normalize(); err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(s)
	if err != nil {
		return nil, nil, err
	}
	if !t.Scheduled {
		return out, nil, nil
	}
	return out, s.checker(env), nil
}

// none is the settings of types without any.
type none struct{}

func (*none) normalize() error     { return nil }
func (*none) checker(*Env) Checker { return nil }

// host checks a host name or IP address.
func host(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", errors.New("the host is required")
	}
	if strings.ContainsAny(h, " /:?#@") && !isIPv6(h) {
		return "", fmt.Errorf("%q is not a host name or IP address", h)
	}
	return h, nil
}

func isIPv6(h string) bool {
	a, err := netip.ParseAddr(strings.Trim(h, "[]"))
	return err == nil && a.Is6()
}
