// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/zjcochran/eyrie/internal/state"
	"github.com/zjcochran/eyrie/internal/store"
)

// alerts collects group alerts as name, down and members.
type alerts struct {
	mu  sync.Mutex
	got []GroupAlert
}

func (a *alerts) add(g GroupAlert) {
	a.mu.Lock()
	a.got = append(a.got, g)
	a.mu.Unlock()
}

// take returns the alerts since the last take.
func (a *alerts) take() []GroupAlert {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.got
	a.got = nil
	return out
}

// groupSetup is Site (alerts on any member) with Web in it, Shop in Site
// (alerts on Pay) with Cart and Pay, and Quiet (no alerts) with Other,
// all up.
type groupSetup struct {
	h                     *harness
	al                    *alerts
	site, shop            store.Group
	web, cart, pay, other store.Monitor
}

func newGroupSetup(t *testing.T) *groupSetup {
	h := newHarness(t)
	ctx := context.Background()
	g := &groupSetup{h: h, al: &alerts{}}
	h.e.OnGroup = g.al.add
	g.site, _ = h.st.CreateGroup(ctx, store.Group{Name: "Site", Alert: store.AlertAny}, t0)
	g.shop, _ = h.st.CreateGroup(ctx, store.Group{Name: "Shop", ParentID: g.site.ID, Alert: store.AlertNone}, t0)
	quiet, _ := h.st.CreateGroup(ctx, store.Group{Name: "Quiet", Alert: store.AlertNone}, t0)
	in := func(name string, group int64) store.Monitor {
		m := monitor(name, "manual", `{}`)
		m.GroupID = group
		return h.add(m)
	}
	g.web, g.cart, g.pay, g.other = in("Web", g.site.ID), in("Cart", g.shop.ID), in("Pay", g.shop.ID), in("Other", quiet.ID)
	g.shop.Alert, g.shop.AlertMonitors = store.AlertSelected, []int64{g.pay.ID}
	h.st.UpdateGroup(ctx, g.shop, t0)
	h.start()
	for _, m := range []store.Monitor{g.web, g.cart, g.pay, g.other} {
		g.set(t, m, state.Up)
	}
	if got := g.al.take(); len(got) != 0 {
		t.Fatalf("all up: %+v", got)
	}
	return g
}

func (g *groupSetup) set(t *testing.T, m store.Monitor, s state.Status) {
	t.Helper()
	if err := g.h.e.SetManual(context.Background(), m.ID, s, ""); err != nil {
		t.Fatal(err)
	}
}

func TestGroupAlerts(t *testing.T) {
	g := newGroupSetup(t)
	al, site, shop, cart, pay, other := g.al, g.site, g.shop, g.cart, g.pay, g.other
	set := func(m store.Monitor, s state.Status) { t.Helper(); g.set(t, m, s) }
	// Cart, in Shop in Site: Site (any member) goes down; Shop only
	// counts Pay.
	set(cart, state.Down)
	if got := al.take(); len(got) != 1 || got[0].Group.ID != site.ID || !got[0].Down || !slices.Equal(got[0].Members, []string{"Cart"}) {
		t.Fatalf("cart down: %+v", got)
	}
	set(pay, state.Down)
	if got := al.take(); len(got) != 1 || got[0].Group.ID != shop.ID || !got[0].Down || !slices.Equal(got[0].Members, []string{"Pay"}) {
		t.Fatalf("pay down: %+v", got)
	}
	set(other, state.Down) // its group does not alert
	set(cart, state.Up)
	if got := al.take(); len(got) != 0 {
		t.Fatalf("site still down by pay: %+v", got)
	}
	set(pay, state.Up)
	got := al.take()
	if len(got) != 2 || got[0].Down || got[1].Down || got[0].Since.IsZero() {
		t.Fatalf("back up: %+v", got)
	}

}

func TestGroupAlertsQuiet(t *testing.T) {
	g := newGroupSetup(t)
	h, al, site, shop, web, cart := g.h, g.al, g.site, g.shop, g.web, g.cart
	ctx := context.Background()
	set := func(m store.Monitor, s state.Status) { t.Helper(); g.set(t, m, s) }
	// A paused member does not count; a change of group alerts nobody.
	set(web, state.Down)
	al.take()
	web.Active = false
	h.st.SetMonitorActive(ctx, web.ID, false, t0)
	h.e.Upsert(ctx, web)
	site.Alert = store.AlertNone
	h.st.UpdateGroup(ctx, site, t0)
	if err := h.e.ReloadGroups(ctx); err != nil {
		t.Fatal(err)
	}
	if got := al.take(); len(got) != 0 {
		t.Fatalf("paused, reloaded: %+v", got)
	}

	// A member down when maintenance starts: the group comes back up
	// without an alert.
	site.Alert = store.AlertAny
	h.st.UpdateGroup(ctx, site, t0)
	h.e.ReloadGroups(ctx)
	set(cart, state.Down)
	if got := al.take(); len(got) != 1 || !got[0].Down {
		t.Fatalf("cart down again: %+v", got)
	}
	h.window(store.Window{Enabled: true, Schedule: json.RawMessage(`{"strategy":"manual"}`), GroupIDs: []int64{shop.ID}})
	if got := al.take(); len(got) != 0 {
		t.Fatalf("maintenance: %+v", got)
	}
	if err := h.e.Remove(ctx, cart.ID); err != nil {
		t.Fatal(err)
	}
}
