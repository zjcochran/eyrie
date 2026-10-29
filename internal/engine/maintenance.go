// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/zjcochran/eyrie/internal/maintenance"
	"github.com/zjcochran/eyrie/internal/state"
)

// readMaintenance reads the maintenance windows, with their schedules
// compiled, and every monitor's tags. A window whose schedule does not
// compile is left out and logged.
func (e *Engine) readMaintenance(ctx context.Context) ([]window, map[int64][]int64, error) {
	ws, err := e.Store.Windows(ctx)
	if err != nil {
		return nil, nil, err
	}
	var out []window
	for _, w := range ws {
		var s maintenance.Schedule
		err := json.Unmarshal(w.Schedule, &s)
		var p maintenance.Plan
		if err == nil {
			p, err = maintenance.Compile(s)
		}
		if err != nil {
			e.Log.Error("a maintenance window has a bad schedule; it is ignored", "window", w.Title, "err", err)
			continue
		}
		out = append(out, window{Window: w, plan: p})
	}
	mt, err := e.Store.MonitorTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	tags := map[int64][]int64{}
	for id, ts := range mt {
		for _, t := range ts {
			tags[id] = append(tags[id], t.TagID)
		}
	}
	return out, tags, nil
}

// ReloadMaintenance reads the maintenance windows and the monitors' tags
// again, after a change to either, and applies them at once.
func (e *Engine) ReloadMaintenance(ctx context.Context) error {
	windows, tags, err := e.readMaintenance(ctx)
	if err != nil {
		return err
	}
	return e.do(ctx, func() error {
		e.windows, e.tags = windows, tags
		e.evaluate()
		return nil
	})
}

// evaluate puts the monitors covered by a window that is on in
// maintenance, takes the others out, and notes when a window next starts
// or ends.
func (e *Engine) evaluate() {
	now := e.Clock.Now()
	e.maintNext = time.Time{}
	var on []*window
	for i := range e.windows {
		w := &e.windows[i]
		if !w.Enabled {
			continue
		}
		active, change := w.plan.At(now)
		if !change.IsZero() && (e.maintNext.IsZero() || change.Before(e.maintNext)) {
			e.maintNext = change
		}
		if active {
			on = append(on, w)
		}
	}
	for _, en := range e.mons {
		title := e.coveredBy(en, on)
		switch {
		case title != "" && en.status.Maintenance == "":
			e.enter(en, title, now)
		case title == "" && en.status.Maintenance != "":
			e.leave(en, now)
		case title != "" && title != en.status.Maintenance:
			en.status.Maintenance = title
			e.setSnap(en)
		}
	}
}

// coveredBy is the title of the first window in on that covers a monitor,
// by itself, by a tag or through a group it is in; "" for none.
func (e *Engine) coveredBy(en *entry, on []*window) string {
	for _, w := range on {
		if w.AllMonitors || slices.Contains(w.MonitorIDs, en.m.ID) || shares(w.TagIDs, e.tags[en.m.ID]) ||
			slices.ContainsFunc(w.GroupIDs, func(g int64) bool { return e.inGroup(en.m, g) }) {
			return w.Title
		}
	}
	return ""
}

func shares(a, b []int64) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// enter puts a monitor in maintenance, keeping its state for the end.
func (e *Engine) enter(en *entry, title string, now time.Time) {
	en.before = en.st
	en.st = state.State{Status: state.Maintenance, Since: now}
	en.status.Maintenance = title
	e.Log.Info("maintenance started", "monitor", en.m.Name, "window", title)
	e.beat(en, state.Outcome{State: en.st, Status: state.Maintenance, Message: "Maintenance: " + title, Important: true}, -1, nil)
}

// leave ends a monitor's maintenance: the state it had before comes back,
// without a notification, and it is checked at once (a push monitor: it
// has an interval to push).
func (e *Engine) leave(en *entry, now time.Time) {
	before := en.before
	en.before = state.State{}
	en.status.Maintenance = ""
	e.Log.Info("maintenance ended", "monitor", en.m.Name)
	if before.Status == "" || before.Status == state.Unknown {
		en.st = state.State{Status: state.Unknown}
		en.status.Status, en.status.Since = state.Unknown, time.Time{}
		e.setSnap(en)
	} else {
		e.beat(en, state.Outcome{State: before, Status: before.Status, Message: "Maintenance ended", Important: true}, -1, nil)
	}
	if !en.m.Active || en.running {
		return
	}
	switch en.m.Type {
	case "manual":
	case "push":
		en.next = now.Add(en.m.Interval)
	default:
		en.next = now
	}
}
