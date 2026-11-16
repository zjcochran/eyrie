// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"sync"
	"time"
)

// Every monitor's uptime adds up its hourly stats over 24 hours and 30
// days and its daily stats over a year: hundreds of thousands of rows with
// many monitors, for each list of monitors. Hours and days that are over
// do not change, so their sums are kept; only the stats since the start of
// the period before the current one are read each time, which also takes
// in checks saved just after their period ended. Stats written before
// that, as a restore does, drop what was kept.
type totalsCache struct {
	mu      sync.Mutex
	fill    sync.Mutex // one sum at a time: the others wait for it
	gen     int        // bumped when kept sums may be out of date
	entries map[totalsKey]map[int64]Totals
}

// totalsKey is a table's periods from from until to, excluded.
type totalsKey struct {
	table    string
	from, to int64
}

// kept returns the sums for k, adding them up first if needed. The map
// returned is shared: it is never changed.
func (c *totalsCache) kept(k totalsKey, sum func() (map[int64]Totals, error)) (map[int64]Totals, error) {
	c.mu.Lock()
	m, ok := c.entries[k]
	c.mu.Unlock()
	if ok {
		return m, nil
	}
	c.fill.Lock()
	defer c.fill.Unlock()
	c.mu.Lock()
	m, ok = c.entries[k]
	gen := c.gen
	c.mu.Unlock()
	if ok {
		return m, nil
	}
	m, err := sum()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen == c.gen { // nothing changed meanwhile
		if c.entries == nil {
			c.entries = map[totalsKey]map[int64]Totals{}
		}
		for old := range c.entries {
			if old.table == k.table && old.to != k.to {
				delete(c.entries, old) // an hour or a day ago
			}
		}
		c.entries[k] = m
	}
	return m, nil
}

// reset drops every kept sum.
func (c *totalsCache) reset() {
	c.mu.Lock()
	c.gen++
	c.entries = nil
	c.mu.Unlock()
}

// saved drops the kept sums when stats were written at or before oldest,
// in a period whose sum may be kept.
func (c *totalsCache) saved(oldest time.Time) {
	now := time.Now().UnixMilli()
	if at := oldest.UnixMilli(); at < keptUntil(now, hour) || at < keptUntil(now, day) {
		c.reset()
	}
}

// keptUntil is where the kept sums of periods of size end: the start of
// the period before the current one.
func keptUntil(now, size int64) int64 { return now - now%size - size }

// cachedTotals adds up every monitor's stats in table, of periods of size,
// from the period that starts at from.
func (s *Store) cachedTotals(ctx context.Context, table string, size, from int64) (map[int64]Totals, error) {
	until := keptUntil(time.Now().UnixMilli(), size)
	if from >= until {
		return s.totals(ctx, table, from, 0, 0)
	}
	done, err := s.cache.kept(totalsKey{table, from, until}, func() (map[int64]Totals, error) {
		return s.totals(ctx, table, from, until, 0)
	})
	if err != nil {
		return nil, err
	}
	latest, err := s.totals(ctx, table, until, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Totals, len(done))
	for id, t := range done {
		out[id] = t
	}
	for id, t := range latest {
		out[id] = out[id].add(t)
	}
	return out, nil
}

func (t Totals) add(o Totals) Totals {
	return Totals{Up: t.Up + o.Up, Down: t.Down + o.Down, LatencySum: t.LatencySum + o.LatencySum,
		LatencyCount: t.LatencyCount + o.LatencyCount, CPUSum: t.CPUSum + o.CPUSum, MemorySum: t.MemorySum + o.MemorySum,
		UsageCount: t.UsageCount + o.UsageCount}
}
