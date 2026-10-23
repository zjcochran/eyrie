// SPDX-License-Identifier: AGPL-3.0-or-later

package check

// RFC 6455 computes Sec-WebSocket-Accept with SHA-1; it protects nothing.
// nosemgrep
import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6455 computes Sec-WebSocket-Accept with SHA-1
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebSocket is the settings of the websocket-upgrade type: the server must
// accept a WebSocket upgrade.
type WebSocket struct {
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Subprotocol string            `json:"subprotocol"`
	IgnoreTLS   bool              `json:"ignore_tls"`
	// IgnoreAccept does not check the server's Sec-WebSocket-Accept, for
	// proxies that get it wrong.
	IgnoreAccept bool `json:"ignore_accept"`
}

func (w *WebSocket) normalize() error {
	w.URL = strings.TrimSpace(w.URL)
	u, err := url.Parse(w.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return errors.New("the URL must start with ws:// or wss://")
	}
	if w.Headers == nil {
		w.Headers = map[string]string{}
	}
	for k, v := range w.Headers {
		if k == "" || strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("the header %q is not valid", k)
		}
	}
	w.Subprotocol = strings.TrimSpace(w.Subprotocol)
	if strings.ContainsAny(w.Subprotocol, " \r\n") {
		return errors.New("the subprotocol must be one word")
	}
	return nil
}

func (w *WebSocket) checker(env *Env) Checker { return &wsChecker{w: w, env: env} }

func (w *WebSocket) describe() []Fact {
	f := []Fact{{"URL", publicURL(w.URL)}}
	if w.Subprotocol != "" {
		f = append(f, Fact{"Subprotocol", w.Subprotocol})
	}
	if w.IgnoreTLS {
		f = append(f, Fact{"TLS errors", "ignored"})
	}
	return f
}

type wsChecker struct {
	w   *WebSocket
	env *Env
}

func (c *wsChecker) Check(ctx context.Context) Result {
	start := time.Now()
	h := http.Header{}
	for k, v := range c.w.Headers {
		h.Set(k, v)
	}
	if c.w.Subprotocol != "" {
		h.Set("Sec-WebSocket-Protocol", c.w.Subprotocol)
	}
	ws, status, state, err := wsDial(ctx, c.env, c.w.URL, h, c.w.IgnoreTLS, !c.w.IgnoreAccept)
	r := Result{Latency: time.Since(start)}
	if state != nil && len(state.PeerCertificates) > 0 {
		r.CertExpires = state.PeerCertificates[0].NotAfter
	}
	if err != nil {
		r.Latency = -1
		r.Message = sentence(err.Error())
		return r
	}
	ws.Close()
	r.OK, r.Message = true, status
	return r
}

// dialAddr opens a TCP connection to a URL's host, with TLS for https and
// wss. It returns the TLS state, if any.
func dialAddr(ctx context.Context, env *Env, u *url.URL, insecure bool) (net.Conn, *tls.ConnectionState, error) {
	port := u.Port()
	secure := u.Scheme == "https" || u.Scheme == "wss"
	if port == "" {
		port = "80"
		if secure {
			port = "443"
		}
	}
	conn, err := env.Dial(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return nil, nil, errors.New(dialError(err))
	}
	if !secure {
		return conn, nil, nil
	}
	tc := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: insecure}) //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
	if err := tc.HandshakeContext(ctx); err != nil {
		conn.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, errTimeout
		}
		return nil, nil, err
	}
	st := tc.ConnectionState()
	return tc, &st, nil
}

// wsDial opens a WebSocket connection. It returns the status of the
// answer to the upgrade request.
func wsDial(ctx context.Context, env *Env, raw string, h http.Header, insecure, checkAccept bool) (*wsConn, string, *tls.ConnectionState, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", nil, err
	}
	conn, state, err := dialAddr(ctx, env, u, insecure)
	if err != nil {
		return nil, "", state, err
	}
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d) //nolint:errcheck,gosec // a failure shows as a timeout
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) }) //nolint:errcheck,gosec // a failure shows as a timeout
	defer stop()
	nonce := make([]byte, 16)
	rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)
	hu := *u
	hu.Scheme = strings.Replace(u.Scheme, "ws", "http", 1)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hu.String(), nil)
	req.Header = h.Clone()
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Version", "13")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", env.UserAgent)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, "", state, connError(ctx, err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err == nil {
		resp.Body.Close()
	}
	if err != nil {
		conn.Close()
		return nil, "", state, connError(ctx, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, resp.Status, state, fmt.Errorf("status %s, not 101 Switching Protocols", resp.Status)
	}
	if checkAccept && resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		conn.Close()
		return nil, resp.Status, state, errors.New("the server's Sec-WebSocket-Accept is wrong")
	}
	conn.SetDeadline(time.Time{}) //nolint:errcheck,gosec // a failure shows as a timeout
	return &wsConn{conn: conn, r: br}, resp.Status, state, nil
}

func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) //nolint:gosec // G401: RFC 6455
	return base64.StdEncoding.EncodeToString(sum[:])
}

// connError says what went wrong on a connection.
func connError(ctx context.Context, err error) error {
	var ne net.Error
	if ctx.Err() != nil || (errors.As(err, &ne) && ne.Timeout()) {
		return errTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New("the server closed the connection")
	}
	return err
}

// wsConn is the client end of a WebSocket connection.
type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
}

// wsMax is the largest message read.
const wsMax = 8 << 20

const (
	opCont  = 0x0
	opText  = 0x1
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xa
)

// write sends one masked frame, as clients must.
func (c *wsConn) write(op byte, payload []byte) error {
	head := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n <= 0xffff:
		head = append(head, 0x80|126)
		head = binary.BigEndian.AppendUint16(head, uint16(n))
	default:
		head = append(head, 0x80|127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	head = append(head, mask...)
	data := make([]byte, n)
	for i, b := range payload {
		data[i] = b ^ mask[i%4]
	}
	_, err := c.conn.Write(append(head, data...))
	return err
}

// WriteText sends a text message.
func (c *wsConn) WriteText(p []byte) error { return c.write(opText, p) }

// Read returns the next text or binary message, answering pings.
func (c *wsConn) Read() ([]byte, error) {
	var msg []byte
	for {
		head := make([]byte, 2)
		if _, err := io.ReadFull(c.r, head); err != nil {
			return nil, err
		}
		fin, op := head[0]&0x80 != 0, head[0]&0x0f
		n := uint64(head[1] & 0x7f)
		switch n {
		case 126:
			b := make([]byte, 2)
			if _, err := io.ReadFull(c.r, b); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b))
		case 127:
			b := make([]byte, 8)
			if _, err := io.ReadFull(c.r, b); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b)
		}
		var mask []byte
		if head[1]&0x80 != 0 {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(c.r, mask); err != nil {
				return nil, err
			}
		}
		if n > wsMax || uint64(len(msg))+n > wsMax {
			return nil, errors.New("a WebSocket message is too large")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return nil, err
		}
		for i := range payload {
			if mask != nil {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case opClose:
			return nil, io.EOF
		case opPing:
			if err := c.write(opPong, payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		}
		msg = append(msg, payload...)
		if fin {
			return msg, nil
		}
	}
}

// Close sends a close frame and closes the connection.
func (c *wsConn) Close() error {
	c.conn.SetWriteDeadline(time.Now().Add(time.Second)) //nolint:errcheck,gosec // a failure shows as a timeout
	c.write(opClose, []byte{0x03, 0xe8})                 //nolint:errcheck,gosec // closing anyway
	return c.conn.Close()
}
