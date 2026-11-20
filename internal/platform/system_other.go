//go:build !windows && !linux && !darwin

package platform

import (
	"context"
	"time"
)

// osSystemMeasure keeps the single binary buildable on every other GOOS: no
// measurement source exists there, so the provider reports unavailable (a
// healthy absence, D-7/D-8), never a failure.
func osSystemMeasure(ctx context.Context, interval time.Duration) SystemReport {
	return systemUnavailable("platform_not_supported")
}
