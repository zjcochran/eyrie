// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build ui

package ui

import (
	"embed"
	"io/fs"
)

// dist is the app as Vite builds it (npm run build in web/).
//
//go:embed all:dist
var dist embed.FS

// Files returns the embedded app.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a valid path: cannot happen
	}
	return sub
}
