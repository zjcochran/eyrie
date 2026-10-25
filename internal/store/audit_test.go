// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"
)

func TestAudit(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		for i, e := range []AuditEntry{
			{Actor: "alice", IP: "192.0.2.1", Action: "signed in"},
			{Actor: "alice", IP: "192.0.2.1", Action: "tag created", Details: "tag=prod"},
			{Actor: "bob", IP: "192.0.2.2", Action: "signed in"},
			{Actor: "", IP: "192.0.2.3", Action: "failed sign-in"},
		} {
			e.At = t0.Add(time.Duration(i) * time.Minute)
			if err := s.AddAudit(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		all, err := s.Audit(ctx, AuditFilter{})
		if err != nil || len(all) != 4 || all[0].Action != "failed sign-in" || !all[3].At.Equal(t0) || all[2].Details != "tag=prod" {
			t.Fatalf("all %+v %v", all, err)
		}
		if got, _ := s.Audit(ctx, AuditFilter{Action: "signed in", Actor: "alice"}); len(got) != 1 || got[0].IP != "192.0.2.1" {
			t.Errorf("filtered %+v", got)
		}
		if got, _ := s.Audit(ctx, AuditFilter{BeforeID: all[1].ID, Limit: 1}); len(got) != 1 || got[0].ID != all[2].ID {
			t.Errorf("page %+v", got)
		}
		actions, actors, err := s.AuditNames(ctx)
		if err != nil || len(actions) != 3 || actions[0] != "failed sign-in" || len(actors) != 2 || actors[1] != "bob" {
			t.Errorf("names %v %v %v", actions, actors, err)
		}
		if err := s.Prune(ctx, t0, t0, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Audit(ctx, AuditFilter{}); len(got) != 2 {
			t.Errorf("after pruning %+v", got)
		}
	})
}

func TestAuditClosed(t *testing.T) {
	s := openSQLite(t)
	ctx := context.Background()
	s.Close()
	if _, err := s.Audit(ctx, AuditFilter{}); err == nil {
		t.Error("closed: no error")
	}
	if _, _, err := s.AuditNames(ctx); err == nil {
		t.Error("closed names: no error")
	}
}
