// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
)

func TestAppearance(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		alice, bob := twoUsers(t, s)
		if alice.Appearance != DefaultAppearance {
			t.Errorf("new user: %+v", alice.Appearance)
		}
		want := Appearance{Layout: "aurora", Scheme: "ember", Mode: "night"}
		if err := s.SetAppearance(ctx, bob.ID, want); err != nil {
			t.Fatal(err)
		}
		if u, _ := s.UserByID(ctx, bob.ID); u.Appearance != want {
			t.Errorf("stored %+v", u.Appearance)
		}
		var bad *InvalidError
		for _, a := range []Appearance{
			{Layout: "grid", Scheme: "ash", Mode: "day"},
			{Layout: "console", Scheme: "ember", Mode: "day"},
			{Layout: "bento", Scheme: "ash", Mode: "dusk"},
		} {
			if err := s.SetAppearance(ctx, bob.ID, a); !errors.As(err, &bad) {
				t.Errorf("%+v: %v", a, err)
			}
		}
		if err := s.SetAppearance(ctx, 999, DefaultAppearance); !errors.Is(err, ErrNotFound) {
			t.Errorf("no such user: %v", err)
		}
	})
}
