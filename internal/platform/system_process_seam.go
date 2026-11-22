package platform

import (
	"context"
	"time"
)

// NewProcessMemProviderWithMeasure returns an OSProcessMemProvider with an
// injected measurement function and deadline. It is the test seam (the
// system_seam.go pattern) used to exercise the real single-flight / deadline
// / no-cache path with deterministic measurements from outside this package;
// production uses NewProcessMemProvider. It adds no behavior.
func NewProcessMemProviderWithMeasure(timeout time.Duration, measure func(ctx context.Context, tracked []TrackedProcess) ProcessMemReport) *OSProcessMemProvider {
	return &OSProcessMemProvider{timeout: timeout, measure: measure}
}
