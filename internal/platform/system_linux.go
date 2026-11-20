//go:build linux

package platform

import (
	"context"
	"os"
	"time"
)

// osSystemMeasure binds the pure /proc/sysfs measurement logic to real file
// reads and the cancellable measurement window (ADR 026 RES-03B).
func osSystemMeasure(ctx context.Context, interval time.Duration) SystemReport {
	return measureLinuxSystem(ctx, os.ReadFile, sleepCtx, interval)
}
