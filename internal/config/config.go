// Package config reads Eyrie's settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
)

// Config is everything set through the environment.
type Config struct {
	Listen      string     // EYRIE_LISTEN: address the web server listens on
	DataDir     string     // EYRIE_DATA_DIR: the SQLite database and the secret key file
	DatabaseURL string     // EYRIE_DATABASE_URL: postgres:// to use PostgreSQL; empty for SQLite
	SecretKey   string     // EYRIE_SECRET_KEY: replaces the key file when set
	LogLevel    slog.Level // EYRIE_LOG_LEVEL: debug, info, warn or error
}

// Defaults.
const (
	DefaultListen  = ":8080"
	DefaultDataDir = "/data"
)

// Load reads the configuration through getenv (os.Getenv outside tests).
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Listen:      get("EYRIE_LISTEN", DefaultListen),
		DataDir:     get("EYRIE_DATA_DIR", DefaultDataDir),
		DatabaseURL: get("EYRIE_DATABASE_URL", ""),
		SecretKey:   getenv("EYRIE_SECRET_KEY"),
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return c, fmt.Errorf("EYRIE_LISTEN %q: want host:port or :port", c.Listen)
	}
	if c.DatabaseURL != "" && !c.Postgres() {
		return c, fmt.Errorf("EYRIE_DATABASE_URL must start with postgres:// or postgresql://; leave it empty for SQLite in EYRIE_DATA_DIR")
	}
	level := get("EYRIE_LOG_LEVEL", "info")
	if err := c.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return c, fmt.Errorf("EYRIE_LOG_LEVEL %q: want debug, info, warn or error", level)
	}
	return c, nil
}

// Postgres reports whether the database is PostgreSQL.
func (c Config) Postgres() bool {
	return strings.HasPrefix(c.DatabaseURL, "postgres://") || strings.HasPrefix(c.DatabaseURL, "postgresql://")
}

// Database is what store.Open takes: the PostgreSQL URL, or the SQLite file.
func (c Config) Database() string {
	if c.Postgres() {
		return c.DatabaseURL
	}
	return filepath.Join(c.DataDir, "eyrie.db")
}

// SecretKeyFile is where the key for stored secrets is kept when
// EYRIE_SECRET_KEY is not set.
func (c Config) SecretKeyFile() string { return filepath.Join(c.DataDir, "secret.key") }
