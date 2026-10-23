// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// gameInfo is what a game server says about itself.
type gameInfo struct {
	name, game, mapName string
	players, max        int
}

func (g gameInfo) String() string {
	parts := []string{}
	for _, p := range []string{g.name, g.game, g.mapName} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	parts = append(parts, fmt.Sprintf("%d/%d players", g.players, g.max))
	return strings.Join(parts, " · ")
}

// Game is the settings of the steam and gamedig types: a game server must
// answer its game's status query.
type Game struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Protocol is the server's query protocol (gamedig only): valve,
	// minecraft, minecraft-bedrock, quake2 or quake3.
	Protocol string `json:"protocol,omitempty"`
}

// gamePorts are the query protocols, with their usual ports.
var gamePorts = map[string]int{"valve": 27015, "minecraft": 25565, "minecraft-bedrock": 19132, "quake2": 27910, "quake3": 27960}

// GameProtocols are the game server query protocols, sorted.
func GameProtocols() []string {
	out := make([]string, 0, len(gamePorts))
	for p := range gamePorts {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func (g *Game) normalize() error {
	h, err := host(g.Host)
	if err != nil {
		return err
	}
	g.Host = h
	if g.Protocol == "" {
		g.Protocol = "valve"
	}
	if _, ok := gamePorts[g.Protocol]; !ok {
		return fmt.Errorf("the protocol must be one of %s", strings.Join(GameProtocols(), ", "))
	}
	if g.Port == 0 {
		g.Port = gamePorts[g.Protocol]
	}
	return checkPort(g.Port)
}

func (g *Game) checker(env *Env) Checker { return &gameChecker{g: g, env: env} }

func (g *Game) describe() []Fact {
	return []Fact{{"Game server", net.JoinHostPort(g.Host, strconv.Itoa(g.Port))}, {"Query protocol", g.Protocol}}
}

// steam is the steam type's settings: a game server with Valve's protocol.
type steam struct{ Game }

func (s *steam) normalize() error {
	if s.Protocol != "" && s.Protocol != "valve" {
		return errors.New("a Steam game server speaks the valve protocol")
	}
	err := s.Game.normalize()
	s.Protocol = ""
	return err
}

func (s *steam) describe() []Fact {
	return []Fact{{"Steam game server", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}}
}

func (s *steam) checker(env *Env) Checker {
	g := s.Game
	g.Protocol = "valve"
	return &gameChecker{g: &g, env: env}
}

type gameChecker struct {
	g   *Game
	env *Env
}

func (c *gameChecker) Check(ctx context.Context) Result {
	network := "udp"
	if c.g.Protocol == "minecraft" {
		network = "tcp"
	}
	conn, err := c.env.Dial(ctx, network, net.JoinHostPort(c.g.Host, strconv.Itoa(c.g.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	start := time.Now()
	var info gameInfo
	switch c.g.Protocol {
	case "minecraft":
		info, err = minecraftStatus(conn, c.g.Host, c.g.Port)
	case "minecraft-bedrock":
		info, err = bedrockStatus(conn)
	case "quake2", "quake3":
		info, err = quakeStatus(conn, c.g.Protocol == "quake3")
	default:
		info, err = a2sInfo(conn)
	}
	if err != nil {
		return failErr(connError(ctx, err))
	}
	return Result{OK: true, Message: info.String(), Latency: time.Since(start)}
}

// a2sInfo asks with Valve's A2S_INFO query, answering a challenge if the
// server sends one.
func a2sInfo(conn net.Conn) (gameInfo, error) {
	query := append([]byte("\xff\xff\xff\xffTSource Engine Query"), 0)
	if _, err := conn.Write(query); err != nil {
		return gameInfo{}, err
	}
	buf := make([]byte, 1400)
	for range 3 {
		n, err := conn.Read(buf)
		if err != nil {
			return gameInfo{}, err
		}
		b := buf[:n]
		if len(b) < 5 || !bytes.Equal(b[:4], []byte{0xff, 0xff, 0xff, 0xff}) {
			continue
		}
		switch b[4] {
		case 'A': // a challenge: ask again with it
			if len(b) < 9 {
				return gameInfo{}, errors.New("the server's challenge is not valid")
			}
			if _, err := conn.Write(append(query, b[5:9]...)); err != nil {
				return gameInfo{}, err
			}
		case 'I':
			return parseA2S(b[5:], false)
		case 'm':
			return parseA2S(b[5:], true)
		}
	}
	return gameInfo{}, errors.New("the server did not answer A2S_INFO")
}

func parseA2S(b []byte, goldSrc bool) (gameInfo, error) {
	var g gameInfo
	str := func() string {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			b = nil
			return ""
		}
		s := string(b[:i])
		b = b[i+1:]
		return s
	}
	if goldSrc {
		str() // the address
	} else if len(b) > 0 {
		b = b[1:] // the protocol version
	}
	g.name, g.mapName = str(), str()
	str() // the folder
	g.game = str()
	if !goldSrc && len(b) >= 2 {
		b = b[2:] // the Steam application id
	}
	if len(b) < 2 {
		return g, errors.New("the server's A2S_INFO answer is too short")
	}
	g.players, g.max = int(b[0]), int(b[1])
	return g, nil
}

// putVarint appends a Minecraft VarInt.
func putVarint(b []byte, v int) []byte {
	u := uint32(v) //nolint:gosec // G115: VarInts are 32-bit two's complement
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

func readVarint(r io.ByteReader) (int, error) {
	var v uint32
	for i := range 5 {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int(int32(v)), nil //nolint:gosec // G115: VarInts are 32-bit two's complement
		}
	}
	return 0, errors.New("a VarInt is too long")
}

func mcPacket(id int, body []byte) []byte {
	p := putVarint(nil, id)
	p = append(p, body...)
	return append(putVarint(nil, len(p)), p...)
}

// minecraftStatus asks a Minecraft Java Edition server for its status
// (the server list ping).
func minecraftStatus(conn net.Conn, host string, port int) (gameInfo, error) {
	hs := putVarint(nil, -1) // any protocol version
	hs = putVarint(hs, len(host))
	hs = append(hs, host...)
	hs = binary.BigEndian.AppendUint16(hs, uint16(port)) //nolint:gosec // G115: checked to be a port
	hs = putVarint(hs, 1)                                // status
	if _, err := conn.Write(append(mcPacket(0, hs), mcPacket(0, nil)...)); err != nil {
		return gameInfo{}, err
	}
	r := bufio.NewReader(conn)
	n, err := readVarint(r)
	if err != nil {
		return gameInfo{}, err
	}
	if n < 1 || n > 1<<21 {
		return gameInfo{}, errors.New("the server's status is not valid")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return gameInfo{}, err
	}
	br := bytes.NewReader(body)
	if id, err := readVarint(br); err != nil || id != 0 {
		return gameInfo{}, errors.New("the server's status is not valid")
	}
	l, err := readVarint(br)
	if err != nil || l < 0 || l > br.Len() {
		return gameInfo{}, errors.New("the server's status is not valid")
	}
	var st struct {
		Version struct{ Name string }
		Players struct{ Online, Max int }
		// Description is text or a chat component.
		Description json.RawMessage
	}
	if err := json.Unmarshal(body[len(body)-br.Len():][:l], &st); err != nil {
		return gameInfo{}, errors.New("the server's status is not JSON")
	}
	return gameInfo{name: chatText(st.Description), game: "Minecraft " + st.Version.Name, players: st.Players.Online, max: st.Players.Max}, nil
}

// chatText is the text of a Minecraft chat component, without formatting
// codes.
func chatText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var c struct {
			Text  string
			Extra []json.RawMessage
		}
		json.Unmarshal(raw, &c) //nolint:errcheck,gosec // no description then
		s = c.Text
		for _, e := range c.Extra {
			s += chatText(e)
		}
	}
	var out []rune
	skip := false
	for _, r := range s {
		switch {
		case skip:
			skip = false
		case r == '§':
			skip = true
		default:
			out = append(out, r)
		}
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// raknetMagic marks RakNet's offline messages.
var raknetMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

// bedrockStatus asks a Minecraft Bedrock Edition server for its status
// with RakNet's unconnected ping.
func bedrockStatus(conn net.Conn) (gameInfo, error) {
	ping := []byte{0x01}
	ping = binary.BigEndian.AppendUint64(ping, uint64(time.Now().UnixMilli())) //nolint:gosec // G115: a positive time
	ping = append(ping, raknetMagic...)
	guid := make([]byte, 8)
	rand.Read(guid)
	if _, err := conn.Write(append(ping, guid...)); err != nil {
		return gameInfo{}, err
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return gameInfo{}, err
	}
	b := buf[:n]
	if len(b) < 35 || b[0] != 0x1c || !bytes.Equal(b[17:33], raknetMagic) {
		return gameInfo{}, errors.New("the server's answer is not a RakNet pong")
	}
	l := int(binary.BigEndian.Uint16(b[33:]))
	if 35+l > len(b) {
		return gameInfo{}, errors.New("the server's pong is too short")
	}
	f := strings.Split(string(b[35:35+l]), ";")
	if len(f) < 6 {
		return gameInfo{}, errors.New("the server's pong is not valid")
	}
	g := gameInfo{name: f[1], game: "Minecraft Bedrock " + f[3]}
	g.players, _ = strconv.Atoi(f[4])
	g.max, _ = strconv.Atoi(f[5])
	return g, nil
}

// quakeStatus asks a Quake 2 or Quake 3 server for its status.
func quakeStatus(conn net.Conn, q3 bool) (gameInfo, error) {
	query, answer := "status", "print"
	if q3 {
		query, answer = "getstatus", "statusResponse"
	}
	if _, err := conn.Write([]byte("\xff\xff\xff\xff" + query + "\n")); err != nil {
		return gameInfo{}, err
	}
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		return gameInfo{}, err
	}
	head := "\xff\xff\xff\xff" + answer + "\n"
	s, ok := strings.CutPrefix(string(buf[:n]), head)
	if !ok {
		return gameInfo{}, errors.New("the server's answer is not a status")
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	vars := map[string]string{}
	kv := strings.Split(strings.TrimPrefix(lines[0], "\\"), "\\")
	for i := 0; i+1 < len(kv); i += 2 {
		vars[strings.ToLower(kv[i])] = kv[i+1]
	}
	g := gameInfo{name: cmp.Or(vars["sv_hostname"], vars["hostname"]), game: vars["gamename"], mapName: vars["mapname"],
		players: len(lines) - 1}
	g.max, _ = strconv.Atoi(vars["sv_maxclients"])
	if g.max == 0 {
		g.max, _ = strconv.Atoi(vars["maxclients"])
	}
	return g, nil
}
