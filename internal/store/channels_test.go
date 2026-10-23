// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func newChannel(name string) Channel {
	return Channel{Name: name, Kind: "ntfy", URL: "v1:sealed-url", Token: "v1:sealed-token", Enabled: true}
}

func TestChannels(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		var bad *InvalidError
		if _, err := s.CreateChannel(ctx, newChannel(" "), false, t0); !errors.As(err, &bad) {
			t.Errorf("no name: %v", err)
		}
		phone, err := s.CreateChannel(ctx, newChannel(" Phone "), true, t0)
		if err != nil || phone.Name != "Phone" || phone.Monitors != 2 || phone.URL != "v1:sealed-url" || !phone.CreatedAt.Equal(t0) {
			t.Fatalf("phone %+v %v", phone, err)
		}
		mail := newChannel("Mail")
		mail.Kind, mail.Default, mail.Recipients, mail.Sender = "email", true, "alice@example.com", "eyrie@example.com"
		mail, err = s.CreateChannel(ctx, mail, false, t0)
		if err != nil || mail.Monitors != 0 || !mail.Default {
			t.Fatalf("mail %+v %v", mail, err)
		}
		if ids, err := s.DefaultChannels(ctx); err != nil || !slices.Equal(ids, []int64{mail.ID}) {
			t.Errorf("defaults %v %v", ids, err)
		}

		if err := s.SetMonitorChannels(ctx, a.ID, []int64{mail.ID, mail.ID}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetMonitorChannels(ctx, a.ID, []int64{9999}); !errors.As(err, &bad) {
			t.Errorf("unknown channel: %v", err)
		}
		all, err := s.MonitorChannels(ctx)
		if err != nil || !slices.Equal(all[a.ID], []int64{mail.ID}) || !slices.Equal(all[b.ID], []int64{phone.ID}) {
			t.Fatalf("monitor channels %v %v", all, err)
		}

	})
}

func TestUpdateDeleteChannels(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		var bad *InvalidError
		s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		b, _ := s.CreateMonitor(ctx, newMonitor("B", "http"), t0)
		phone, _ := s.CreateChannel(ctx, newChannel("Phone"), true, t0)
		mail, _ := s.CreateChannel(ctx, newChannel("Mail"), false, t0)
		mail.Enabled, mail.Name = false, "Mail (off)"
		later := t0.Add(time.Hour)
		mail, err := s.UpdateChannel(ctx, mail, true, later)
		if err != nil || mail.Monitors != 2 || mail.Enabled || !mail.UpdatedAt.Equal(later) {
			t.Fatalf("update %+v %v", mail, err)
		}
		// Disabled channels are not used.
		if cs, err := s.ChannelsForMonitor(ctx, b.ID); err != nil || len(cs) != 1 || cs[0].ID != phone.ID {
			t.Errorf("for B %+v %v", cs, err)
		}
		if _, err := s.UpdateChannel(ctx, Channel{ID: 9999, Name: "x"}, false, later); !errors.Is(err, ErrNotFound) {
			t.Errorf("update unknown: %v", err)
		}
		if _, err := s.UpdateChannel(ctx, Channel{ID: mail.ID}, false, later); !errors.As(err, &bad) {
			t.Errorf("update without a name: %v", err)
		}
		list, err := s.Channels(ctx)
		if err != nil || len(list) != 2 || list[0].Name != "Mail (off)" {
			t.Errorf("list %+v %v", list, err)
		}

		// Deleting a channel or a monitor removes the links.
		if err := s.DeleteChannel(ctx, phone.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteChannel(ctx, phone.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete twice: %v", err)
		}
		s.DeleteMonitor(ctx, b.ID)
		if c, _ := s.ChannelByID(ctx, mail.ID); c.Monitors != 1 {
			t.Errorf("after deletes %+v", c)
		}
		if _, err := s.ChannelByID(ctx, 9999); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown: %v", err)
		}
	})
}

func TestCertNotifiedSaved(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		m, _ := s.CreateMonitor(ctx, newMonitor("A", "http"), t0)
		r := beat(m.ID, t0, "up", 1)
		r.State.CertNotified = 14
		if err := s.Save(ctx, []Record{r}); err != nil {
			t.Fatal(err)
		}
		if st, _ := s.States(ctx); st[m.ID].CertNotified != 14 {
			t.Errorf("state %+v", st[m.ID])
		}
	})
}

func TestChannelsClosed(t *testing.T) {
	s := openSQLite(t)
	ctx := context.Background()
	s.Close()
	checks := map[string]error{}
	_, checks["create"] = s.CreateChannel(ctx, newChannel("x"), false, t0)
	_, checks["update"] = s.UpdateChannel(ctx, newChannel("x"), false, t0)
	checks["delete"] = s.DeleteChannel(ctx, 1)
	_, checks["list"] = s.Channels(ctx)
	_, checks["links"] = s.MonitorChannels(ctx)
	_, checks["defaults"] = s.DefaultChannels(ctx)
	checks["set"] = s.SetMonitorChannels(ctx, 1, nil)
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s worked on a closed database", name)
		}
	}
}
