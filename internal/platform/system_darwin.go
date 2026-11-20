//go:build darwin

package platform

import (
	"context"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// Real macOS bindings for the pure measurement logic (ADR 026 RES-03B):
// sysctl via x/sys/unix (no child, no cgo); vm_stat and top as bounded
// read-only children through the shared runner.

func darwinSysctl(name string) (uint64, error) {
	if name == "hw.logicalcpu" {
		v, err := unix.SysctlUint32(name)
		return uint64(v), err
	}
	return unix.SysctlUint64(name)
}

// osSystemMeasure binds the pure macOS measurement logic to real sysctl reads
// and bounded children.
func osSystemMeasure(ctx context.Context, interval time.Duration) SystemReport {
	return measureDarwinSystem(ctx, darwinSysctl, exec.LookPath, runSystemChild)
}
