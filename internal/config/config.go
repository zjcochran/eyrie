// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config reads Eyrie's settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
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
	// BaseURL is EYRIE_BASE_URL: how browsers reach Eyrie, without a
	// trailing slash; empty to use the address of each request.
	BaseURL string
	// TrustedProxies is EYRIE_TRUSTED_PROXIES: reverse proxies whose
	// X-Forwarded-* headers are believed.
	TrustedProxies []netip.Prefix
	// ForceLocal is EYRIE_LOCAL_LOGIN=force: password sign-in works even
	// when single sign-on is set to be the only way in.
	ForceLocal bool
	// SourceURL is EYRIE_SOURCE_URL: where this version's source code is,
	// linked from every page (AGPL section 13); empty for the one set at
	// build time.
	SourceURL string
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
	c.BaseURL = strings.TrimRight(get("EYRIE_BASE_URL", ""), "/")
	if !webURL(c.BaseURL, false) {
		return c, fmt.Errorf("EYRIE_BASE_URL %q: want a URL such as https://eyrie.example.com", c.BaseURL)
	}
	if c.SourceURL = get("EYRIE_SOURCE_URL", ""); !webURL(c.SourceURL, true) {
		return c, fmt.Errorf("EYRIE_SOURCE_URL %q: want a URL such as https://git.example.com/eyrie", c.SourceURL)
	}
	proxies, err := ParseProxies(SplitList(getenv("EYRIE_TRUSTED_PROXIES")))
	if err != nil {
		return c, fmt.Errorf("EYRIE_TRUSTED_PROXIES: %w", err)
	}
	c.TrustedProxies = proxies
	switch v := strings.ToLower(get("EYRIE_LOCAL_LOGIN", "")); v {
	case "":
	case "force":
		c.ForceLocal = true
	default:
		return c, fmt.Errorf("EYRIE_LOCAL_LOGIN %q: the only value is force", v)
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

// webURL reports whether s is empty or an http(s) URL with a host; a query
// or fragment is allowed only when query is true.
func webURL(s string, query bool) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return query || (u.RawQuery == "" && u.Fragment == "")
}

// SplitList splits a list separated by commas or white space.
func SplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

// ParseProxies parses IP addresses and CIDR ranges.
func ParseProxies(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", s)
		}
		ip = ip.Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	return out, nil
}
