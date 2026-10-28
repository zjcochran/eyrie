// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agents is Eyrie's side of its agents: adding them, enrolling
// them, checking their signed requests, keeping what they report, and
// handing them the checks of the monitors that run on them. See
// docs/technical/agent-protocol.md.
package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/workload"
)

// Errors for the agent API: ErrRefused is 401, ErrInvalid 400.
var (
	ErrRefused = errors.New("refused")
	ErrInvalid = errors.New("invalid")
)

// Limits on what an agent reports.
const (
	maxWorkloads = 10000
	maxKey       = 300
	maxText      = 300
)

// Manager keeps the agents and what they last reported.
type Manager struct {
	Store *store.Store
	Log   *slog.Logger
	Now   func() time.Time
	// Changed is called, never while a lock is held, when an agent's
	// workloads changed (keys) or the agent went offline, came back or
	// could not list them (keys nil: every one).
	Changed func(agent int64, keys []string)

	mu      sync.Mutex
	agents  map[int64]*store.Agent
	offline map[int64]bool
	refused map[int64]time.Time // when a refusal was last logged
	nonces  agentauth.Nonces
	queues  map[int64]*queue   // each agent's jobs not yet fetched
	waiting map[string]*waiter // jobs waiting for their result, by ID
}

// New returns a manager; Load fills it.
func New(st *store.Store, log *slog.Logger) *Manager {
	return &Manager{Store: st, Log: log, Now: time.Now, agents: map[int64]*store.Agent{}, offline: map[int64]bool{}, refused: map[int64]time.Time{},
		queues: map[int64]*queue{}, waiting: map[string]*waiter{}}
}

// Load reads the agents from the store, at start and after a restore.
func (m *Manager) Load(ctx context.Context) error {
	all, err := m.Store.Agents(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents = map[int64]*store.Agent{}
	for i := range all {
		m.agents[all[i].ID] = &all[i]
	}
	return nil
}

func (m *Manager) changed(agent int64, keys []string) {
	if m.Changed != nil {
		m.Changed(agent, keys)
	}
}

// Online reports whether an enrolled agent reported recently.
func Online(a store.Agent, now time.Time) bool {
	return a.PublicKey != "" && now.Sub(a.LastSeen) <= agentauth.OfflineAfter
}

// List returns the agents by name.
func (m *Manager) List() []store.Agent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one agent.
func (m *Manager) Get(id int64) (store.Agent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	if !ok {
		return store.Agent{}, false
	}
	return *a, true
}

// Add adds an agent and returns its one-time enrollment token.
func (m *Manager) Add(ctx context.Context, name string) (store.Agent, string, error) {
	now := m.Now()
	a, token, err := m.Store.AddAgent(ctx, name, now, now.Add(agentauth.EnrollFor))
	if err != nil {
		return a, "", err
	}
	m.mu.Lock()
	m.agents[a.ID] = &a
	m.mu.Unlock()
	return a, token, nil
}

// NewToken makes a new enrollment token for an agent and forgets its key:
// the agent is refused until it enrolls again.
func (m *Manager) NewToken(ctx context.Context, id int64) (string, error) {
	expires := m.Now().Add(agentauth.EnrollFor)
	token, err := m.Store.NewAgentToken(ctx, id, expires)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	if a, ok := m.agents[id]; ok {
		a.PublicKey, a.PreviousKey, a.KeyCreated, a.TokenExpires = "", "", time.Time{}, expires
	}
	delete(m.offline, id)
	m.mu.Unlock()
	m.changed(id, nil)
	return token, nil
}

// Remove deletes an agent; its monitors go down.
func (m *Manager) Remove(ctx context.Context, id int64) (string, error) {
	name, err := m.Store.DeleteAgent(ctx, id)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	delete(m.agents, id)
	delete(m.offline, id)
	m.dropJobs(id)
	m.mu.Unlock()
	m.changed(id, nil)
	return name, nil
}

// clip trims s and shortens it to at most n bytes, on a character
// boundary.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Enroll trades a one-time token for an agent's key.
func (m *Manager) Enroll(ctx context.Context, req agentauth.EnrollRequest, addr string) (store.Agent, error) {
	key, err := agentauth.ParsePublicKey(req.PublicKey)
	if err != nil {
		return store.Agent{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	a, err := m.Store.EnrollAgent(ctx, strings.TrimSpace(req.Token), key.String(), store.AgentReport{
		Seen: m.Now(), Address: addr, Version: clip(req.Version, 64), Platform: clip(req.Platform, 32), Hostname: clip(req.Hostname, 255),
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Agent{}, fmt.Errorf("%w: the enrollment token is unknown, used or expired: make a new one in Settings → Agents", ErrRefused)
	}
	if err != nil {
		return store.Agent{}, err
	}
	m.mu.Lock()
	m.agents[a.ID] = &a
	delete(m.offline, a.ID)
	m.mu.Unlock()
	m.changed(a.ID, nil)
	return a, nil
}

// Authenticate checks a signed request with its body, and returns the
// agent and the key it signed with. A request signed with an agent's new
// key ends its rotation.
func (m *Manager) Authenticate(ctx context.Context, r *http.Request, body []byte) (store.Agent, agentauth.PublicKey, error) {
	auth, err := agentauth.ParseAuth(r.Header.Get("Authorization"))
	if err != nil {
		return store.Agent{}, nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	a, ok := m.Get(auth.Agent)
	if !ok || a.PublicKey == "" {
		return a, nil, fmt.Errorf("%w: no enrolled agent %d", ErrRefused, auth.Agent)
	}
	var keys []agentauth.PublicKey
	for _, s := range []string{a.PublicKey, a.PreviousKey} {
		if k, err := agentauth.ParsePublicKey(s); err == nil {
			keys = append(keys, k)
		}
	}
	now := m.Now()
	key, err := auth.Verify(keys, r.Method, r.URL.RequestURI(), body, now)
	switch {
	case err != nil:
		return a, nil, fmt.Errorf("%w: %w", ErrRefused, err)
	case !m.nonces.Use(fmt.Sprintf("%d/%s", a.ID, auth.Nonce), now):
		return a, nil, fmt.Errorf("%w: the request was replayed", ErrRefused)
	case now.Sub(a.KeyCreated) > agentauth.MaxKeyAge:
		return a, nil, fmt.Errorf("%w: the agent's key is older than %d days; make a new enrollment token in Settings → Agents", ErrRefused, int(agentauth.MaxKeyAge.Hours()/24))
	}
	if a.PreviousKey != "" && key.String() == a.PublicKey {
		if err := m.Store.ClearAgentPreviousKey(ctx, a.ID); err != nil {
			return a, nil, err
		}
		m.mu.Lock()
		if p, ok := m.agents[a.ID]; ok {
			p.PreviousKey = ""
		}
		m.mu.Unlock()
		a.PreviousKey = ""
	}
	return a, key, nil
}

// LogRefusal reports whether a refusal of agent should be logged: at most
// once an hour each, so a misconfigured agent does not flood the log.
func (m *Manager) LogRefusal(agent int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	if last, ok := m.refused[agent]; ok && now.Sub(last) < time.Hour {
		return false
	}
	m.refused[agent] = now
	return true
}

// cleanReport checks and trims a report's workloads.
func cleanReport(rep *agentauth.Report) error {
	if len(rep.Workloads) > maxWorkloads {
		return fmt.Errorf("%w: more than %d workloads", ErrInvalid, maxWorkloads)
	}
	seen := map[string]bool{}
	for i := range rep.Workloads {
		w := &rep.Workloads[i]
		w.Key = strings.TrimSpace(w.Key)
		if w.Key == "" || len(w.Key) > maxKey || seen[w.Key] {
			return fmt.Errorf("%w: workload %d has an empty, long or repeated key", ErrInvalid, i+1)
		}
		seen[w.Key] = true
		w.Name, w.Kind, w.Group, w.Status = clip(w.Name, maxText), clip(w.Kind, 32), clip(w.Group, maxText), clip(w.Status, maxText)
	}
	rep.Error = clip(rep.Error, 1000)
	if rep.Workloads == nil {
		rep.Workloads = []workload.Workload{}
	}
	return nil
}

// diff returns the keys whose workload is new, gone or changed.
func diff(old, cur []workload.Workload) []string {
	was := map[string]workload.Workload{}
	for _, w := range old {
		was[w.Key] = w
	}
	var keys []string
	for _, w := range cur {
		if o, ok := was[w.Key]; !ok || o.Up != w.Up || o.Status != w.Status {
			keys = append(keys, w.Key)
		}
		delete(was, w.Key)
	}
	for k := range was {
		keys = append(keys, k)
	}
	return keys
}

// Report keeps an agent's report and has the monitors of what changed
// checked again.
func (m *Manager) Report(ctx context.Context, a store.Agent, rep agentauth.Report, addr string) error {
	if err := cleanReport(&rep); err != nil {
		return err
	}
	r := store.AgentReport{
		Seen: m.Now(), Address: addr, Version: clip(rep.Version, 64), Platform: clip(rep.Platform, 32),
		Hostname: clip(rep.Hostname, 255), Error: rep.Error,
	}
	// all: every monitor of the agent is affected, when it could not list
	// its workloads, could not before, or was offline.
	all := rep.Error != "" || a.Error != "" || !Online(a, r.Seen)
	var keys []string
	if !all {
		keys = diff(a.Workloads, rep.Workloads)
	}
	if rep.Error == "" && (all || len(keys) > 0) {
		r.Workloads = rep.Workloads
	}
	if err := m.Store.SaveAgentReport(ctx, a.ID, r); err != nil {
		return err
	}
	m.mu.Lock()
	p, ok := m.agents[a.ID]
	if ok {
		p.LastSeen, p.LastAddress, p.Version, p.Platform, p.Hostname, p.Error = r.Seen, r.Address, r.Version, r.Platform, r.Hostname, r.Error
		if r.Workloads != nil {
			p.Workloads = r.Workloads
		}
	}
	back := m.offline[a.ID]
	delete(m.offline, a.ID)
	m.mu.Unlock()
	switch {
	case !ok:
	case all:
		m.changed(a.ID, nil)
	case len(keys) > 0:
		m.changed(a.ID, keys)
	}
	if back {
		m.Log.Info("agent reporting again", "agent", a.Name)
	}
	return nil
}

// Rotate makes an agent's new key current. The request must be signed
// with the agent's current key, and carry the new key's proof.
func (m *Manager) Rotate(ctx context.Context, a store.Agent, signedWith agentauth.PublicKey, req agentauth.RotateRequest) error {
	newKey, err := agentauth.ParsePublicKey(req.NewKey)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	now := m.Now()
	if d := now.Sub(time.Unix(req.TS, 0)); d > agentauth.MaxSkew || d < -agentauth.MaxSkew || !newKey.Verify(agentauth.RotationProof(a.ID, newKey, req.TS), req.Proof) {
		return fmt.Errorf("%w: the new key's proof does not verify", ErrInvalid)
	}
	if newKey.String() == a.PublicKey {
		return nil // a rotation cut short, sent again
	}
	if signedWith.String() != a.PublicKey {
		return fmt.Errorf("%w: a rotation must be signed with the current key", ErrRefused)
	}
	if err := m.Store.RotateAgentKey(ctx, a.ID, newKey.String(), now); err != nil {
		return err
	}
	m.mu.Lock()
	if p, ok := m.agents[a.ID]; ok {
		p.PreviousKey, p.PublicKey, p.KeyCreated = p.PublicKey, newKey.String(), now
	}
	m.mu.Unlock()
	return nil
}

// Watch notices agents that stop reporting, every interval until ctx
// ends, so their monitors go down without waiting for their next check.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		m.sweep()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) sweep() {
	now := m.Now()
	var gone []store.Agent
	m.mu.Lock()
	for id, a := range m.agents {
		if a.PublicKey != "" && !Online(*a, now) && !m.offline[id] {
			m.offline[id] = true
			gone = append(gone, *a)
		}
	}
	m.mu.Unlock()
	for _, a := range gone {
		m.Log.Warn("agent stopped reporting", "agent", a.Name, "last_seen", a.LastSeen.UTC().Format(time.RFC3339))
		m.changed(a.ID, nil)
	}
}
