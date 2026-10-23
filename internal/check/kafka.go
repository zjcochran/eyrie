// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// Kafka is the settings of the kafka-producer type: a Kafka cluster must
// take a message on a topic.
type Kafka struct {
	Brokers   []string `json:"brokers"` // host:port
	Topic     string   `json:"topic"`
	Message   string   `json:"message"` // "Eyrie check" by default
	TLS       bool     `json:"tls"`
	IgnoreTLS bool     `json:"ignore_tls"`
	// SASL is none (the default), plain, scram-sha-256 or scram-sha-512.
	SASL      string `json:"sasl"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	AutoTopic bool   `json:"allow_auto_topic_creation"`
}

var kafkaSASL = []string{"none", "plain", "scram-sha-256", "scram-sha-512"}

func (k *Kafka) secrets() map[string]*string { return map[string]*string{"password": &k.Password} }

func (k *Kafka) normalize() error {
	if len(k.Brokers) == 0 || len(k.Brokers) > 20 {
		return errors.New("give 1 to 20 brokers")
	}
	for i, b := range k.Brokers {
		b = strings.TrimSpace(b)
		h, p, err := net.SplitHostPort(b)
		if port, perr := strconv.Atoi(p); err != nil || h == "" || perr != nil || checkPort(port) != nil {
			return fmt.Errorf("%q is not host:port", b)
		}
		k.Brokers[i] = b
	}
	k.Topic = strings.TrimSpace(k.Topic)
	if k.Topic == "" || len(k.Topic) > 249 || strings.ContainsAny(k.Topic, " /\\") {
		return errors.New("the topic must be 1 to 249 letters, digits, dots, dashes and underscores")
	}
	if k.Message == "" {
		k.Message = "Eyrie check"
	}
	if len(k.Message) > 10000 {
		return errors.New("the message is at most 10000 characters")
	}
	if k.SASL == "" {
		k.SASL = "none"
	}
	if !slices.Contains(kafkaSASL, k.SASL) {
		return fmt.Errorf("SASL must be one of %s", strings.Join(kafkaSASL, ", "))
	}
	if k.SASL != "none" && strings.TrimSpace(k.Username) == "" {
		return errors.New("SASL needs a user name")
	}
	return nil
}

func (k *Kafka) checker(env *Env) Checker { return &kafkaChecker{k: k, env: env} }

func (k *Kafka) describe() []Fact {
	return []Fact{{"Kafka brokers", strings.Join(k.Brokers, ", ")}, {"Topic", k.Topic}, {"SASL", k.SASL},
		{"TLS", strconv.FormatBool(k.TLS)}}
}

type kafkaChecker struct {
	k   *Kafka
	env *Env
}

func (c *kafkaChecker) mechanism() sasl.Mechanism {
	k := c.k
	switch k.SASL {
	case "plain":
		return plain.Auth{User: k.Username, Pass: k.Password}.AsMechanism()
	case "scram-sha-256":
		return scram.Auth{User: k.Username, Pass: k.Password}.AsSha256Mechanism()
	case "scram-sha-512":
		return scram.Auth{User: k.Username, Pass: k.Password}.AsSha512Mechanism()
	}
	return nil
}

// dial dials a broker through the Env, with TLS if asked.
func (c *kafkaChecker) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := c.env.Dial(ctx, network, addr)
	if err != nil || !c.k.TLS {
		return conn, err
	}
	h, _, _ := net.SplitHostPort(addr)
	tc := tls.Client(conn, &tls.Config{ServerName: h, InsecureSkipVerify: c.k.IgnoreTLS}) //nolint:gosec // G402: only when a monitor asks to ignore TLS errors
	if err := tc.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tc, nil
}

func (c *kafkaChecker) Check(ctx context.Context) Result {
	k := c.k
	opts := []kgo.Opt{
		kgo.SeedBrokers(k.Brokers...),
		kgo.Dialer(c.dial),
		kgo.ClientID("eyrie"),
		kgo.SoftwareNameAndVersion("eyrie", "1"),
		kgo.DisableIdempotentWrite(),
		kgo.RecordDeliveryTimeout(max(time.Until(deadline(ctx)), time.Second)), // its least
	}
	if m := c.mechanism(); m != nil {
		opts = append(opts, kgo.SASL(m))
	}
	if k.AutoTopic {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return failErr(err)
	}
	defer client.Close()
	start := time.Now()
	rec := &kgo.Record{Topic: k.Topic, Value: []byte(k.Message)}
	if err := client.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return failErr(sqlError(ctx, err))
	}
	return Result{OK: true, Latency: time.Since(start),
		Message: fmt.Sprintf("Sent to %s, partition %d, offset %d", k.Topic, rec.Partition, rec.Offset)}
}
