package platform

import (
	"context"
	"errors"
	"time"
)

// Pure Windows attribution logic (ADR 026 Amendment A-026-1 A-3/A-4): ONE
// process handle per tracked PID carries all three queries — image path,
// creation time, working set — so the identity check and the RSS read
// concern the same process object. While the handle is open the OS cannot
// reissue that PID to a new process, so reuse after the open is closed
// structurally; reuse before it is caught by the kill-tier identity check
// (except the documented A-3 residual). The primitives are injected so the
// logic is testable on any OS; the real syscall binding lives in
// system_process_windows.go.

// processHandleOps is the injected Windows process syscall surface. Open
// returns a handle token that the remaining calls consume unchanged — the
// fake records handle identity to prove the single-handle contract.
type processHandleOps interface {
	Open(pid int) (handle any, err error)
	ImagePath(handle any) (string, error)
	CreationTime(handle any) (time.Time, bool, error)
	WorkingSet(handle any) (int64, error)
	Close(handle any)
}

const windowsProcessRSSSource = "K32GetProcessMemoryInfo.WorkingSetSize"

func measureWindowsProcessMem(ctx context.Context, ops processHandleOps, tracked []TrackedProcess) ProcessMemReport {
	entries := make([]ProcessMemEntry, 0, len(tracked))
	for _, t := range tracked {
		if err := ctx.Err(); err != nil {
			// The fixed sample deadline struck mid-pass: no partial claims,
			// the whole sample is a failed measurement (A-4/A-5).
			return processMemFailed("measurement_timeout")
		}
		e := sampleWindowsProcess(t, ops)
		if !e.OK {
			// Unavailable readings still name the counter that would have
			// supplied the value (consistency with the system provider).
			e.Source = windowsProcessRSSSource
		}
		entries = append(entries, e)
	}
	return processMemOK(entries)
}

// sampleWindowsProcess runs the single-handle combined check+read for one
// tracked instance. Every branch returns a bounded A-5 outcome; nothing here
// touches the process.
func sampleWindowsProcess(t TrackedProcess, ops processHandleOps) ProcessMemEntry {
	if t.PID <= 0 {
		return processMemUnavail(t, ProcessMemReasonPIDNotAssigned)
	}
	h, err := ops.Open(t.PID)
	if err != nil {
		if errors.Is(err, errProcessAccessDenied) {
			// The identity check is impossible for this sample (A-5 row 4).
			return processMemUnavail(t, ProcessMemReasonIdentityUnconfirmed)
		}
		// OpenProcess failed for any other reason: no such object (A-5 row 2).
		return processMemUnavail(t, ProcessMemReasonProcessExited)
	}
	defer ops.Close(h)

	exePath, pathErr := ops.ImagePath(h)
	startTime, hasStart, timeErr := ops.CreationTime(h)
	if pathErr != nil || timeErr != nil {
		return processMemUnavail(t, ProcessMemReasonIdentityUnconfirmed)
	}
	if reason := identityMismatchReason(t, exePath, startTime, hasStart); reason != "" {
		return processMemUnavail(t, reason)
	}

	rss, err := ops.WorkingSet(h)
	if err != nil {
		// Identity verified against this handle, but the counter broke:
		// unavailable, never a zero (A-5 row 5).
		return processMemUnavail(t, ProcessMemReasonCounterUnavailable)
	}
	return processMemMeasured(t, rss, windowsProcessRSSSource)
}
