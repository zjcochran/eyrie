// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// Port is the settings of the port type: a TCP connection must open.
type Port struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func (p *Port) normalize() error {
	h, err := host(p.Host)
	if err != nil {
		return err
	}
	p.Host = h
	if p.Port < 1 || p.Port > 65535 {
		return errors.New("the port must be from 1 to 65535")
	}
	return nil
}

func (p *Port) checker(env *Env) Checker { return &portChecker{p: p, env: env} }

type portChecker struct {
	p   *Port
	env *Env
}

func (c *portChecker) Check(ctx context.Context) Result {
	start := time.Now()
	conn, err := c.env.Dial(ctx, "tcp", net.JoinHostPort(c.p.Host, strconv.Itoa(c.p.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	conn.Close()
	return Result{OK: true, Message: "Connected", Latency: time.Since(start)}
}

// dialError says what went wrong, without Go's "dial tcp …:" prefix.
func dialError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Timed out"
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	return err.Error()
}

// Ping is the settings of the ping type: ICMP echo requests must be
// answered.
type Ping struct {
	Host       string `json:"host"`
	Count      int    `json:"count"`
	PacketSize int    `json:"packet_size"`
}

func (p *Ping) normalize() error {
	h, err := host(p.Host)
	if err != nil {
		return err
	}
	p.Host = h
	if p.Count == 0 {
		p.Count = 1
	}
	if p.PacketSize == 0 {
		p.PacketSize = 56
	}
	if p.Count < 1 || p.Count > 10 {
		return errors.New("the number of pings must be from 1 to 10")
	}
	if p.PacketSize < 8 || p.PacketSize > 1400 {
		return errors.New("the packet size must be from 8 to 1400 bytes")
	}
	return nil
}

func (p *Ping) checker(env *Env) Checker { return &pingChecker{p: p, env: env} }

type pingChecker struct {
	p   *Ping
	env *Env
}

// Check sends Count pings, one after another, each with an equal share of
// the time left. It is up when at least one is answered; the latency is
// the mean of the answers.
func (c *pingChecker) Check(ctx context.Context) Result {
	addr, err := c.resolve(ctx)
	if err != nil {
		return fail("%s", err)
	}
	var total time.Duration
	replies := 0
	var lastErr error
	for i := range c.p.Count {
		share := time.Until(deadline(ctx)) / time.Duration(c.p.Count-i)
		pctx, cancel := context.WithTimeout(ctx, share)
		rtt, err := c.env.Ping(pctx, addr, c.p.PacketSize, i+1)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		replies++
		total += rtt
	}
	if replies == 0 {
		if errors.Is(lastErr, context.DeadlineExceeded) {
			return fail("No reply from %s", addr)
		}
		return fail("No reply from %s: %v", addr, lastErr)
	}
	avg := total / time.Duration(replies)
	return Result{OK: true, Latency: avg,
		Message: fmt.Sprintf("%d of %d replies from %s, %.1f ms on average", replies, c.p.Count, addr, float64(avg)/1e6)}
}

func (c *pingChecker) resolve(ctx context.Context) (netip.Addr, error) {
	if a, err := netip.ParseAddr(c.p.Host); err == nil {
		return a.Unmap(), nil
	}
	addrs, err := c.env.Resolve(ctx, c.p.Host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("cannot resolve %s: %w", c.p.Host, err)
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("%s has no address", c.p.Host)
	}
	// IPv4 first: it works in more containers.
	for _, a := range addrs {
		if a.Unmap().Is4() {
			return a.Unmap(), nil
		}
	}
	return addrs[0], nil
}

func deadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(10 * time.Second)
}
