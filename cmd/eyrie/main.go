// Command eyrie is a self-hosted uptime and infrastructure monitor.
//
//	eyrie              run the server (the default)
//	eyrie serve        the same
//	eyrie healthcheck  exit 0 if the running server answers, for Docker
//	eyrie version      print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // distroless has no zoneinfo; TZ still works

	"github.com/zjcochran/eyrie/internal/api"
	"github.com/zjcochran/eyrie/internal/config"
	"github.com/zjcochran/eyrie/internal/secrets"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/ui"
)

const usage = `usage:
  eyrie              run the server
  eyrie serve        the same
  eyrie healthcheck  exit 0 if the running server answers
  eyrie version      print the version`

// Set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	os.Exit(cli(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// cli runs one invocation and returns the process exit code.
func cli(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	if len(args) > 1 {
		cmd = "" // no command takes arguments yet
	}
	var err error
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "healthcheck":
		err = healthcheck(getenv)
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = serve(ctx, getenv, stderr, nil)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
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
	if !cfg.Postgres() {
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return err
		}
	}
	// The key for stored secrets is made now, so it exists from the first
	// start and is backed up with the data folder.
	if _, err := secrets.Load(cfg.SecretKey, cfg.SecretKeyFile()); err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.Database())
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{
		Handler: (&api.Server{
			Store: st, Version: version, Log: log, UI: ui.Handler(ui.Files()),
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}
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
