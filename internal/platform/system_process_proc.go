package platform

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Pure Linux attribution logic (ADR 026 Amendment A-026-1 A-3): /proc has no
// process handle, so each sample pins its window with a verify → read →
// re-verify sequence: (1) the kill-tier identity check against
// /proc/<pid>/exe + /proc/<pid>/stat field 22; (2) the VmRSS read; (3) a
// start-time re-read — if the proc is gone or the start time changed between
// the checks, the sample is discarded as identity_mismatch (a PID reuse
// landing inside the read is detected here, start time being monotone per
// PID value). The primitives are injected so the logic is testable on any
// OS; the production binding lives in system_process_linux.go.

type processProcFSOps interface {
	// Identity runs the pre-check (exe path + whole-second start-time
	// anchor). A missing /proc entry surfaces as errProcessGone.
	Identity(pid int) (ProcessIdentity, error)
	// StartTime re-reads only the field-22 start-time anchor (A-3 step 3).
	StartTime(pid int) (time.Time, bool, error)
	// RSS reads /proc/<pid>/status VmRSS in bytes.
	RSS(pid int) (int64, error)
}

const linuxProcessRSSSource = "/proc/<pid>/status VmRSS"

func measureLinuxProcessMem(ctx context.Context, ops processProcFSOps, tracked []TrackedProcess) ProcessMemReport {
	entries := make([]ProcessMemEntry, 0, len(tracked))
	for _, t := range tracked {
		if err := ctx.Err(); err != nil {
			return processMemFailed("measurement_timeout")
		}
		e := sampleLinuxProcess(t, ops)
		if !e.OK {
			// Unavailable readings still name the counter that would have
			// supplied the value (consistency with the system provider).
			e.Source = linuxProcessRSSSource
		}
		entries = append(entries, e)
	}
	return processMemOK(entries)
}

// sampleLinuxProcess runs one bounded verify/read/re-verify sequence. Every
// branch returns a bounded A-5 outcome; nothing here touches the process.
func sampleLinuxProcess(t TrackedProcess, ops processProcFSOps) ProcessMemEntry {
	if t.PID <= 0 {
		return processMemUnavail(t, ProcessMemReasonPIDNotAssigned)
	}

	// Step 1 — verify against the recorded launch triple.
	id, err := ops.Identity(t.PID)
	if err != nil {
		if errors.Is(err, errProcessGone) {
			return processMemUnavail(t, ProcessMemReasonProcessExited)
		}
		return processMemUnavail(t, ProcessMemReasonIdentityUnconfirmed)
	}
	if reason := identityMismatchReason(t, id.ExecutablePath, id.StartTime, id.HasStartTime); reason != "" {
		return processMemUnavail(t, reason)
	}

	// Step 2 — read VmRSS.
	rss, err := ops.RSS(t.PID)
	if err != nil {
		return processMemUnavail(t, ProcessMemReasonCounterUnavailable)
	}

	// Step 3 — re-verify the start time. The proc being gone, the anchor
	// vanishing, or a changed anchor all mean a PID reuse raced the read:
	// discard the value as identity_mismatch (A-3), never attribute it.
	start2, has2, err := ops.StartTime(t.PID)
	if err != nil || !has2 || !start2.Equal(id.StartTime) {
		return processMemUnavail(t, ProcessMemReasonIdentityMismatch)
	}

	return processMemMeasured(t, rss, linuxProcessRSSSource)
}

// parseProcStatusRSS extracts VmRSS (kB) from /proc/<pid>/status content and
// returns bytes. A missing or unparseable line is a bounded error; the
// caller yields counter_unavailable, never a zero (D-6).
func parseProcStatusRSS(raw []byte) (int64, error) {
	for _, line := range strings.Split(string(raw), "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "VmRSS" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, errSystemMalformed
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kb < 0 {
			return 0, errSystemMalformed
		}
		return kb * 1024, nil
	}
	return 0, errSystemMalformed
}
