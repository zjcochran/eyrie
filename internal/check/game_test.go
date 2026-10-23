// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func a2sServer(challenge bool, goldSrc bool) func(net.Conn) {
	return func(c net.Conn) {
		buf := make([]byte, 1400)
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		if challenge && n == 25 {
			c.Write([]byte("\xff\xff\xff\xff\x41\x01\x02\x03\x04"))
			if n, err = c.Read(buf); err != nil || !bytes.HasSuffix(buf[:n], []byte{1, 2, 3, 4}) {
				return
			}
		}
		c.Write([]byte("junk"))
		if goldSrc {
			c.Write([]byte("\xff\xff\xff\xffm192.0.2.5:27015\x00Old Shop\x00yard\x00valve\x00Old Game\x00\x03\x10\x2f"))
			return
		}
		c.Write([]byte("\xff\xff\xff\xffI\x11Shop Server\x00harbor\x00example\x00Example Game\x00\xda\x02\x05\x18\x00"))
	}
}

func TestSteam(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = pipeDial(a2sServer(true, false), &dialed)
	r := run(t, env, "steam", `{"host":"game.example.com"}`)
	if !r.OK || r.Message != "Shop Server · Example Game · harbor · 5/24 players" || dialed != "udp game.example.com:27015" {
		t.Errorf("steam: %+v %s", r, dialed)
	}
	env.Dial = pipeDial(a2sServer(false, true), nil)
	if r := run(t, env, "gamedig", `{"host":"game.example.com","protocol":"valve"}`); !r.OK || r.Message != "Old Shop · Old Game · yard · 3/16 players" {
		t.Errorf("goldsrc: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 100)); c.Write([]byte("\xff\xff\xff\xffA\x01")) }, nil)
	if r := run(t, env, "steam", `{"host":"game.example.com"}`); r.OK || r.Message != "The server's challenge is not valid" {
		t.Errorf("bad challenge: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 100)); c.Write([]byte("\xff\xff\xff\xffI\x11a\x00b\x00")) }, nil)
	if r := run(t, env, "steam", `{"host":"game.example.com"}`); r.OK || r.Message != "The server's A2S_INFO answer is too short" {
		t.Errorf("short: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) {
		c.Read(make([]byte, 100))
		for range 3 {
			c.Write([]byte("\xff\xff\xff\xffZ"))
		}
	}, nil)
	if r := run(t, env, "steam", `{"host":"game.example.com"}`); r.OK || r.Message != "The server did not answer A2S_INFO" {
		t.Errorf("other answers: %+v", r)
	}
	if _, _, err := Parse(env, "steam", []byte(`{"host":"game.example.com","protocol":"quake3"}`)); err == nil {
		t.Error("steam with another protocol")
	}
	if f := Describe("steam", []byte(`{"host":"game.example.com","port":27016}`)); len(f) != 1 || f[0].Value != "game.example.com:27016" {
		t.Errorf("describe %v", f)
	}
}

func minecraftServer(status string) func(net.Conn) {
	return func(c net.Conn) {
		r := bufio.NewReader(c)
		for range 2 { // the handshake and the status request
			n, err := readVarint(r)
			if err != nil {
				return
			}
			r.Discard(n)
		}
		body := putVarint(nil, len(status))
		body = append(body, status...)
		c.Write(mcPacket(0, body))
	}
}

func TestMinecraft(t *testing.T) {
	env := testEnv()
	var dialed string
	env.Dial = pipeDial(minecraftServer(`{"version":{"name":"1.21"},"players":{"online":3,"max":20},"description":{"text":"§aShop ","extra":[{"text":"Craft"}]}}`), &dialed)
	r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft"}`)
	if !r.OK || r.Message != "Shop Craft · Minecraft 1.21 · 3/20 players" || dialed != "tcp mc.example.com:25565" {
		t.Errorf("java: %+v %s", r, dialed)
	}
	env.Dial = pipeDial(minecraftServer(`{"version":{"name":"1.8"},"players":{"online":0,"max":5},"description":"Plain"}`), nil)
	if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft"}`); !r.OK || r.Message != "Plain · Minecraft 1.8 · 0/5 players" {
		t.Errorf("plain: %+v", r)
	}
	env.Dial = pipeDial(minecraftServer(`nope`), nil)
	if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft"}`); r.OK || r.Message != "The server's status is not JSON" {
		t.Errorf("garbage: %+v", r)
	}
	for _, reply := range [][]byte{{0}, {2, 1, 0}, {3, 0, 0x7f, 1}, {0xff, 0xff, 0xff, 0xff, 0xff}} {
		env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 100)); c.Write(reply) }, nil)
		if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft"}`); r.OK {
			t.Errorf("%x: %+v", reply, r)
		}
	}
}

func TestBedrock(t *testing.T) {
	env := testEnv()
	pong := func(motd string) func(net.Conn) {
		return func(c net.Conn) {
			c.Read(make([]byte, 100))
			p := []byte{0x1c}
			p = append(p, make([]byte, 16)...)
			p = append(p, raknetMagic...)
			p = binary.BigEndian.AppendUint16(p, uint16(len(motd)))
			c.Write(append(p, motd...))
		}
	}
	env.Dial = pipeDial(pong("MCPE;Shop Bedrock;712;1.21.2;4;10;123;World;Survival"), nil)
	if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft-bedrock"}`); !r.OK ||
		r.Message != "Shop Bedrock · Minecraft Bedrock 1.21.2 · 4/10 players" {
		t.Errorf("bedrock: %+v", r)
	}
	env.Dial = pipeDial(pong("MCPE;short"), nil)
	if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft-bedrock"}`); r.OK {
		t.Errorf("short motd: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 100)); c.Write([]byte{0x1c, 1}) }, nil)
	if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft-bedrock"}`); r.OK {
		t.Errorf("not a pong: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) {
		c.Read(make([]byte, 100))
		p := append(append([]byte{0x1c}, make([]byte, 16)...), raknetMagic...)
		c.Write(append(p, 0, 50))
	}, nil)
	if r := run(t, env, "gamedig", `{"host":"mc.example.com","protocol":"minecraft-bedrock"}`); r.OK {
		t.Errorf("cut pong: %+v", r)
	}
}

func TestQuake(t *testing.T) {
	env := testEnv()
	env.Dial = pipeDial(func(c net.Conn) {
		buf := make([]byte, 100)
		n, _ := c.Read(buf)
		if !strings.Contains(string(buf[:n]), "getstatus") {
			return
		}
		c.Write([]byte("\xff\xff\xff\xffstatusResponse\n\\sv_hostname\\Arena\\mapname\\arena1\\sv_maxclients\\16\\gamename\\baseq3\n0 50 \"alice\"\n3 40 \"bob\"\n"))
	}, nil)
	if r := run(t, env, "gamedig", `{"host":"q.example.com","protocol":"quake3"}`); !r.OK || r.Message != "Arena · baseq3 · arena1 · 2/16 players" {
		t.Errorf("quake3: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) {
		c.Read(make([]byte, 100))
		c.Write([]byte("\xff\xff\xff\xffprint\n\\hostname\\Q2\\maxclients\\8\\mapname\\base1\n"))
	}, nil)
	if r := run(t, env, "gamedig", `{"host":"q.example.com","protocol":"quake2"}`); !r.OK || r.Message != "Q2 · base1 · 0/8 players" {
		t.Errorf("quake2: %+v", r)
	}
	env.Dial = pipeDial(func(c net.Conn) { c.Read(make([]byte, 100)); c.Write([]byte("hello")) }, nil)
	if r := run(t, env, "gamedig", `{"host":"q.example.com","protocol":"quake2"}`); r.OK || r.Message != "The server's answer is not a status" {
		t.Errorf("garbage: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "gamedig", `{"host":"q.example.com","protocol":"quake2"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	for _, s := range []string{`{"host":""}`, `{"host":"q.example.com","protocol":"doom"}`, `{"host":"q.example.com","port":-1}`} {
		if _, _, err := Parse(env, "gamedig", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if len(GameProtocols()) != 5 || len(Describe("gamedig", []byte(`{"host":"q.example.com","protocol":"quake2"}`))) != 2 {
		t.Error("protocols")
	}
}
