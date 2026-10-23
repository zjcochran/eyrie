// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MQTT is the settings of the mqtt type: a message must arrive on a topic
// of an MQTT broker, with a keyword or a JSON value in it.
type MQTT struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	TLS       bool   `json:"tls"`
	IgnoreTLS bool   `json:"ignore_tls"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Topic     string `json:"topic"`
	// Check is "keyword" (the default) or "json-query".
	Check    string `json:"check"`
	Keyword  string `json:"keyword"` // "": any message
	JSONPath string `json:"json_path"`
	Operator string `json:"operator"`
	Expected string `json:"expected"`
}

func (m *MQTT) secrets() map[string]*string { return map[string]*string{"password": &m.Password} }

func (m *MQTT) normalize() error {
	h, err := host(m.Host)
	if err != nil {
		return err
	}
	m.Host = h
	if m.Port == 0 {
		m.Port = 1883
		if m.TLS {
			m.Port = 8883
		}
	}
	if m.Topic == "" || len(m.Topic) > 65535 || strings.ContainsRune(m.Topic, 0) {
		return errors.New("the topic is required")
	}
	if m.Check == "" {
		m.Check = "keyword"
	}
	switch m.Check {
	case "keyword":
	case "json-query":
		if _, err := parsePath(m.JSONPath); err != nil {
			return fmt.Errorf("the JSON path is not valid: %w", err)
		}
		if m.Operator == "" {
			m.Operator = "=="
		}
		if !slices.Contains(snmpOperators, m.Operator) {
			return fmt.Errorf("the comparison must be one of %s", strings.Join(snmpOperators, " "))
		}
	default:
		return errors.New("the check must be keyword or json-query")
	}
	if len(m.Username) > 65535 {
		return errors.New("the user name is too long")
	}
	return checkPort(m.Port)
}

func (m *MQTT) checker(env *Env) Checker { return &mqttChecker{m: m, env: env} }

func (m *MQTT) describe() []Fact {
	f := []Fact{{"Broker", net.JoinHostPort(m.Host, strconv.Itoa(m.Port))}, {"Topic", m.Topic}}
	switch {
	case m.Check == "json-query":
		f = append(f, Fact{"JSON query", m.JSONPath + " " + m.Operator + " " + strconv.Quote(m.Expected)})
	case m.Keyword != "":
		f = append(f, Fact{"Message must contain", strconv.Quote(m.Keyword)})
	}
	return f
}

type mqttChecker struct {
	m   *MQTT
	env *Env
}

// MQTT 3.1.1 packet types.
const (
	mqttConnect    = 1
	mqttConnack    = 2
	mqttPublish    = 3
	mqttPuback     = 4
	mqttSubscribe  = 8
	mqttSuback     = 9
	mqttDisconnect = 14
)

var connackErrors = map[byte]string{1: "the broker does not accept MQTT 3.1.1", 2: "the broker refused the client ID",
	3: "the broker is unavailable", 4: "wrong user name or password", 5: "not authorized"}

func mqttString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s))) //nolint:gosec // G115: checked to fit
	return append(b, s...)
}

func mqttPacket(typ, flags byte, body []byte) []byte {
	p := []byte{typ<<4 | flags}
	n := len(body)
	for {
		c := byte(n % 128)
		n /= 128
		if n > 0 {
			c |= 0x80
		}
		p = append(p, c)
		if n == 0 {
			break
		}
	}
	return append(p, body...)
}

func readMQTT(r *bufio.Reader) (byte, byte, []byte, error) {
	h, err := r.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	n, mult := 0, 1
	for i := 0; ; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, 0, nil, err
		}
		n += int(c&0x7f) * mult
		if c&0x80 == 0 {
			break
		}
		if i == 3 {
			return 0, 0, nil, errors.New("the broker's packet is not valid")
		}
		mult *= 128
	}
	if n > MaxBody {
		return 0, 0, nil, errors.New("the broker's packet is too large")
	}
	body := make([]byte, n)
	_, err = io.ReadFull(r, body)
	return h >> 4, h & 0x0f, body, err
}

func (c *mqttChecker) Check(ctx context.Context) Result {
	m := c.m
	conn, err := c.env.Dial(ctx, "tcp", net.JoinHostPort(m.Host, strconv.Itoa(m.Port)))
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	start := time.Now()
	var certExpires time.Time
	if m.TLS {
		tc := tls.Client(conn, &tls.Config{ServerName: m.Host, InsecureSkipVerify: m.IgnoreTLS}) //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
		if err := tc.HandshakeContext(ctx); err != nil {
			return failErr(connError(ctx, err))
		}
		if st := tc.ConnectionState(); len(st.PeerCertificates) > 0 {
			certExpires = st.PeerCertificates[0].NotAfter
		}
		conn = tc
	}
	r := bufio.NewReader(conn)
	payload, err := c.receive(conn, r)
	if err != nil {
		if errors.Is(connError(ctx, err), errTimeout) {
			return Result{Latency: -1, Message: "No message on " + m.Topic + " in time", CertExpires: certExpires}
		}
		return failErr(connError(ctx, err))
	}
	conn.Write(mqttPacket(mqttDisconnect, 0, nil)) //nolint:errcheck,gosec // leaving anyway
	res := c.evaluate(payload)
	res.Latency, res.CertExpires = time.Since(start), certExpires
	return res
}

// receive connects, subscribes and returns the first message's payload.
func (c *mqttChecker) receive(conn net.Conn, r *bufio.Reader) ([]byte, error) {
	m := c.m
	flags := byte(0x02) // clean session
	body := mqttString(nil, "MQTT")
	payload := mqttString(nil, "eyrie-"+token())
	if m.Username != "" {
		flags |= 0x80
		payload = mqttString(payload, m.Username)
	}
	if m.Password != "" {
		flags |= 0x40
		payload = mqttString(payload, m.Password)
	}
	body = append(body, 4, flags, 0, 60) // version 3.1.1, keep alive 60 s
	if _, err := conn.Write(mqttPacket(mqttConnect, 0, append(body, payload...))); err != nil {
		return nil, err
	}
	typ, _, ack, err := readMQTT(r)
	if err != nil {
		return nil, err
	}
	if typ != mqttConnack || len(ack) != 2 {
		return nil, errors.New("the broker did not answer CONNECT")
	}
	if ack[1] != 0 {
		return nil, fmt.Errorf("the broker refused: %s", cmp.Or(connackErrors[ack[1]], "code "+strconv.Itoa(int(ack[1]))))
	}
	sub := mqttString([]byte{0, 1}, m.Topic)
	if _, err := conn.Write(mqttPacket(mqttSubscribe, 2, append(sub, 0))); err != nil {
		return nil, err
	}
	for {
		typ, flags, body, err := readMQTT(r)
		if err != nil {
			return nil, err
		}
		switch typ {
		case mqttSuback:
			if len(body) == 3 && body[2] == 0x80 {
				return nil, fmt.Errorf("the broker refused the subscription to %s", m.Topic)
			}
		case mqttPublish:
			return publishPayload(conn, flags, body)
		}
	}
}

// publishPayload reads a PUBLISH, acknowledging it if it asks.
func publishPayload(conn net.Conn, flags byte, body []byte) ([]byte, error) {
	if len(body) < 2 {
		return nil, errors.New("the broker's message is not valid")
	}
	n := int(binary.BigEndian.Uint16(body))
	rest := body[2:]
	if n > len(rest) {
		return nil, errors.New("the broker's message is not valid")
	}
	rest = rest[n:]
	if qos := flags >> 1 & 3; qos > 0 {
		if len(rest) < 2 {
			return nil, errors.New("the broker's message is not valid")
		}
		if qos == 1 {
			conn.Write(mqttPacket(mqttPuback, 0, rest[:2])) //nolint:errcheck,gosec // the message is here either way
		}
		rest = rest[2:]
	}
	return rest, nil
}

func (c *mqttChecker) evaluate(payload []byte) Result {
	m := c.m
	if m.Check == "json-query" {
		got, err := Query(payload, m.JSONPath)
		if err != nil {
			return Result{Message: sentence(err.Error())}
		}
		msg := fmt.Sprintf("%s is %s", m.JSONPath, got)
		if !compare(got, m.Operator, m.Expected) {
			return Result{Message: msg + ", not " + m.Operator + " " + m.Expected}
		}
		return Result{OK: true, Message: msg}
	}
	if m.Keyword == "" || strings.Contains(string(payload), m.Keyword) {
		return Result{OK: true, Message: "Message: " + snippet(payload)}
	}
	return Result{Message: fmt.Sprintf("%q not found in %s", m.Keyword, snippet(payload))}
}
