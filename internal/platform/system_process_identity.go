package platform

import (
	"runtime"
	"strings"
	"time"
)

// The identity composition for per-process RSS attribution (ADR 026
// Amendment A-026-1 A-3). It mirrors the existing kill-tier rule
// (internal/process/supervisor_kill.go verifyIdentityForKill, ADR 008 D1):
// executable path equal AND the OS start-time anchor present on both sides
// AND within the existing ±5 s tolerance. An exe-path-only match is never
// sufficient; a missing anchor on either side refuses attribution as
// identity_unconfirmed. The lifecycle/kill contracts themselves are
// unchanged (D-14) — this is the attribution-time application of the same
// composition, deliberately no weaker than the recovery-detection rule.

// identityMismatchReason returns the bounded A-5 reason when the observed OS
// identity does not attribute to the recorded launch triple, and "" when the
// kill-tier composition matches.
func identityMismatchReason(rec TrackedProcess, observedPath string, observedStart time.Time, observedHasStart bool) string {
	if rec.Executable == "" || observedPath == "" {
		// The kill-tier composition refuses on either empty path anchor; an
		// absent anchor is an impossible check, not a mismatch fact.
		return ProcessMemReasonIdentityUnconfirmed
	}
	if !processPathsEqual(rec.Executable, observedPath) {
		return ProcessMemReasonIdentityMismatch
	}
	if !observedHasStart || !rec.HasStartedAt {
		return ProcessMemReasonIdentityUnconfirmed
	}
	if !processTimesApproximatelyEqual(rec.StartedAt, observedStart) {
		return ProcessMemReasonIdentityMismatch
	}
	return ""
}

// processPathsEqual compares two filesystem paths platform-aware, mirroring
// the internal/process pathsEqual rule: on Windows the .exe extension is
// optional and comparison is case-insensitive; elsewhere exact equality.
func processPathsEqual(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" {
		fa, fb := strings.ToLower(a), strings.ToLower(b)
		if fa == fb {
			return true
		}
		const exe = ".exe"
		if strings.HasSuffix(fb, exe) && fa == fb[:len(fb)-len(exe)] {
			return true
		}
		if strings.HasSuffix(fa, exe) && fb == fa[:len(fa)-len(exe)] {
			return true
		}
	}
	return false
}

// processTimesApproximatelyEqual reports whether two times are within the
// existing ±5 s window (the same tolerance the recovery layer applies;
// A-3 precision analysis rules out tightening it).
func processTimesApproximatelyEqual(a, b time.Time) bool {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 5*time.Second
}
