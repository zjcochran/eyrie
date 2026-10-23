// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

// fakeDB is a database/sql driver whose queries answer from a table.
type fakeDB struct{ rows map[string][][]driver.Value }

func (f fakeDB) Connect(context.Context) (driver.Conn, error) { return fakeConn(f), nil }
func (f fakeDB) Driver() driver.Driver                        { return nil }

type fakeConn fakeDB

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no statements") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("no transactions") }

func (c fakeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	rows, ok := c.rows[q]
	if !ok {
		return nil, errors.New(`relation "nope" does not exist`)
	}
	return &fakeRows{rows: rows}, nil
}

type fakeRows struct{ rows [][]driver.Value }

func (r *fakeRows) Columns() []string { return []string{"a"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

func TestSQLQueries(t *testing.T) {
	db := fakeDB{rows: map[string][][]driver.Value{
		"SELECT 1":    {{int64(1)}},
		"SELECT many": {{"a"}, {"b"}, {"c"}},
		"SELECT none": {},
		"SELECT null": {{nil}},
		"SELECT time": {{time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)}},
		"SELECT long": {{[]byte(strings.Repeat("x", 300))}},
	}}
	connect := func(*Env, string) (driver.Connector, error) { return db, nil }
	for q, want := range map[string]string{
		"SELECT 1":    "Query OK: 1",
		"SELECT many": "Query OK: a (3 rows)",
		"SELECT none": "Query OK, no rows",
		"SELECT null": "Query OK: NULL",
		"SELECT time": "Query OK: 2027-01-02T03:04:05Z",
		"SELECT long": "Query OK: " + strings.Repeat("x", 200) + "…",
	} {
		c := &sqlChecker{s: &SQL{ConnectionString: "x", Query: q}, env: testEnv(), connect: connect}
		if r := c.Check(context.Background()); !r.OK || r.Message != want {
			t.Errorf("%s: %+v", q, r)
		}
	}
	c := &sqlChecker{s: &SQL{ConnectionString: "x", Query: "SELECT nope"}, env: testEnv(), connect: connect}
	if r := c.Check(context.Background()); r.OK || r.Message != `Relation "nope" does not exist` {
		t.Errorf("error: %+v", r)
	}
	c.connect = func(*Env, string) (driver.Connector, error) { return nil, errors.New("bad") }
	if r := c.Check(context.Background()); r.OK || r.Message != "The connection string is not right: bad" {
		t.Errorf("connector: %+v", r)
	}
}

// TestSQLDrivers checks that each driver reads its connection strings and
// dials through the Env.
func TestSQLDrivers(t *testing.T) {
	for _, c := range []struct{ typ, dsn, dialed string }{
		{"postgres", "postgres://alice:" + "pw" + "@db.example.com/shop?sslmode=disable", "tcp db.example.com:5432"},
		{"mysql", "mysql://alice:" + "p%40ss%2Fword" + "@db.example.com/shop?timeout=5s", "tcp db.example.com:3306"},
		{"mysql", "alice:pw@tcp(db.example.com:3307)/shop", "tcp db.example.com:3307"},
		{"sqlserver", "sqlserver://alice:" + "pw" + "@db.example.com?database=shop&encrypt=disable", "tcp db.example.com:1433"},
		{"oracledb", "oracle://alice:" + "pw" + "@db.example.com:1521/shop", "tcp db.example.com:1521"},
	} {
		env := testEnv()
		var dialed string
		env.Dial = func(_ context.Context, network, addr string) (net.Conn, error) {
			if dialed == "" {
				dialed = network + " " + addr
			}
			return nil, errors.New("no route to host")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, ch, err := Parse(env, c.typ, []byte(`{"connection_string":"`+c.dsn+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		r := ch.Check(ctx)
		cancel()
		if r.OK || !strings.Contains(strings.ToLower(r.Message), "no route to host") || dialed != c.dialed {
			t.Errorf("%s: %+v, dialed %q", c.dsn, r, dialed)
		}
	}
}

func TestSQLSettings(t *testing.T) {
	env := testEnv()
	for _, c := range []struct{ typ, settings string }{
		{"postgres", `{}`},
		{"postgres", `{"connection_string":"postgres://db.example.com","query":"` + strings.Repeat("x", maxQuery+1) + `"}`},
		{"postgres", `{"connection_string":"postgres://db.example.com:x"}`},
		{"mysql", `{"connection_string":"mysql://"}`},
		{"mysql", `{"connection_string":"mysql://db.example.com/shop?timeout=never"}`},
		{"mysql", `{"connection_string":"nonsense"}`},
		{"sqlserver", `{"connection_string":"sqlserver://db.example.com?encrypt=maybe"}`},
		{"oracledb", `{"connection_string":"db.example.com/shop"}`},
	} {
		if _, err := Prepare(env, c.typ, []byte(c.settings), nil, nil); err == nil {
			t.Errorf("%s %s accepted", c.typ, c.settings)
		}
	}
	saved, err := Prepare(env, "oracledb", []byte(`{"connection_string":"oracle://db.example.com/shop"}`), nil, nil)
	if err != nil || !strings.Contains(string(saved), `"query":"SELECT 1 FROM DUAL"`) {
		t.Errorf("oracle default: %s %v", saved, err)
	}
	// A kept connection string is not read again: it is sealed.
	env.Secrets = sealer{}
	saved, _ = Prepare(env, "postgres", []byte(`{"connection_string":"postgres://db.example.com"}`), nil, nil)
	if _, err := Prepare(env, "postgres", []byte(`{"query":"SELECT 2"}`), saved, nil); err != nil {
		t.Errorf("kept: %v", err)
	}
	if f := Describe("postgres", saved); len(f) != 2 || f[1].Value != "SELECT 1" {
		t.Errorf("describe %v", f)
	}
}

// fakePostgres answers one simple query with one row.
func fakePostgres(c net.Conn) {
	be := pgproto3.NewBackend(c, c)
	msg, err := be.ReceiveStartupMessage()
	if _, ok := msg.(*pgproto3.SSLRequest); ok {
		c.Write([]byte("N"))
		_, err = be.ReceiveStartupMessage()
	}
	if err != nil {
		return
	}
	be.Send(&pgproto3.AuthenticationOk{})
	be.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "18.0"})
	be.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
	be.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
	be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	be.Flush()
	for {
		msg, err := be.Receive()
		if err != nil {
			return
		}
		q, ok := msg.(*pgproto3.Query)
		if !ok {
			return
		}
		if q.String != "SELECT 1" {
			be.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42P01", Message: "relation does not exist"})
		} else {
			be.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("x"), DataTypeOID: 23, DataTypeSize: 4, Format: 0}}})
			be.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("1")}})
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		}
		be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		be.Flush()
	}
}

func TestPostgres(t *testing.T) {
	env := testEnv()
	env.Dial = listenDial(t, fakePostgres, nil)
	if r := run(t, env, "postgres", `{"connection_string":"postgres://alice:`+"pw"+`@db.example.com/shop"}`); !r.OK || r.Message != "Query OK: 1" {
		t.Errorf("postgres: %+v", r)
	}
	if r := run(t, env, "postgres", `{"connection_string":"postgres://alice:`+"pw"+`@db.example.com/shop","query":"SELECT * FROM nope"}`); r.OK ||
		!strings.Contains(r.Message, "relation does not exist") {
		t.Errorf("error: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, c, _ := Parse(env, "postgres", []byte(`{"connection_string":"postgres://db.example.com"}`))
	if r := c.Check(ctx); r.OK || r.Message != "Timed out" {
		t.Errorf("cancelled: %+v", r)
	}
}
