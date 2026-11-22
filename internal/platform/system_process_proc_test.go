package platform

import (
	"context"
	"errors"
	"testing"
	"time"
)

// C-10 (Linux leg, fakes): verify -> read -> re-verify over /proc primitives.
// The re-check decides: a process gone, a missing anchor, or a changed start
// time after the read discards the value as identity_mismatch — the read
// number is never published against a contradicted identity (A-3), and no
// failure is ever reported as a measured zero (A-5/D-6).

type fakeIdentStep struct {
	id  ProcessIdentity
	err error
}

type fakeStartStep struct {
	start time.Time
	has   bool
	err   error
}

type fakeRSSStep struct {
	rss int64
	err error
}

// fakeProcFSOps serves per-PID result sequences. Each primitive consumes the
// next step for its PID; after the sequence is exhausted the last step
// repeats, so a one-step list pins the answer for every call.
type fakeProcFSOps struct {
	idSeq    map[int][]fakeIdentStep
	startSeq map[int][]fakeStartStep
	rssSeq   map[int][]fakeRSSStep

	idCalls    map[int]int
	startCalls map[int]int
	rssCalls   map[int]int
}

func newFakeProcFSOps() *fakeProcFSOps {
	return &fakeProcFSOps{
		idSeq: map[int][]fakeIdentStep{}, startSeq: map[int][]fakeStartStep{},
		rssSeq: map[int][]fakeRSSStep{}, idCalls: map[int]int{},
		startCalls: map[int]int{}, rssCalls: map[int]int{},
	}
}

func step[T any](steps []T, call int) T {
	if len(steps) == 0 {
		var zero T
		return zero
	}
	if call >= len(steps) {
		return steps[len(steps)-1]
	}
	return steps[call]
}

func (f *fakeProcFSOps) Identity(pid int) (ProcessIdentity, error) {
	s := step(f.idSeq[pid], f.idCalls[pid])
	f.idCalls[pid]++
	return s.id, s.err
}

func (f *fakeProcFSOps) StartTime(pid int) (time.Time, bool, error) {
	s := step(f.startSeq[pid], f.startCalls[pid])
	f.startCalls[pid]++
	return s.start, s.has, s.err
}

func (f *fakeProcFSOps) RSS(pid int) (int64, error) {
	s := step(f.rssSeq[pid], f.rssCalls[pid])
	f.rssCalls[pid]++
	return s.rss, s.err
}

func linuxRecord(pid int, at time.Time) TrackedProcess {
	return TrackedProcess{
		InstanceID: "inst-lin", ModelID: "m1", RuntimeID: "rt1",
		PID: pid, Executable: "/opt/rt/fake", StartedAt: at, HasStartedAt: true,
	}
}

func linuxIdentity(start time.Time) ProcessIdentity {
	return ProcessIdentity{ExecutablePath: "/opt/rt/fake", StartTime: start, HasStartTime: true}
}

// consistentOps returns a full identity triple whose step-3 re-check agrees.
func consistentOps(start time.Time, rss int64) *fakeProcFSOps {
	ops := newFakeProcFSOps()
	ops.idSeq[4242] = []fakeIdentStep{{id: linuxIdentity(start)}}
	ops.startSeq[4242] = []fakeStartStep{{start: start, has: true}}
	ops.rssSeq[4242] = []fakeRSSStep{{rss: rss}}
	return ops
}

func TestLinuxProcessMemMeasuredVerifyReadReverify(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	ops := consistentOps(start, 50_331_648)
	rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{linuxRecord(4242, start)})

	if rep.State != ProcessMemStateOK || !rep.SourceAvailable {
		t.Fatalf("report = %+v", rep)
	}
	e := rep.Entries[0]
	if !e.OK || e.Value != 50_331_648 || e.Source != linuxProcessRSSSource {
		t.Fatalf("entry = %+v, want measured VmRSS bytes", e)
	}
	// C-10: the pinned sequence runs the identity verification BEFORE the
	// counter read and the start-time re-verification AFTER it.
	if ops.idCalls[4242] != 1 || ops.rssCalls[4242] != 1 || ops.startCalls[4242] != 1 {
		t.Fatalf("calls identity=%d rss=%d starttime=%d, want 1/1/1 (verify -> read -> re-verify)",
			ops.idCalls[4242], ops.rssCalls[4242], ops.startCalls[4242])
	}
}

func TestLinuxProcessMemReverifyContradictionDiscardsValue(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)

	t.Run("start time advanced between checks", func(t *testing.T) {
		ops := consistentOps(start, 222_222_222)
		ops.startSeq[4242] = []fakeStartStep{{start: start.Add(120 * time.Second), has: true}} // PID reuse inside the read window
		rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{linuxRecord(4242, start)})
		e := rep.Entries[0]
		if e.OK || e.Reason != ProcessMemReasonIdentityMismatch || e.Value != 0 {
			t.Fatalf("entry = %+v, want identity_mismatch with the read value discarded", e)
		}
	})

	t.Run("anchor vanished at re-check", func(t *testing.T) {
		ops := consistentOps(start, 333_333_333)
		ops.startSeq[4242] = []fakeStartStep{{start: start, has: false}}
		rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{linuxRecord(4242, start)})
		e := rep.Entries[0]
		if e.OK || e.Reason != ProcessMemReasonIdentityMismatch {
			t.Fatalf("entry = %+v, want identity_mismatch (missing anchor at re-check discards the sample)", e)
		}
	})

	t.Run("process gone at re-check", func(t *testing.T) {
		ops := consistentOps(start, 444_444_444)
		ops.startSeq[4242] = []fakeStartStep{{err: errProcessGone}}
		rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{linuxRecord(4242, start)})
		e := rep.Entries[0]
		if e.OK || e.Reason != ProcessMemReasonIdentityMismatch {
			t.Fatalf("entry = %+v, want identity_mismatch when the process died mid-sample", e)
		}
	})
}

func TestLinuxProcessMemBoundaryMatrix(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)

	cases := []struct {
		name    string
		setup   func(*fakeProcFSOps)
		tracked func() TrackedProcess
		want    string // expected Reason ("" = measured)
		rssRead int    // expected VmRSS read count
	}{
		{
			name: "proc entry gone",
			setup: func(f *fakeProcFSOps) {
				f.idSeq[4242] = []fakeIdentStep{{err: errProcessGone}}
			},
			want:    ProcessMemReasonProcessExited,
			rssRead: 0,
		},
		{
			name: "identity unreadable",
			setup: func(f *fakeProcFSOps) {
				f.idSeq[4242] = []fakeIdentStep{{err: errors.New("permission denied")}}
			},
			want:    ProcessMemReasonIdentityUnconfirmed,
			rssRead: 0,
		},
		{
			name: "exe differs at first check",
			setup: func(f *fakeProcFSOps) {
				f.idSeq[4242] = []fakeIdentStep{{id: ProcessIdentity{ExecutablePath: "/usr/bin/someone-else", StartTime: start, HasStartTime: true}}}
			},
			want:    ProcessMemReasonIdentityMismatch,
			rssRead: 0,
		},
		{
			name: "OS anchor missing",
			setup: func(f *fakeProcFSOps) {
				f.idSeq[4242] = []fakeIdentStep{{id: ProcessIdentity{ExecutablePath: "/opt/rt/fake", HasStartTime: false}}}
			},
			want:    ProcessMemReasonIdentityUnconfirmed,
			rssRead: 0,
		},
		{
			name: "missing record anchor with matching identity",
			setup: func(f *fakeProcFSOps) {
				f.idSeq[4242] = []fakeIdentStep{{id: linuxIdentity(start)}}
			},
			tracked: func() TrackedProcess {
				r := linuxRecord(4242, time.Time{})
				r.HasStartedAt = false
				return r
			},
			want:    ProcessMemReasonIdentityUnconfirmed,
			rssRead: 0,
		},
		{
			name: "VmRSS unreadable after verified identity",
			setup: func(f *fakeProcFSOps) {
				f.idSeq[4242] = []fakeIdentStep{{id: linuxIdentity(start)}}
				f.startSeq[4242] = []fakeStartStep{{start: start, has: true}}
				f.rssSeq[4242] = []fakeRSSStep{{err: errSystemMalformed}}
			},
			want:    ProcessMemReasonCounterUnavailable,
			rssRead: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops := newFakeProcFSOps()
			tc.setup(ops)
			rec := linuxRecord(4242, start)
			if tc.tracked != nil {
				rec = tc.tracked()
			}
			rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{rec})
			if rep.State != ProcessMemStateOK {
				t.Fatalf("state = %q, want ok (per-entry failures never abort the pass, A-6)", rep.State)
			}
			e := rep.Entries[0]
			if tc.want == "" {
				if !e.OK {
					t.Fatalf("entry = %+v, want measured", e)
				}
			} else {
				if e.OK || e.Reason != tc.want {
					t.Fatalf("entry = %+v, want unavailable reason %q", e, tc.want)
				}
				if e.Value != 0 {
					t.Fatalf("unavailable entry carries a value: %+v (never zero-as-data)", e)
				}
				if e.Source != linuxProcessRSSSource {
					t.Fatalf("unavailable entry must still name the counter source: %+v", e)
				}
			}
			if ops.rssCalls[4242] != tc.rssRead {
				t.Fatalf("VmRSS reads = %d, want %d", ops.rssCalls[4242], tc.rssRead)
			}
		})
	}
}

func TestLinuxProcessMemPIDNotAssignedAndEmpty(t *testing.T) {
	ops := newFakeProcFSOps()
	rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{linuxRecord(0, time.Now())})
	if e := rep.Entries[0]; e.Reason != ProcessMemReasonPIDNotAssigned || e.OK || ops.idCalls[0] != 0 {
		t.Fatalf("pid-less entry = %+v identity calls=%d, want pid_not_assigned with no proc access", e, ops.idCalls[0])
	}

	rep = measureLinuxProcessMem(context.Background(), ops, nil)
	if rep.State != ProcessMemStateOK || len(rep.Entries) != 0 || !rep.SourceAvailable {
		t.Fatalf("empty pass = %+v, want ok with zero entries (a successful empty list IS a value)", rep)
	}
}

func TestLinuxProcessMemPerEntryFailuresNeverAbortPass(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	ops := newFakeProcFSOps()
	ops.idSeq[1] = []fakeIdentStep{{err: errors.New("permission denied")}}
	ops.idSeq[2] = []fakeIdentStep{{id: linuxIdentity(start)}}
	ops.startSeq[2] = []fakeStartStep{{start: start, has: true}}
	ops.rssSeq[2] = []fakeRSSStep{{rss: 4096}}

	bad := linuxRecord(1, start)
	bad.InstanceID = "inst-bad"
	good := linuxRecord(2, start)
	good.InstanceID = "inst-good"

	rep := measureLinuxProcessMem(context.Background(), ops, []TrackedProcess{bad, good})
	if rep.State != ProcessMemStateOK {
		t.Fatalf("state = %q, want ok", rep.State)
	}
	if e := rep.Entries[0]; e.OK || e.Reason != ProcessMemReasonIdentityUnconfirmed {
		t.Fatalf("bad entry = %+v", e)
	}
	if e := rep.Entries[1]; !e.OK || e.Value != 4096 {
		t.Fatalf("good entry = %+v", e)
	}
}

func TestLinuxProcessMemDeadlineAbortsSample(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep := measureLinuxProcessMem(ctx, consistentOps(time.Now(), 1), []TrackedProcess{linuxRecord(4242, time.Now())})
	if rep.State != ProcessMemStateFailed || rep.Error != "measurement_timeout" {
		t.Fatalf("expired-deadline report = %+v", rep)
	}
	if len(rep.Entries) != 0 {
		t.Fatalf("a timed-out sample carries no partial claims: %+v", rep.Entries)
	}
}

func TestParseProcStatusRSS(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{"typical", "Name:\tfake-runtime\nState:\tR (running)\nVmRSS:\t   64420 kB\nThreads:\t7\n", 64420 * 1024, false},
		{"tabs and spaces", "VmRSS: 1 kB", 1024, false},
		{"missing", "Name:\twhatever\nVmSize:\t100 kB\n", 0, true},
		{"malformed", "VmRSS:\tnotanumber kB\n", 0, true},
		{"no unit field", "VmRSS:\t64420\n", 64420 * 1024, false},
		{"empty", "", 0, true},
		{"negative rejected", "VmRSS:\t-5 kB", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProcStatusRSS([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("RSS = %d, want %d", got, tc.want)
			}
		})
	}
}
