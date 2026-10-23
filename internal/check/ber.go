// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// BER (the subset SNMP uses): tags, lengths, integers, strings and object
// identifiers.

const (
	berInteger   = 0x02
	berOctets    = 0x04
	berNull      = 0x05
	berOID       = 0x06
	berSequence  = 0x30
	berIPAddress = 0x40
	berCounter32 = 0x41
	berGauge32   = 0x42
	berTimeTicks = 0x43
	berOpaque    = 0x44
	berCounter64 = 0x46
	berNoObject  = 0x80
	berNoInst    = 0x81
	berEndOfMIB  = 0x82
)

// berLen encodes a length.
func berLen(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)} //nolint:gosec // G115: below 0x80
	}
	var b []byte
	for ; n > 0; n >>= 8 {
		b = append([]byte{byte(n)}, b...)
	}
	return append([]byte{0x80 | byte(len(b))}, b...) //nolint:gosec // G115: at most 8 bytes
}

// tlv encodes one value.
func tlv(tag byte, content ...[]byte) []byte {
	var body []byte
	for _, c := range content {
		body = append(body, c...)
	}
	return append(append([]byte{tag}, berLen(len(body))...), body...)
}

// berInt encodes an integer.
func berInt(v int64) []byte {
	b := []byte{byte(v)} //nolint:gosec // G115: the low byte, on purpose
	for v > 127 || v < -128 {
		v >>= 8
		b = append([]byte{byte(v)}, b...) //nolint:gosec // G115: the low byte, on purpose
	}
	return tlv(berInteger, b)
}

func berString(s []byte) []byte { return tlv(berOctets, s) }

// parseOID reads a dotted object identifier, with or without a leading
// dot.
func parseOID(s string) ([]uint64, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "."), ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("%q is not an OID such as 1.3.6.1.2.1.1.3.0", s)
	}
	out := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an OID such as 1.3.6.1.2.1.1.3.0", s)
		}
		out[i] = n
	}
	if out[0] > 2 || (out[0] < 2 && out[1] > 39) {
		return nil, fmt.Errorf("%q is not a valid OID", s)
	}
	return out, nil
}

// berOIDValue encodes an object identifier.
func berOIDValue(oid []uint64) []byte {
	b := base128(nil, oid[0]*40+oid[1])
	for _, n := range oid[2:] {
		b = base128(b, n)
	}
	return tlv(berOID, b)
}

func base128(b []byte, n uint64) []byte {
	var tmp []byte
	tmp = append(tmp, byte(n&0x7f))
	for n >>= 7; n > 0; n >>= 7 {
		tmp = append([]byte{byte(n&0x7f) | 0x80}, tmp...)
	}
	return append(b, tmp...)
}

var errBER = errors.New("the answer is not valid BER")

// berRead reads one value: its tag, its content and what follows it.
func berRead(b []byte) (tag byte, content, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, errBER
	}
	tag, n, i := b[0], int(b[1]), 2
	if n&0x80 != 0 {
		k := n & 0x7f
		if k == 0 || k > 4 || len(b) < 2+k {
			return 0, nil, nil, errBER
		}
		n = 0
		for _, c := range b[2 : 2+k] {
			n = n<<8 | int(c)
		}
		i += k
	}
	if n < 0 || len(b)-i < n {
		return 0, nil, nil, errBER
	}
	return tag, b[i : i+n], b[i+n:], nil
}

// berExpect reads one value with the given tag.
func berExpect(b []byte, tag byte) (content, rest []byte, err error) {
	t, c, r, err := berRead(b)
	if err != nil {
		return nil, nil, err
	}
	if t != tag {
		return nil, nil, fmt.Errorf("%w: tag 0x%02x, not 0x%02x", errBER, t, tag)
	}
	return c, r, nil
}

// berIntValue reads an integer's content.
func berIntValue(c []byte) (int64, error) {
	if len(c) == 0 || len(c) > 8 {
		return 0, errBER
	}
	v := int64(int8(c[0])) //nolint:gosec // G115: the sign bit, on purpose
	for _, x := range c[1:] {
		v = v<<8 | int64(x)
	}
	return v, nil
}

// berReadInt reads an integer value.
func berReadInt(b []byte) (int64, []byte, error) {
	c, rest, err := berExpect(b, berInteger)
	if err != nil {
		return 0, nil, err
	}
	v, err := berIntValue(c)
	return v, rest, err
}

func oidString(c []byte) string {
	var parts []string
	var n uint64
	for _, x := range c {
		n = n<<7 | uint64(x&0x7f)
		if x&0x80 != 0 {
			continue
		}
		if len(parts) == 0 {
			first := min(n/40, 2)
			parts = append(parts, strconv.FormatUint(first, 10), strconv.FormatUint(n-first*40, 10))
		} else {
			parts = append(parts, strconv.FormatUint(n, 10))
		}
		n = 0
	}
	return strings.Join(parts, ".")
}

// snmpValue writes a varbind's value as text. ok is false for SNMP's
// "no such object" answers.
func snmpValue(tag byte, c []byte) (string, bool) {
	switch tag {
	case berInteger:
		v, _ := berIntValue(c)
		return strconv.FormatInt(v, 10), true
	case berOctets, berOpaque:
		if printable(c) {
			return string(c), true
		}
		hex := make([]string, len(c))
		for i, x := range c {
			hex[i] = fmt.Sprintf("%02x", x)
		}
		return strings.Join(hex, ":"), true
	case berNull:
		return "", true
	case berOID:
		return oidString(c), true
	case berIPAddress:
		if len(c) == 4 {
			return fmt.Sprintf("%d.%d.%d.%d", c[0], c[1], c[2], c[3]), true
		}
	case berCounter32, berGauge32, berTimeTicks, berCounter64:
		return new(big.Int).SetBytes(c).String(), true
	case berNoObject:
		return "no such object", false
	case berNoInst:
		return "no such instance", false
	case berEndOfMIB:
		return "end of MIB view", false
	}
	return fmt.Sprintf("a value of type 0x%02x", tag), true
}

func printable(b []byte) bool {
	for _, c := range b {
		if (c < 0x20 || c > 0x7e) && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
	}
	return true
}
