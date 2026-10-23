// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"time"
)

// NTP is the settings of the ntp type: a time server must answer with a
// synchronized clock, and its offset from Eyrie's may be limited.
type NTP struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// MaxOffset is the largest offset accepted, in milliseconds; 0: any.
	MaxOffset float64 `json:"max_offset_ms"`
}

func (n *NTP) normalize() error {
	h, err := host(n.Host)
	if err != nil {
		return err
	}
	n.Host = h
	if n.Port == 0 {
		n.Port = 123
	}
	if n.MaxOffset < 0 || n.MaxOffset > 3600000 {
		return errors.New("the largest offset must be from 0 (any) to 3600000 ms")
	}
	return checkPort(n.Port)
}

func (n *NTP) checker(env *Env) Checker { return &ntpChecker{n: n, env: env} }

func (n *NTP) describe() []Fact {
	f := []Fact{{"Server", net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}}
	if n.MaxOffset > 0 {
		f = append(f, Fact{"Largest offset", strconv.FormatFloat(n.MaxOffset, 'f', -1, 64) + " ms"})
	}
	return f
}

type ntpChecker struct {
	n   *NTP
	env *Env
	// now is the clock; nil: time.Now.
	now func() time.Time
}

// ntpEpoch is 1900-01-01, where NTP time starts.
var ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

func ntpTime(b []byte) time.Time {
	sec, frac := binary.BigEndian.Uint32(b), binary.BigEndian.Uint32(b[4:])
	return ntpEpoch.Add(time.Duration(sec)*time.Second + time.Duration(uint64(frac)*uint64(time.Second)>>32))
}

func (c *ntpChecker) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *ntpChecker) Check(ctx context.Context) Result {
	conn, err := c.env.Dial(ctx, "udp", net.JoinHostPort(c.n.Host, strconv.Itoa(c.n.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	// The transmit time is random: the answer must echo it, and it says
	// nothing about Eyrie's clock.
	req := make([]byte, 48)
	req[0] = 0x23 // no leap warning, version 4, client
	rand.Read(req[40:48])
	t1 := c.clock()
	if _, err := conn.Write(req); err != nil {
		return failErr(connError(ctx, err))
	}
	resp := make([]byte, 128)
	var n int
	for {
		if n, err = conn.Read(resp); err != nil {
			return failErr(connError(ctx, err))
		}
		if n >= 48 && string(resp[24:32]) == string(req[40:48]) {
			break
		}
	}
	t4 := c.clock()
	r := Result{Latency: t4.Sub(t1)}
	stratum := resp[1]
	switch {
	case resp[0]&0x07 != 4:
		r.Message = "Not a server's answer"
	case stratum == 0:
		r.Message = fmt.Sprintf("The server refused: %q", trimZero(resp[12:16]))
	case resp[0]>>6 == 3 || stratum >= 16:
		r.Message = "The server's clock is not synchronized"
	default:
		t2, t3 := ntpTime(resp[32:40]), ntpTime(resp[40:48])
		offset := (t2.Sub(t1) + t3.Sub(t4)) / 2
		ms := float64(offset) / 1e6
		r.Message = fmt.Sprintf("Stratum %d, offset %+.3f ms", stratum, ms)
		r.OK = c.n.MaxOffset == 0 || math.Abs(ms) <= c.n.MaxOffset
		if !r.OK {
			r.Message += fmt.Sprintf(", more than %s ms", strconv.FormatFloat(c.n.MaxOffset, 'f', -1, 64))
		}
	}
	return r
}

func trimZero(b []byte) string {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}
