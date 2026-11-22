package platform

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// C-10 (Windows leg, fakes): the single-handle combined check+read logic.
// The fake records every query with the handle object it received, so the
// A-3 contract "identity verification and counter read used the same handle"
// is asserted structurally, and a second handle anywhere is a violation.

type fakeProcHandle struct {
	pid       int
	queries   []string
	closeSeen bool
}

type fakeHandleOps struct {
	path      map[int]string
	start     map[int]time.Time
	hasTime   map[int]bool
	rss       map[int]int64
	openErr   map[int]error
	pathErr   map[int]error
	timeErr   map[int]error
	rssErr    map[int]error
	openCalls map[int]int
	handles   []*fakeProcHandle
	closes    int
}

func newFakeHandleOps() *fakeHandleOps {
	return &fakeHandleOps{
		path: map[int]string{}, start: map[int]time.Time{}, hasTime: map[int]bool{},
		rss: map[int]int64{}, openErr: map[int]error{}, pathErr: map[int]error{},
		timeErr: map[int]error{}, rssErr: map[int]error{}, openCalls: map[int]int{},
	}
}

func (f *fakeHandleOps) Open(pid int) (any, error) {
	f.openCalls[pid]++
	if err := f.openErr[pid]; err != nil {
		return nil, err
	}
	h := &fakeProcHandle{pid: pid}
	f.handles = append(f.handles, h)
	return h, nil
}

func (f *fakeHandleOps) ImagePath(handle any) (string, error) {
	h := handle.(*fakeProcHandle)
	h.queries = append(h.queries, "path")
	if err := f.pathErr[h.pid]; err != nil {
		return "", err
	}
	return f.path[h.pid], nil
}

func (f *fakeHandleOps) CreationTime(handle any) (time.Time, bool, error) {
	h := handle.(*fakeProcHandle)
	h.queries = append(h.queries, "time")
	if err := f.timeErr[h.pid]; err != nil {
		return time.Time{}, false, err
	}
	return f.start[h.pid], f.hasTime[h.pid], nil
}

func (f *fakeHandleOps) WorkingSet(handle any) (int64, error) {
	h := handle.(*fakeProcHandle)
	h.queries = append(h.queries, "ws")
	if err := f.rssErr[h.pid]; err != nil {
		return 0, err
	}
	return f.rss[h.pid], nil
}

func (f *fakeHandleOps) Close(handle any) {
	h := handle.(*fakeProcHandle)
	h.closeSeen = true
	f.closes++
}

func windowsRecord(pid int, at time.Time) TrackedProcess {
	return TrackedProcess{
		InstanceID: "inst-win", ModelID: "m1", RuntimeID: "rt1",
		PID: pid, Executable: "C:\\rt\\fake.exe", StartedAt: at, HasStartedAt: true,
	}
}

func runWindows(t *testing.T, ops *fakeHandleOps, tracked []TrackedProcess) ProcessMemReport {
	t.Helper()
	return measureWindowsProcessMem(context.Background(), ops, tracked)
}

func entryOf(t *testing.T, rep ProcessMemReport, id string) ProcessMemEntry {
	t.Helper()
	for _, e := range rep.Entries {
		if e.InstanceID == id {
			return e
		}
	}
	t.Fatalf("no entry for %s in %+v", id, rep.Entries)
	return ProcessMemEntry{}
}

// TestProcessPathsEqualCaseToleranceIsWindowsOnly pins the OS branch of the
// identity composition itself: the case-insensitive and .exe-optional
// tolerances exist only in Windows builds, every other GOOS compares exactly.
// It asserts whichever branch the current GOOS defines, so the same test is
// truthful on the Windows host and on Linux CI.
func TestProcessPathsEqualCaseToleranceIsWindowsOnly(t *testing.T) {
	const lower = "C:\\rt\\fake.exe"
	upper := strings.ToUpper(lower)
	withoutExe := "C:\\rt\\fake"

	caseTolerant := processPathsEqual(lower, upper)
	exeOptional := processPathsEqual(lower, withoutExe)

	if runtime.GOOS == "windows" {
		if !caseTolerant || !exeOptional {
			t.Fatalf("windows must tolerate case (%v) and the optional .exe (%v)", caseTolerant, exeOptional)
		}
		return
	}
	if caseTolerant || exeOptional {
		t.Fatalf("GOOS=%s comparison must be exact: caseTolerant=%v exeOptional=%v", runtime.GOOS, caseTolerant, exeOptional)
	}
}

func TestWindowsProcessMemMeasuredSingleHandle(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	ops := newFakeHandleOps()
	ops.path[4242] = "C:\\rt\\fake.exe" // exact case: the measured leg must not lean on the Windows-only comparison branch
	ops.start[4242] = start.Add(120 * time.Millisecond)
	ops.hasTime[4242] = true
	ops.rss[4242] = 872_415_232

	rep := runWindows(t, ops, []TrackedProcess{windowsRecord(4242, start)})
	if rep.State != ProcessMemStateOK || !rep.SourceAvailable {
		t.Fatalf("report = %+v", rep)
	}
	e := entryOf(t, rep, "inst-win")
	if !e.OK || e.Value != 872_415_232 || e.Source != windowsProcessRSSSource {
		t.Fatalf("entry = %+v, want measured value from the psapi counter", e)
	}
	// C-10 single-handle assertion: exactly one open, and ALL THREE queries
	// plus the counter read ran against that one handle object, then closed.
	if ops.openCalls[4242] != 1 || len(ops.handles) != 1 {
		t.Fatalf("opens=%d handles=%d, want 1/1 (one handle per tracked PID)", ops.openCalls[4242], len(ops.handles))
	}
	h := ops.handles[0]
	want := []string{"path", "time", "ws"}
	if len(h.queries) != len(want) {
		t.Fatalf("handle queries = %v, want %v", h.queries, want)
	}
	for i, q := range want {
		if h.queries[i] != q {
			t.Fatalf("handle queries = %v, want %v (identity before counter)", h.queries, want)
		}
	}
	if !h.closeSeen || ops.closes != 1 {
		t.Fatalf("handle closed=%v closes=%d, want true/1 (no handle leak)", h.closeSeen, ops.closes)
	}
}

func TestWindowsProcessMemReusedPIDNeverYieldsForeignNumber(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	ops := newFakeHandleOps()
	ops.path[4242] = "C:\\somewhere\\else.exe" // PID reissued to a different image
	ops.start[4242] = start.Add(time.Hour)
	ops.hasTime[4242] = true
	ops.rss[4242] = 999_999_999

	rep := runWindows(t, ops, []TrackedProcess{windowsRecord(4242, start)})
	e := entryOf(t, rep, "inst-win")
	if e.OK || e.Reason != ProcessMemReasonIdentityMismatch || e.Value != 0 {
		t.Fatalf("mismatched entry = %+v, want identity_mismatch with no number", e)
	}
	if len(ops.handles) == 1 && containsQuery(ops.handles[0].queries, "ws") {
		t.Fatalf("counter was read after an identity mismatch — foreign number risk")
	}
}

func containsQuery(qs []string, want string) bool {
	for _, q := range qs {
		if q == want {
			return true
		}
	}
	return false
}

func TestWindowsProcessMemBoundaryMatrix(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)

	cases := []struct {
		name    string
		ops     func(*fakeHandleOps)
		tracked func() TrackedProcess
		want    string // expected Reason ("" = measured)
	}{
		{
			name: "open access denied",
			ops: func(f *fakeHandleOps) {
				f.openErr[4242] = errProcessAccessDenied
			},
			want: ProcessMemReasonIdentityUnconfirmed,
		},
		{
			name: "open fails process gone",
			ops: func(f *fakeHandleOps) {
				f.openErr[4242] = errors.New("The process identified by PID is no longer existing")
			},
			want: ProcessMemReasonProcessExited,
		},
		{
			name: "identity call error",
			ops: func(f *fakeHandleOps) {
				f.path[4242] = "C:\\rt\\fake.exe"
				f.timeErr[4242] = errors.New("GetProcessTimes failed")
			},
			want: ProcessMemReasonIdentityUnconfirmed,
		},
		{
			name: "OS anchor missing",
			ops: func(f *fakeHandleOps) {
				f.path[4242] = "C:\\rt\\fake.exe"
				f.start[4242] = start
			},
			want: ProcessMemReasonIdentityUnconfirmed,
		},
		{
			name: "counter error after verified identity",
			ops: func(f *fakeHandleOps) {
				f.path[4242] = "C:\\rt\\fake.exe"
				f.start[4242] = start
				f.hasTime[4242] = true
				f.rssErr[4242] = errors.New("K32GetProcessMemoryInfo failed")
			},
			want: ProcessMemReasonCounterUnavailable,
		},
		{
			name: "missing record anchor with matching path",
			ops: func(f *fakeHandleOps) {
				f.path[4242] = "C:\\rt\\fake.exe"
				f.start[4242] = start
				f.hasTime[4242] = true
			},
			tracked: func() TrackedProcess {
				r := windowsRecord(4242, time.Time{})
				r.HasStartedAt = false
				return r
			},
			want: ProcessMemReasonIdentityUnconfirmed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops := newFakeHandleOps()
			tc.ops(ops)
			rec := windowsRecord(4242, start)
			if tc.tracked != nil {
				rec = tc.tracked()
			}
			rep := runWindows(t, ops, []TrackedProcess{rec})
			// A completed pass is ok even when the entry is unavailable (A-6).
			if rep.State != ProcessMemStateOK {
				t.Fatalf("state = %q, want ok", rep.State)
			}
			e := entryOf(t, rep, "inst-win")
			if e.OK || e.Reason != tc.want {
				t.Fatalf("entry = %+v, want unavailable reason %q", e, tc.want)
			}
			if e.Source != windowsProcessRSSSource {
				t.Fatalf("unavailable entry must still name the counter source: %+v", e)
			}
			for _, h := range ops.handles {
				if !h.closeSeen {
					t.Fatalf("handle for pid %d left open", h.pid)
				}
			}
		})
	}
}

func TestWindowsProcessMemPIDNotAssigned(t *testing.T) {
	ops := newFakeHandleOps()
	rec := windowsRecord(0, time.Now())
	rep := runWindows(t, ops, []TrackedProcess{rec})
	e := entryOf(t, rep, "inst-win")
	if e.Reason != ProcessMemReasonPIDNotAssigned || e.OK {
		t.Fatalf("entry = %+v, want pid_not_assigned", e)
	}
	if ops.closes != 0 || len(ops.handles) != 0 {
		t.Fatalf("no handle may be opened for a PID-less instance")
	}
}

func TestWindowsProcessMemEmptyTrackedIsOK(t *testing.T) {
	rep := runWindows(t, newFakeHandleOps(), nil)
	if rep.State != ProcessMemStateOK || len(rep.Entries) != 0 || !rep.SourceAvailable {
		t.Fatalf("empty pass = %+v, want ok with zero entries (a successful empty list IS a value)", rep)
	}
}

func TestWindowsProcessMemPerEntryFailuresNeverAbortPass(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	ops := newFakeHandleOps()
	// pid 1: broken identity; pid 2: healthy.
	ops.openErr[1] = errProcessAccessDenied
	ops.path[2] = "C:\\rt\\ok.exe"
	ops.start[2] = start
	ops.hasTime[2] = true
	ops.rss[2] = 4096

	bad := windowsRecord(1, start)
	bad.InstanceID = "inst-bad"
	bad.Executable = "C:\\rt\\bad.exe"
	good := windowsRecord(2, start)
	good.InstanceID = "inst-good"
	good.Executable = "C:\\rt\\ok.exe"

	rep := runWindows(t, ops, []TrackedProcess{bad, good})
	if rep.State != ProcessMemStateOK {
		t.Fatalf("state = %q, want ok", rep.State)
	}
	if e := entryOf(t, rep, "inst-bad"); e.Reason != ProcessMemReasonIdentityUnconfirmed {
		t.Fatalf("bad entry = %+v", e)
	}
	if e := entryOf(t, rep, "inst-good"); !e.OK || e.Value != 4096 {
		t.Fatalf("good entry = %+v", e)
	}
}

func TestWindowsProcessMemDeadlineAbortsSample(t *testing.T) {
	ops := newFakeHandleOps()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep := measureWindowsProcessMem(ctx, ops, []TrackedProcess{windowsRecord(4242, time.Now())})
	if rep.State != ProcessMemStateFailed || rep.Error != "measurement_timeout" {
		t.Fatalf("expired-deadline report = %+v", rep)
	}
	if ops.closes != 0 || len(rep.Entries) != 0 {
		t.Fatalf("a timed-out sample carries no partial claims: %+v", rep)
	}
}
