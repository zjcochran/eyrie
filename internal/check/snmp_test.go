// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/hex"
	"net"
	"strings"
	"testing"
)

// fakeAgent is an SNMP agent with one value per OID.
type fakeAgent struct {
	community string
	values    map[string][]byte // OID → encoded value
	errStatus int64
	// SNMPv3
	user, authProto, authPass, privProto, privPass string
	engineID                                       []byte
	staleOnce                                      bool // report notInTimeWindow once
	badSignature                                   bool // sign answers with the wrong key
	report                                         string
}

func (a *fakeAgent) value(oid string) []byte {
	if v, ok := a.values[oid]; ok {
		return v
	}
	return tlv(berNoInst)
}

func (a *fakeAgent) response(reqID int64, oid string) []byte {
	o, _ := parseOID(oid)
	return tlv(pduResponse, berInt(reqID), berInt(a.errStatus), berInt(0), tlv(berSequence, tlv(berSequence, berOIDValue(o), a.value(oid))))
}

func (a *fakeAgent) serve(c net.Conn) {
	buf := make([]byte, 65535)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		b := buf[:n]
		if m, err := parseV3(b); err == nil {
			if out := a.v3(b, m); out != nil {
				c.Write(out)
			}
			continue
		}
		body, _, _ := berExpect(b, berSequence)
		ver, body, _ := berReadInt(body)
		comm, body, _ := berExpect(body, berOctets)
		p, err := parsePDU(body)
		if err != nil || string(comm) != a.community {
			continue // agents drop wrong communities
		}
		// An answer to another request first.
		c.Write(tlv(berSequence, berInt(ver), berString(comm), a.response(p.reqID+1, p.oid)))
		c.Write(tlv(berSequence, berInt(ver), berString(comm), a.response(p.reqID, p.oid)))
	}
}

func (a *fakeAgent) v3(b []byte, m v3msg) []byte {
	u := usm{engineID: a.engineID, boots: 5, time: 100, user: []byte(a.user)}
	if len(m.usm.engineID) == 0 {
		o, _ := parseOID("1.3.6.1.6.3.15.1.1.4.0")
		pdu := tlv(pduReport, berInt(0), berInt(0), berInt(0), tlv(berSequence, tlv(berSequence, berOIDValue(o), tlv(berCounter32, []byte{1}))))
		msg, _ := buildV3(m.msgID, 0, u, tlv(berSequence, berString(a.engineID), berString(nil), pdu))
		return msg
	}
	var sec v3security
	if a.authProto != "none" {
		sec.hash = authHashes[a.authProto]
		sec.authKey = localKey(sec.hash, a.authPass, a.engineID)
		if a.privProto != "none" {
			sec.priv = privacy{a.privProto, localKey(sec.hash, a.privPass, a.engineID)}
		}
	}
	req, err := sec.read(b, m)
	report := a.report
	switch {
	case err != nil && strings.Contains(err.Error(), "authentication"):
		report = "1.3.6.1.6.3.15.1.1.5.0"
	case err != nil:
		report = "1.3.6.1.6.3.15.1.1.6.0"
	case a.staleOnce:
		a.staleOnce = false
		report = "1.3.6.1.6.3.15.1.1.2.0"
		u.boots, u.time = 6, 1
	}
	var pdu []byte
	if report != "" {
		o, _ := parseOID(report)
		pdu = tlv(pduReport, berInt(req.reqID), berInt(0), berInt(0), tlv(berSequence, tlv(berSequence, berOIDValue(o), tlv(berCounter32, []byte{1}))))
		msg, _ := buildV3(m.msgID, 0, u, tlv(berSequence, berString(a.engineID), berString(nil), pdu))
		return msg
	}
	if m.usm.boots != 5 && m.usm.boots != 6 {
		return nil
	}
	scoped := tlv(berSequence, berString(a.engineID), berString(nil), a.response(req.reqID, req.oid))
	flags := byte(0)
	if sec.authKey != nil {
		flags |= 0x01
		u.authParams = make([]byte, sec.hash.macLen)
	}
	if sec.priv.key != nil {
		flags |= 0x02
		enc, salt, _ := sec.priv.encrypt(scoped, u.boots, u.time)
		scoped, u.privParams = berString(enc), salt
	}
	msg, off := buildV3(m.msgID, flags, u, scoped)
	if sec.authKey != nil {
		key := sec.authKey
		if a.badSignature {
			key = localKey(sec.hash, "wrongpass", a.engineID)
		}
		sign(sec.hash, key, msg, off)
	}
	return msg
}

func newAgent() *fakeAgent {
	return &fakeAgent{community: "public", engineID: []byte("\x80\x00\x1f\x88\x04agent"), values: map[string][]byte{
		"1.3.6.1.2.1.1.3.0":     tlv(berTimeTicks, []byte{0x01, 0x00}),
		"1.3.6.1.2.1.1.5.0":     berString([]byte("router")),
		"1.3.6.1.2.1.2.2.1.6.1": berString([]byte{0x02, 0, 0, 0, 0, 1}),
		"1.3.6.1.2.1.1.2.0":     berOIDValue([]uint64{1, 3, 6, 1, 4, 1, 64496}),
		"1.3.6.1.2.1.4.20.1.1":  tlv(berIPAddress, []byte{192, 0, 2, 1}),
		"1.3.6.1.2.1.1.7.0":     berInt(-72),
		"1.3.6.1.2.1.1.8.0":     tlv(berNull),
		"1.3.6.1.2.1.1.9.0":     tlv(0x47, []byte{1}),
	}}
}

func TestSNMPCommunity(t *testing.T) {
	env := testEnv()
	a := newAgent()
	var dialed string
	env.Dial = pipeDial(a.serve, &dialed)
	for _, c := range []struct {
		settings string
		ok       bool
		msg      string
	}{
		{`{"oid":"1.3.6.1.2.1.1.3.0","operator":">","expected":"200"}`, true, "1.3.6.1.2.1.1.3.0 is 256"},
		{`{"oid":".1.3.6.1.2.1.1.5.0","expected":"router","version":"1"}`, true, "1.3.6.1.2.1.1.5.0 is router"},
		{`{"oid":"1.3.6.1.2.1.1.5.0","expected":"switch"}`, false, "1.3.6.1.2.1.1.5.0 is router, not == switch"},
		{`{"oid":"1.3.6.1.2.1.2.2.1.6.1"}`, true, "1.3.6.1.2.1.2.2.1.6.1 is 02:00:00:00:00:01"},
		{`{"oid":"1.3.6.1.2.1.1.2.0","operator":"contains","expected":"64496"}`, true, "1.3.6.1.2.1.1.2.0 is 1.3.6.1.4.1.64496"},
		{`{"oid":"1.3.6.1.2.1.4.20.1.1"}`, true, "1.3.6.1.2.1.4.20.1.1 is 192.0.2.1"},
		{`{"oid":"1.3.6.1.2.1.1.7.0","operator":"<","expected":"0"}`, true, "1.3.6.1.2.1.1.7.0 is -72"},
		{`{"oid":"1.3.6.1.2.1.1.8.0"}`, true, "1.3.6.1.2.1.1.8.0 is "},
		{`{"oid":"1.3.6.1.2.1.1.9.0"}`, true, "1.3.6.1.2.1.1.9.0 is a value of type 0x47"},
		{`{"oid":"1.3.6.1.2.1.99.0"}`, false, "1.3.6.1.2.1.99.0: no such instance"},
	} {
		r := run(t, env, "snmp", `{"host":"router.example.com",`+strings.TrimPrefix(c.settings, "{"))
		if r.OK != c.ok || r.Message != c.msg {
			t.Errorf("%s: %+v", c.settings, r)
		}
	}
	if dialed != "udp router.example.com:161" {
		t.Errorf("dialed %s", dialed)
	}
	a.errStatus = 2
	if r := run(t, env, "snmp", `{"host":"router.example.com","oid":"1.3.6.1.2.1.1.5.0"}`); r.OK || r.Message != "The agent answered noSuchName" {
		t.Errorf("error status: %+v", r)
	}
	a.errStatus = 99
	if r := run(t, env, "snmp", `{"host":"router.example.com","oid":"1.3.6.1.2.1.1.5.0"}`); r.OK || r.Message != "The agent answered error 99" {
		t.Errorf("error status: %+v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	_, c, _ := Parse(env, "snmp", []byte(`{"host":"router.example.com","oid":"1.3.6.1.2.1.1.5.0","community":"secret"}`))
	if r := c.Check(ctx); r.OK || r.Message != "No answer: is the community right?" {
		t.Errorf("wrong community: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "snmp", `{"host":"router.example.com","oid":"1.3.6.1.2.1.1.5.0"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
}

func TestSNMPv3(t *testing.T) {
	for _, c := range []struct{ auth, priv string }{{"none", "none"}, {"MD5", "none"}, {"SHA", "DES"}, {"SHA-256", "AES"},
		{"SHA-512", "AES"}, {"SHA-224", "none"}, {"SHA-384", "DES"}} {
		env := testEnv()
		a := newAgent()
		a.user, a.authProto, a.authPass, a.privProto, a.privPass = "alice", c.auth, "authpass1", c.priv, "privpass1"
		a.staleOnce = c.auth != "none"
		env.Dial = pipeDial(a.serve, nil)
		s := `{"host":"router.example.com","version":"3","oid":"1.3.6.1.2.1.1.5.0","expected":"router","username":"alice",` +
			`"auth_protocol":"` + c.auth + `","auth_password":"authpass1","priv_protocol":"` + c.priv + `","priv_password":"privpass1"}`
		if r := run(t, env, "snmp", s); !r.OK || r.Message != "1.3.6.1.2.1.1.5.0 is router" {
			t.Errorf("%s/%s: %+v", c.auth, c.priv, r)
		}
		if c.auth == "none" {
			continue
		}
		a.badSignature = true
		if r := run(t, env, "snmp", s); r.OK || r.Message != "The answer's authentication is wrong: are the passwords right?" {
			t.Errorf("%s/%s wrong signature: %+v", c.auth, c.priv, r)
		}
		a.authPass = "otherpass"
		if r := run(t, env, "snmp", s); r.OK || r.Message != "Wrong authentication password or protocol" {
			t.Errorf("%s/%s wrong password: %+v", c.auth, c.priv, r)
		}
	}
	env := testEnv()
	a := newAgent()
	a.user, a.authProto, a.privProto, a.report = "alice", "none", "none", "1.3.6.1.6.3.15.1.1.3.0"
	env.Dial = pipeDial(a.serve, nil)
	base := `{"host":"router.example.com","version":"3","oid":"1.3.6.1.2.1.1.5.0","username":"alice"`
	if r := run(t, env, "snmp", base+`}`); r.OK || r.Message != "The agent does not know this user" {
		t.Errorf("unknown user: %+v", r)
	}
	a.report = "1.3.6.1.9"
	if r := run(t, env, "snmp", base+`}`); r.OK || r.Message != "The agent reported 1.3.6.1.9" {
		t.Errorf("other report: %+v", r)
	}
	if r := run(t, env, "snmp", base+`,"auth_protocol":"MD5","auth_password":"short"}`); r.OK || !strings.Contains(r.Message, "8 characters") {
		t.Errorf("short password: %+v", r)
	}
	a.engineID = nil
	if r := run(t, env, "snmp", base+`}`); r.OK || r.Message != "The agent did not say its engine ID" {
		t.Errorf("no engine: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 1000)) }, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	_, c, _ := Parse(env, "snmp", []byte(base+`}`))
	if r := c.Check(ctx); r.OK || r.Message != "No answer to SNMPv3 discovery" {
		t.Errorf("silent: %+v", r)
	}
}

func TestSNMPInvalid(t *testing.T) {
	for _, s := range []string{
		`{"host":"","oid":"1.3.6"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"4"}`,
		`{"host":"r.example.com","oid":"x"}`,
		`{"host":"r.example.com","oid":"1"}`,
		`{"host":"r.example.com","oid":"3.1.2"}`,
		`{"host":"r.example.com","oid":"1.3.6","operator":"~"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"3"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"3","username":"a","auth_protocol":"SHA-1"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"3","username":"a","priv_protocol":"3DES"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"3","username":"a","auth_protocol":"MD5"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"3","username":"a","priv_protocol":"AES","priv_password":"x"}`,
		`{"host":"r.example.com","oid":"1.3.6","version":"3","username":"a","auth_protocol":"MD5","auth_password":"x","priv_protocol":"AES"}`,
	} {
		if _, _, err := Parse(testEnv(), "snmp", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	s := &SNMP{Host: "r.example.com", Port: 161, Version: "3", OID: "1.3.6", Operator: "==", Expected: "x", Username: "alice",
		AuthProtocol: "SHA", PrivProtocol: "AES"}
	if f := s.describe(); len(f) != 7 {
		t.Errorf("describe %v", f)
	}
}

func TestBER(t *testing.T) {
	if b := berLen(300); len(b) != 3 || b[0] != 0x82 {
		t.Errorf("length %x", b)
	}
	for _, v := range []int64{0, 127, 128, -1, -129, 1 << 40} {
		c, _, err := berExpect(berInt(v), berInteger)
		if got, _ := berIntValue(c); err != nil || got != v {
			t.Errorf("int %d: %d", v, got)
		}
	}
	long := berString(make([]byte, 300))
	if c, rest, err := berExpect(long, berOctets); err != nil || len(c) != 300 || len(rest) != 0 {
		t.Error("long string")
	}
}

func TestBERErrors(t *testing.T) {
	for _, b := range [][]byte{{4}, {4, 0x85, 1, 1, 1, 1, 1}, {4, 0x81}, {4, 5, 1}} {
		if _, _, _, err := berRead(b); err == nil {
			t.Errorf("%x read", b)
		}
	}
	if _, _, err := berExpect(berInt(1), berOctets); err == nil {
		t.Error("wrong tag")
	}
	if _, err := berIntValue(nil); err == nil {
		t.Error("empty int")
	}
	if oidString(berOIDValue([]uint64{2, 999, 1})[2:]) != "2.999.1" {
		t.Error("OID 2.999")
	}
	if v, ok := snmpValue(berEndOfMIB, nil); ok || v != "end of MIB view" {
		t.Error("end of MIB")
	}
	if v, ok := snmpValue(berNoObject, nil); ok || v != "no such object" {
		t.Error("no object")
	}
	if _, err := parsePDU([]byte{0xa2, 1, 0}); err == nil {
		t.Error("bad PDU")
	}
	if _, err := parseV3(tlv(berSequence, berInt(2))); err == nil {
		t.Error("v2 as v3")
	}
	if signedOK(authHashes["MD5"], []byte("k"), []byte{1}, 5) {
		t.Error("short message signed")
	}
	p := privacy{"DES", make([]byte, 16)}
	if _, err := p.decrypt([]byte{1, 2, 3}, make([]byte, 8), 0, 0); err == nil {
		t.Error("DES length")
	}
	if _, err := p.decrypt(nil, nil, 0, 0); err == nil {
		t.Error("no salt")
	}
}

// TestSNMPVectors checks the keys and the cipher against published
// vectors: RFC 3414 A.3 and NIST SP 800-38A F.3.13.
func TestSNMPVectors(t *testing.T) {
	engine, _ := hex.DecodeString("000000000000000000000002")
	if k := hex.EncodeToString(localKey(authHashes["MD5"], "maplesyrup", engine)); k != "526f5eed9fcce26f8964c2930787d82b" {
		t.Errorf("MD5 key %s", k)
	}
	if k := hex.EncodeToString(localKey(authHashes["SHA"], "maplesyrup", engine)); k != "6695febc9288e36282235fc7151f128497b38f3f" {
		t.Errorf("SHA key %s", k)
	}
	key, _ := hex.DecodeString("2b7e151628aed2a6abf7158809cf4f3c")
	iv, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	plain, _ := hex.DecodeString("6bc1bee22e409f96e93d7e117393172aae2d")
	out, err := aesCFB(key, iv, plain, true)
	if err != nil || hex.EncodeToString(out[:16]) != "3b3fd92eb72dad20333449f8e83cfb4a" {
		t.Errorf("AES-CFB %x %v", out, err)
	}
	back, _ := aesCFB(key, iv, out, false)
	if hex.EncodeToString(back) != hex.EncodeToString(plain) {
		t.Error("AES-CFB round trip")
	}
	if _, err := aesCFB([]byte{1}, iv, plain, true); err == nil {
		t.Error("bad key")
	}
}
