// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"
)

// recount adds up a table's stats from from without the kept sums.
func recount(t *testing.T, s *Store, table string, from int64) map[int64]Totals {
	t.Helper()
	m, err := s.totals(context.Background(), table, from, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCachedTotals(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		if _, err := s.CreateFirstAdmin(ctx, "alice", "a long password here", t0); err != nil {
			t.Fatal(err)
		}
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		now := time.Now()
		// Checks over the last three days, every 30 minutes, B down
		// every fifth; the latest a minute ago.
		var recs []Record
		for i := range 3 * 48 {
			at := now.Add(-time.Minute - time.Duration(i)*30*time.Minute)
			recs = append(recs, beat(a.ID, at, "up", 10*time.Millisecond))
			status := "up"
			if i%5 == 0 {
				status = "down"
			}
			recs = append(recs, beat(b.ID, at, status, 20*time.Millisecond))
		}
		if err := s.Save(ctx, recs); err != nil {
			t.Fatal(err)
		}
		since := now.Add(-48 * time.Hour)
		from := since.UnixMilli() - since.UnixMilli()%hour
		check := func(what string) {
			t.Helper()
			got, err := s.HourlyTotals(ctx, since)
			if err != nil {
				t.Fatal(err)
			}
			if want := recount(t, s, "stats_hourly", from); !maps.Equal(got, want) {
				t.Errorf("%s: hourly %+v, recounted %+v", what, got, want)
			}
			days, err := s.DailyTotals(ctx, since)
			if err != nil {
				t.Fatal(err)
			}
			if want := recount(t, s, "stats_daily", since.UnixMilli()-since.UnixMilli()%day); !maps.Equal(days, want) {
				t.Errorf("%s: daily %+v, recounted %+v", what, days, want)
			}
		}
		check("first")
		if len(s.cache.entries) != 2 {
			t.Errorf("kept %d sums, want the hourly and the daily", len(s.cache.entries))
		}
		check("kept")

		// A check now is read with the latest hours; the kept sums stay.
		if err := s.Save(ctx, []Record{beat(a.ID, time.Now(), "down", time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
		check("a new check")
		if len(s.cache.entries) != 2 {
			t.Errorf("a new check dropped the kept sums")
		}

		// A check saved in an hour whose sum is kept drops it.
		if err := s.Save(ctx, []Record{beat(b.ID, now.Add(-30*time.Hour), "down", time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
		check("a late check")

		sameForOne(t, s, since, a, b)
		pruneAndRestore(t, s, now, check)
		// The latest periods only: nothing to keep.
		recent := time.Now().Add(-30 * time.Minute)
		got, err := s.HourlyTotals(ctx, recent)
		if want := recount(t, s, "stats_hourly", recent.UnixMilli()-recent.UnixMilli()%hour); err != nil || !maps.Equal(got, want) {
			t.Errorf("recent %+v, recounted %+v, %v", got, want, err)
		}
	})
}

func TestTotalsCacheRace(t *testing.T) {
	var c totalsCache
	k := totalsKey{"stats_hourly", 0, hour}
	// Stats change while they are being added up: the sum is not kept.
	m, err := c.kept(k, func() (map[int64]Totals, error) {
		c.reset()
		return map[int64]Totals{1: {Up: 1}}, nil
	})
	if err != nil || m[1].Up != 1 || len(c.entries) != 0 {
		t.Errorf("kept %v %v, entries %v", m, err, c.entries)
	}
	// A failed sum is not kept either.
	if _, err := c.kept(k, func() (map[int64]Totals, error) { return nil, errors.New("closed") }); err == nil || len(c.entries) != 0 {
		t.Errorf("failed: %v, entries %v", err, c.entries)
	}
	// An older period's sum goes when the next one is kept.
	c.kept(k, func() (map[int64]Totals, error) { return map[int64]Totals{}, nil }) //nolint:errcheck // cannot fail
	next := totalsKey{"stats_hourly", hour, 2 * hour}
	c.kept(next, func() (map[int64]Totals, error) { return map[int64]Totals{}, nil }) //nolint:errcheck // cannot fail
	if _, ok := c.entries[k]; ok || len(c.entries) != 1 {
		t.Errorf("entries %v", c.entries)
	}
	// A save in the latest periods leaves them.
	c.saved(time.Now())
	if len(c.entries) != 1 {
		t.Error("a save now dropped the kept sums")
	}
}

// sameForOne checks that each monitor's own sums are those of all.
func sameForOne(t *testing.T, s *Store, since time.Time, mons ...Monitor) {
	t.Helper()
	ctx := context.Background()
	for _, m := range mons {
		all, _ := s.HourlyTotals(ctx, since)
		one, err := s.MonitorHourlyTotals(ctx, m.ID, since)
		if err != nil || one != all[m.ID] {
			t.Errorf("%s hourly: %+v, of all %+v, %v", m.Name, one, all[m.ID], err)
		}
		allDays, _ := s.DailyTotals(ctx, since)
		oneDay, err := s.MonitorDailyTotals(ctx, m.ID, since)
		if err != nil || oneDay != allDays[m.ID] {
			t.Errorf("%s daily: %+v, of all %+v, %v", m.Name, oneDay, allDays[m.ID], err)
		}
	}
}

// pruneAndRestore checks that pruning and restoring drop the kept sums.
func pruneAndRestore(t *testing.T, s *Store, now time.Time, check func(string)) {
	t.Helper()
	ctx := context.Background()
	since := now.Add(-48 * time.Hour)
	if err := s.Prune(ctx, now.Add(-24*time.Hour), now.Add(-24*time.Hour), now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	check("pruned")
	snap, err := s.Export(ctx, true, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	s.HourlyTotals(ctx, since) //nolint:errcheck // keeps the sums again
	if err := s.Import(ctx, roundTrip(t, snap), false); err != nil {
		t.Fatal(err)
	}
	if s.cache.entries != nil {
		t.Error("a restore kept the sums")
	}
	check("restored")
}
