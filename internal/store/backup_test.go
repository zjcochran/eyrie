// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fill gives a store some of everything a backup carries.
func fill(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC)
	admin, err := s.CreateFirstAdmin(ctx, "alice", "a long password here", now)
	if err != nil {
		t.Fatal(err)
	}
	bob, _ := s.CreateUser(ctx, "bob", "another long password", RoleViewer, now)
	s.SetUserDisabled(ctx, bob.ID, true)
	s.SetUserPicture(ctx, admin.ID, "image/png", []byte{0x89, 'P', 'N', 'G', 0, 1, 2}, "upload", now)
	s.CreateSession(ctx, admin.ID, "192.0.2.1", "test", now)
	s.CreateAPIToken(ctx, "ci", "alice", now)
	s.SetSetting(ctx, "retention", map[string]int{"heartbeat_days": 30}, now)
	ch, err := s.CreateChannel(ctx, Channel{Name: "ntfy", Kind: "ntfy", URL: "sealed-url", Enabled: true, Default: true}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	tag, _ := s.CreateTag(ctx, Tag{Name: "prod", Color: "#1f5fbf"}, now)
	// A child made before its group, so the group's id is the larger one.
	child, err := s.CreateMonitor(ctx, Monitor{Name: "DB", Type: "port", Active: true, Interval: time.Minute, RetryInterval: time.Minute,
		Timeout: time.Second, Config: json.RawMessage(`{"host":"db.example.com","port":5432}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	group, _ := s.CreateMonitor(ctx, Monitor{Name: "Shop", Type: "group", Active: true, Interval: time.Minute,
		RetryInterval: time.Minute, Timeout: time.Second, Config: json.RawMessage(`{}`)}, now)
	child.ParentID = group.ID
	if _, err := s.UpdateMonitor(ctx, child, now); err != nil {
		t.Fatal(err)
	}
	s.SetMonitorChannels(ctx, child.ID, []int64{ch.ID})
	s.SetMonitorTags(ctx, child.ID, []MonitorTag{{TagID: tag.ID, Value: "eu"}})
	if _, err := s.CreateWindow(ctx, Window{Title: "Patch", Enabled: true, Schedule: json.RawMessage(`{"strategy":"manual"}`),
		MonitorIDs: []int64{child.ID}, TagIDs: []int64{tag.ID}}, now); err != nil {
		t.Fatal(err)
	}
	page, err := s.CreatePage(ctx, Page{Slug: "shop", Title: "Shop", Published: true, Theme: "auto",
		Sections: []Section{{Title: "Core", TagID: tag.ID, MonitorIDs: []int64{group.ID}}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	s.SetPageLogo(ctx, page.ID, "image/png", []byte{1, 2, 3}, now)
	inc, _ := s.CreateIncident(ctx, page.ID, "Slow checkout", "investigating", "Looking", "alice", now)
	s.AddIncidentUpdate(ctx, inc.ID, "resolved", "Fixed", "alice", now.Add(time.Hour))
	var recs []Record
	for i, at := range []time.Time{now.Add(-48 * time.Hour), now.Add(-time.Hour), now} {
		recs = append(recs, Record{Beat: Beat{MonitorID: child.ID, At: at, Status: "up", Latency: time.Duration(i+1) * time.Millisecond, Important: i == 0},
			State: MonitorState{MonitorID: child.ID, Status: "up", Since: now.Add(-48 * time.Hour), LastCheck: at, Latency: time.Millisecond}})
	}
	if err := s.Save(ctx, recs); err != nil {
		t.Fatal(err)
	}
}

// roundTrip is a snapshot as it comes back out of a backup file.
func roundTrip(t *testing.T, snap Snapshot) Snapshot {
	t.Helper()
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out Snapshot
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func asJSON(t *testing.T, snap Snapshot) string {
	t.Helper()
	b, _ := json.Marshal(snap)
	return string(b)
}

func TestBackupAcrossDatabases(t *testing.T) {
	opens := map[string]func(*testing.T) *Store{"sqlite": openSQLite, "postgres": openPostgres}
	for from, openFrom := range opens {
		for to, openTo := range opens {
			t.Run(from+" to "+to, func(t *testing.T) {
				ctx := context.Background()
				src, dst := openFrom(t), openTo(t)
				fill(t, src)
				snap, err := src.Export(ctx, true, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				fill(t, dst) // replaced by the restore
				if err := dst.Import(ctx, roundTrip(t, snap), false); err != nil {
					t.Fatal(err)
				}
				again, err := dst.Export(ctx, true, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				if a, b := asJSON(t, snap), asJSON(t, again); a != b {
					t.Errorf("restored data differs\nfrom %s\n  to %s", a, b)
				}
				// Sessions end; new rows get new ids; the parent survived.
				if n, _ := dst.CountUsers(ctx); n != 2 {
					t.Errorf("users %d", n)
				}
				var sessions int
				dst.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessions)
				ms, _ := dst.Monitors(ctx)
				m, err := dst.CreateMonitor(ctx, Monitor{Name: "New", Type: "manual", Interval: time.Minute, RetryInterval: time.Minute,
					Timeout: time.Second, Config: json.RawMessage(`{}`)}, time.Now())
				if sessions != 0 || len(ms) != 2 || err != nil || m.ID <= ms[1].ID && m.ID <= ms[0].ID {
					t.Errorf("sessions %d, monitors %+v, new %d %v", sessions, ms, m.ID, err)
				}
				parents := 0
				for _, x := range ms {
					if x.ParentID != 0 {
						parents++
					}
				}
				if parents != 1 {
					t.Errorf("parent lost: %+v", ms)
				}
			})
		}
	}
}

func TestBackupHistory(t *testing.T) {
	ctx := context.Background()
	s := openSQLite(t)
	fill(t, s)
	snap, err := s.Export(ctx, false, time.Time{})
	if err != nil || snap.History != nil || len(snap.Config["users"]) != 2 {
		t.Fatalf("configuration only: %v %v", snap.History, err)
	}
	if strings.Contains(asJSON(t, snap), `"sessions"`) {
		t.Error("sessions exported")
	}
	since := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	snap, _ = s.Export(ctx, true, since)
	if n := len(snap.History["heartbeats"]); n != 2 {
		t.Errorf("heartbeats since %v: %d", since, n)
	}
	if n := len(snap.History["monitor_state"]); n != 1 {
		t.Errorf("states: %d", n)
	}
	// A configuration-only restore leaves no history: it belonged to the
	// monitors it replaced.
	cfg, _ := s.Export(ctx, false, time.Time{})
	if err := s.Import(ctx, roundTrip(t, cfg), false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Export(ctx, true, time.Time{})
	if len(after.History["heartbeats"]) != 0 || len(after.Config["monitors"]) != 2 {
		t.Errorf("after: %d heartbeats", len(after.History["heartbeats"]))
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v < 6 {
		t.Errorf("schema %d %v", v, err)
	}
}

func TestBackupRefused(t *testing.T) {
	ctx := context.Background()
	s := openSQLite(t)
	fill(t, s)
	good, _ := s.Export(ctx, false, time.Time{})
	before := asJSON(t, good)
	bad := func(edit func(Snapshot)) Snapshot {
		snap := roundTrip(t, good)
		edit(snap)
		return snap
	}
	cases := map[string]Snapshot{
		"unknown table":   bad(func(s Snapshot) { s.Config["sessions"] = []Row{} }),
		"unknown column":  bad(func(s Snapshot) { s.Config["tags"][0]["evil"] = "x" }),
		"wrong kind":      bad(func(s Snapshot) { s.Config["users"][0]["disabled"] = "yes" }),
		"wrong number":    bad(func(s Snapshot) { s.Config["tags"][0]["id"] = "x" }),
		"bad bytes":       bad(func(s Snapshot) { s.Config["status_page_logos"][0]["data"] = "!!" }),
		"broken key":      bad(func(s Snapshot) { s.Config["monitor_tags"][0]["tag_id"] = json.Number("999") }),
		"history table":   {Config: good.Config, History: map[string][]Row{"users": nil}},
		"no admin":        bad(func(s Snapshot) { s.Config["users"][0]["role"] = "viewer" }),
		"disabled admins": bad(func(s Snapshot) { s.Config["users"][0]["disabled"] = true }),
	}
	for name, snap := range cases {
		err := s.Import(ctx, snap, false)
		if err == nil {
			t.Errorf("%s: imported", name)
		}
		if name == "no admin" && !errors.Is(err, ErrNoAdmin) {
			t.Errorf("no admin: %v", err)
		}
	}
	if err := s.Import(ctx, roundTrip(t, good), true); !errors.Is(err, ErrSetupDone) {
		t.Errorf("into a used store: %v", err)
	}
	after, _ := s.Export(ctx, false, time.Time{})
	if asJSON(t, after) != before {
		t.Error("a refused restore changed something")
	}
	empty := openSQLite(t)
	if err := empty.Import(ctx, roundTrip(t, good), true); err != nil {
		t.Errorf("into an empty store: %v", err)
	}
}

func TestBackupValues(t *testing.T) {
	s := &Store{dialect: SQLite}
	for _, c := range []struct {
		kind string
		in   any
		want any
	}{
		{kindInt, json.Number("7"), int64(7)}, {kindInt, 7.0, int64(7)}, {kindFloat, json.Number("1.5"), 1.5}, {kindFloat, 2.5, 2.5},
		{kindBool, true, int64(1)}, {kindBool, false, int64(0)}, {kindBytes, "AQI=", []byte{1, 2}}, {kindText, "x", "x"}, {kindText, nil, nil},
	} {
		got, err := s.importValue(c.kind, c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v: %v %v", c.kind, c.in, got, err)
		}
	}
	for _, c := range []struct {
		kind string
		in   any
	}{{kindFloat, "x"}, {kindText, 1.0}, {kindInt, true}} {
		if _, err := s.importValue(c.kind, c.in); err == nil {
			t.Errorf("%s %v accepted", c.kind, c.in)
		}
	}
	pg := &Store{dialect: Postgres}
	if v, _ := pg.importValue(kindBool, true); v != true {
		t.Error("PostgreSQL booleans")
	}
	for typ, kind := range map[string]string{"BIGINT": kindInt, "boolean": kindBool, "BLOB": kindBytes, "bytea": kindBytes,
		"double precision": kindFloat, "TEXT": kindText} {
		if kindOf(typ) != kind {
			t.Errorf("%s: %s", typ, kindOf(typ))
		}
	}
	if v := exportValue(kindText, []byte("x")); v != "x" {
		t.Errorf("text bytes: %v", v)
	}
	if v := exportValue(kindInt, int32(3)); v != int64(3) {
		t.Errorf("int32: %v", v)
	}
	if _, err := openSQLite(t).columns(context.Background(), openSQLite(t).db, "nope"); err == nil {
		t.Error("no table")
	}
}

// A backup of an older version lacks the tables and columns later
// migrations added: they restore empty and with their defaults.
func TestBackupOlderSchema(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		fill(t, s)
		snap, err := s.Export(ctx, true, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		old := roundTrip(t, snap)
		for _, r := range old.History["monitor_state"] {
			delete(r, "cert_notified") // added by 00004
			delete(r, "before_status") // added by 00005
			delete(r, "before_since")
		}
		for _, table := range []string{"monitor_state", "heartbeats"} {
			for _, r := range old.History[table] {
				delete(r, "latency_us") // added by 00007
			}
		}
		for _, table := range []string{"stats_hourly", "stats_daily"} {
			for _, r := range old.History[table] {
				delete(r, "latency_us_sum")
			}
		}
		for _, table := range []string{"status_pages", "status_page_logos", "status_page_sections", "status_page_monitors", "incidents", "incident_updates"} {
			delete(old.Config, table) // added by 00006
		}
		if err := s.Import(ctx, old, false); err != nil {
			t.Fatal(err)
		}
		after, _ := s.Export(ctx, true, time.Time{})
		st := after.History["monitor_state"]
		if len(st) != 1 || st[0]["before_status"] != "" || fmt.Sprint(st[0]["cert_notified"]) != "0" {
			t.Errorf("states: %v", st)
		}
		// Response times come back from the milliseconds.
		var child int64
		ms, _ := s.Monitors(ctx)
		for _, m := range ms {
			if m.Type == "port" {
				child = m.ID
			}
		}
		if beats, _ := s.Heartbeats(ctx, child, time.Time{}, 10); len(beats) != 3 || beats[0].Latency != 3*time.Millisecond {
			t.Errorf("beats %+v", beats)
		}
		if days, _ := s.DailyTotals(ctx, time.Time{}); days[child].AvgLatency() != 2*time.Millisecond {
			t.Errorf("days %+v", days)
		}
		if len(after.Config["status_pages"]) != 0 || len(after.Config["monitors"]) != 2 {
			t.Errorf("after: %d pages, %d monitors", len(after.Config["status_pages"]), len(after.Config["monitors"]))
		}
	})
}
