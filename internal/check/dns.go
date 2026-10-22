// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DNS is the settings of the dns type: a DNS server must answer a query
// with at least one record of the type, and one record must contain
// Expected when it is set.
type DNS struct {
	Host         string `json:"host"`
	Resolver     string `json:"resolver"`
	ResolverPort int    `json:"resolver_port"`
	RecordType   string `json:"record_type"`
	Expected     string `json:"expected"`
}

// recordTypes are the record types a dns monitor can ask for, with their
// numbers (RFC 1035 and later).
var recordTypes = map[string]uint16{
	"A": 1, "NS": 2, "CNAME": 5, "SOA": 6, "PTR": 12, "MX": 15, "TXT": 16, "AAAA": 28, "SRV": 33, "CAA": 257,
}

// DefaultResolver is the DNS server asked when a monitor names none.
const DefaultResolver = "1.1.1.1"

func (d *DNS) normalize() error {
	d.Host = strings.TrimSuffix(strings.TrimSpace(d.Host), ".")
	if d.Host == "" || strings.ContainsAny(d.Host, " /:@") && !isIPv6(d.Host) {
		return errors.New("the host name to look up is required")
	}
	d.Resolver = strings.TrimSpace(d.Resolver)
	if d.Resolver == "" {
		d.Resolver = DefaultResolver
	}
	if _, err := host(d.Resolver); err != nil {
		return fmt.Errorf("the DNS server: %w", err)
	}
	if d.ResolverPort == 0 {
		d.ResolverPort = 53
	}
	if d.ResolverPort < 1 || d.ResolverPort > 65535 {
		return errors.New("the DNS server's port must be from 1 to 65535")
	}
	d.RecordType = strings.ToUpper(strings.TrimSpace(d.RecordType))
	if d.RecordType == "" {
		d.RecordType = "A"
	}
	if _, ok := recordTypes[d.RecordType]; !ok {
		var names []string
		for n := range recordTypes {
			names = append(names, n)
		}
		slices.Sort(names)
		return fmt.Errorf("the record type must be one of %s", strings.Join(names, ", "))
	}
	return nil
}

func (d *DNS) checker(env *Env) Checker { return &dnsChecker{d: d, env: env} }

type dnsChecker struct {
	d   *DNS
	env *Env
}

func (c *dnsChecker) Check(ctx context.Context) Result {
	d := c.d
	name := d.Host
	// PTR records are asked for by the reverse name of an address.
	if a, err := netip.ParseAddr(name); err == nil && d.RecordType == "PTR" {
		name = reverseName(a)
	}
	server := net.JoinHostPort(strings.Trim(d.Resolver, "[]"), strconv.Itoa(d.ResolverPort))
	start := time.Now()
	records, err := lookup(ctx, c.env, server, name, recordTypes[d.RecordType])
	latency := time.Since(start)
	if err != nil {
		return Result{Message: err.Error(), Latency: -1}
	}
	if len(records) == 0 {
		return Result{Message: fmt.Sprintf("No %s record for %s", d.RecordType, d.Host), Latency: latency}
	}
	list := strings.Join(records, ", ")
	if d.Expected != "" && !slices.ContainsFunc(records, func(r string) bool { return strings.Contains(r, d.Expected) }) {
		return Result{Message: fmt.Sprintf("No %s record contains %q: %s", d.RecordType, d.Expected, list), Latency: latency}
	}
	return Result{OK: true, Message: d.RecordType + " " + list, Latency: latency}
}

func reverseName(a netip.Addr) string {
	b := a.Unmap().AsSlice()
	var parts []string
	if len(b) == 4 {
		for i := 3; i >= 0; i-- {
			parts = append(parts, strconv.Itoa(int(b[i])))
		}
		return strings.Join(parts, ".") + ".in-addr.arpa"
	}
	const hex = "0123456789abcdef"
	for i := len(b) - 1; i >= 0; i-- {
		parts = append(parts, string(hex[b[i]&0x0f]), string(hex[b[i]>>4]))
	}
	return strings.Join(parts, ".") + ".ip6.arpa"
}

// lookup asks server for name's records of type qtype over UDP, and again
// over TCP when the answer was truncated. It returns the records as text.
func lookup(ctx context.Context, env *Env, server, name string, qtype uint16) ([]string, error) {
	var idb [2]byte
	rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	q, err := query(id, name, qtype)
	if err != nil {
		return nil, err
	}
	resp, err := exchange(ctx, env, "udp", server, q)
	if err != nil {
		return nil, err
	}
	if len(resp) > 2 && resp[2]&0x02 != 0 { // TC: truncated
		if resp, err = exchange(ctx, env, "tcp", server, q); err != nil {
			return nil, err
		}
	}
	return parseAnswer(resp, id, qtype)
}

func exchange(ctx context.Context, env *Env, network, server string, q []byte) ([]byte, error) {
	conn, err := env.Dial(ctx, network, server)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the DNS server %s: %s", server, dialError(err))
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d) //nolint:errcheck,gosec // a failure shows as a timeout below
	}
	timeout := func(err error) error {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("the DNS server %s did not answer in time", server)
		}
		return fmt.Errorf("the DNS server %s: %w", server, err)
	}
	if network == "tcp" {
		msg := binary.BigEndian.AppendUint16(nil, uint16(len(q))) //nolint:gosec // G115: a query is far below 64 KiB
		if _, err := conn.Write(append(msg, q...)); err != nil {
			return nil, timeout(err)
		}
		var n [2]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return nil, timeout(err)
		}
		buf := make([]byte, binary.BigEndian.Uint16(n[:]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, timeout(err)
		}
		return buf, nil
	}
	if _, err := conn.Write(q); err != nil {
		return nil, timeout(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, timeout(err)
	}
	return buf[:n], nil
}

// query builds a DNS query with recursion desired.
func query(id uint16, name string, qtype uint16) ([]byte, error) {
	b := binary.BigEndian.AppendUint16(nil, id)
	b = append(b, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0) // RD; one question
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if label == "" || len(label) > 63 {
			return nil, fmt.Errorf("%q is not a valid DNS name", name)
		}
		b = append(b, byte(len(label))) //nolint:gosec // G115: at most 63, checked above
		b = append(b, label...)
	}
	b = append(b, 0)
	b = binary.BigEndian.AppendUint16(b, qtype)
	return binary.BigEndian.AppendUint16(b, 1), nil // class IN
}

var errShort = errors.New("the DNS answer is cut short")

var rcodes = map[byte]string{1: "FORMERR", 2: "SERVFAIL", 3: "NXDOMAIN", 4: "NOTIMP", 5: "REFUSED"}

// parseAnswer reads the records of type qtype in a DNS response.
func parseAnswer(m []byte, id, qtype uint16) ([]string, error) {
	if len(m) < 12 {
		return nil, errShort
	}
	if binary.BigEndian.Uint16(m) != id || m[2]&0x80 == 0 {
		return nil, errors.New("the DNS server sent an answer to another question")
	}
	if rc := m[3] & 0x0f; rc != 0 {
		name := rcodes[rc]
		if name == "" {
			name = fmt.Sprintf("error %d", rc)
		}
		return nil, fmt.Errorf("the DNS server answered %s", name)
	}
	qd, an := int(binary.BigEndian.Uint16(m[4:])), int(binary.BigEndian.Uint16(m[6:]))
	off := 12
	for range qd {
		_, n, err := readName(m, off)
		if err != nil {
			return nil, err
		}
		off = n + 4
	}
	var out []string
	for range an {
		_, n, err := readName(m, off)
		if err != nil {
			return nil, err
		}
		if n+10 > len(m) {
			return nil, errShort
		}
		typ := binary.BigEndian.Uint16(m[n:])
		rdlen := int(binary.BigEndian.Uint16(m[n+8:]))
		start := n + 10
		if start+rdlen > len(m) {
			return nil, errShort
		}
		if typ == qtype {
			s, err := rdata(m, start, rdlen, typ)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		off = start + rdlen
	}
	return out, nil
}

// rdata renders a record's data as text.
func rdata(m []byte, off, n int, typ uint16) (string, error) {
	d := m[off : off+n]
	name := func(at int) (string, error) {
		s, _, err := readName(m, off+at)
		return s, err
	}
	switch typ {
	case 1, 28: // A, AAAA
		a, ok := netip.AddrFromSlice(d)
		if !ok || (typ == 1) != (len(d) == 4) {
			return "", errShort
		}
		return a.String(), nil
	case 2, 5, 12: // NS, CNAME, PTR
		return name(0)
	case 15: // MX
		if n < 3 {
			return "", errShort
		}
		host, err := name(2)
		return fmt.Sprintf("%d %s", binary.BigEndian.Uint16(d), host), err
	case 16: // TXT: one or more character strings
		var parts []string
		for i := 0; i < n; {
			l := int(d[i])
			if i+1+l > n {
				return "", errShort
			}
			parts = append(parts, string(d[i+1:i+1+l]))
			i += 1 + l
		}
		return strings.Join(parts, ""), nil
	case 33: // SRV
		if n < 7 {
			return "", errShort
		}
		target, err := name(6)
		return fmt.Sprintf("%d %d %d %s", binary.BigEndian.Uint16(d), binary.BigEndian.Uint16(d[2:]),
			binary.BigEndian.Uint16(d[4:]), target), err
	case 257: // CAA
		if n < 2 || 2+int(d[1]) > n {
			return "", errShort
		}
		return fmt.Sprintf("%d %s %q", d[0], d[2:2+int(d[1])], d[2+int(d[1]):]), nil
	}
	// SOA
	mname, next, err := readName(m, off)
	if err != nil {
		return "", err
	}
	rname, next, err := readName(m, next)
	if err != nil {
		return "", err
	}
	if next+20 > off+n {
		return "", errShort
	}
	v := func(i int) uint32 { return binary.BigEndian.Uint32(m[next+4*i:]) }
	return fmt.Sprintf("%s %s %d %d %d %d %d", mname, rname, v(0), v(1), v(2), v(3), v(4)), nil
}

// readName reads a domain name at off, following compression pointers,
// and returns it with the offset after it.
func readName(m []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for jumps := 0; ; {
		if off >= len(m) {
			return "", 0, errShort
		}
		l := int(m[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			if len(labels) == 0 {
				return ".", end, nil
			}
			return strings.Join(labels, "."), end, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(m) {
				return "", 0, errShort
			}
			if end < 0 {
				end = off + 2
			}
			if jumps++; jumps > 20 {
				return "", 0, errors.New("the DNS answer has a name that loops")
			}
			off = int(binary.BigEndian.Uint16(m[off:]) & 0x3fff)
		default:
			if off+1+l > len(m) {
				return "", 0, errShort
			}
			labels = append(labels, string(m[off+1:off+1+l]))
			off += 1 + l
		}
	}
}
