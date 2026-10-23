// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	mssql "github.com/microsoft/go-mssqldb"
	goora "github.com/sijms/go-ora/v2"
)

// SQL is the settings of the postgres, mysql, sqlserver and oracledb
// types: a query must run.
type SQL struct {
	// ConnectionString is the database's URL or DSN, with its password.
	ConnectionString string `json:"connection_string"`
	// Query is run; by default SELECT 1 (SELECT 1 FROM DUAL on Oracle).
	Query string `json:"query"`

	kind string // postgres, mysql, sqlserver or oracledb
}

func (s *SQL) secrets() map[string]*string {
	return map[string]*string{"connection_string": &s.ConnectionString}
}

// maxQuery is the longest query accepted.
const maxQuery = 10000

func (s *SQL) normalize() error {
	if s.ConnectionString == "" {
		return errors.New("the connection string is required")
	}
	s.Query = strings.TrimSpace(s.Query)
	if s.Query == "" {
		s.Query = "SELECT 1"
		if s.kind == "oracledb" {
			s.Query = "SELECT 1 FROM DUAL"
		}
	}
	if len(s.Query) > maxQuery {
		return errors.New("the query is at most 10000 characters")
	}
	return nil
}

// checkPlain checks a connection string that was just typed in, before it
// is sealed.
func (s *SQL) checkPlain(plain func(name string) bool) error {
	if !plain("connection_string") {
		return nil
	}
	if _, err := sqlConnectors[s.kind](parseOnly, s.ConnectionString); err != nil {
		return fmt.Errorf("the connection string is not right: %w", err)
	}
	return nil
}

// parseOnly is an Env for reading connection strings: it never connects.
var parseOnly = &Env{Dial: func(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("not connecting")
}}

func (s *SQL) checker(env *Env) Checker {
	return &sqlChecker{s: s, env: env, connect: sqlConnectors[s.kind]}
}

func (s *SQL) describe() []Fact {
	return []Fact{{"Database", s.kind}, {"Query", s.Query}}
}

// sqlConnectors make a driver.Connector, dialing through the Env, for each
// type.
var sqlConnectors = map[string]func(env *Env, dsn string) (driver.Connector, error){
	"postgres":  postgresConnector,
	"mysql":     mysqlConnector,
	"sqlserver": sqlserverConnector,
	"oracledb":  oracleConnector,
}

func postgresConnector(env *Env, dsn string) (driver.Connector, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), "cannot parse `"+dsn+"`: "))
	}
	cfg.DialFunc = env.Dial
	// The host is dialed by name: Env.Dial resolves it.
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	// The simple protocol works through every pooler and runs the query in
	// one round trip.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return stdlib.GetConnector(*cfg), nil
}

func mysqlConnector(env *Env, dsn string) (driver.Connector, error) {
	cfg, err := mysqlConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.DialFunc = env.Dial
	return mysql.NewConnector(cfg)
}

// mysqlConfig reads a mysql://host:port/database URL (with user:password@
// before the host), as
// Uptime Kuma takes, or the driver's own DSN (user:password@tcp(host)/db).
func mysqlConfig(dsn string) (*mysql.Config, error) {
	if !strings.HasPrefix(dsn, "mysql://") && !strings.HasPrefix(dsn, "mariadb://") {
		return mysql.ParseDSN(dsn)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return nil, errors.New("not a mysql://host:port/database URL")
	}
	// The driver reads the parameters; the rest is set as is, so a password
	// can hold any character.
	cfg, err := mysql.ParseDSN(u.Path + "?" + u.RawQuery)
	if err != nil {
		return nil, err
	}
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.Net, cfg.Addr = "tcp", u.Host
	if u.Port() == "" {
		cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	return cfg, nil
}

// envDialer dials through an Env, for drivers that take a dialer.
type envDialer struct{ env *Env }

func (d envDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.env.Dial(ctx, network, addr)
}

// hostDialer is an envDialer that SQL Server's driver gives host names
// rather than addresses it looked up itself.
type hostDialer struct{ envDialer }

func (hostDialer) HostName() string { return "" }

func sqlserverConnector(env *Env, dsn string) (driver.Connector, error) {
	c, err := mssql.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	c.Dialer = hostDialer{envDialer{env}}
	return c, nil
}

func oracleConnector(env *Env, dsn string) (driver.Connector, error) {
	if !strings.HasPrefix(dsn, "oracle://") {
		return nil, errors.New("not an oracle://host:port/service URL")
	}
	c, err := goora.NewDriver().OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	c.(*goora.OracleConnector).Dialer(envDialer{env})
	return c, nil
}

type sqlChecker struct {
	s       *SQL
	env     *Env
	connect func(env *Env, dsn string) (driver.Connector, error)
}

func (c *sqlChecker) Check(ctx context.Context) Result {
	conn, err := c.connect(c.env, c.s.ConnectionString)
	if err != nil {
		return fail("The connection string is not right: %s", err)
	}
	db := sql.OpenDB(conn)
	defer db.Close()
	start := time.Now()
	rows, err := db.QueryContext(ctx, c.s.Query)
	if err != nil {
		return failErr(sqlError(ctx, err))
	}
	defer rows.Close()
	n, first, err := readRows(rows)
	if err != nil {
		return failErr(sqlError(ctx, err))
	}
	r := Result{OK: true, Latency: time.Since(start)}
	switch n {
	case 0:
		r.Message = "Query OK, no rows"
	case 1:
		r.Message = "Query OK: " + first
	default:
		r.Message = fmt.Sprintf("Query OK: %s (%d rows)", first, n)
	}
	return r
}

// maxRows is the most rows read; more are counted as this many.
const maxRows = 1000

// readRows counts the rows, up to maxRows, and returns the first column of
// the first row as text.
func readRows(rows *sql.Rows) (int, string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return 0, "", err
	}
	n, first := 0, ""
	for n < maxRows && rows.Next() {
		if n == 0 && len(cols) > 0 {
			vals := make([]any, len(cols))
			for i := range vals {
				vals[i] = new(any)
			}
			if err := rows.Scan(vals...); err != nil {
				return 0, "", err
			}
			first = sqlText(*vals[0].(*any))
		}
		n++
	}
	return n, first, rows.Err()
}

func sqlText(v any) string {
	var s string
	switch v := v.(type) {
	case nil:
		s = "NULL"
	case []byte:
		s = string(v)
	case time.Time:
		s = v.UTC().Format(time.RFC3339)
	default:
		s = fmt.Sprint(v)
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// sqlError says what went wrong, with a timeout said plainly.
func sqlError(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return errTimeout
	}
	return err
}
