// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is Eyrie's agent mode (eyrie agent): it runs where Eyrie
// cannot see, next to a Docker engine, on a swarm manager, in a Kubernetes
// cluster or beside a Proxmox VE cluster, lists the workloads there and
// reports them to Eyrie. It only reads, calls Eyrie and opens no port.
package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/workload"
)

// Platforms an agent can list.
const (
	PlatformDocker     = "docker"
	PlatformSwarm      = "swarm"
	PlatformKubernetes = "kubernetes"
	PlatformProxmox    = "proxmox"
)

// Platforms are the values of EYRIE_AGENT_PLATFORM.
var Platforms = []string{PlatformDocker, PlatformSwarm, PlatformKubernetes, PlatformProxmox}

// Files in the agent's folder.
const (
	keyFile       = "key"
	nextKeyFile   = "key.next"
	stateFile     = "state.json"
	heartbeatFile = "heartbeat"
)

// refusedWait is how long the agent waits after Eyrie refuses it.
const refusedWait = 5 * time.Minute

// Config is the agent's settings, from the environment.
type Config struct {
	URL        string // Eyrie's address, as browsers reach it
	Token      string // the one-time enrollment token; ignored once enrolled
	Platform   string
	Dir        string // where the key and state are kept
	DockerHost string
	Namespaces []string // Kubernetes namespaces to list; empty: all
	// Proxmox VE's API and token, and its certificate's SHA-256.
	ProxmoxURL, ProxmoxTokenID, ProxmoxSecret, ProxmoxFingerprint string
	Version                                                       string
	Hostname                                                      string
}

// ConfigFromEnv reads the agent's settings.
func ConfigFromEnv(getenv func(string) string, version string) (Config, error) {
	get := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		URL: strings.TrimRight(get("EYRIE_AGENT_URL", ""), "/"), Token: get("EYRIE_AGENT_TOKEN", ""),
		Platform: get("EYRIE_AGENT_PLATFORM", PlatformDocker), Dir: get("EYRIE_AGENT_DIR", "/data/agent"),
		DockerHost: get("DOCKER_HOST", "unix:///var/run/docker.sock"),
		ProxmoxURL: get("PROXMOX_URL", ""), ProxmoxTokenID: get("PROXMOX_TOKEN_ID", ""),
		ProxmoxSecret: get("PROXMOX_TOKEN_SECRET", ""), ProxmoxFingerprint: get("PROXMOX_FINGERPRINT", ""),
		Version: version,
	}
	for ns := range strings.SplitSeq(getenv("EYRIE_AGENT_NAMESPACES"), ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			c.Namespaces = append(c.Namespaces, ns)
		}
	}
	if !slices.Contains(Platforms, c.Platform) {
		return c, fmt.Errorf("EYRIE_AGENT_PLATFORM must be one of %s", strings.Join(Platforms, ", "))
	}
	u, err := url.Parse(c.URL)
	if c.URL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" {
		return c, errors.New("EYRIE_AGENT_URL must be Eyrie's address, such as https://eyrie.example.com")
	}
	c.Hostname, _ = os.Hostname()
	return c, nil
}

// Events calls changed when a workload may have changed, until ctx ends or
// the stream breaks.
type Events func(ctx context.Context, changed func()) error

// Open connects to the configured platform. getenv reads the environment,
// for the Kubernetes ServiceAccount's address.
func Open(ctx context.Context, cfg Config, getenv func(string) string) (workload.Lister, Events, error) {
	switch cfg.Platform {
	case PlatformKubernetes:
		k, err := workload.InCluster(getenv, "")
		if err != nil {
			return nil, nil, err
		}
		k.Namespaces = cfg.Namespaces
		return k, nil, nil
	case PlatformProxmox:
		p, err := workload.NewProxmox(cfg.ProxmoxURL, cfg.ProxmoxTokenID, cfg.ProxmoxSecret, cfg.ProxmoxFingerprint)
		return p, nil, err
	}
	d, err := workload.NewDocker(cfg.DockerHost)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Platform == PlatformSwarm {
		s := workload.Swarm{Docker: d}
		if err := s.CheckManager(ctx); err != nil {
			return nil, nil, err
		}
		return s, d.Events, nil
	}
	return d, d.Events, nil
}

// Agent is one running agent.
type Agent struct {
	Cfg         Config
	Lister      workload.Lister
	Events      Events // may be nil
	HTTP        *http.Client
	Log         *slog.Logger
	Now         func() time.Time
	ReportEvery time.Duration
	// Settle is how long after a change the agent reports, so a burst of
	// events makes one report.
	Settle time.Duration
	// Reconnect is how long the agent waits before following events again
	// when their stream breaks.
	Reconnect time.Duration

	rot sync.Mutex // one key rotation at a time
	mu  sync.Mutex
	key agentauth.Key
	st  state
}

// state is what the agent keeps between runs, besides its key.
type state struct {
	Agent      int64     `json:"agent"`
	Name       string    `json:"name"`
	KeyCreated time.Time `json:"key_created"`
	// Token is the SHA-256 of the token it enrolled with: a different
	// EYRIE_AGENT_TOKEN enrolls it again.
	Token string `json:"token"`
}

func tokenHash(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// New returns an agent with the usual timings.
func New(cfg Config, lister workload.Lister, events Events, log *slog.Logger) *Agent {
	return &Agent{
		Cfg: cfg, Lister: lister, Events: events, Log: log, Now: time.Now,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		ReportEvery: agentauth.ReportEvery, Settle: 2 * time.Second, Reconnect: 5 * time.Second,
	}
}

// Run enrolls if needed, then reports and rotates its key until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if err := os.MkdirAll(a.Cfg.Dir, 0o700); err != nil {
		return err
	}
	if err := a.loadKey(); err != nil {
		return err
	}
	if err := a.load(); err != nil {
		return err
	}
	if strings.HasPrefix(a.Cfg.URL, "http://") {
		a.Log.Warn("Eyrie is reached over plain HTTP: reports are signed, but anyone on the way can read them")
	}
	if a.st.Agent == 0 || (a.Cfg.Token != "" && tokenHash(a.Cfg.Token) != a.st.Token) {
		if err := a.enroll(ctx); err != nil {
			return err
		}
	}
	a.Log.Info("agent running", "agent", a.st.Name, "eyrie", a.Cfg.URL, "platform", a.Cfg.Platform, "key", a.key.Public().Fingerprint())
	if err := a.finishRotation(ctx); err != nil {
		a.Log.Warn("cannot finish an earlier key rotation; trying again later", "err", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { a.reportLoop(ctx) })
	wg.Go(func() { a.rotateLoop(ctx) })
	wg.Wait()
	return nil
}

func (a *Agent) path(name string) string { return filepath.Join(a.Cfg.Dir, name) }

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (a *Agent) loadKey() error {
	b, err := os.ReadFile(a.path(keyFile))
	if errors.Is(err, os.ErrNotExist) {
		k, err := agentauth.NewKey()
		if err != nil {
			return err
		}
		a.key = k
		return writeFile(a.path(keyFile), []byte(k.String()+"\n"))
	}
	if err != nil {
		return err
	}
	a.key, err = agentauth.ParseKey(string(b))
	return err
}

func (a *Agent) load() error {
	b, err := os.ReadFile(a.path(stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &a.st)
}

func (a *Agent) save() error {
	b, err := json.MarshalIndent(a.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(a.path(stateFile), b)
}

// HealthyFor is how recent the last report must be for the health check.
const HealthyFor = 90 * time.Second

// Healthy reports whether the agent in dir reported to Eyrie recently.
func Healthy(dir string, now time.Time) error {
	fi, err := os.Stat(filepath.Join(dir, heartbeatFile))
	if err != nil {
		return errors.New("the agent has not reported to Eyrie yet")
	}
	if age := now.Sub(fi.ModTime()); age > HealthyFor {
		return fmt.Errorf("the agent last reported to Eyrie %s ago", age.Round(time.Second))
	}
	return nil
}

func (a *Agent) heartbeat() {
	now := a.Now()
	p := a.path(heartbeatFile)
	if err := os.WriteFile(p, nil, 0o600); err == nil {
		os.Chtimes(p, now, now) //nolint:errcheck,gosec // only the health check reads it
	}
}

// errRefused is Eyrie refusing the agent: removed, re-enrolled or a bad
// key.
var errRefused = errors.New("refused by Eyrie")

// do sends a signed request.
func (a *Agent) do(ctx context.Context, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	a.mu.Lock()
	key, id := a.key, a.st.Agent
	a.mu.Unlock()
	auth, err := agentauth.SignRequest(key, id, http.MethodPost, path, raw, a.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.URL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	return a.send(req, nil)
}

func (a *Agent) send(req *http.Request, out any) error {
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg := func() string {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var e struct{ Error string }
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return e.Error
		}
		return strings.TrimSpace(string(b))
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s", errRefused, msg())
	case resp.StatusCode >= 300:
		return fmt.Errorf("Eyrie answered %s: %s", resp.Status, msg()) //nolint:staticcheck // ST1005: Eyrie is a name
	case out != nil:
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}

// enroll trades the one-time token for the agent's identity.
func (a *Agent) enroll(ctx context.Context) error {
	if a.Cfg.Token == "" {
		return errors.New("not enrolled yet: set EYRIE_AGENT_TOKEN to the token from Settings → Agents in Eyrie")
	}
	if a.st.Agent != 0 {
		// Enrolled before: a new token gets a new key too.
		k, err := agentauth.NewKey()
		if err != nil {
			return err
		}
		if err := writeFile(a.path(keyFile), []byte(k.String()+"\n")); err != nil {
			return err
		}
		a.key = k
	}
	body, err := json.Marshal(agentauth.EnrollRequest{Token: a.Cfg.Token, PublicKey: a.key.Public().String(),
		Hostname: a.Cfg.Hostname, Platform: a.Cfg.Platform, Version: a.Cfg.Version})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.URL+agentauth.PathEnroll, bytes.NewReader(body))
	if err != nil {
		return err
	}
	var resp agentauth.EnrollResponse
	if err := a.send(req, &resp); err != nil {
		return fmt.Errorf("enrollment failed: %w", err)
	}
	a.st = state{Agent: resp.Agent, Name: resp.Name, KeyCreated: a.Now(), Token: tokenHash(a.Cfg.Token)}
	if err := a.save(); err != nil {
		return err
	}
	a.Log.Info("enrolled", "agent", resp.Name)
	return nil
}

// reportLoop reports at start, every ReportEvery, and soon after events.
func (a *Agent) reportLoop(ctx context.Context) {
	changed := make(chan struct{}, 1)
	if a.Events != nil {
		go a.watch(ctx, changed)
	}
	tick := time.NewTicker(a.ReportEvery)
	defer tick.Stop()
	for {
		pause := a.reportOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-changed:
			select { // let a burst of events settle
			case <-ctx.Done():
				return
			case <-time.After(a.Settle):
			}
		}
	}
}

// watch follows the platform's events, reconnecting when the stream
// breaks.
func (a *Agent) watch(ctx context.Context, changed chan<- struct{}) {
	for ctx.Err() == nil {
		err := a.Events(ctx, func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
		if ctx.Err() != nil {
			return
		}
		a.Log.Warn("the event stream ended; reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.Reconnect):
		}
	}
}

// reportOnce lists the workloads and reports them, and returns how long to
// wait before the next report beyond the usual: long after a refusal.
func (a *Agent) reportOnce(ctx context.Context) time.Duration {
	rep := agentauth.Report{Version: a.Cfg.Version, Platform: a.Cfg.Platform, Hostname: a.Cfg.Hostname, Workloads: []workload.Workload{}}
	ws, err := a.Lister.List(ctx)
	if err != nil {
		rep.Error = err.Error()
		a.Log.Warn("cannot list the workloads", "err", err)
	} else {
		rep.Workloads = ws
	}
	err = a.do(ctx, agentauth.PathReport, rep)
	switch {
	case ctx.Err() != nil:
	case errors.Is(err, errRefused):
		a.Log.Error("Eyrie refuses this agent; if it was removed or given a new token, enroll it again", "err", err)
		return refusedWait
	case err != nil:
		a.Log.Warn("cannot report to Eyrie", "err", err)
	default:
		a.heartbeat()
	}
	return 0
}

func (a *Agent) rotateLoop(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		a.mu.Lock()
		due := a.Now().Sub(a.st.KeyCreated) >= agentauth.RotateEvery
		a.mu.Unlock()
		if due {
			if err := a.rotate(ctx); err != nil && ctx.Err() == nil {
				a.Log.Warn("key rotation failed; trying again in an hour", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// rotate makes a new key and gives it to Eyrie. The new key is saved
// before it is sent, so a rotation cut short is finished at the next
// start.
func (a *Agent) rotate(ctx context.Context) error {
	a.rot.Lock()
	defer a.rot.Unlock()
	k, err := agentauth.NewKey()
	if err != nil {
		return err
	}
	if err := writeFile(a.path(nextKeyFile), []byte(k.String()+"\n")); err != nil {
		return err
	}
	return a.sendRotation(ctx, k)
}

func (a *Agent) sendRotation(ctx context.Context, k agentauth.Key) error {
	a.mu.Lock()
	id := a.st.Agent
	a.mu.Unlock()
	ts := a.Now().Unix()
	req := agentauth.RotateRequest{NewKey: k.Public().String(), TS: ts, Proof: k.Sign(agentauth.RotationProof(id, k.Public(), ts))}
	if err := a.do(ctx, agentauth.PathRotate, req); err != nil {
		return err
	}
	if err := os.Rename(a.path(nextKeyFile), a.path(keyFile)); err != nil {
		return err
	}
	a.mu.Lock()
	a.key, a.st.KeyCreated = k, a.Now()
	err := a.save()
	a.mu.Unlock()
	if err == nil {
		a.Log.Info("key rotated", "key", k.Public().Fingerprint())
	}
	return err
}

// finishRotation sends a new key left from a rotation cut short.
func (a *Agent) finishRotation(ctx context.Context) error {
	b, err := os.ReadFile(a.path(nextKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	k, err := agentauth.ParseKey(string(b))
	if err != nil {
		return os.Remove(a.path(nextKeyFile))
	}
	return a.sendRotation(ctx, k)
}
