// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIncidents(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		p, _ := s.CreatePage(ctx, newPage("shop"), t0)
		in, err := s.CreateIncident(ctx, p.ID, " Checkout errors ", "investigating", " Payments fail. ", "alice", t0)
		if err != nil || in.Title != "Checkout errors" || in.Status != "investigating" || !in.ResolvedAt.IsZero() ||
			len(in.Updates) != 1 || in.Updates[0].Message != "Payments fail." || in.Updates[0].Author != "alice" {
			t.Fatalf("open %+v %v", in, err)
		}
		t1 := t0.Add(time.Hour)
		if in, err = s.AddIncidentUpdate(ctx, in.ID, "resolved", "Fixed.", "bob", t1); err != nil || in.Status != "resolved" ||
			!in.ResolvedAt.Equal(t1) || len(in.Updates) != 2 || in.Updates[0].Message != "Fixed." || !in.UpdatedAt.Equal(t1) {
			t.Fatalf("resolve %+v %v", in, err)
		}
		// Reopened by an update that is not resolved.
		if in, _ = s.AddIncidentUpdate(ctx, in.ID, "monitoring", "Again.", "bob", t1.Add(time.Minute)); !in.ResolvedAt.IsZero() {
			t.Errorf("reopen %+v", in)
		}
		if in, err = s.RenameIncident(ctx, in.ID, "Checkout", t1); err != nil || in.Title != "Checkout" {
			t.Errorf("rename %+v %v", in, err)
		}
	})
}

func TestIncidentErrors(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		p, _ := s.CreatePage(ctx, newPage("shop"), t0)
		in, _ := s.CreateIncident(ctx, p.ID, "x", "investigating", "m", "alice", t0)
		t1 := t0.Add(time.Hour)
		var bad *InvalidError
		for name, f := range map[string]func() error{
			"title": func() error {
				_, err := s.CreateIncident(ctx, p.ID, " ", "investigating", "m", "alice", t0)
				return err
			},
			"status": func() error { _, err := s.CreateIncident(ctx, p.ID, "x", "fixed", "m", "alice", t0); return err },
			"message": func() error {
				_, err := s.CreateIncident(ctx, p.ID, "x", "investigating", " ", "alice", t0)
				return err
			},
		} {
			if err := f(); !errors.As(err, &bad) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := s.CreateIncident(ctx, 999, "x", "investigating", "m", "alice", t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing page: %v", err)
		}
		if _, err := s.AddIncidentUpdate(ctx, 999, "resolved", "x", "bob", t1); !errors.Is(err, ErrNotFound) {
			t.Errorf("update missing: %v", err)
		}
		if _, err := s.AddIncidentUpdate(ctx, in.ID, "nope", "x", "bob", t1); !errors.As(err, &bad) {
			t.Errorf("bad status: %v", err)
		}
		if _, err := s.RenameIncident(ctx, in.ID, "", t1); !errors.As(err, &bad) {
			t.Errorf("rename empty: %v", err)
		}
		if _, err := s.RenameIncident(ctx, 999, "x", t1); !errors.Is(err, ErrNotFound) {
			t.Errorf("rename missing: %v", err)
		}
	})
}

func TestIncidentList(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		p, _ := s.CreatePage(ctx, newPage("shop"), t0)
		old, _ := s.CreateIncident(ctx, p.ID, "Old", "resolved", "Done.", "alice", t0.Add(-30*24*time.Hour))
		recent, _ := s.CreateIncident(ctx, p.ID, "Recent", "resolved", "Done.", "alice", t0.Add(-time.Hour))
		open, _ := s.CreateIncident(ctx, p.ID, "Open", "identified", "Working on it.", "alice", t0)
		list, err := s.Incidents(ctx, p.ID, t0.Add(-14*24*time.Hour))
		if err != nil || len(list) != 2 || list[0].ID != open.ID || list[1].ID != recent.ID {
			t.Fatalf("list %+v %v", list, err)
		}
		if err := s.DeleteIncident(ctx, old.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteIncident(ctx, old.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete again: %v", err)
		}
		// Deleting the page deletes its incidents.
		s.DeletePage(ctx, p.ID)
		if _, err := s.IncidentByID(ctx, open.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("after page: %v", err)
		}
	})
}
