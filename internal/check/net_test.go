// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPort(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = func(_ context.Context, network, addr string) (net.Conn, error) {
		dialed = network + " " + addr
		if strings.HasPrefix(addr, "closed") {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	if r := run(t, env, "port", `{"host":"db.example.com","port":5432}`); !r.OK || r.Message != "Connected" || dialed != "tcp db.example.com:5432" {
		t.Errorf("open: %+v %s", r, dialed)
	}
	if r := run(t, env, "port", `{"host":"2001:db8::1","port":22}`); !r.OK || dialed != "tcp [2001:db8::1]:22" {
		t.Errorf("IPv6: %+v %s", r, dialed)
	}
	if r := run(t, env, "port", `{"host":"closed.example.com","port":1}`); r.OK || r.Message != "connection refused" {
		t.Errorf("closed: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "port", `{"host":"example.com","port":1}`); r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	if dialError(errors.New("plain")) != "plain" {
		t.Error("plain error")
	}
}

func TestPing(t *testing.T) {
	env := testEnv()
	var seen []string
	env.Resolve = func(_ context.Context, h string) ([]netip.Addr, error) {
		switch h {
		case "dual.example.com":
			return []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.7")}, nil
		case "v6.example.com":
			return []netip.Addr{netip.MustParseAddr("2001:db8::2")}, nil
		case "none.example.com":
			return nil, nil
		}
		return nil, errors.New("no such host")
	}
	env.Ping = func(ctx context.Context, a netip.Addr, size, seq int) (time.Duration, error) {
		seen = append(seen, a.String())
		if _, ok := ctx.Deadline(); !ok || size != 56 {
			t.Errorf("ping without a deadline or with size %d", size)
		}
		switch {
		case a.String() == "192.0.2.99":
			return 0, context.DeadlineExceeded
		case a.String() == "192.0.2.98":
			return 0, errors.New("network is unreachable")
		case seq == 2:
			return 0, context.DeadlineExceeded // one lost
		}
		return 10 * time.Millisecond, nil
	}
	if r := run(t, env, "ping", `{"host":"dual.example.com","count":3}`); !r.OK || r.Latency != 10*time.Millisecond ||
		r.Message != "2 of 3 replies from 192.0.2.7, 10.0 ms on average" {
		t.Errorf("ping: %+v", r)
	}
	if r := run(t, env, "ping", `{"host":"v6.example.com"}`); !r.OK || seen[len(seen)-1] != "2001:db8::2" {
		t.Errorf("IPv6 only: %+v", r)
	}
	for host, want := range map[string]string{
		"192.0.2.99":       "No reply from 192.0.2.99",
		"192.0.2.98":       "No reply from 192.0.2.98: network is unreachable",
		"none.example.com": "has no address",
		"bad.example.com":  "cannot resolve bad.example.com: no such host",
	} {
		if r := run(t, env, "ping", `{"host":"`+host+`"}`); r.OK || !strings.Contains(r.Message, want) {
			t.Errorf("%s: %+v", host, r)
		}
	}
	// Without a deadline, the pings share 10 seconds.
	_, c, _ := Parse(env, "ping", []byte(`{"host":"192.0.2.1"}`))
	if r := c.Check(context.Background()); !r.OK {
		t.Errorf("no deadline: %+v", r)
	}
}

func TestICMP(t *testing.T) {
	m := echo(false, 0x1234, 7, 56)
	if len(m) != 64 || m[0] != 8 || checksum(m) != 0 {
		t.Errorf("v4 request: % x", m[:8])
	}
	if m6 := echo(true, 1, 2, 8); m6[0] != 128 || m6[2] != 0 || m6[3] != 0 {
		t.Errorf("v6 request: % x", m6[:8])
	}
	if checksum([]byte{1}) != ^uint16(0x0100) {
		t.Error("odd checksum")
	}
	reply := []byte{0, 0, 0, 0, 0x12, 0x34, 0, 7}
	if seq, err := replySeq(reply, false); err != nil || seq != 7 {
		t.Errorf("reply: %d %v", seq, err)
	}
	// With an IPv4 header in front, as macOS gives it.
	withIP := append(append([]byte{0x45}, make([]byte, 19)...), reply...)
	if seq, err := replySeq(withIP, false); err != nil || seq != 7 {
		t.Errorf("reply with header: %d %v", seq, err)
	}
	if seq, err := replySeq([]byte{129, 0, 0, 0, 0, 0, 0, 9}, true); err != nil || seq != 9 {
		t.Errorf("v6 reply: %d %v", seq, err)
	}
	for _, bad := range [][]byte{{0, 0}, {8, 0, 0, 0, 0, 0, 0, 1}, {0, 1, 0, 0, 0, 0, 0, 1}} {
		if _, err := replySeq(bad, false); err == nil {
			t.Errorf("% x accepted", bad)
		}
	}
}

// TestPingLoopback pings 127.0.0.1 through a real ICMP socket where the
// system allows one; it never leaves the machine.
func TestPingLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rtt, err := ping(ctx, netip.MustParseAddr("127.0.0.1"), 56, 1)
	if err != nil && strings.Contains(err.Error(), "ICMP socket") {
		t.Skip(err)
	}
	if err != nil || rtt <= 0 {
		t.Errorf("loopback ping: %v %v", rtt, err)
	}
}

// dnsServer answers DNS queries through net.Pipe with answer(query).
func dnsServer(answer func(q []byte, network string) []byte) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, network, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 4096)
			n, err := server.Read(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			if network == "tcp" {
				q = q[2:]
			}
			resp := answer(q, network)
			if resp == nil {
				io.Copy(io.Discard, server) // silent until the client gives up
				return
			}
			if network == "tcp" {
				resp = append(binary.BigEndian.AppendUint16(nil, uint16(len(resp))), resp...)
			}
			server.Write(resp)
		}()
		return client, nil
	}
}

// reply builds a response to q with the given answers, each the answer
// name as a pointer to the question, then type and record data.
func reply(q []byte, rcode byte, answers ...rr) []byte {
	m := append([]byte{}, q...)
	m[2], m[3] = 0x81, 0x80|rcode
	binary.BigEndian.PutUint16(m[6:], uint16(len(answers)))
	for _, a := range answers {
		m = append(m, 0xc0, 12)
		m = binary.BigEndian.AppendUint16(m, a.typ)
		m = append(m, 0, 1, 0, 0, 0, 60)
		m = binary.BigEndian.AppendUint16(m, uint16(len(a.data)))
		m = append(m, a.data...)
	}
	return m
}

type rr struct {
	typ  uint16
	data []byte
}

// name encodes a name; "@" ends it with a pointer to the question name.
func name(labels ...string) []byte {
	var b []byte
	for _, l := range labels {
		if l == "@" {
			return append(b, 0xc0, 12)
		}
		b = append(append(b, byte(len(l))), l...)
	}
	return append(b, 0)
}

func TestDNS(t *testing.T) {
	env := testEnv()
	env.Dial = dnsServer(func(q []byte, network string) []byte {
		qtype := binary.BigEndian.Uint16(q[len(q)-4:])
		switch {
		case strings.Contains(string(q), "missing"):
			return reply(q, 3)
		case strings.Contains(string(q), "big") && network == "udp":
			r := reply(q, 0)
			r[2] |= 0x02 // truncated: ask again over TCP
			return r
		}
		var a []rr
		switch qtype {
		case 1:
			a = []rr{{1, []byte{192, 0, 2, 10}}, {5, name("www", "@")}, {1, []byte{192, 0, 2, 11}}}
		case 28:
			a = []rr{{28, netip.MustParseAddr("2001:db8::10").AsSlice()}}
		case 5:
			a = []rr{{5, name("www", "@")}}
		case 15:
			a = []rr{{15, append([]byte{0, 10}, name("mail", "@")...)}}
		case 16:
			a = []rr{{16, append(append([]byte{5}, "v=spf"...), append([]byte{3}, "1 a"...)...)}}
		case 33:
			a = []rr{{33, append([]byte{0, 10, 0, 5, 1, 187}, name("svc", "@")...)}}
		case 257:
			a = []rr{{257, append([]byte{0, 5}, "issueca.example.net"...)}}
		case 6:
			soa := append(name("ns1", "@"), name("hostmaster", "@")...)
			for _, v := range []uint32{2027010101, 3600, 600, 86400, 300} {
				soa = binary.BigEndian.AppendUint32(soa, v)
			}
			a = []rr{{6, soa}}
		case 12:
			if !strings.Contains(string(q), "in-addr") && !strings.Contains(string(q), "ip6") {
				return reply(q, 0)
			}
			a = []rr{{12, name("host", "example", "com")}}
		case 2:
			a = []rr{{2, name("ns1", "@")}}
		}
		return reply(q, 0, a...)
	})
	for _, c := range []struct {
		settings string
		ok       bool
		msg      string
	}{
		{`{"host":"example.com"}`, true, "A 192.0.2.10, 192.0.2.11"},
		{`{"host":"example.com","expected":"192.0.2.11"}`, true, "A 192.0.2.10"},
		{`{"host":"example.com","expected":"192.0.2.99"}`, false, `No A record contains "192.0.2.99"`},
		{`{"host":"example.com","record_type":"aaaa"}`, true, "AAAA 2001:db8::10"},
		{`{"host":"example.com","record_type":"CNAME"}`, true, "CNAME www.example.com"},
		{`{"host":"example.com","record_type":"MX"}`, true, "MX 10 mail.example.com"},
		{`{"host":"example.com","record_type":"TXT"}`, true, "TXT v=spf1 a"},
		{`{"host":"example.com","record_type":"SRV"}`, true, "SRV 10 5 443 svc.example.com"},
		{`{"host":"example.com","record_type":"CAA"}`, true, `CAA 0 issue "ca.example.net"`},
		{`{"host":"example.com","record_type":"SOA"}`, true, "SOA ns1.example.com hostmaster.example.com 2027010101 3600 600 86400 300"},
		{`{"host":"example.com","record_type":"NS"}`, true, "NS ns1.example.com"},
		{`{"host":"192.0.2.10","record_type":"PTR"}`, true, "PTR host.example.com"},
		{`{"host":"2001:db8::1","record_type":"PTR"}`, true, "PTR host.example.com"},
		{`{"host":"example.com","record_type":"PTR"}`, false, "No PTR record for example.com"},
		{`{"host":"missing.example.com"}`, false, "answered NXDOMAIN"},
		{`{"host":"big.example.com"}`, true, "A 192.0.2.10"},
	} {
		r := run(t, env, "dns", c.settings)
		if r.OK != c.ok || !strings.Contains(r.Message, c.msg) {
			t.Errorf("%s: %+v", c.settings, r)
		}
	}
}

func TestDNSFailures(t *testing.T) {
	env := testEnv()
	env.Dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("network is unreachable")
	}
	if r := run(t, env, "dns", `{"host":"example.com","resolver":"192.0.2.53"}`); r.OK || r.Message != "cannot reach the DNS server 192.0.2.53:53: network is unreachable" {
		t.Errorf("unreachable: %+v", r)
	}
	env.Dial = dnsServer(func([]byte, string) []byte { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, c, _ := Parse(env, "dns", []byte(`{"host":"example.com"}`))
	if r := c.Check(ctx); r.OK || !strings.Contains(r.Message, "did not answer in time") {
		t.Errorf("silent server: %+v", r)
	}
	env.Dial = dnsServer(func(q []byte, _ string) []byte { return reply(q, 9) })
	if r := run(t, env, "dns", `{"host":"example.com"}`); !strings.Contains(r.Message, "answered error 9") {
		t.Errorf("odd rcode: %+v", r)
	}
	if _, err := query(1, "a..b", 1); err == nil {
		t.Error("empty label")
	}
	if r := run(t, env, "dns", `{"host":"a..example.com"}`); r.OK {
		t.Errorf("bad name: %+v", r)
	}
}

func TestParseAnswerErrors(t *testing.T) {
	q, _ := query(7, "example.com", 1)
	good := reply(q, 0, rr{1, []byte{192, 0, 2, 1}})
	if r, err := parseAnswer(good, 7, 1); err != nil || r[0] != "192.0.2.1" {
		t.Fatalf("good: %v %v", r, err)
	}
	if _, err := parseAnswer(good, 8, 1); err == nil {
		t.Error("another ID")
	}
	for n := range len(good) {
		parseAnswer(good[:n], 7, 1) // must not panic
	}
	for typ, data := range map[uint16][]byte{1: {1, 2, 3}, 28: {1, 2, 3, 4}, 15: {0}, 33: {0, 1}, 257: {0, 9, 1}, 16: {9, 1}, 6: name("a")} {
		if _, err := parseAnswer(reply(q, 0, rr{typ, data}), 7, typ); err == nil {
			t.Errorf("type %d with % x accepted", typ, data)
		}
	}
	loop := reply(q, 0, rr{5, []byte{0xc0, byte(len(q))}})
	loop = append(loop[:len(q)], 0xc0, byte(len(q)))
	if _, _, err := readName(loop, len(q)); err == nil {
		t.Error("a looping name")
	}
	if n, _, err := readName([]byte{0}, 0); err != nil || n != "." {
		t.Errorf("root: %q %v", n, err)
	}
	if _, _, err := readName([]byte{0xc0}, 0); err == nil {
		t.Error("cut pointer")
	}
}
