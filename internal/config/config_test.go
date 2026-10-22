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
	if c.Listen != ":8080" || c.DataDir != "/data" || c.LogLevel != slog.LevelInfo || c.Postgres() {
		t.Errorf("defaults = %+v", c)
	}
	if c.Database() != "/data/eyrie.db" || c.SecretKeyFile() != "/data/secret.key" {
		t.Errorf("paths = %s %s", c.Database(), c.SecretKeyFile())
	}
}

func TestSet(t *testing.T) {
	c, err := Load(env(map[string]string{
		"EYRIE_LISTEN":       "127.0.0.1:9000",
		"EYRIE_DATA_DIR":     "/srv/eyrie",
		"EYRIE_DATABASE_URL": "postgresql://eyrie@db.example.com/eyrie",
		"EYRIE_SECRET_KEY":   "k",
		"EYRIE_LOG_LEVEL":    "DEBUG",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.SecretKey != "k" || c.LogLevel != slog.LevelDebug || !c.Postgres() {
		t.Errorf("config = %+v", c)
	}
	if c.Database() != "postgresql://eyrie@db.example.com/eyrie" || c.SecretKeyFile() != "/srv/eyrie/secret.key" {
		t.Errorf("paths = %s %s", c.Database(), c.SecretKeyFile())
	}
}

func TestInvalid(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"EYRIE_LISTEN":       {"EYRIE_LISTEN": "8080"},
		"EYRIE_DATABASE_URL": {"EYRIE_DATABASE_URL": "mysql://db.example.com"},
		"EYRIE_LOG_LEVEL":    {"EYRIE_LOG_LEVEL": "loud"},
	} {
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error %v", name, err)
		}
	}
}
