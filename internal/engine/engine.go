// SPDX-License-Identifier: AGPL-3.0-or-later

// Package engine runs the monitors: it schedules checks, applies package
// state to their results, records heartbeats and reports changes.
//
// One goroutine owns every monitor's state; everything else talks to it
// through commands, so there are no locks around monitor state. Checks run
// on a bounded pool of workers, and a check never overlaps itself. The next
// check starts an interval after the last one started, so the schedule does
// not drift. Heartbeats are written in batches about once a second.
//
// Monitors covered by a maintenance window that is on are in maintenance:
// they are still checked, but their checks are recorded as maintenance and
// notify nobody. When the window ends, the status they had before comes
// back and they are checked at once.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/maintenance"
	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// Workers is how many checks run at once.
const Workers = 64

// flushEvery is how often heartbeats are written.
const flushEvery = time.Second

// Clock tells the time; tests replace it.
type Clock interface {
	Now() time.Time
	// At fires once the time is t, or at once if it is past.
	At(t time.Time) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) At(t time.Time) <-chan time.Time { return time.After(time.Until(t)) }

// Event is a heartbeat, sent to Publish as it happens.
type Event struct {
	Beat store.Beat
}

// Status is a monitor's current status, as Statuses reports it.
type Status struct {
	MonitorID   int64
	Active      bool
	Status      state.Status
	Since       time.Time // zero: never checked
	Message     string
	LastCheck   time.Time
	Latency     time.Duration // negative: none
	CertExpires time.Time
	Up, Down    int64 // checks counted up and down since Eyrie started
	Running     bool  // a check is in progress
	// Maintenance is the title of the maintenance window the monitor is in;
	// "" when it is not in one.
	Maintenance string
}

// Engine runs the monitors. Set its fields, then call Run.
type Engine struct {
	Store   *store.Store
	Env     *check.Env
	Log     *slog.Logger
	Clock   Clock       // nil: the real time
	Publish func(Event) // called for every heartbeat; may be nil. It must not block.
	// OnNotify is called when a status change is worth a notification; may
	// be nil. since is when the down status began, for a monitor back up or
	// still down; zero for one that just went down.
	OnNotify func(m store.Monitor, b store.Beat, since time.Time)
	// CertDays are the days before a certificate expires when OnCert is
	// called, once each; nil for none.
	CertDays func() []int
	// OnCert warns that a monitor's certificate expires in days whole days.
	OnCert func(m store.Monitor, expires time.Time, days int)
	// OnGroup is called when a group with alerts goes down or comes back
	// up; may be nil.
	OnGroup func(GroupAlert)
	// Agents checks the monitors that run on an agent; nil: their checks
	// fail.
	Agents Agents

	cmds    chan func()
	results chan result
	writes  chan store.Record
	flushes chan chan struct{} // the writer saves what it holds, then closes the channel
	// epoch counts Replace calls; results of checks started before one are
	// dropped.
	epoch  int
	mons   map[int64]*entry
	groups map[int64]*groupEntry
	sem    chan struct{}
	ready  chan struct{}
	// windows are the maintenance windows, tags each monitor's tag IDs, and
	// maintNext when a window next starts or ends (zero: never).
	windows   []window
	tags      map[int64][]int64
	maintNext time.Time

	mu   sync.RWMutex
	snap map[int64]Status
}

// Agents has a monitor checked on its agent.
type Agents interface {
	Checker(env *check.Env, m store.Monitor) (check.Checker, error)
}

// AgentGrace is how much longer than its timeout a check on an agent may
// take: the time to fetch it and send back its result.
const AgentGrace = 20 * time.Second

// noAgents fails the checks of monitors on agents when there are none.
type noAgents struct{}

func (noAgents) Check(context.Context) check.Result {
	return check.Result{Message: "Agents are not available here", Latency: -1}
}

type entry struct {
	m       store.Monitor
	checker check.Checker
	rules   state.Rules
	st      state.State
	status  Status
	next    time.Time // zero: not scheduled
	running bool
	gen     int // bumped on every change of settings, to drop older results
	// wantsCert: the monitor asks for certificate warnings; certNotified is
	// the threshold, in days, of the last one sent for the current
	// certificate (0: none).
	wantsCert    bool
	certNotified int
	// before is, in maintenance, the state the monitor had before it began.
	before state.State
}

// window is a maintenance window with its schedule compiled.
type window struct {
	store.Window
	plan maintenance.Plan
}

type result struct {
	id      int64
	epoch   int
	gen     int
	started time.Time
	res     check.Result
}

func (e *Engine) init() {
	if e.Clock == nil {
		e.Clock = realClock{}
	}
	e.cmds = make(chan func())
	e.results = make(chan result, Workers)
	e.writes = make(chan store.Record, 10000)
	e.flushes = make(chan chan struct{})
	e.mons = map[int64]*entry{}
	e.groups = map[int64]*groupEntry{}
	e.sem = make(chan struct{}, Workers)
	e.snap = map[int64]Status{}
	e.ready = make(chan struct{})
	e.tags = map[int64][]int64{}
}

// New returns an engine, ready for Run.
func New(st *store.Store, env *check.Env, log *slog.Logger) *Engine {
	e := &Engine{Store: st, Env: env, Log: log}
	e.init()
	return e
}

// Run loads the monitors and runs them until ctx ends, then writes the
// heartbeats still waiting.
func (e *Engine) Run(ctx context.Context) error {
	if e.cmds == nil {
		e.init()
	}
	if err := e.load(ctx); err != nil {
		return err
	}
	written := make(chan struct{})
	go e.writer(written)
	close(e.ready)
	defer func() {
		close(e.writes)
		<-written
	}()
	for {
		wake := e.Clock.Now().Add(time.Minute)
		for _, en := range e.mons {
			if !en.next.IsZero() && !en.running && en.next.Before(wake) {
				wake = en.next
			}
		}
		if !e.maintNext.IsZero() && e.maintNext.Before(wake) {
			wake = e.maintNext
		}
		select {
		case <-ctx.Done():
			return nil
		case f := <-e.cmds:
			f()
		case r := <-e.results:
			e.apply(r)
		case <-e.Clock.At(wake):
			e.runDue()
		}
	}
}

func (e *Engine) load(ctx context.Context) error {
	ms, err := e.Store.Monitors(ctx)
	if err != nil {
		return err
	}
	saved, err := e.Store.States(ctx)
	if err != nil {
		return err
	}
	now := e.Clock.Now()
	for _, m := range ms {
		en := &entry{status: Status{MonitorID: m.ID, Status: state.Unknown, Latency: -1}}
		if s, ok := saved[m.ID]; ok {
			en.st = state.State{Status: state.Status(s.Status), Since: s.Since, Failures: s.Failures, DownChecks: s.DownChecks}
			en.status = Status{MonitorID: m.ID, Status: en.st.Status, Since: s.Since, Message: s.Message,
				LastCheck: s.LastCheck, Latency: s.Latency, CertExpires: s.CertExpires}
			en.certNotified = s.CertNotified
			if en.st.Status == state.Maintenance {
				// Which window it is in is known once they are read.
				en.before = state.State{Status: state.Status(s.BeforeStatus), Since: s.BeforeSince}
				en.status.Maintenance = "…"
			}
		} else {
			en.st.Status = state.Unknown
		}
		e.mons[m.ID] = en
		if err := e.configure(en, m); err != nil {
			e.Log.Error("a saved monitor has bad settings; it is not checked", "monitor", m.Name, "err", err)
			en.next = time.Time{}
			continue
		}
		// Spread the first checks over each interval, so they do not all
		// start at once.
		if !en.next.IsZero() && m.Type != "push" {
			en.next = now.Add(spread(m.ID, m.Interval))
		}
	}
	if e.groups, err = e.readGroups(ctx); err != nil {
		return err
	}
	windows, tags, err := e.readMaintenance(ctx)
	if err != nil {
		return err
	}
	e.windows, e.tags = windows, tags
	e.evaluate()
	e.evalGroups(false)
	e.publishAll()
	return nil
}

// spread is a monitor's fixed offset into its interval.
func spread(id int64, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	return time.Duration((id * 7919) % int64(interval/time.Millisecond) * int64(time.Millisecond))
}

// configure applies a monitor's settings to its entry and schedules it.
func (e *Engine) configure(en *entry, m store.Monitor) error {
	en.m = m
	en.gen++
	en.rules = state.Rules{Retries: m.Retries, ResendEvery: m.ResendEvery, UpsideDown: m.UpsideDown}
	en.wantsCert = check.NotifiesCertExpiry(m.Config)
	en.status.Active = m.Active
	en.next = time.Time{}
	_, c, err := check.Parse(e.Env, m.Type, m.Config)
	if err != nil {
		en.checker = nil
		return err
	}
	en.checker = c
	if m.AgentID != 0 && c != nil {
		en.checker = noAgents{}
		if e.Agents != nil {
			if en.checker, err = e.Agents.Checker(e.Env, m); err != nil {
				en.checker = nil
				return err
			}
		}
	}
	if !m.Active || m.Type == "manual" {
		return nil
	}
	now := e.Clock.Now()
	en.next = now
	if m.Type == "push" {
		en.next = now.Add(m.Interval) // the first push has an interval to arrive
	}
	return nil
}

// runDue starts or ends the maintenance windows that are due, then starts
// every check that is due.
func (e *Engine) runDue() {
	now := e.Clock.Now()
	if !e.maintNext.IsZero() && !e.maintNext.After(now) {
		e.evaluate()
	}
	for id, en := range e.mons {
		if en.next.IsZero() || en.running || en.next.After(now) {
			continue
		}
		switch en.m.Type {
		case "push":
			e.record(en, now, check.Result{Latency: -1,
				Message: fmt.Sprintf("No push received in %s", en.m.Interval)})
		default:
			en.running = true
			e.setSnap(en)
			timeout := en.m.Timeout
			if en.m.AgentID != 0 {
				timeout += AgentGrace
			}
			go e.work(result{id: id, epoch: e.epoch, gen: en.gen, started: now}, en.checker, timeout)
		}
	}
}

func (e *Engine) work(r result, c check.Checker, timeout time.Duration) {
	e.sem <- struct{}{}
	defer func() { <-e.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r.res = c.Check(ctx)
	e.results <- r
}

func (e *Engine) apply(r result) {
	en, ok := e.mons[r.id]
	if !ok || en.gen != r.gen || r.epoch != e.epoch {
		return // deleted or changed meanwhile
	}
	en.running = false
	e.record(en, r.started, r.res)
}

// record applies a result to a monitor checked at started. In maintenance,
// it is recorded and changes nothing.
func (e *Engine) record(en *entry, started time.Time, res check.Result) {
	if en.status.Maintenance != "" {
		if en.m.Active {
			en.next = started.Add(en.m.Interval)
		}
		if !res.CertExpires.IsZero() {
			e.checkCert(en, res.CertExpires)
		}
		e.beat(en, state.Outcome{State: en.st, Status: state.Maintenance, Message: res.Message}, res.Latency)
		return
	}
	o := state.Next(en.rules, en.st, state.Result{OK: res.OK, Message: res.Message}, e.Clock.Now())
	interval := en.m.Interval
	if o.Retry {
		interval = en.m.RetryInterval
	}
	if en.m.Active {
		en.next = started.Add(interval)
	}
	if !res.CertExpires.IsZero() {
		e.checkCert(en, res.CertExpires)
	}
	e.beat(en, o, res.Latency)
}

// checkCert warns once per threshold before a certificate expires. A new
// certificate starts the warnings again.
func (e *Engine) checkCert(en *entry, expires time.Time) {
	if !expires.Equal(en.status.CertExpires) {
		en.certNotified = 0
	}
	en.status.CertExpires = expires
	if !en.wantsCert || e.CertDays == nil || e.OnCert == nil {
		return
	}
	days := int(math.Floor(expires.Sub(e.Clock.Now()).Hours() / 24))
	threshold := 0
	for _, t := range e.CertDays() {
		if days <= t && (threshold == 0 || t < threshold) {
			threshold = t
		}
	}
	if threshold != 0 && (en.certNotified == 0 || threshold < en.certNotified) {
		en.certNotified = threshold
		e.OnCert(en.m, expires, days)
	}
}

// beat records a heartbeat for an outcome.
func (e *Engine) beat(en *entry, o state.Outcome, latency time.Duration) {
	now := e.Clock.Now()
	old := en.st
	en.st = o.State
	s := &en.status
	s.Status, s.Since, s.Message, s.LastCheck, s.Latency = o.Status, o.State.Since, o.Message, now, latency
	switch o.Status {
	case state.Maintenance:
	case state.Down:
		s.Down++
	default:
		s.Up++
	}
	b := store.Beat{MonitorID: en.m.ID, At: now, Status: string(o.Status), Latency: latency, Message: o.Message,
		Important: o.Important}
	rec := store.Record{Beat: b, State: store.MonitorState{MonitorID: en.m.ID, Status: string(o.Status), Since: o.State.Since,
		Message: o.Message, LastCheck: now, Latency: latency, CertExpires: s.CertExpires, Failures: o.State.Failures,
		DownChecks: o.State.DownChecks, CertNotified: en.certNotified}}
	if o.Status == state.Maintenance {
		rec.State.BeforeStatus, rec.State.BeforeSince = string(en.before.Status), en.before.Since
	}
	select {
	case e.writes <- rec:
	default:
		e.Log.Warn("the heartbeat queue is full; a heartbeat was dropped", "monitor", en.m.Name)
	}
	if latency >= 0 {
		e.Log.Debug("check", "monitor", en.m.Name, "status", o.Status, "latency", latency, "message", o.Message)
	} else {
		e.Log.Debug("check", "monitor", en.m.Name, "status", o.Status, "message", o.Message)
	}
	if o.Important {
		e.Log.Info("monitor status", "monitor", en.m.Name, "status", o.Status, "message", o.Message)
	}
	if o.Notify && e.OnNotify != nil {
		var since time.Time
		switch {
		case o.Status == state.Up:
			since = old.Since // when it went down
		case !o.Important:
			since = o.State.Since // still down since
		}
		e.OnNotify(en.m, b, since)
	}
	e.setSnap(en)
	if e.Publish != nil {
		e.Publish(Event{Beat: b})
	}
	// A member's change can change its groups. They alert with the
	// member's own alerts, not when maintenance starts or ends.
	if o.Important {
		e.evalGroups(o.Notify)
	}
}

// writer saves heartbeats in batches until writes is closed.
func (e *Engine) writer(done chan<- struct{}) {
	defer close(done)
	var batch []store.Record
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := e.Store.Save(ctx, batch); err != nil {
			e.Log.Error("cannot save heartbeats", "count", len(batch), "err", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case r, ok := <-e.writes:
			if !ok {
				flush()
				return
			}
			batch = append(batch, r)
			if len(batch) >= 500 {
				flush()
			}
		case <-tick.C:
			flush()
		case done := <-e.flushes:
			// Only the engine's loop queues records, and it waits for
			// this: what is queued now is all there is.
			for drained := false; !drained; {
				select {
				case r := <-e.writes:
					batch = append(batch, r)
				default:
					drained = true
				}
			}
			flush()
			close(done)
		}
	}
}

func (e *Engine) setSnap(en *entry) {
	s := en.status
	s.Running = en.running
	s.Active = en.m.Active
	e.mu.Lock()
	e.snap[en.m.ID] = s
	e.mu.Unlock()
}

func (e *Engine) publishAll() {
	for _, en := range e.mons {
		e.setSnap(en)
	}
}

// Statuses returns every monitor's current status, by ID.
func (e *Engine) Statuses() map[int64]Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[int64]Status, len(e.snap))
	for k, v := range e.snap {
		out[k] = v
	}
	return out
}

// ErrStopped is returned by commands when the engine is not running.
var ErrStopped = errors.New("the monitor engine is not running")

// do runs f on the engine's goroutine and waits for it.
func (e *Engine) do(ctx context.Context, f func() error) error {
	select {
	case <-e.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	errc := make(chan error, 1)
	select {
	case e.cmds <- func() { errc <- f() }:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Replace stops every check, waits until the heartbeats already made are
// saved, runs swap (which replaces the data, such as a restore) and loads
// every monitor again, whether swap worked or not. Checks running meanwhile
// are dropped when they end.
func (e *Engine) Replace(ctx context.Context, swap func(context.Context) error) error {
	return e.do(ctx, func() error {
		e.epoch++
		e.mons = map[int64]*entry{}
		done := make(chan struct{})
		e.flushes <- done
		<-done
		err := swap(ctx)
		e.mu.Lock()
		e.snap = map[int64]Status{}
		e.mu.Unlock()
		if lerr := e.load(ctx); err == nil {
			err = lerr
		}
		return err
	})
}

// Upsert starts running a new monitor, or applies a monitor's new
// settings. Its status carries on; it is checked at once.
func (e *Engine) Upsert(ctx context.Context, m store.Monitor) error {
	return e.do(ctx, func() error {
		en, ok := e.mons[m.ID]
		if !ok {
			en = &entry{st: state.State{Status: state.Unknown}, status: Status{MonitorID: m.ID, Status: state.Unknown, Latency: -1}}
			e.mons[m.ID] = en
		}
		en.running = false
		err := e.configure(en, m)
		e.evaluate() // its group may have changed
		e.evalGroups(false)
		e.setSnap(en)
		return err
	})
}

// Remove stops a deleted monitor.
func (e *Engine) Remove(ctx context.Context, id int64) error {
	return e.do(ctx, func() error {
		delete(e.mons, id)
		e.evalGroups(false)
		e.mu.Lock()
		delete(e.snap, id)
		e.mu.Unlock()
		return nil
	})
}

// CheckNow checks a monitor at once.
func (e *Engine) CheckNow(ctx context.Context, id int64) error {
	return e.do(ctx, func() error {
		en, ok := e.mons[id]
		switch {
		case !ok:
			return store.ErrNotFound
		case !en.m.Active:
			return errors.New("the monitor is paused")
		case en.m.Type == "push" || en.m.Type == "manual":
			return fmt.Errorf("a %s monitor is not checked by Eyrie", en.m.Type)
		}
		if !en.running {
			en.next = e.Clock.Now()
		}
		return nil
	})
}

// CheckMatching checks at once every active, scheduled monitor that
// matches, such as the monitors of a workload whose agent's report changed.
func (e *Engine) CheckMatching(ctx context.Context, match func(store.Monitor) bool) error {
	return e.do(ctx, func() error {
		for _, en := range e.mons {
			if en.checker != nil && en.m.Active && !en.running && match(en.m) {
				en.next = e.Clock.Now()
			}
		}
		return nil
	})
}

// ErrNotPush is returned by Push for a monitor that is not an active push
// monitor.
var ErrNotPush = errors.New("not an active push monitor")

// Push records a push: up, or down with ok false. The next push is due an
// interval later.
func (e *Engine) Push(ctx context.Context, id int64, ok bool, msg string, latency time.Duration) error {
	return e.do(ctx, func() error {
		en, found := e.mons[id]
		if !found || en.m.Type != "push" || !en.m.Active {
			return ErrNotPush
		}
		if msg == "" {
			msg = map[bool]string{true: "OK", false: "Down"}[ok]
		}
		now := e.Clock.Now()
		e.record(en, now, check.Result{OK: ok, Message: msg, Latency: latency})
		en.next = now.Add(en.m.Interval)
		return nil
	})
}

// SetManual sets a manual monitor's status.
func (e *Engine) SetManual(ctx context.Context, id int64, status state.Status, msg string) error {
	return e.do(ctx, func() error {
		en, ok := e.mons[id]
		if !ok || en.m.Type != "manual" {
			return errors.New("not a manual monitor")
		}
		if status != state.Up && status != state.Down {
			return errors.New("the status must be up or down")
		}
		if msg == "" {
			msg = "Set to " + string(status)
		}
		if en.status.Maintenance != "" {
			// It takes this status when the maintenance ends.
			en.before = state.Set(en.before, status, e.Clock.Now()).State
			e.beat(en, state.Outcome{State: en.st, Status: state.Maintenance, Message: msg + " (after the maintenance)"}, -1)
			return nil
		}
		o := state.Set(en.st, status, e.Clock.Now())
		o.Message = msg
		e.beat(en, o, -1)
		return nil
	})
}
