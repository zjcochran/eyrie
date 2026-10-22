// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !ui

package ui

import "io/fs"

// Files returns nil: this build has no app (see the package comment).
func Files() fs.FS { return nil }
