// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/binary"
	"errors"
)

// ICMP echo messages (RFC 792, RFC 4443), for unprivileged "ping sockets".
const (
	echoRequest4 = 8
	echoReply4   = 0
	echoRequest6 = 128
	echoReply6   = 129
)

// echo builds an echo request with size bytes of data. IPv4 needs the
// checksum; for IPv6 the kernel computes it.
func echo(v6 bool, id, seq, size int) []byte {
	b := make([]byte, 8+size)
	b[0] = echoRequest4
	if v6 {
		b[0] = echoRequest6
	}
	binary.BigEndian.PutUint16(b[4:], uint16(id))  //nolint:gosec // G115: wraps on purpose
	binary.BigEndian.PutUint16(b[6:], uint16(seq)) //nolint:gosec // G115: wraps on purpose
	for i := range size {
		b[8+i] = byte(i)
	}
	if !v6 {
		binary.BigEndian.PutUint16(b[2:], checksum(b))
	}
	return b
}

// checksum is the Internet checksum (RFC 1071).
func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum) //nolint:gosec // G115: the loop above folded it into 16 bits
}

var errNotReply = errors.New("not an echo reply")

// replySeq reads an echo reply and returns its sequence number. Some
// systems (macOS) hand IPv4 replies over with their IP header; it is
// skipped. The identifier is not checked: Linux replaces it with the
// socket's own.
func replySeq(b []byte, v6 bool) (int, error) {
	if !v6 && len(b) >= 20 && b[0]>>4 == 4 {
		b = b[int(b[0]&0x0f)*4:]
	}
	if len(b) < 8 {
		return 0, errNotReply
	}
	want := byte(echoReply4)
	if v6 {
		want = echoReply6
	}
	if b[0] != want || b[1] != 0 {
		return 0, errNotReply
	}
	return int(binary.BigEndian.Uint16(b[6:])), nil
}
