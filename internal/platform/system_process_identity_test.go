package platform

import (
	"testing"
	"time"
)

// A-026-1 A-3 identity composition tests (kill-tier): path equal AND both
// start-time anchors present AND within ±5 s. An exe-path-only match is
// never sufficient; a missing anchor is identity_unconfirmed, a contradicting
// anchor is identity_mismatch — never a measured zero (A-5/D-6).

func baseRecord() TrackedProcess {
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	return TrackedProcess{
		InstanceID:   "inst-1",
		PID:          4242,
		Executable:   "C:\\runtimes\\fake.exe",
		StartedAt:    start,
		HasStartedAt: true,
	}
}

func TestIdentityMismatchReasonMatrix(t *testing.T) {
	rec := baseRecord()
	obsStart := rec.StartedAt.Add(2 * time.Second)

	cases := []struct {
		name       string
		path       string
		start      time.Time
		hasStart   bool
		mutate     func(*TrackedProcess)
		wantReason string
	}{
		{"match within tolerance", rec.Executable, obsStart, true, nil, ""},
		{"match exact boundary 5s", rec.Executable, rec.StartedAt.Add(5 * time.Second), true, nil, ""},
		{"path mismatch", "C:\\other\\imposter.exe", obsStart, true, nil, ProcessMemReasonIdentityMismatch},
		{"start time beyond tolerance", rec.Executable, rec.StartedAt.Add(5*time.Second + time.Millisecond), true, nil, ProcessMemReasonIdentityMismatch},
		{"OS anchor missing", rec.Executable, time.Time{}, false, nil, ProcessMemReasonIdentityUnconfirmed},
		{"record anchor missing", rec.Executable, obsStart, true, func(r *TrackedProcess) { r.HasStartedAt = false; r.StartedAt = time.Time{} }, ProcessMemReasonIdentityUnconfirmed},
		{"record path empty", "", obsStart, true, func(r *TrackedProcess) { r.Executable = "" }, ProcessMemReasonIdentityUnconfirmed},
		{"observed path empty", "", obsStart, true, nil, ProcessMemReasonIdentityUnconfirmed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := rec
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			if got := identityMismatchReason(r, tc.path, tc.start, tc.hasStart); got != tc.wantReason {
				t.Fatalf("identityMismatchReason = %q, want %q", got, tc.wantReason)
			}
		})
	}
}

func TestIdentityPathOnlyMatchNeverAttributes(t *testing.T) {
	rec := baseRecord()
	// Kill-tier composition: an exe-only match with NO OS start-time anchor
	// must refuse attribution even though the path equals (A-3).
	if reason := identityMismatchReason(rec, rec.Executable, time.Time{}, false); reason != ProcessMemReasonIdentityUnconfirmed {
		t.Fatalf("exe-only match reason = %q, want identity_unconfirmed", reason)
	}
}

func TestProcessTimesApproximatelyEqual(t *testing.T) {
	base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d    time.Duration
		want bool
	}{
		{-5 * time.Second, true},
		{5 * time.Second, true},
		{-5*time.Second - time.Millisecond, false},
		{5*time.Second + time.Millisecond, false},
		{0, true},
	} {
		if got := processTimesApproximatelyEqual(base, base.Add(tc.d)); got != tc.want {
			t.Fatalf("within %v: got %v, want %v", tc.d, got, tc.want)
		}
	}
}
