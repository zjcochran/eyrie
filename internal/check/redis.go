// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Redis is the settings of the redis type: a Redis server (or one that
// speaks its protocol, such as Valkey) must answer PING.
type Redis struct {
	// ConnectionString is redis://host[:port][/db], with user:password@ (or
	// :password@) before the host to sign in, or
	// rediss:// for TLS.
	ConnectionString string `json:"connection_string"`
	IgnoreTLS        bool   `json:"ignore_tls"`
}

func (r *Redis) secrets() map[string]*string {
	return map[string]*string{"connection_string": &r.ConnectionString}
}

func (r *Redis) normalize() error {
	if r.ConnectionString == "" {
		return errors.New("the connection string is required")
	}
	return nil
}

func (r *Redis) checkPlain(plain func(name string) bool) error {
	if !plain("connection_string") {
		return nil
	}
	_, err := parseRedisURL(r.ConnectionString)
	return err
}

func (r *Redis) checker(env *Env) Checker { return &redisChecker{r: r, env: env} }

func (r *Redis) describe() []Fact { return []Fact{{"Database", "Redis"}} }

// redisTarget is what a Redis URL says.
type redisTarget struct {
	addr, host     string
	tls            bool
	user, password string
	db             int
}

func parseRedisURL(raw string) (redisTarget, error) {
	var t redisTarget
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Hostname() == "" {
		return t, errors.New("the connection string must be redis://host[:port][/db] or rediss://…, with user:password@ before the host to sign in")
	}
	t.host, t.tls = u.Hostname(), u.Scheme == "rediss"
	t.addr = net.JoinHostPort(t.host, cmp.Or(u.Port(), "6379"))
	t.user = u.User.Username()
	t.password, _ = u.User.Password()
	if db := strings.Trim(u.Path, "/"); db != "" {
		if t.db, err = strconv.Atoi(db); err != nil || t.db < 0 {
			return t, errors.New("the database in the connection string must be a number")
		}
	}
	return t, nil
}

type redisChecker struct {
	r   *Redis
	env *Env
}

func (c *redisChecker) Check(ctx context.Context) Result {
	t, err := parseRedisURL(c.r.ConnectionString)
	if err != nil {
		return failErr(err)
	}
	conn, err := c.env.Dial(ctx, "tcp", t.addr)
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	start := time.Now()
	var certExpires time.Time
	if t.tls {
		tc := tls.Client(conn, &tls.Config{ServerName: t.host, InsecureSkipVerify: c.r.IgnoreTLS}) //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
		if err := tc.HandshakeContext(ctx); err != nil {
			return failErr(connError(ctx, err))
		}
		if st := tc.ConnectionState(); len(st.PeerCertificates) > 0 {
			certExpires = st.PeerCertificates[0].NotAfter
		}
		conn = tc
	}
	rc := &resp{conn: conn, r: bufio.NewReader(conn)}
	var cmds [][]string
	switch {
	case t.password != "" && t.user != "":
		cmds = append(cmds, []string{"AUTH", t.user, t.password})
	case t.password != "":
		cmds = append(cmds, []string{"AUTH", t.password})
	}
	if t.db != 0 {
		cmds = append(cmds, []string{"SELECT", strconv.Itoa(t.db)})
	}
	cmds = append(cmds, []string{"PING"})
	var reply string
	for _, cmd := range cmds {
		if reply, err = rc.do(cmd...); err != nil {
			return failErr(connError(ctx, err))
		}
	}
	return Result{OK: reply == "PONG", Message: reply, Latency: time.Since(start), CertExpires: certExpires}
}

// resp speaks RESP2, Redis's protocol.
type resp struct {
	conn net.Conn
	r    *bufio.Reader
}

// do sends a command and returns its simple or bulk string reply; an
// error reply is an error.
func (c *resp) do(args ...string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := c.conn.Write([]byte(b.String())); err != nil {
		return "", err
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("the server's answer is not RESP")
	}
	switch line[0] {
	case '+', ':':
		return line[1:], nil
	case '-':
		return "", errors.New(line[1:])
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n > MaxBody {
			return "", errors.New("the server's answer is not RESP")
		}
		if n < 0 {
			return "", nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	}
	return "", errors.New("the server's answer is not RESP")
}
