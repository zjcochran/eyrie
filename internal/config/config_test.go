// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.DataDir != "/data" || c.LogLevel != slog.LevelInfo || c.Postgres() ||
		c.BaseURL != "" || c.TrustedProxies != nil || c.ForceLocal || c.ClaudeCommand != "claude" {
		t.Errorf("defaults = %+v", c)
	}
	if c.Database() != "/data/eyrie.db" || c.SecretKeyFile() != "/data/secret.key" {
		t.Errorf("paths = %s %s", c.Database(), c.SecretKeyFile())
	}
}

func TestSet(t *testing.T) {
	c, err := Load(env(map[string]string{
		"EYRIE_LISTEN":          "127.0.0.1:9000",
		"EYRIE_DATA_DIR":        "/srv/eyrie",
		"EYRIE_DATABASE_URL":    "postgresql://eyrie@db.example.com/eyrie",
		"EYRIE_SECRET_KEY":      "k",
		"EYRIE_LOG_LEVEL":       "DEBUG",
		"EYRIE_BASE_URL":        "https://eyrie.example.com/",
		"EYRIE_TRUSTED_PROXIES": "192.0.2.10, 198.51.100.0/24\n2001:db8::/32 ::ffff:203.0.113.5",
		"EYRIE_LOCAL_LOGIN":     "Force",
		"EYRIE_METRICS_TOKEN":   " t0ken ",
		"EYRIE_SOURCE_URL":      "https://git.example.com/eyrie",
		"EYRIE_CLAUDE_COMMAND":  "/opt/claude/bin/claude",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.SecretKey != "k" || c.LogLevel != slog.LevelDebug || !c.Postgres() {
		t.Errorf("config = %+v", c)
	}
	if c.BaseURL != "https://eyrie.example.com" || !c.ForceLocal || c.SourceURL != "https://git.example.com/eyrie" || c.MetricsToken != "t0ken" ||
		c.ClaudeCommand != "/opt/claude/bin/claude" {
		t.Errorf("base URL %q, force %v, claude %q", c.BaseURL, c.ForceLocal, c.ClaudeCommand)
	}
	var proxies []string
	for _, p := range c.TrustedProxies {
		proxies = append(proxies, p.String())
	}
	if strings.Join(proxies, " ") != "192.0.2.10/32 198.51.100.0/24 2001:db8::/32 203.0.113.5/32" {
		t.Errorf("proxies %v", proxies)
	}
	if c.Database() != "postgresql://eyrie@db.example.com/eyrie" || c.SecretKeyFile() != "/srv/eyrie/secret.key" {
		t.Errorf("paths = %s %s", c.Database(), c.SecretKeyFile())
	}
}

func TestInvalid(t *testing.T) {
	for _, c := range []struct{ name, value string }{
		{"EYRIE_LISTEN", "8080"},
		{"EYRIE_DATABASE_URL", "mysql://db.example.com"},
		{"EYRIE_LOG_LEVEL", "loud"},
		{"EYRIE_BASE_URL", "eyrie.example.com"},
		{"EYRIE_BASE_URL", "https://eyrie.example.com/?x=1"},
		{"EYRIE_TRUSTED_PROXIES", "192.0.2.1, proxy.example.com"},
		{"EYRIE_LOCAL_LOGIN", "yes"},
		{"EYRIE_SOURCE_URL", "git.example.com/eyrie"},
	} {
		if _, err := Load(env(map[string]string{c.name: c.value})); err == nil || !strings.Contains(err.Error(), c.name) {
			t.Errorf("%s=%s: error %v", c.name, c.value, err)
		}
	}
}
