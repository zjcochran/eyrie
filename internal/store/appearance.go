// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"slices"
)

// Appearance is how the web app looks for a user.
type Appearance struct {
	Layout string // one of Layouts
	Scheme string // one of the layout's colour schemes
	Mode   string // one of Modes
}

// DefaultAppearance is a new user's.
var DefaultAppearance = Appearance{Layout: "console", Scheme: "ash", Mode: "system"}

// Layouts are the web app's layouts, each with its colour schemes, the
// layout's default first.
var Layouts = map[string][]string{
	"console": {"ash", "dusk", "glacier"},
	"bento":   {"ash", "cobalt", "lagoon"},
	"aurora":  {"ash", "borealis", "ember"},
}

// Modes are day, night, or the system's choice.
var Modes = []string{"system", "day", "night"}

// Check reports what is wrong with an appearance.
func (a Appearance) Check() error {
	schemes, ok := Layouts[a.Layout]
	switch {
	case !ok:
		return invalidf("the layout must be console, bento or aurora")
	case !slices.Contains(schemes, a.Scheme):
		return invalidf("the %s layout's colour schemes are %s, %s and %s", a.Layout, schemes[0], schemes[1], schemes[2])
	case !slices.Contains(Modes, a.Mode):
		return invalidf("the mode must be system, day or night")
	}
	return nil
}

// SetAppearance stores how the web app looks for a user.
func (s *Store) SetAppearance(ctx context.Context, userID int64, a Appearance) error {
	if err := a.Check(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE users SET layout = ?, scheme = ?, mode = ? WHERE id = ?`), a.Layout, a.Scheme, a.Mode, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
