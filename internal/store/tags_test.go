// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func TestTags(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		var bad *InvalidError
		for _, tg := range []Tag{{Name: " ", Color: "#000000"}, {Name: "Prod", Color: "red"}} {
			if _, err := s.CreateTag(ctx, tg, t0); !errors.As(err, &bad) {
				t.Errorf("%+v: %v", tg, err)
			}
		}
		prod, err := s.CreateTag(ctx, Tag{Name: " Prod ", Color: "#1F5FBF"}, t0)
		if err != nil || prod.Name != "Prod" || prod.Color != "#1f5fbf" || !prod.CreatedAt.Equal(t0) {
			t.Fatalf("prod %+v %v", prod, err)
		}
		if _, err := s.CreateTag(ctx, Tag{Name: "PROD", Color: "#000000"}, t0); !errors.As(err, &bad) {
			t.Errorf("same name: %v", err)
		}
		db, _ := s.CreateTag(ctx, Tag{Name: "db", Color: "#12a150"}, t0)
		if _, err := s.UpdateTag(ctx, Tag{ID: db.ID, Name: "prod", Color: "#12a150"}, t0); !errors.As(err, &bad) {
			t.Errorf("rename to a taken name: %v", err)
		}
		if _, err := s.UpdateTag(ctx, Tag{ID: db.ID, Name: "x", Color: "nope"}, t0); !errors.As(err, &bad) {
			t.Errorf("bad colour: %v", err)
		}
		if _, err := s.UpdateTag(ctx, Tag{ID: 999, Name: "x", Color: "#000000"}, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: %v", err)
		}
		db, err = s.UpdateTag(ctx, Tag{ID: db.ID, Name: "Database", Color: "#12A150"}, t0)
		if err != nil || db.Name != "Database" {
			t.Errorf("rename %+v %v", db, err)
		}
	})
}

// TestTagsAndWindowsClosed checks that every call reports a database that
// is gone.
func TestTagsAndWindowsClosed(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		s.Close()
		sched := json.RawMessage(`{}`)
		for name, f := range map[string]func() error{
			"tags":         func() error { _, err := s.Tags(ctx); return err },
			"monitor tags": func() error { _, err := s.MonitorTags(ctx); return err },
			"set tags":     func() error { return s.SetMonitorTags(ctx, 1, nil) },
			"create tag":   func() error { _, err := s.CreateTag(ctx, Tag{Name: "x", Color: "#000000"}, t0); return err },
			"update tag":   func() error { _, err := s.UpdateTag(ctx, Tag{ID: 1, Name: "x", Color: "#000000"}, t0); return err },
			"delete tag":   func() error { return s.DeleteTag(ctx, 1) },
			"windows":      func() error { _, err := s.Windows(ctx); return err },
			"window":       func() error { _, err := s.WindowByID(ctx, 1); return err },
			"create":       func() error { _, err := s.CreateWindow(ctx, Window{Schedule: sched}, t0); return err },
			"update":       func() error { _, err := s.UpdateWindow(ctx, Window{ID: 1, Schedule: sched}, t0); return err },
			"delete":       func() error { return s.DeleteWindow(ctx, 1) },
		} {
			if f() == nil {
				t.Errorf("%s worked without a database", name)
			}
		}
	})
}

func TestMonitorTags(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		var bad *InvalidError
		prod, _ := s.CreateTag(ctx, Tag{Name: "Prod", Color: "#000000"}, t0)
		db, _ := s.CreateTag(ctx, Tag{Name: "Database", Color: "#000000"}, t0)
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		if err := s.SetMonitorTags(ctx, a.ID, []MonitorTag{{TagID: prod.ID, Value: " eu "}, {TagID: db.ID}}); err != nil {
			t.Fatal(err)
		}
		s.SetMonitorTags(ctx, b.ID, []MonitorTag{{TagID: prod.ID}})
		for name, tags := range map[string][]MonitorTag{
			"twice":      {{TagID: prod.ID}, {TagID: prod.ID}},
			"no such":    {{TagID: 999}},
			"long value": {{TagID: prod.ID, Value: string(make([]rune, 101))}},
		} {
			if err := s.SetMonitorTags(ctx, b.ID, tags); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
		all, err := s.MonitorTags(ctx)
		// Database before Prod: by the tags' names.
		if err != nil || !slices.Equal(all[a.ID], []MonitorTag{{db.ID, ""}, {prod.ID, "eu"}}) || !slices.Equal(all[b.ID], []MonitorTag{{prod.ID, ""}}) {
			t.Fatalf("monitor tags %v %v", all, err)
		}
		tags, err := s.Tags(ctx)
		if err != nil || len(tags) != 2 || tags[0].Name != "Database" || tags[0].Monitors != 1 || tags[1].Monitors != 2 {
			t.Fatalf("tags %+v %v", tags, err)
		}
	})
}

func TestDeleteTag(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		prod, _ := s.CreateTag(ctx, Tag{Name: "prod", Color: "#000000"}, t0)
		db, _ := s.CreateTag(ctx, Tag{Name: "db", Color: "#000000"}, t0)
		s.SetMonitorTags(ctx, a.ID, []MonitorTag{{TagID: prod.ID}, {TagID: db.ID}})
		// Deleting a tag takes it off monitors and windows.
		w, err := s.CreateWindow(ctx, Window{Title: "W", Schedule: json.RawMessage(`{}`), TagIDs: []int64{db.ID}}, t0)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteTag(ctx, db.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteTag(ctx, db.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("again: %v", err)
		}
		if all, _ := s.MonitorTags(ctx); len(all[a.ID]) != 1 {
			t.Errorf("after delete %v", all)
		}
		if w, _ = s.WindowByID(ctx, w.ID); len(w.TagIDs) != 0 {
			t.Errorf("window tags %v", w.TagIDs)
		}
		if _, err := s.TagByID(ctx, db.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("by id: %v", err)
		}
	})
}
