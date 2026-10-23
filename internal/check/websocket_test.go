// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsServer is the server end of a WebSocket connection, for fakes.
type wsServer struct {
	*wsConn
}

// send writes an unmasked frame, as servers do.
func (s wsServer) send(op byte, fin bool, payload []byte) {
	head := []byte{op}
	if fin {
		head[0] |= 0x80
	}
	switch n := len(payload); {
	case n < 126:
		head = append(head, byte(n))
	case n <= 0xffff:
		head = append(head, 126)
		head = binary.BigEndian.AppendUint16(head, uint16(n))
	default:
		head = append(head, 127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	s.conn.Write(append(head, payload...))
}

// upgrade answers a WebSocket upgrade and hands the connection to serve.
func upgrade(serve func(wsServer), accept func(key string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "not a WebSocket", http.StatusBadRequest)
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
			accept(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
		rw.Flush()
		serve(wsServer{&wsConn{conn: conn, r: rw.Reader}})
	}
}

func TestWebSocket(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Shop") + "|" + r.Header.Get("Sec-WebSocket-Protocol")
		switch r.URL.Path {
		case "/ws":
			upgrade(func(s wsServer) { s.Read() }, wsAccept)(w, r)
		case "/wrong":
			upgrade(func(wsServer) {}, func(string) string { return "nope" })(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")
	env := testEnv()
	r := run(t, env, "websocket-upgrade", `{"url":"`+ws+`/ws","headers":{"X-Shop":"1"},"subprotocol":"chat"}`)
	if !r.OK || r.Message != "101 Switching Protocols" || gotHeader != "1|chat" || r.Latency <= 0 {
		t.Errorf("upgrade: %+v %s", r, gotHeader)
	}
	if r := run(t, env, "websocket-upgrade", `{"url":"`+ws+`/none"}`); r.OK || r.Message != "Status 404 Not Found, not 101 Switching Protocols" {
		t.Errorf("not found: %+v", r)
	}
	if r := run(t, env, "websocket-upgrade", `{"url":"`+ws+`/wrong"}`); r.OK || !strings.Contains(r.Message, "Sec-WebSocket-Accept") {
		t.Errorf("wrong accept: %+v", r)
	}
	if r := run(t, env, "websocket-upgrade", `{"url":"`+ws+`/wrong","ignore_accept":true}`); !r.OK {
		t.Errorf("ignored accept: %+v", r)
	}
}

func TestWebSocketTLS(t *testing.T) {
	env := testEnv()
	tlsSrv := httptest.NewTLSServer(upgrade(func(wsServer) {}, wsAccept))
	defer tlsSrv.Close()
	wss := "wss" + strings.TrimPrefix(tlsSrv.URL, "https")
	if r := run(t, env, "websocket-upgrade", `{"url":"`+wss+`"}`); r.OK || !strings.Contains(r.Message, "certificate") {
		t.Errorf("untrusted: %+v", r)
	}
	if r := run(t, env, "websocket-upgrade", `{"url":"`+wss+`","ignore_tls":true}`); !r.OK || r.CertExpires.IsZero() {
		t.Errorf("TLS: %+v", r)
	}

	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "websocket-upgrade", `{"url":"ws://ws.example.com"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("timeout: %+v", r)
	}
	var dialed string
	env.Dial = pipeDial(func(c net.Conn) { bufio.NewReader(c).ReadString('\n') }, &dialed)
	if r := run(t, env, "websocket-upgrade", `{"url":"wss://ws.example.com/x"}`); r.OK || dialed != "tcp ws.example.com:443" {
		t.Errorf("closed during TLS: %+v %s", r, dialed)
	}
	env.Dial = pipeDial(func(c net.Conn) { bufio.NewReader(c).ReadString('\n') }, &dialed)
	if r := run(t, env, "websocket-upgrade", `{"url":"ws://ws.example.com/x"}`); r.OK || r.Message != "The server closed the connection" ||
		dialed != "tcp ws.example.com:80" {
		t.Errorf("closed: %+v %s", r, dialed)
	}
	if f := (&WebSocket{URL: "wss://u:p@ws.example.com/x?k=1", Subprotocol: "chat", IgnoreTLS: true}).describe(); len(f) != 3 ||
		f[0].Value != "wss://ws.example.com/x" {
		t.Errorf("describe %v", f)
	}
}

func TestWebSocketInvalid(t *testing.T) {
	for _, s := range []string{`{"url":"https://example.com"}`, `{"url":"ws://"}`, `{"url":"ws://example.com","headers":{"a b":"1"}}`,
		`{"url":"ws://example.com","subprotocol":"a b"}`} {
		if _, _, err := Parse(testEnv(), "websocket-upgrade", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestWSFrames(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	c := &wsConn{conn: client, r: bufio.NewReader(client)}
	s := wsServer{&wsConn{conn: server, r: bufio.NewReader(server)}}
	big := strings.Repeat("x", 70000)
	go func() {
		s.send(opPing, true, []byte("hi"))
		io.ReadFull(s.r, make([]byte, 8)) // the pong: header, mask, "hi"
		s.send(opText, false, []byte("hel"))
		s.send(opPong, true, nil)
		s.send(opCont, true, []byte("lo"))
		s.send(opText, true, []byte(strings.Repeat("y", 300)))
		s.send(opText, true, []byte(big))
		s.send(opClose, true, nil)
	}()
	if m, err := c.Read(); err != nil || string(m) != "hello" {
		t.Errorf("fragments: %q %v", m, err)
	}
	if m, err := c.Read(); err != nil || len(m) != 300 {
		t.Errorf("medium: %d %v", len(m), err)
	}
	if m, err := c.Read(); err != nil || string(m) != big {
		t.Errorf("large: %d %v", len(m), err)
	}
	if _, err := c.Read(); err == nil {
		t.Error("close frame")
	}
	go func() {
		s.Read()
		s.Read()
	}()
	if err := c.WriteText([]byte(big)); err != nil {
		t.Error(err)
	}
	if err := c.WriteText([]byte(strings.Repeat("z", 200))); err != nil {
		t.Error(err)
	}
	go func() {
		s.send(opText, true, make([]byte, 0))
		s.conn.Write([]byte{0x81, 127, 0xff, 0, 0, 0, 0, 0, 0, 0})
	}()
	c.Read()
	if _, err := c.Read(); err == nil {
		t.Error("too large")
	}
	server.Close()
	if _, err := c.Read(); err == nil {
		t.Error("closed")
	}
	c.conn.SetDeadline(time.Now())
}

func TestDialAddrPlainTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	env := testEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := mustURL(t, srv.URL)
	conn, st, err := dialAddr(ctx, env, u, true)
	if err != nil || st == nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, ok := conn.(*tls.Conn); !ok {
		t.Error("not TLS")
	}
}
