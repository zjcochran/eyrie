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
	// Accessible turns on the accessibility mode: announcements, controls
	// in place of dragging, charts as tables, more contrast and no motion.
	Accessible bool
	Vision     string // one of Visions: the status colours for a colour vision
	TextSize   int    // one of TextSizes, in percent
}

// DefaultAppearance is a new user's.
var DefaultAppearance = Appearance{Layout: "console", Scheme: "ash", Mode: "system", Vision: "typical", TextSize: 100}

// Layouts are the web app's layouts, each with its colour schemes, the
// layout's default first.
var Layouts = map[string][]string{
	"console": {"ash", "dusk", "glacier"},
	"bento":   {"ash", "cobalt", "lagoon"},
	"aurora":  {"ash", "borealis", "ember"},
}

// Modes are day, night, or the system's choice.
var Modes = []string{"system", "day", "night"}

// Visions are the colour visions the status colours can be chosen for:
// typical, red-green (protanopia and deuteranopia), blue-yellow
// (tritanopia) and monochrome (no colour).
var Visions = []string{"typical", "red-green", "blue-yellow", "monochrome"}

// TextSizes are the text sizes, in percent.
var TextSizes = []int{100, 115, 130, 150}

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
	case !slices.Contains(Visions, a.Vision):
		return invalidf("the colour vision must be typical, red-green, blue-yellow or monochrome")
	case !slices.Contains(TextSizes, a.TextSize):
		return invalidf("the text size must be 100, 115, 130 or 150 percent")
	}
	return nil
}

// SetAppearance stores how the web app looks for a user.
func (s *Store) SetAppearance(ctx context.Context, userID int64, a Appearance) error {
	if err := a.Check(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE users SET layout = ?, scheme = ?, mode = ?, accessible = ?, vision = ?, text_size = ? WHERE id = ?`),
		a.Layout, a.Scheme, a.Mode, a.Accessible, a.Vision, a.TextSize, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
