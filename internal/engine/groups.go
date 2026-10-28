// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"slices"
	"time"

	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// groupEntry is a group and whether its alert considers it down.
type groupEntry struct {
	g     store.Group
	down  bool
	since time.Time // when it went down
}

// GroupAlert is a group's alert: down when its first counted member went
// down, or back up when none is down any more.
type GroupAlert struct {
	Group store.Group
	Down  bool
	// Members are the names of the counted members down, by name; Since,
	// for a group back up, is when it went down.
	Members []string
	Since   time.Time
}

// readGroups reads the groups, keeping what is known of those already
// there.
func (e *Engine) readGroups(ctx context.Context) (map[int64]*groupEntry, error) {
	gs, err := e.Store.Groups(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]*groupEntry{}
	for _, g := range gs {
		ge := &groupEntry{g: g}
		if old, ok := e.groups[g.ID]; ok {
			ge.down, ge.since = old.down, old.since
		}
		out[g.ID] = ge
	}
	return out, nil
}

// ReloadGroups reads the groups, and which group each monitor is in,
// again after a change, and applies them at once. A change alerts nobody:
// only members' status changes do.
func (e *Engine) ReloadGroups(ctx context.Context) error {
	return e.do(ctx, func() error {
		groups, err := e.readGroups(ctx)
		if err != nil {
			return err
		}
		ms, err := e.Store.Monitors(ctx)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if en, ok := e.mons[m.ID]; ok {
				en.m.GroupID, en.m.Position = m.GroupID, m.Position
			}
		}
		e.groups = groups
		e.evaluate() // windows cover groups' members
		e.evalGroups(false)
		return nil
	})
}

// inGroup says whether a monitor is in group id, or in a group inside it.
func (e *Engine) inGroup(m store.Monitor, id int64) bool {
	for g, depth := m.GroupID, 0; g != 0 && depth < 20; depth++ {
		if g == id {
			return true
		}
		ge, ok := e.groups[g]
		if !ok {
			return false
		}
		g = ge.g.ParentID
	}
	return false
}

// groupDown is the names of a group's counted members that are down: its
// active members, or with AlertSelected only the chosen ones.
func (e *Engine) groupDown(ge *groupEntry) []string {
	var down []string
	for _, en := range e.mons {
		if !en.m.Active || en.st.Status != state.Down || !e.inGroup(en.m, ge.g.ID) {
			continue
		}
		if ge.g.Alert == store.AlertSelected && !slices.Contains(ge.g.AlertMonitors, en.m.ID) {
			continue
		}
		down = append(down, en.m.Name)
	}
	slices.Sort(down)
	return down
}

// evalGroups works out which groups are down. With alert, a group that
// went down or came back up alerts, unless its alert is off.
func (e *Engine) evalGroups(alert bool) {
	now := e.Clock.Now()
	for _, ge := range e.groups {
		down := e.groupDown(ge)
		if (len(down) > 0) == ge.down {
			continue
		}
		since := ge.since
		ge.down = len(down) > 0
		if ge.down {
			ge.since = now
		}
		if alert && ge.g.Alert != store.AlertNone && e.OnGroup != nil {
			e.OnGroup(GroupAlert{Group: ge.g, Down: ge.down, Members: down, Since: since})
		}
	}
}
