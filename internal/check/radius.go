// SPDX-License-Identifier: AGPL-3.0-or-later

package check

// RADIUS (RFC 2865) is built on MD5: the protocol fixes it.
// nosemgrep
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // G501: RADIUS (RFC 2865) is built on MD5
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Radius is the settings of the radius type: a RADIUS server must accept
// a user's password (PAP).
type Radius struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Secret           string `json:"secret"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	NASIdentifier    string `json:"nas_identifier"`
	CalledStationID  string `json:"called_station_id"`
	CallingStationID string `json:"calling_station_id"`
}

func (r *Radius) secrets() map[string]*string {
	return map[string]*string{"secret": &r.Secret, "password": &r.Password}
}

func (r *Radius) normalize() error {
	h, err := host(r.Host)
	if err != nil {
		return err
	}
	r.Host = h
	if r.Port == 0 {
		r.Port = 1812
	}
	if r.Secret == "" {
		return errors.New("the shared secret is required")
	}
	r.Username = strings.TrimSpace(r.Username)
	if r.Username == "" {
		return errors.New("the user name is required")
	}
	if r.NASIdentifier == "" {
		r.NASIdentifier = "eyrie"
	}
	for _, v := range []string{r.Username, r.NASIdentifier, r.CalledStationID, r.CallingStationID} {
		if len(v) > 253 {
			return errors.New("RADIUS values are at most 253 bytes")
		}
	}
	return checkPort(r.Port)
}

func (r *Radius) checker(env *Env) Checker { return &radiusChecker{r: r, env: env} }

func (r *Radius) describe() []Fact {
	return []Fact{{"Server", net.JoinHostPort(r.Host, strconv.Itoa(r.Port))}, {"User name", r.Username},
		{"NAS identifier", r.NASIdentifier}}
}

type radiusChecker struct {
	r   *Radius
	env *Env
}

// RADIUS codes and attributes.
const (
	radAccessRequest   = 1
	radAccessAccept    = 2
	radAccessReject    = 3
	radAccessChallenge = 11

	radUserName         = 1
	radUserPassword     = 2
	radReplyMessage     = 18
	radCalledStationID  = 30
	radCallingStationID = 31
	radNASIdentifier    = 32
	radMessageAuth      = 80
)

// radiusAttr appends one attribute.
func radiusAttr(b []byte, typ byte, v []byte) []byte {
	return append(append(b, typ, byte(len(v)+2)), v...) //nolint:gosec // G115: values are at most 253 bytes
}

// papPassword hides a password as RFC 2865 section 5.2 says.
func papPassword(password, secret string, auth []byte) []byte {
	p := []byte(password)
	if len(p) == 0 || len(p)%16 != 0 {
		p = append(p, make([]byte, 16-len(p)%16)...)
	}
	out := make([]byte, 0, len(p))
	prev := auth
	for i := 0; i < len(p); i += 16 {
		h := md5.New() //nolint:gosec // G401: RFC 2865
		h.Write([]byte(secret))
		h.Write(prev)
		sum := h.Sum(nil)
		for j := range 16 {
			sum[j] ^= p[i+j]
		}
		out = append(out, sum...)
		prev = sum
	}
	return out
}

// request builds an Access-Request with a Message-Authenticator, first, as
// servers protected against BlastRADIUS ask.
func (c *radiusChecker) request(id byte, auth []byte) []byte {
	r := c.r
	pkt := []byte{radAccessRequest, id, 0, 0}
	pkt = append(pkt, auth...)
	pkt = radiusAttr(pkt, radMessageAuth, make([]byte, 16))
	pkt = radiusAttr(pkt, radUserName, []byte(r.Username))
	pkt = radiusAttr(pkt, radUserPassword, papPassword(r.Password, r.Secret, auth))
	pkt = radiusAttr(pkt, radNASIdentifier, []byte(r.NASIdentifier))
	if r.CalledStationID != "" {
		pkt = radiusAttr(pkt, radCalledStationID, []byte(r.CalledStationID))
	}
	if r.CallingStationID != "" {
		pkt = radiusAttr(pkt, radCallingStationID, []byte(r.CallingStationID))
	}
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt))) //nolint:gosec // G115: at most about 1 KiB
	mac := hmac.New(md5.New, []byte(r.Secret))
	mac.Write(pkt)
	copy(pkt[22:38], mac.Sum(nil))
	return pkt
}

func (c *radiusChecker) Check(ctx context.Context) Result {
	if len(c.r.Password) > 128 {
		return fail("The password is longer than RADIUS allows (128 bytes)")
	}
	conn, err := c.env.Dial(ctx, "udp", net.JoinHostPort(c.r.Host, strconv.Itoa(c.r.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	auth := make([]byte, 16)
	rand.Read(auth)
	id := auth[0]
	start := time.Now()
	if _, err := conn.Write(c.request(id, auth)); err != nil {
		return failErr(connError(ctx, err))
	}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return failErr(connError(ctx, err))
		}
		if n < 20 || buf[1] != id {
			continue
		}
		r := c.answer(buf[:n], auth)
		r.Latency = time.Since(start)
		return r
	}
}

// answer reads the server's answer to the request with authenticator
// auth.
func (c *radiusChecker) answer(pkt, auth []byte) Result {
	length := int(binary.BigEndian.Uint16(pkt[2:]))
	if length < 20 || length > len(pkt) {
		return Result{Message: "The answer is not a RADIUS packet"}
	}
	pkt = pkt[:length]
	h := md5.New() //nolint:gosec // G401: RFC 2865
	h.Write(pkt[:4])
	h.Write(auth)
	h.Write(pkt[20:])
	h.Write([]byte(c.r.Secret))
	if !hmac.Equal(h.Sum(nil), pkt[4:20]) {
		return Result{Message: "The answer's authenticator is wrong: is the shared secret right?"}
	}
	var reply []string
	for a := pkt[20:]; len(a) >= 2; {
		n := int(a[1])
		if n < 2 || n > len(a) {
			return Result{Message: "The answer's attributes are not valid"}
		}
		switch a[0] {
		case radReplyMessage:
			reply = append(reply, string(a[2:n]))
		case radMessageAuth:
			if !c.messageAuthOK(pkt, auth, len(pkt)-len(a)) {
				return Result{Message: "The answer's Message-Authenticator is wrong"}
			}
		}
		a = a[n:]
	}
	msg := ""
	if len(reply) > 0 {
		msg = ": " + strings.Join(reply, " ")
	}
	switch pkt[0] {
	case radAccessAccept:
		return Result{OK: true, Message: "Access-Accept" + msg}
	case radAccessReject:
		return Result{Message: "Access-Reject" + msg}
	case radAccessChallenge:
		return Result{Message: "Access-Challenge: the server asks for more than a password" + msg}
	}
	return Result{Message: fmt.Sprintf("Unexpected RADIUS code %d", pkt[0])}
}

// messageAuthOK checks the Message-Authenticator attribute at off.
func (c *radiusChecker) messageAuthOK(pkt, auth []byte, off int) bool {
	if pkt[off+1] != 18 {
		return false
	}
	cp := bytes.Clone(pkt)
	copy(cp[4:20], auth)
	got := bytes.Clone(cp[off+2 : off+18])
	clear(cp[off+2 : off+18])
	mac := hmac.New(md5.New, []byte(c.r.Secret))
	mac.Write(cp)
	return hmac.Equal(mac.Sum(nil), got)
}
