//go:build linux

package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Production Linux bindings for the pure /proc attribution logic
// (ADR 026 Amendment A-026-1 A-3/A-4). Identity anchors reuse the existing
// recovery-layer reads verbatim (RecoveryProber.GetProcessIdentity:
// /proc/<pid>/exe + /proc/<pid>/stat field 22) — no new identity mechanism.
// Absent source (no procfs) is unavailable, a broken present source is a
// failed measurement (D-7).

type linuxProcessProcOps struct {
	prober RecoveryProber
}

func (o linuxProcessProcOps) Identity(pid int) (ProcessIdentity, error) {
	id, err := o.prober.GetProcessIdentity(pid)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return id, fmt.Errorf("%w: %v", errProcessGone, err)
	}
	return id, err
}

func (o linuxProcessProcOps) StartTime(pid int) (time.Time, bool, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, false, fmt.Errorf("%w: %v", errProcessGone, err)
		}
		return time.Time{}, false, err
	}
	// parseProcStatStartTime reconstructs bootTime + ticks internally
	// (recovery_unix.go), quantized to whole seconds; the re-verify compares
	// the same reconstruction, so equality is exact by construction.
	startTime, ok := parseProcStatStartTime(data)
	if !ok {
		return time.Time{}, false, errSystemMalformed
	}
	return startTime, true, nil
}

func (o linuxProcessProcOps) RSS(pid int) (int64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	return parseProcStatusRSS(raw)
}

// osProcessMemMeasure binds the pure Linux logic to the real /proc reads.
// procfs absent ⇒ no attribution source (unavailable, A-4).
func osProcessMemMeasure(ctx context.Context, tracked []TrackedProcess) ProcessMemReport {
	if _, err := os.Stat("/proc"); err != nil {
		return processMemUnavailable("procfs_not_found")
	}
	return measureLinuxProcessMem(ctx, linuxProcessProcOps{prober: newRecoveryProber()}, tracked)
}
