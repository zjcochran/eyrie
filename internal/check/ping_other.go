// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin

package check

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

func ping(context.Context, netip.Addr, int, int) (time.Duration, error) {
	return 0, errors.New("ping works on Linux and macOS only")
}
