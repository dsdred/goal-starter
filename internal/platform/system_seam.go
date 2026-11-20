package platform

import (
	"context"
	"time"
)

// NewSystemProviderWithMeasure returns an OSSystemProvider with an injected
// measurement function and bounds. It is the test seam (the gpu_seam.go
// pattern) used to exercise the real single-flight / deadline / no-cache path
// with deterministic measurements from outside this package; production uses
// NewSystemProvider. It adds no behavior.
func NewSystemProviderWithMeasure(timeout, interval time.Duration, measure func(ctx context.Context, interval time.Duration) SystemReport) *OSSystemProvider {
	return &OSSystemProvider{timeout: timeout, interval: interval, measure: measure}
}
