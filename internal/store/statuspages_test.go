// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func newPage(slug string) Page {
	return Page{Slug: slug, Title: "Shop status", Theme: "auto"}
}

func TestPages(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		tag, _ := s.CreateTag(ctx, Tag{Name: "prod", Color: "#000000"}, t0)
		p := newPage(" Shop ")
		p.Accent, p.Published = "#1F5FBF", true
		p.Sections = []Section{{Title: " Web ", TagID: tag.ID, MonitorIDs: []int64{b.ID, a.ID, a.ID}}, {Title: "Other"}}
		p, err := s.CreatePage(ctx, p, t0)
		if err != nil || p.Slug != "shop" || p.Accent != "#1f5fbf" || len(p.Sections) != 2 || p.Sections[0].Title != "Web" ||
			p.Sections[0].TagID != tag.ID || !slices.Equal(p.Sections[0].MonitorIDs, []int64{a.ID, b.ID}) ||
			len(p.Sections[1].MonitorIDs) != 0 || !p.LogoUpdated.IsZero() || !p.CreatedAt.Equal(t0) {
			t.Fatalf("create %+v %v", p, err)
		}
		other, _ := s.CreatePage(ctx, newPage("other"), t0)
		later := t0.Add(time.Hour)
		p.Title, p.Sections = "Shop", []Section{{Title: "Only", MonitorIDs: []int64{b.ID}}}
		if p, err = s.UpdatePage(ctx, p, later); err != nil || p.Title != "Shop" || len(p.Sections) != 1 ||
			!slices.Equal(p.Sections[0].MonitorIDs, []int64{b.ID}) || !p.UpdatedAt.Equal(later) {
			t.Fatalf("update %+v %v", p, err)
		}
		list, err := s.Pages(ctx)
		if err != nil || len(list) != 2 || list[0].Slug != "shop" || list[1].ID != other.ID { // by title: Shop, Shop status
			t.Errorf("list %+v %v", list, err)
		}
	})
}

func TestPageLookup(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		p, _ := s.CreatePage(ctx, newPage("shop"), t0)
		if got, err := s.PageBySlug(ctx, "shop"); err != nil || got.ID != p.ID {
			t.Errorf("by slug %+v %v", got, err)
		}
		if _, err := s.PageBySlug(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing slug: %v", err)
		}
		if err := s.DeletePage(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeletePage(ctx, p.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete again: %v", err)
		}
	})
}

func TestPageValidation(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		var bad *InvalidError
		long := string(make([]rune, 2001))
		for name, p := range map[string]Page{
			"slug":        {Slug: "Not a slug", Title: "x", Theme: "auto"},
			"double dash": {Slug: "a--b", Title: "x", Theme: "auto"},
			"title":       {Slug: "x", Title: " ", Theme: "auto"},
			"footer":      {Slug: "x", Title: "x", Theme: "auto", Footer: long},
			"theme":       {Slug: "x", Title: "x", Theme: "pink"},
			"accent":      {Slug: "x", Title: "x", Theme: "auto", Accent: "blue"},
			"sections":    {Slug: "x", Title: "x", Theme: "auto", Sections: make([]Section, 51)},
			"section":     {Slug: "x", Title: "x", Theme: "auto", Sections: []Section{{Title: string(make([]rune, 101))}}},
			"no tag":      {Slug: "x", Title: "x", Theme: "auto", Sections: []Section{{TagID: 999}}},
			"no monitor":  {Slug: "x", Title: "x", Theme: "auto", Sections: []Section{{MonitorIDs: []int64{999}}}},
		} {
			if _, err := s.CreatePage(ctx, p, t0); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
		p, _ := s.CreatePage(ctx, newPage("x"), t0)
		p.Theme = "pink"
		if _, err := s.UpdatePage(ctx, p, t0); !errors.As(err, &bad) {
			t.Errorf("update: %v", err)
		}
		if _, err := s.CreatePage(ctx, newPage("x"), t0); !errors.As(err, &bad) {
			t.Errorf("same address: %v", err)
		}
		other, _ := s.CreatePage(ctx, newPage("other"), t0)
		other.Slug = "x"
		if _, err := s.UpdatePage(ctx, other, t0); !errors.As(err, &bad) {
			t.Errorf("rename to a taken address: %v", err)
		}
		if _, err := s.UpdatePage(ctx, Page{ID: 999, Slug: "y", Title: "y", Theme: "auto"}, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("update missing: %v", err)
		}
	})
}

func TestPublishedMonitorsAndLogo(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		c, _ := s.CreateMonitor(ctx, newMonitor("C", "http"), t0)
		tag, _ := s.CreateTag(ctx, Tag{Name: "prod", Color: "#000000"}, t0)
		s.SetMonitorTags(ctx, b.ID, []MonitorTag{{TagID: tag.ID}})
		pub := newPage("pub")
		pub.Published, pub.Sections = true, []Section{{MonitorIDs: []int64{a.ID}}, {TagID: tag.ID}}
		pub, _ = s.CreatePage(ctx, pub, t0)
		draft := newPage("draft")
		draft.Sections = []Section{{MonitorIDs: []int64{c.ID}}}
		s.CreatePage(ctx, draft, t0)
		ids, err := s.PublishedMonitors(ctx)
		if err != nil || !ids[a.ID] || !ids[b.ID] || ids[c.ID] {
			t.Errorf("published %v %v", ids, err)
		}

		if _, ok, err := s.PageLogo(ctx, pub.ID); ok || err != nil {
			t.Errorf("no logo yet: %v %v", ok, err)
		}
		if err := s.SetPageLogo(ctx, pub.ID, "image/png", []byte("png"), t0); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPageLogo(ctx, 999, "image/png", []byte("png"), t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("logo of a missing page: %v", err)
		}
		logo, ok, err := s.PageLogo(ctx, pub.ID)
		if err != nil || !ok || logo.ContentType != "image/png" || string(logo.Data) != "png" || !logo.UpdatedAt.Equal(t0) {
			t.Errorf("logo %+v %v %v", logo, ok, err)
		}
		if p, _ := s.PageByID(ctx, pub.ID); !p.LogoUpdated.Equal(t0) {
			t.Errorf("page logo time %v", p.LogoUpdated)
		}
		s.DeletePageLogo(ctx, pub.ID)
		if _, ok, _ := s.PageLogo(ctx, pub.ID); ok {
			t.Error("logo kept")
		}
	})
}

func TestDays(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		m, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		var recs []Record
		for i, st := range []string{"up", "down", "up"} {
			at := t0.Add(time.Duration(i-1) * 24 * time.Hour)
			recs = append(recs, Record{Beat: Beat{MonitorID: m.ID, At: at, Status: st, Latency: -1},
				State: MonitorState{MonitorID: m.ID, Status: st, Since: at, LastCheck: at}})
		}
		s.Save(ctx, recs)
		days, err := s.Days(ctx, t0.Add(-24*time.Hour))
		if d := days[m.ID]; err != nil || len(d) != 3 || d[1].Down != 1 || d[2].Up != 1 || !d[0].Start.Equal(t0.Add(-24*time.Hour).Truncate(24*time.Hour)) {
			t.Errorf("days %+v %v", d, err)
		}
	})
}

func TestPagesAndIncidentsClosed(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		s.Close()
		for name, f := range map[string]func() error{
			"pages":       func() error { _, err := s.Pages(ctx); return err },
			"page":        func() error { _, err := s.PageByID(ctx, 1); return err },
			"create":      func() error { _, err := s.CreatePage(ctx, newPage("x"), t0); return err },
			"update":      func() error { p := newPage("x"); p.ID = 1; _, err := s.UpdatePage(ctx, p, t0); return err },
			"delete":      func() error { return s.DeletePage(ctx, 1) },
			"published":   func() error { _, err := s.PublishedMonitors(ctx); return err },
			"set logo":    func() error { return s.SetPageLogo(ctx, 1, "image/png", nil, t0) },
			"logo":        func() error { _, _, err := s.PageLogo(ctx, 1); return err },
			"days":        func() error { _, err := s.Days(ctx, t0); return err },
			"incidents":   func() error { _, err := s.Incidents(ctx, 1, t0); return err },
			"incident":    func() error { _, err := s.IncidentByID(ctx, 1); return err },
			"open":        func() error { _, err := s.CreateIncident(ctx, 1, "x", "investigating", "m", "alice", t0); return err },
			"add update":  func() error { _, err := s.AddIncidentUpdate(ctx, 1, "resolved", "m", "alice", t0); return err },
			"rename":      func() error { _, err := s.RenameIncident(ctx, 1, "x", t0); return err },
			"delete inc.": func() error { return s.DeleteIncident(ctx, 1) },
		} {
			if f() == nil {
				t.Errorf("%s worked without a database", name)
			}
		}
	})
}
