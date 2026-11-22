//go:build !windows && !linux

package platform

import (
	"context"
)

// osProcessMemMeasure keeps the single binary buildable on every other GOOS.
// macOS and anything else attribute nothing in this slice (A-4): there is no
// working darwin identity anchor, and D-10 forbids attribution without
// identity — so the provider reports unavailable with a bounded reason and
// processes stays []. A darwin provider is a future slice together with a
// darwin identity prober.
func osProcessMemMeasure(ctx context.Context, tracked []TrackedProcess) ProcessMemReport {
	return processMemUnavailable(ProcessMemReasonPlatformNotSupported)
}
