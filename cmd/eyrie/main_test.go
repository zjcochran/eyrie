package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestVersionAndUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli([]string{"version"}, env(nil), nil, &out, &errOut); code != 0 || out.String() != "dev\n" {
		t.Errorf("version: %d %q", code, out.String())
	}
	for _, args := range [][]string{{"nonsense"}, {"serve", "extra"}, {"reset-password"}, {"reset-2fa", "a", "b"}} {
		errOut.Reset()
		if code := cli(args, env(nil), nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage:") {
			t.Errorf("%v: %d %q", args, code, errOut.String())
		}
	}
}

func TestServe(t *testing.T) {
	dir := t.TempDir()
	vars := env(map[string]string{"EYRIE_LISTEN": "127.0.0.1:0", "EYRIE_DATA_DIR": filepath.Join(dir, "data"),
		"EYRIE_LOCAL_LOGIN": "force"})
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan string, 1)
	done := make(chan error, 1)
	var log bytes.Buffer
	go func() { done <- serve(ctx, vars, &log, func(a string) { addrs <- a }) }()
	var addr string
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatalf("serve stopped: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Errorf("healthz: %d %q", resp.StatusCode, body)
	}

	// The health check command reaches it.
	if err := healthcheck(env(map[string]string{"EYRIE_LISTEN": addr})); err != nil {
		t.Errorf("healthcheck: %v", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("shutdown: %v", err)
	}
	for _, f := range []string{"eyrie.db", "secret.key"} {
		if _, err := os.Stat(filepath.Join(dir, "data", f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if !strings.Contains(log.String(), "eyrie started") || !strings.Contains(log.String(), "eyrie stopped") ||
		!strings.Contains(log.String(), "EYRIE_LOCAL_LOGIN=force") {
		t.Errorf("log: %s", log.String())
	}
}

func TestServeErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	for name, vars := range map[string]map[string]string{
		"config":       {"EYRIE_LOG_LEVEL": "loud"},
		"data folder":  {"EYRIE_DATA_DIR": filepath.Join(file, "data")},
		"secret key":   {"EYRIE_DATA_DIR": dir, "EYRIE_SECRET_KEY": ""},
		"listen":       {"EYRIE_DATA_DIR": t.TempDir(), "EYRIE_LISTEN": "203.0.113.1:-1"},
		"database url": {"EYRIE_DATA_DIR": t.TempDir(), "EYRIE_DATABASE_URL": "postgres://%zz"},
	} {
		if name == "secret key" {
			// A folder where the key file should be.
			os.Mkdir(filepath.Join(dir, "secret.key"), 0o700)
		}
		if err := serve(ctx, env(vars), io.Discard, nil); err == nil {
			t.Errorf("%s: serve started", name)
		}
	}
	if code := cli(nil, env(map[string]string{"EYRIE_LOG_LEVEL": "loud"}), nil, io.Discard, io.Discard); code != 1 {
		t.Errorf("cli serve with bad config: %d", code)
	}
}

func TestHealthcheckFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	var errOut bytes.Buffer
	if code := cli([]string{"healthcheck"}, env(map[string]string{"EYRIE_LISTEN": addr}), nil, io.Discard, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "status 503") {
		t.Errorf("unhealthy: %d %q", code, errOut.String())
	}
	if err := healthcheck(env(map[string]string{"EYRIE_LISTEN": "bad"})); err == nil {
		t.Error("bad EYRIE_LISTEN accepted")
	}
}

func TestResetCommands(t *testing.T) {
	dir := t.TempDir()
	vars := env(map[string]string{"EYRIE_DATA_DIR": dir})
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := cli(args, vars, strings.NewReader(stdin), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, _, errOut := run("", "reset-2fa", "alice"); code != 1 || !strings.Contains(errOut, `no user "alice"`) {
		t.Errorf("unknown user: %d %q", code, errOut)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateFirstAdmin(ctx, "alice", "correct horse battery", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	st.EnableTwoFactor(ctx, u.ID, "v1:sealed", 1, nil)
	st.Close()

	if code, out, errOut := run("a new long password\n", "reset-password", "ALICE"); code != 0 ||
		!strings.Contains(out, "password for alice changed") || !strings.Contains(errOut, "new password for alice") {
		t.Errorf("reset-password: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := run("short", "reset-password", "alice"); code != 1 || !strings.Contains(errOut, "12 characters") {
		t.Errorf("short password: %d %q", code, errOut)
	}
	if code, out, _ := run("", "reset-2fa", "alice"); code != 0 || !strings.Contains(out, "is off") {
		t.Errorf("reset-2fa: %d %q", code, out)
	}
	if code, out, _ := run("", "reset-2fa", "alice"); code != 0 || !strings.Contains(out, "does not use") {
		t.Errorf("reset-2fa again: %d %q", code, out)
	}
	st, _ = store.Open(ctx, filepath.Join(dir, "eyrie.db"))
	defer st.Close()
	if _, err := st.Authenticate(ctx, "alice", "a new long password", time.Now()); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
	if code := cli([]string{"reset-2fa", "alice"}, env(map[string]string{"EYRIE_LOG_LEVEL": "loud"}), nil, io.Discard, io.Discard); code != 1 {
		t.Errorf("bad config: %d", code)
	}
	if code := cli([]string{"reset-2fa", "alice"}, env(map[string]string{"EYRIE_DATA_DIR": filepath.Join(dir, "eyrie.db", "x")}), nil, io.Discard, io.Discard); code != 1 {
		t.Errorf("bad data folder: %d", code)
	}
	if code := cli([]string{"reset-password", "alice"}, vars, failing{}, io.Discard, io.Discard); code != 1 {
		t.Errorf("unreadable stdin: %d", code)
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, os.ErrClosed }

func TestPruneSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	st.Close() // the prune fails and is logged
	done := make(chan struct{})
	go func() { pruneSessions(ctx, st, slog.New(slog.NewTextHandler(&log, nil))); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if !strings.Contains(log.String(), "cannot delete expired sessions") {
		t.Errorf("log %q", log.String())
	}
}
