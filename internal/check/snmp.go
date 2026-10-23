// SPDX-License-Identifier: AGPL-3.0-or-later

package check

// SNMPv3 (RFC 3414) authenticates with HMAC-MD5 and HMAC-SHA-1 and encrypts
// with DES for devices that have nothing better: the protocol fixes them.
// nosemgrep
import (
	"bytes"
	"cmp"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des" //nolint:gosec // G502: SNMPv3's DES privacy, for devices that have nothing better
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // G501: SNMPv3's HMAC-MD5-96 authentication
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: SNMPv3's HMAC-SHA-96 authentication
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SNMP is the settings of the snmp type: an SNMP agent's value for an OID
// must compare as wanted with an expected value.
type SNMP struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Version string `json:"version"` // "1", "2c" (the default) or "3"
	// Community is for versions 1 and 2c; "public" by default.
	Community string `json:"community"`
	OID       string `json:"oid"`
	Operator  string `json:"operator"` // as JSON queries; "==" by default
	Expected  string `json:"expected"` // "": any value
	// Version 3.
	Username     string `json:"username"`
	AuthProtocol string `json:"auth_protocol"` // none, MD5, SHA, SHA-224, SHA-256, SHA-384, SHA-512
	AuthPassword string `json:"auth_password"`
	PrivProtocol string `json:"priv_protocol"` // none, DES, AES
	PrivPassword string `json:"priv_password"`
	ContextName  string `json:"context_name"`
}

var (
	snmpVersions  = []string{"1", "2c", "3"}
	snmpAuths     = []string{"none", "MD5", "SHA", "SHA-224", "SHA-256", "SHA-384", "SHA-512"}
	snmpPrivs     = []string{"none", "DES", "AES"}
	snmpOperators = []string{"==", "!=", "<", "<=", ">", ">=", "contains"}
)

func (s *SNMP) secrets() map[string]*string {
	return map[string]*string{"community": &s.Community, "auth_password": &s.AuthPassword, "priv_password": &s.PrivPassword}
}

func (s *SNMP) normalize() error {
	h, err := host(s.Host)
	if err != nil {
		return err
	}
	s.Host = h
	if s.Port == 0 {
		s.Port = 161
	}
	if s.Version == "" {
		s.Version = "2c"
	}
	if !slices.Contains(snmpVersions, s.Version) {
		return errors.New("the SNMP version must be 1, 2c or 3")
	}
	s.OID = strings.TrimPrefix(strings.TrimSpace(s.OID), ".")
	if _, err := parseOID(s.OID); err != nil {
		return err
	}
	if s.Operator == "" {
		s.Operator = "=="
	}
	if !slices.Contains(snmpOperators, s.Operator) {
		return fmt.Errorf("the comparison must be one of %s", strings.Join(snmpOperators, " "))
	}
	if s.Version != "3" {
		if s.Community == "" {
			s.Community = "public"
		}
		return checkPort(s.Port)
	}
	return s.normalizeV3()
}

func (s *SNMP) normalizeV3() error {
	s.Username = strings.TrimSpace(s.Username)
	if s.Username == "" {
		return errors.New("SNMP version 3 needs a user name")
	}
	if s.AuthProtocol == "" {
		s.AuthProtocol = "none"
	}
	if s.PrivProtocol == "" {
		s.PrivProtocol = "none"
	}
	if !slices.Contains(snmpAuths, s.AuthProtocol) {
		return fmt.Errorf("the authentication must be one of %s", strings.Join(snmpAuths, ", "))
	}
	if !slices.Contains(snmpPrivs, s.PrivProtocol) {
		return errors.New("the privacy must be none, DES or AES")
	}
	if s.AuthProtocol != "none" && s.AuthPassword == "" {
		return errors.New("the authentication password is required")
	}
	if s.PrivProtocol != "none" {
		if s.AuthProtocol == "none" {
			return errors.New("privacy needs authentication too")
		}
		if s.PrivPassword == "" {
			return errors.New("the privacy password is required")
		}
	}
	return checkPort(s.Port)
}

func (s *SNMP) checker(env *Env) Checker { return &snmpChecker{s: s, env: env} }

func (s *SNMP) describe() []Fact {
	f := []Fact{{"Agent", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}, {"SNMP version", s.Version}, {"OID", s.OID}}
	if s.Expected != "" {
		f = append(f, Fact{"Expected", s.Operator + " " + strconv.Quote(s.Expected)})
	}
	if s.Version == "3" {
		f = append(f, Fact{"User", s.Username}, Fact{"Authentication", s.AuthProtocol}, Fact{"Privacy", s.PrivProtocol})
	}
	return f
}

type snmpChecker struct {
	s   *SNMP
	env *Env
}

// PDU types.
const (
	pduGet      = 0xa0
	pduResponse = 0xa2
	pduReport   = 0xa8
)

var snmpErrors = map[int64]string{1: "tooBig", 2: "noSuchName", 3: "badValue", 4: "readOnly", 5: "genErr",
	6: "noAccess", 13: "resourceUnavailable", 16: "authorizationError"}

func randInt31() int64 {
	b := make([]byte, 4)
	rand.Read(b)
	return int64(binary.BigEndian.Uint32(b) & 0x7fffffff)
}

// getPDU is a GetRequest for oid, or for nothing when oid is nil.
func getPDU(reqID int64, oid []uint64) []byte {
	var binds []byte
	if oid != nil {
		binds = tlv(berSequence, berOIDValue(oid), tlv(berNull))
	}
	return tlv(pduGet, berInt(reqID), berInt(0), berInt(0), tlv(berSequence, binds))
}

// pduAnswer is what a response or report PDU holds.
type pduAnswer struct {
	tag      byte
	reqID    int64
	errState int64
	oid      string
	valTag   byte
	value    []byte
}

func parsePDU(b []byte) (pduAnswer, error) {
	var a pduAnswer
	tag, c, _, err := berRead(b)
	if err != nil {
		return a, err
	}
	a.tag = tag
	if a.reqID, c, err = berReadInt(c); err != nil {
		return a, err
	}
	if a.errState, c, err = berReadInt(c); err != nil {
		return a, err
	}
	if _, c, err = berReadInt(c); err != nil {
		return a, err
	}
	binds, _, err := berExpect(c, berSequence)
	if err != nil || len(binds) == 0 {
		return a, err
	}
	bind, _, err := berExpect(binds, berSequence)
	if err != nil {
		return a, err
	}
	oid, rest, err := berExpect(bind, berOID)
	if err != nil {
		return a, err
	}
	a.oid = oidString(oid)
	a.valTag, a.value, _, err = berRead(rest)
	return a, err
}

func (c *snmpChecker) Check(ctx context.Context) Result {
	conn, err := c.env.Dial(ctx, "udp", net.JoinHostPort(c.s.Host, strconv.Itoa(c.s.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	oid, _ := parseOID(c.s.OID)
	start := time.Now()
	var a pduAnswer
	if c.s.Version == "3" {
		a, err = c.v3(ctx, conn, oid)
	} else {
		a, err = c.community(ctx, conn, oid)
	}
	if err != nil {
		return failErr(err)
	}
	r := Result{Latency: time.Since(start)}
	if a.errState != 0 {
		r.Message = "The agent answered " + cmp.Or(snmpErrors[a.errState], "error "+strconv.FormatInt(a.errState, 10))
		return r
	}
	value, ok := snmpValue(a.valTag, a.value)
	if !ok {
		r.Message = fmt.Sprintf("%s: %s", c.s.OID, value)
		return r
	}
	r.Message = fmt.Sprintf("%s is %s", c.s.OID, value)
	r.OK = c.s.Expected == "" || compare(value, c.s.Operator, c.s.Expected)
	if !r.OK {
		r.Message += fmt.Sprintf(", not %s %s", c.s.Operator, c.s.Expected)
	}
	return r
}

// udpExchange sends a request and reads answers until accept takes one.
func udpExchange(ctx context.Context, conn net.Conn, req []byte, accept func([]byte) bool) error {
	if _, err := conn.Write(req); err != nil {
		return connError(ctx, err)
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return connError(ctx, err)
		}
		if accept(buf[:n]) {
			return nil
		}
	}
}

// community asks with SNMP version 1 or 2c.
func (c *snmpChecker) community(ctx context.Context, conn net.Conn, oid []uint64) (pduAnswer, error) {
	ver := int64(1)
	if c.s.Version == "1" {
		ver = 0
	}
	reqID := randInt31()
	req := tlv(berSequence, berInt(ver), berString([]byte(c.s.Community)), getPDU(reqID, oid))
	var a pduAnswer
	err := udpExchange(ctx, conn, req, func(b []byte) bool {
		msg, _, err := berExpect(b, berSequence)
		if err != nil {
			return false
		}
		if _, msg, err = berReadInt(msg); err != nil {
			return false
		}
		if _, msg, err = berExpect(msg, berOctets); err != nil {
			return false
		}
		got, err := parsePDU(msg)
		if err != nil || got.reqID != reqID || got.tag != pduResponse {
			return false
		}
		a = got
		return true
	})
	if err != nil && errors.Is(err, errTimeout) {
		return a, errors.New("no answer: is the community right?")
	}
	return a, err
}

// SNMPv3 (RFC 3414 and 7860): the user-based security model.

type authHash struct {
	newHash func() hash.Hash
	macLen  int
}

var authHashes = map[string]authHash{
	"MD5": {md5.New, 12}, "SHA": {sha1.New, 12}, "SHA-224": {sha256.New224, 16},
	"SHA-256": {sha256.New, 24}, "SHA-384": {sha512.New384, 32}, "SHA-512": {sha512.New, 48},
}

// localKey turns a password into a key for one engine (RFC 3414 A.2).
func localKey(h authHash, password string, engineID []byte) []byte {
	d := h.newHash()
	pw := []byte(password)
	chunk := make([]byte, 64)
	for i := 0; i < 1048576; i += 64 {
		for j := range chunk {
			chunk[j] = pw[(i+j)%len(pw)]
		}
		d.Write(chunk)
	}
	ku := d.Sum(nil)
	d = h.newHash()
	d.Write(ku)
	d.Write(engineID)
	d.Write(ku)
	return d.Sum(nil)
}

// usm is the security parameters of an SNMPv3 message.
type usm struct {
	engineID    []byte
	boots, time int64
	user        []byte
	authParams  []byte
	privParams  []byte
}

// v3msg is a parsed SNMPv3 message.
type v3msg struct {
	msgID   int64
	flags   byte
	usm     usm
	authOff int    // where authParams is in the message
	scoped  []byte // the scoped PDU, or its encryption
}

func parseV3(b []byte) (v3msg, error) {
	var m v3msg
	body, _, err := berExpect(b, berSequence)
	if err != nil {
		return m, err
	}
	ver, body, err := berReadInt(body)
	if err != nil || ver != 3 {
		return m, errBER
	}
	global, body, err := berExpect(body, berSequence)
	if err != nil {
		return m, err
	}
	if m.msgID, global, err = berReadInt(global); err != nil {
		return m, err
	}
	if _, global, err = berReadInt(global); err != nil {
		return m, err
	}
	flags, _, err := berExpect(global, berOctets)
	if err != nil || len(flags) != 1 {
		return m, errBER
	}
	m.flags = flags[0]
	sec, scoped, err := berExpect(body, berOctets)
	if err != nil {
		return m, err
	}
	u, _, err := berExpect(sec, berSequence)
	if err != nil {
		return m, err
	}
	if m.usm.engineID, u, err = berExpect(u, berOctets); err != nil {
		return m, err
	}
	if m.usm.boots, u, err = berReadInt(u); err != nil {
		return m, err
	}
	if m.usm.time, u, err = berReadInt(u); err != nil {
		return m, err
	}
	if m.usm.user, u, err = berExpect(u, berOctets); err != nil {
		return m, err
	}
	if m.usm.authParams, u, err = berExpect(u, berOctets); err != nil {
		return m, err
	}
	m.authOff = cap(b) - cap(m.usm.authParams)
	if m.usm.privParams, _, err = berExpect(u, berOctets); err != nil {
		return m, err
	}
	m.scoped = scoped
	return m, nil
}

// buildV3 makes an SNMPv3 message, and says where its authParams are.
func buildV3(msgID int64, flags byte, u usm, scoped []byte) ([]byte, int) {
	ver := berInt(3)
	global := tlv(berSequence, berInt(msgID), berInt(65507), berString([]byte{flags}), berInt(3))
	parts := [][]byte{berString(u.engineID), berInt(u.boots), berInt(u.time), berString(u.user), berString(u.authParams),
		berString(u.privParams)}
	sec := tlv(berSequence, parts...)
	secOct := berString(sec)
	msg := tlv(berSequence, ver, global, secOct, scoped)
	off := len(msg) - len(scoped) - len(secOct) // where secOct starts
	off += len(secOct) - len(sec)               // its header
	inner := 0
	for _, p := range parts {
		inner += len(p)
	}
	off += len(sec) - inner // the sequence's header
	for _, p := range parts[:4] {
		off += len(p)
	}
	off += len(parts[4]) - len(u.authParams) // authParams' header
	return msg, off
}

// sign puts a message's MAC at off.
func sign(h authHash, key, msg []byte, off int) {
	mac := hmac.New(h.newHash, key)
	mac.Write(msg)
	copy(msg[off:off+h.macLen], mac.Sum(nil))
}

// signedOK checks a received message's MAC.
func signedOK(h authHash, key, msg []byte, off int) bool {
	if off+h.macLen > len(msg) {
		return false
	}
	cp := bytes.Clone(msg)
	got := bytes.Clone(cp[off : off+h.macLen])
	clear(cp[off : off+h.macLen])
	mac := hmac.New(h.newHash, key)
	mac.Write(cp)
	return hmac.Equal(mac.Sum(nil)[:h.macLen], got)
}

// privacy encrypts and decrypts scoped PDUs.
type privacy struct {
	proto string
	key   []byte
}

func (p privacy) encrypt(plain []byte, boots, t int64) (out, salt []byte, err error) {
	salt = make([]byte, 8)
	rand.Read(salt)
	switch p.proto {
	case "DES":
		binary.BigEndian.PutUint32(salt, uint32(boots)) //nolint:gosec // G115: engine boots fit 31 bits
		block, err := des.NewCipher(p.key[:8])          //nolint:gosec // G405: RFC 3414 section 8
		if err != nil {
			return nil, nil, err
		}
		iv := make([]byte, 8)
		for i := range iv {
			iv[i] = p.key[8+i] ^ salt[i]
		}
		plain = append(bytes.Clone(plain), make([]byte, (8-len(plain)%8)%8)...)
		out = make([]byte, len(plain))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
		return out, salt, nil
	default:
		out, err := aesCFB(p.key[:16], aesIV(boots, t, salt), plain, true)
		return out, salt, err
	}
}

func (p privacy) decrypt(data, salt []byte, boots, t int64) ([]byte, error) {
	if len(salt) != 8 {
		return nil, errors.New("the answer's privacy parameters are not valid")
	}
	if p.proto == "DES" {
		if len(data)%8 != 0 {
			return nil, errors.New("the answer is not valid DES")
		}
		block, err := des.NewCipher(p.key[:8]) //nolint:gosec // G405: RFC 3414 section 8
		if err != nil {
			return nil, err
		}
		iv := make([]byte, 8)
		for i := range iv {
			iv[i] = p.key[8+i] ^ salt[i]
		}
		out := make([]byte, len(data))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
		return out, nil
	}
	return aesCFB(p.key[:16], aesIV(boots, t, salt), data, false)
}

func aesIV(boots, t int64, salt []byte) []byte {
	iv := binary.BigEndian.AppendUint32(nil, uint32(boots)) //nolint:gosec // G115: engine boots fit 31 bits
	iv = binary.BigEndian.AppendUint32(iv, uint32(t))       //nolint:gosec // G115: engine time fits 31 bits
	return append(iv, salt...)
}

// aesCFB is AES in 128-bit cipher feedback mode (RFC 3826).
func aesCFB(key, iv, in []byte, encrypt bool) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(in))
	reg := bytes.Clone(iv)
	stream := make([]byte, aes.BlockSize)
	for i := 0; i < len(in); i += aes.BlockSize {
		block.Encrypt(stream, reg)
		end := min(i+aes.BlockSize, len(in))
		for j := i; j < end; j++ {
			out[j] = in[j] ^ stream[j-i]
		}
		if encrypt {
			copy(reg, out[i:end])
		} else {
			copy(reg, in[i:end])
		}
	}
	return out, nil
}

// usmReports are what an agent's reports mean.
var usmReports = map[string]string{
	"1.3.6.1.6.3.15.1.1.1.0": "the agent does not support this security level",
	"1.3.6.1.6.3.15.1.1.2.0": "the agent's clock is outside the time window",
	"1.3.6.1.6.3.15.1.1.3.0": "the agent does not know this user",
	"1.3.6.1.6.3.15.1.1.4.0": "the agent does not know its own engine ID",
	"1.3.6.1.6.3.15.1.1.5.0": "wrong authentication password or protocol",
	"1.3.6.1.6.3.15.1.1.6.0": "wrong privacy password or protocol",
}

// v3 asks with SNMPv3: first to discover the agent's engine, then for the
// OID.
func (c *snmpChecker) v3(ctx context.Context, conn net.Conn, oid []uint64) (pduAnswer, error) {
	s := c.s
	if (s.AuthProtocol != "none" && len(s.AuthPassword) < 8) || (s.PrivProtocol != "none" && len(s.PrivPassword) < 8) {
		return pduAnswer{}, errors.New("SNMPv3 passwords are at least 8 characters")
	}
	found, err := c.send(ctx, conn, usm{}, nil, nil)
	if err != nil {
		return pduAnswer{}, err
	}
	u := found.usm
	if len(u.engineID) == 0 {
		return pduAnswer{}, errors.New("the agent did not say its engine ID")
	}
	for try := 0; ; try++ {
		got, err := c.send(ctx, conn, usm{engineID: u.engineID, boots: u.boots, time: u.time, user: []byte(s.Username)}, oid, &u)
		if err != nil {
			return got.pdu, err
		}
		if got.pdu.tag != pduReport {
			return got.pdu, nil
		}
		why := cmp.Or(usmReports[got.pdu.oid], "the agent reported "+got.pdu.oid)
		if got.pdu.oid != "1.3.6.1.6.3.15.1.1.2.0" || try > 0 {
			return got.pdu, errors.New(why)
		}
		u.boots, u.time = got.usm.boots, got.usm.time
	}
}

// v3answer is an answer to an SNMPv3 request.
type v3answer struct {
	usm usm
	pdu pduAnswer
}

// send sends one SNMPv3 GetRequest. Without oid it is the discovery
// request: no security, no variables.
func (c *snmpChecker) send(ctx context.Context, conn net.Conn, u usm, oid []uint64, keyFor *usm) (v3answer, error) {
	s := c.s
	msgID, reqID := randInt31(), randInt31()
	flags := byte(0x04) // reportable
	var sec v3security
	scoped := tlv(berSequence, berString(u.engineID), berString([]byte(s.ContextName)), getPDU(reqID, oid))
	if oid != nil && s.AuthProtocol != "none" {
		sec.hash = authHashes[s.AuthProtocol]
		sec.authKey = localKey(sec.hash, s.AuthPassword, keyFor.engineID)
		flags |= 0x01
		u.authParams = make([]byte, sec.hash.macLen)
		if s.PrivProtocol != "none" {
			flags |= 0x02
			sec.priv = privacy{s.PrivProtocol, localKey(sec.hash, s.PrivPassword, keyFor.engineID)}
			enc, salt, err := sec.priv.encrypt(scoped, u.boots, u.time)
			if err != nil {
				return v3answer{}, err
			}
			scoped, u.privParams = berString(enc), salt
		}
	}
	msg, off := buildV3(msgID, flags, u, scoped)
	if sec.authKey != nil {
		sign(sec.hash, sec.authKey, msg, off)
	}
	var out v3answer
	var why error
	err := udpExchange(ctx, conn, msg, func(b []byte) bool {
		m, err := parseV3(b)
		if err != nil || m.msgID != msgID {
			return false
		}
		out.usm = m.usm
		out.pdu, why = sec.read(b, m)
		return true
	})
	if err == nil {
		err = why
	}
	if err != nil && errors.Is(err, errTimeout) && oid == nil {
		err = errors.New("no answer to SNMPv3 discovery")
	}
	return out, err
}

// v3security is how a request was secured, to read its answer.
type v3security struct {
	hash    authHash
	authKey []byte // nil: not authenticated
	priv    privacy
}

// read checks and decrypts an answer, and reads its PDU.
func (sec v3security) read(b []byte, m v3msg) (pduAnswer, error) {
	data := m.scoped
	if m.flags&0x01 != 0 && sec.authKey != nil && !signedOK(sec.hash, sec.authKey, b, m.authOff) {
		return pduAnswer{}, errors.New("the answer's authentication is wrong: are the passwords right?")
	}
	if m.flags&0x02 != 0 && sec.priv.key != nil {
		enc, _, err := berExpect(data, berOctets)
		if err == nil {
			data, err = sec.priv.decrypt(enc, m.usm.privParams, m.usm.boots, m.usm.time)
		}
		if err != nil {
			return pduAnswer{}, err
		}
	}
	sp, _, err := berExpect(data, berSequence)
	if err == nil {
		_, sp, err = berExpect(sp, berOctets)
	}
	if err == nil {
		_, sp, err = berExpect(sp, berOctets)
	}
	var a pduAnswer
	if err == nil {
		a, err = parsePDU(sp)
	}
	if err != nil {
		return a, errors.New("the answer cannot be read: are the privacy settings right?")
	}
	return a, nil
}
