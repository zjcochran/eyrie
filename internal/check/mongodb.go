// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoDB is the settings of the mongodb type: a database command must
// succeed and, with a JSON path, its answer must hold a value.
type MongoDB struct {
	// ConnectionString is a mongodb:// or mongodb+srv:// URI.
	ConnectionString string `json:"connection_string"`
	// Command is a database command as (extended) JSON; {"ping": 1} by
	// default. It runs on the URI's database, or admin.
	Command  string `json:"command"`
	JSONPath string `json:"json_path"` // "": the command must succeed
	Operator string `json:"operator"`
	Expected string `json:"expected"`
}

func (m *MongoDB) secrets() map[string]*string {
	return map[string]*string{"connection_string": &m.ConnectionString}
}

func (m *MongoDB) normalize() error {
	if m.ConnectionString == "" {
		return errors.New("the connection string is required")
	}
	m.Command = strings.TrimSpace(m.Command)
	if m.Command == "" {
		m.Command = `{"ping": 1}`
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON([]byte(m.Command), false, &doc); err != nil || len(doc) == 0 {
		return errors.New(`the command must be a JSON object, such as {"ping": 1}`)
	}
	m.JSONPath = strings.TrimSpace(m.JSONPath)
	if m.JSONPath == "" {
		return nil
	}
	if _, err := parsePath(m.JSONPath); err != nil {
		return fmt.Errorf("the JSON path is not valid: %w", err)
	}
	if m.Operator == "" {
		m.Operator = "=="
	}
	if !slices.Contains(snmpOperators, m.Operator) {
		return fmt.Errorf("the comparison must be one of %s", strings.Join(snmpOperators, " "))
	}
	return nil
}

func (m *MongoDB) checkPlain(plain func(name string) bool) error {
	if !plain("connection_string") {
		return nil
	}
	if err := options.Client().ApplyURI(m.ConnectionString).Validate(); err != nil {
		return fmt.Errorf("the connection string is not right: %w", err)
	}
	return nil
}

func (m *MongoDB) checker(env *Env) Checker {
	c := &mongoChecker{m: m, env: env}
	c.run = c.command
	return c
}

func (m *MongoDB) describe() []Fact {
	f := []Fact{{"Database", "MongoDB"}, {"Command", m.Command}}
	if m.JSONPath != "" {
		f = append(f, Fact{"JSON query", m.JSONPath + " " + m.Operator + " " + strconv.Quote(m.Expected)})
	}
	return f
}

type mongoChecker struct {
	m   *MongoDB
	env *Env
	// run runs the command and returns its answer as JSON.
	run func(ctx context.Context) ([]byte, error)
}

// mongoDatabase is the database a URI names, or admin.
func mongoDatabase(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "admin"
	}
	if db := strings.Trim(u.Path, "/"); db != "" {
		return db
	}
	return "admin"
}

func (c *mongoChecker) command(ctx context.Context) ([]byte, error) {
	opts := options.Client().ApplyURI(c.m.ConnectionString).SetDialer(envDialer{c.env}).SetAppName("eyrie").
		SetServerSelectionTimeout(time.Until(deadline(ctx)))
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		client.Disconnect(dctx) //nolint:errcheck,gosec // the check is done
	}()
	var doc bson.D
	if err := bson.UnmarshalExtJSON([]byte(c.m.Command), false, &doc); err != nil {
		return nil, err
	}
	raw, err := client.Database(mongoDatabase(c.m.ConnectionString)).RunCommand(ctx, doc).Raw()
	if err != nil {
		return nil, err
	}
	return bson.MarshalExtJSON(raw, false, false)
}

func (c *mongoChecker) Check(ctx context.Context) Result {
	start := time.Now()
	answer, err := c.run(ctx)
	if err != nil {
		return failErr(sqlError(ctx, err))
	}
	r := Result{Latency: time.Since(start)}
	if c.m.JSONPath == "" {
		r.OK, r.Message = true, "OK: "+snippet(answer)
		return r
	}
	got, err := Query(answer, c.m.JSONPath)
	if err != nil {
		r.Message = sentence(err.Error())
		return r
	}
	r.Message = fmt.Sprintf("%s is %s", c.m.JSONPath, got)
	r.OK = compare(got, c.m.Operator, c.m.Expected)
	if !r.OK {
		r.Message += ", not " + c.m.Operator + " " + c.m.Expected
	}
	return r
}
