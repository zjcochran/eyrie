// SPDX-License-Identifier: AGPL-3.0-or-later

// Command eyrie is a self-hosted uptime and infrastructure monitor.
//
//	eyrie                       run the server (the default)
//	eyrie serve                 the same
//	eyrie healthcheck           exit 0 if the running server answers, for Docker
//	eyrie reset-password <user> set a user's password from standard input
//	eyrie reset-2fa <user>      turn off a user's two-factor sign-in
//	eyrie version               print the version
package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // distroless has no zoneinfo; TZ still works

	"github.com/zjcochran/eyrie/internal/api"
	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/config"
	"github.com/zjcochran/eyrie/internal/engine"
	"github.com/zjcochran/eyrie/internal/secrets"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/ui"
)

const usage = `usage:
  eyrie                       run the server
  eyrie serve                 the same
  eyrie healthcheck           exit 0 if the running server answers
  eyrie reset-password <user> set a user's password, read from standard input
  eyrie reset-2fa <user>      turn off a user's two-factor sign-in
  eyrie version               print the version`

// Set at build time: -ldflags "-X main.version=v1.2.3 -X main.sourceURL=https://…".
// sourceURL is where this build's source code is; Eyrie is under the AGPL,
// so every page links to it. EYRIE_SOURCE_URL overrides it.
var (
	version   = "dev"
	sourceURL = ""
)

func main() {
	os.Exit(cli(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// arguments is how many arguments each command takes.
var arguments = map[string]int{"serve": 0, "healthcheck": 0, "version": 0, "reset-password": 1, "reset-2fa": 1}

// cli runs one invocation and returns the process exit code.
func cli(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if n, ok := arguments[args[0]]; !ok || len(args)-1 != n {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "healthcheck":
		err = healthcheck(getenv)
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = serve(ctx, getenv, stderr, nil)
	case "reset-password":
		err = resetPassword(getenv, args[1], stdin, stdout, stderr)
	case "reset-2fa":
		err = resetTwoFactor(getenv, args[1], stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "eyrie:", err)
		return 1
	}
	return 0
}

// serve runs the server until ctx is cancelled. ready, when set, is called
// with the address once the server listens.
func serve(ctx context.Context, getenv func(string) string, stderr io.Writer, ready func(addr string)) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	st, box, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	if cfg.ForceLocal {
		log.Warn("EYRIE_LOCAL_LOGIN=force: password sign-in works even if single sign-on is the only way in; remove it once you can sign in")
	}
	go prune(ctx, st, log)

	env := check.DefaultEnv("Eyrie/" + version)
	eng := engine.New(st, env, log)
	apiSrv := &api.Server{
		Store: st, Secrets: box, Version: version, Log: log, UI: ui.Handler(ui.Files()),
		BaseURL: cfg.BaseURL, TrustedProxies: cfg.TrustedProxies, ForceLocal: cfg.ForceLocal,
		SourceURL: cmp.Or(cfg.SourceURL, sourceURL), Engine: eng, Env: env, MetricsToken: cfg.MetricsToken,
	}
	srv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}
	eng.Publish = apiSrv.Publish
	engCtx, stopEngine := context.WithCancel(context.Background())
	engDone := make(chan error, 1)
	go func() { engDone <- eng.Run(engCtx) }()
	// The engine stops after the web server, and writes its last heartbeats.
	defer func() {
		stopEngine()
		if err := <-engDone; err != nil {
			log.Error("the monitor engine stopped", "err", err)
		}
	}()
	log.Info("eyrie started", "version", version, "listen", ln.Addr().String(), "database", st.Dialect())
	if ready != nil {
		ready(ln.Addr().String())
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return err
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("eyrie stopped")
	return nil
}

// open makes the data folder (for SQLite), loads the key for stored secrets
// and opens the database.
func open(ctx context.Context, cfg config.Config) (*store.Store, *secrets.Box, error) {
	if !cfg.Postgres() {
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return nil, nil, err
		}
	}
	// The key is made at the first start, so it is backed up with the data
	// folder from then on.
	box, err := secrets.Load(cfg.SecretKey, cfg.SecretKeyFile())
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(ctx, cfg.Database())
	return st, box, err
}

// How long the stats are kept: hourly ones for the 30-day figures, daily
// ones for the 1-year figures.
const (
	hourlyKept = 35 * 24 * time.Hour
	dailyKept  = 400 * 24 * time.Hour
)

// prune deletes expired sessions, and heartbeats and stats past their
// retention, at start and then daily, until ctx ends.
func prune(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		now := time.Now()
		if err := st.PruneSessions(ctx, now); err != nil && ctx.Err() == nil {
			log.Warn("cannot delete expired sessions", "err", err)
		}
		keep := api.DefaultRetention
		_, err := st.GetSetting(ctx, api.SettingRetention, &keep)
		if err == nil {
			err = st.Prune(ctx, now.Add(-time.Duration(keep.HeartbeatDays)*24*time.Hour), now.Add(-hourlyKept), now.Add(-dailyKept))
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("cannot delete old heartbeats", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// userCommand opens the store for a command that changes a user.
func userCommand(getenv func(string) string, name string, run func(context.Context, *store.Store, store.User) error) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, _, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.UserByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no user %q", name)
	}
	if err != nil {
		return err
	}
	return run(ctx, st, u)
}

// resetPassword sets a user's password from the first line of stdin, for
// someone locked out. It also gives a single sign-on account a password.
func resetPassword(getenv func(string) string, name string, stdin io.Reader, stdout, stderr io.Writer) error {
	return userCommand(getenv, name, func(ctx context.Context, st *store.Store, u store.User) error {
		fmt.Fprintf(stderr, "new password for %s (at least %d characters), then Enter:\n", u.Username, store.MinPasswordLength)
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if err := st.SetPassword(ctx, u.ID, strings.TrimRight(line, "\r\n")); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "password for %s changed; their sessions were signed out\n", u.Username)
		return nil
	})
}

// resetTwoFactor turns off a user's two-factor sign-in, for a lost phone
// and lost recovery codes.
func resetTwoFactor(getenv func(string) string, name string, stdout io.Writer) error {
	return userCommand(getenv, name, func(ctx context.Context, st *store.Store, u store.User) error {
		on, err := st.DisableTwoFactor(ctx, u.ID)
		if err != nil {
			return err
		}
		if !on {
			fmt.Fprintf(stdout, "%s does not use two-factor sign-in\n", u.Username)
			return nil
		}
		fmt.Fprintf(stdout, "two-factor sign-in for %s is off; their recovery codes were deleted\n", u.Username)
		return nil
	})
}

// healthcheck asks the running server for /healthz, for Docker's HEALTHCHECK.
func healthcheck(getenv func(string) string) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	host, port, _ := net.SplitHostPort(cfg.Listen) // checked by config.Load
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	return nil
}
