//go:build !ui

package ui

import "io/fs"

// Files returns nil: this build has no app (see the package comment).
func Files() fs.FS { return nil }
