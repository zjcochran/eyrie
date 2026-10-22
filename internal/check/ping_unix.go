// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux || darwin

package check

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"
	"time"
)

// ping sends one echo request through an unprivileged ICMP socket
// (SOCK_DGRAM), which needs no root: on Linux the group must be in
// net.ipv4.ping_group_range, which Docker allows for every group.
func ping(ctx context.Context, addr netip.Addr, size, seq int) (time.Duration, error) {
	family, proto := syscall.AF_INET, syscall.IPPROTO_ICMP
	if addr.Is6() {
		family, proto = syscall.AF_INET6, syscall.IPPROTO_ICMPV6
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM, proto)
	if err != nil {
		return 0, fmt.Errorf("cannot open an ICMP socket (on Linux, check net.ipv4.ping_group_range): %w", err)
	}
	f := os.NewFile(uintptr(fd), "icmp")
	conn, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d) //nolint:errcheck,gosec // a failure shows as a timeout below
	}
	start := time.Now()
	if _, err := conn.WriteTo(echo(addr.Is6(), os.Getpid(), seq, size), &net.UDPAddr{IP: addr.AsSlice(), Zone: addr.Zone()}); err != nil {
		return 0, err
	}
	buf := make([]byte, 1500+size)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return 0, context.DeadlineExceeded
			}
			return 0, err
		}
		if got, err := replySeq(buf[:n], addr.Is6()); err == nil && got == seq&0xffff {
			return time.Since(start), nil
		}
	}
}
