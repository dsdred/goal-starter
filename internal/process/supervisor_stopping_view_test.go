package process

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

// ADR 021 Slice 3: deterministic coverage of the stopping window created by the
// package-private Manager.Stop gate (§Test-only stop gate) and of the readers
// migrated in Slice 2 (§D5). Obligations T-1 … T-6.
//
// Every positive assertion here is an ORDERING assertion: the read completed
// while a gate invocation is still parked inside Manager.Stop, which is the same
// interval in which stopCore holds ic.mu across the manager call. The deadlines
// below only convert a hang into a FAIL; a result that arrives after the release
// is rejected as evidence by the helpers, so no test can pass on a timeout.

const (
	// heldReadDeadline bounds a migrated read. A migrated reader has no
	// dependency on the held stop, so exceeding this is the defect under test.
	heldReadDeadline = 10 * time.Second

	// stopReturnDeadline bounds the waits for a stop to reach the gate and for a
	// released stop to return.
	stopReturnDeadline = 30 * time.Second

	// dwellSignalWaitDeadline bounds the waits for the fixture's markers.
	dwellSignalWaitDeadline = 20 * time.Second

	// dwellExitDelayMS keeps the signal-delay child alive after it acknowledges
	// the real OS stop signal, so the process is still running for the reads
	// taken inside the held window, and exits gracefully well before Manager.Stop
	// would re-arm its 5-second force kill.
	dwellExitDelayMS = 1500
)

// stopHold controls one Manager.Stop gate installation. It is reachable only
// from this package's tests through the package-private setStopGate.
type stopHold struct {
	entered  chan struct{}
	released chan struct{}

	releaseOnce sync.Once

	mu      sync.Mutex
	entries int
	exits   int

	forceKilledAtEntry bool
}

func newStopHold(t *testing.T, m *Manager) *stopHold {
	t.Helper()
	if m == nil {
		t.Fatal("no manager to gate")
	}
	// Capture the handle under m.mu: Manager.wait assigns m.control = nil when
	// the process is reaped, so an unsynchronized read would race with it.
	m.mu.Lock()
	control := m.control
	m.mu.Unlock()

	h := &stopHold{
		entered:  make(chan struct{}),
		released: make(chan struct{}),
	}
	m.setStopGate(func() {
		h.mu.Lock()
		h.entries++
		first := h.entries == 1
		if first && control != nil {
			// On Windows a test process without a console cannot deliver a
			// console control event, and GracefulStop escalates to ForceKill
			// before the gate runs. There is then no graceful stopping interval
			// to observe, which the test reports as NOT OBSERVED.
			h.forceKilledAtEntry = control.WasSignaled()
			close(h.entered)
		}
		h.mu.Unlock()

		if first {
			<-h.released
		}

		h.mu.Lock()
		h.exits++
		h.mu.Unlock()
	})
	t.Cleanup(func() {
		// Release first so an in-flight stop cannot stay parked, then disarm the
		// seam so no later generation inherits a widened window. Cleanups run in
		// reverse registration order, so this precedes the supervisor cleanup.
		h.release()
		m.setStopGate(nil)
	})
	return h
}

func (h *stopHold) release() {
	h.releaseOnce.Do(func() { close(h.released) })
}

// awaitEntry blocks until Manager.Stop reached the gate. Reaching the gate is
// the precondition of every held-window read, so a timeout is a failure.
func (h *stopHold) awaitEntry(t *testing.T) {
	t.Helper()
	select {
	case <-h.entered:
	case <-time.After(stopReturnDeadline):
		t.Fatalf("Manager.Stop never reached the stop gate within %s", stopReturnDeadline)
	}
	h.mu.Lock()
	forceKilled := h.forceKilledAtEntry
	h.mu.Unlock()
	if forceKilled {
		// The force-kill flag is set only inside ForceKill (platform_unix.go:71;
		// platform_windows.go:133, :143), so reaching the gate after a force kill
		// means the graceful signal never took effect. On any platform with a
		// real graceful signal that is a regression and must FAIL. On Windows it
		// is also the known console-less configuration, where
		// GenerateConsoleCtrlEvent cannot deliver a control event at all
		// (platform_windows.go:118-122) and no graceful interval exists to
		// observe; internal/platform's maintained dwell test discriminates the
		// two with its console probe, so this leg reports that case as
		// NOT OBSERVED rather than claiming a pass.
		if runtime.GOOS != "windows" {
			t.Fatalf("Manager.Stop reached the gate after GracefulStop escalated to ForceKill on %s: there is no force-kill path before the gate on this platform", runtime.GOOS)
		}
		t.Skipf("NOT OBSERVED / INCONCLUSIVE: the graceful stop signal escalated to ForceKill before the stop gate was reached (GOOS=%s), so no real graceful stopping interval exists in this configuration", runtime.GOOS)
	}
}

func (h *stopHold) entryCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entries
}

// held reports whether a gate invocation is still parked inside the window.
func (h *stopHold) held() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entries > h.exits
}

// readWhileHeld runs fn and returns its value, requiring both completion and
// that a gate invocation is still parked at the moment of completion.
func readWhileHeld[T any](t *testing.T, h *stopHold, what string, fn func() T) T {
	t.Helper()
	ch := make(chan T, 1)
	go func() { ch <- fn() }()
	select {
	case v := <-ch:
		if !h.held() {
			t.Fatalf("%s: the read completed only after the stop gate had returned, so it does not show the reader is independent of the stop's ic.mu region", what)
		}
		return v
	case <-time.After(heldReadDeadline):
		t.Fatalf("%s: did not complete within %s while Manager.Stop was held", what, heldReadDeadline)
	}
	return *new(T)
}

// readWhileHeldErr is readWhileHeld for readers that report an error, which must
// be nil for the read to count as observed.
func readWhileHeldErr[T any](t *testing.T, h *stopHold, what string, fn func() (T, error)) T {
	t.Helper()
	type outcome struct {
		v   T
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		v, err := fn()
		ch <- outcome{v: v, err: err}
	}()
	select {
	case o := <-ch:
		if !h.held() {
			t.Fatalf("%s: the read completed only after the stop gate had returned, so it does not show the reader is independent of the stop's ic.mu region", what)
		}
		if o.err != nil {
			t.Fatalf("%s: %v", what, o.err)
		}
		return o.v
	case <-time.After(heldReadDeadline):
		t.Fatalf("%s: did not complete within %s while Manager.Stop was held", what, heldReadDeadline)
	}
	return *new(T)
}

// dwellMarker mirrors the maintained signal-delay fixture's marker document
// (testdata/fake-runtime/fake.go).
type dwellMarker struct {
	Event       string `json:"event"`
	PID         int    `json:"pid"`
	Executable  string `json:"executable"`
	Signal      string `json:"signal"`
	ExitDelayMS int    `json:"exit_delay_ms"`
	Timestamp   string `json:"timestamp"`
}

func waitForDwellMarker(t *testing.T, path string, timeout time.Duration) dwellMarker {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			lastErr = readErr
		} else {
			var m dwellMarker
			if unmarshalErr := json.Unmarshal(data, &m); unmarshalErr != nil {
				lastErr = unmarshalErr
			} else {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture marker %s not readable within %s: %v", path, timeout, lastErr)
		}
		// Polling for an out-of-process event, the same bounded wait the
		// maintained platform dwell test performs; it never substitutes for the
		// in-test synchronization, which is the gate.
		time.Sleep(5 * time.Millisecond)
	}
}

// recordingStore counts the durable writes of one stop so T-5 and T-6 can assert
// persistence rather than assume it.
type recordingStore struct {
	*mockStore

	mu     sync.Mutex
	states []string
}

func newRecordingStore() *recordingStore {
	return &recordingStore{mockStore: newMockStore()}
}

func (s *recordingStore) Create(e *domain.LaunchInstanceEntry) error {
	err := s.mockStore.Create(e)
	if err == nil {
		s.note(e.State)
	}
	return err
}

func (s *recordingStore) Update(e *domain.LaunchInstanceEntry) error {
	err := s.mockStore.Update(e)
	if err == nil {
		s.note(e.State)
	}
	return err
}

func (s *recordingStore) note(state string) {
	s.mu.Lock()
	s.states = append(s.states, state)
	s.mu.Unlock()
}

func (s *recordingStore) sequence() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.states...)
}

func (s *recordingStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.states)
}

// stoppingHarness owns one supervisor with one running signal-delay instance,
// its controller, and the gate installed on that controller's Manager.
type stoppingHarness struct {
	sup     *Supervisor
	store   *recordingStore
	fixture *stoppingFixture
	ctrl    *InstanceController
	inst    *domain.LaunchInstance
	h       *stopHold

	stopDone chan error
}

type stoppingFixture struct {
	dir     string
	exePath string
}

func newStoppingFixture(t *testing.T) *stoppingFixture {
	t.Helper()
	return &stoppingFixture{dir: t.TempDir(), exePath: buildFakeRuntimeForTest(t)}
}

func (f *stoppingFixture) args() []string {
	return []string{"signal-delay", f.dir, strconv.Itoa(dwellExitDelayMS)}
}

func (f *stoppingFixture) markerPath(name string) string {
	return filepath.Join(f.dir, name)
}

func newStoppingHarness(t *testing.T) *stoppingHarness {
	t.Helper()
	fixture := newStoppingFixture(t)
	store := newRecordingStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64})

	model := &domain.Model{ID: "m-stop", Name: "stop-model", RuntimeID: "rt-stop"}
	rt := &domain.Runtime{ID: "rt-stop", Name: "stop-rt", Executable: fixture.exePath}

	inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, fixture.args(), nil)
	if err != nil {
		t.Fatalf("AdmitAndStart: %v", err)
	}
	ctrl, err := sup.controllerFor(inst.ID)
	if err != nil {
		t.Fatalf("controllerFor(%s): %v", inst.ID, err)
	}
	live := ctrl.Snapshot()
	if live.State != domain.InstanceStateRunning {
		t.Fatalf("launched instance state = %q, want %q", live.State, domain.InstanceStateRunning)
	}

	// ready.json is written only after the child installed its signal handler,
	// so a stop requested after this point is a deliverable graceful stop.
	ready := waitForDwellMarker(t, fixture.markerPath("ready.json"), dwellSignalWaitDeadline)
	if ready.Event != "ready" {
		t.Fatalf("ready marker event = %q, want %q", ready.Event, "ready")
	}
	if ready.PID != live.PID {
		t.Fatalf("ready marker pid = %d, want instance pid %d", ready.PID, live.PID)
	}

	return &stoppingHarness{
		sup:     sup,
		store:   store,
		fixture: fixture,
		ctrl:    ctrl,
		inst:    inst,
		h:       newStopHold(t, ctrl.manager),
	}
}

// beginStop issues the instance stop and waits until Manager.Stop is parked
// inside the stopping window.
func (hs *stoppingHarness) beginStop(t *testing.T) {
	t.Helper()
	hs.stopDone = make(chan error, 1)
	go func() { hs.stopDone <- hs.ctrl.Stop(context.Background()) }()
	hs.h.awaitEntry(t)
}

// releaseStop lets the held stop proceed and waits for InstanceController.Stop
// to return, so the caller only asserts on state the stop itself has finalized.
func (hs *stoppingHarness) releaseStop(t *testing.T) error {
	t.Helper()
	hs.h.release()
	select {
	case err := <-hs.stopDone:
		return err
	case <-time.After(stopReturnDeadline):
		t.Fatalf("Stop did not return within %s after the gate was released", stopReturnDeadline)
		return nil
	}
}

// stopUngated stops the instance with the seam disarmed, for the T-6 baseline
// leg. It is the path production takes, because production never installs a gate.
func (hs *stoppingHarness) stopUngated(t *testing.T) error {
	t.Helper()
	hs.ctrl.manager.setStopGate(nil)
	ch := make(chan error, 1)
	go func() { ch <- hs.ctrl.Stop(context.Background()) }()
	select {
	case err := <-ch:
		return err
	case <-time.After(stopReturnDeadline):
		t.Fatalf("ungated Stop did not return within %s", stopReturnDeadline)
		return nil
	}
}

// assertLiveManagedProcess proves the managed process is alive by identity.
// IsProcessAlive alone answers "does this PID exist", which a recycled PID can
// satisfy, so the comparison is the recovery path's own verifyIdentity against
// the frozen Executable and StartedAt of the published view (T-1, AH-3).
func assertLiveManagedProcess(t *testing.T, snap *domain.LaunchInstance) platform.ProcessIdentity {
	t.Helper()
	prober := platform.NewRecoveryProber()

	alive, err := prober.IsProcessAlive(snap.PID)
	if err != nil {
		t.Fatalf("IsProcessAlive(%d): %v", snap.PID, err)
	}
	if !alive {
		t.Fatalf("IsProcessAlive(%d) = false: the managed process was already gone when the stopping state was read", snap.PID)
	}

	id, err := prober.GetProcessIdentity(snap.PID)
	if err != nil {
		t.Fatalf("GetProcessIdentity(%d): %v", snap.PID, err)
	}
	if !verifyIdentity(snap, id) {
		t.Fatalf("process at pid %d is not the managed process: probed executable %q start %v (has=%t), instance executable %q started at %v",
			snap.PID, id.ExecutablePath, id.StartTime, id.HasStartTime, snap.Executable, snap.StartedAt)
	}
	return id
}

// TestT1StoppingViewReadableWhileProcessAlive is ADR 021 T-1: with Manager.Stop
// held by the gate, the published view reports "stopping" while the managed
// process is still provably alive, and the released stop then produces the
// terminal state.
func TestT1StoppingViewReadableWhileProcessAlive(t *testing.T) {
	hs := newStoppingHarness(t)
	running := hs.ctrl.currentRun()
	runningPID := hs.ctrl.Snapshot().PID
	runningGeneration := hs.ctrl.view.Load().generation

	hs.beginStop(t)

	// Corroborating in-test signal, not the liveness proof: the generation that
	// was running has not completed while the stop is parked.
	select {
	case <-running.done:
		t.Fatal("the live generation completed while Manager.Stop was still held")
	default:
	}

	snap := readWhileHeld(t, hs.h, "InstanceController.Snapshot", func() domain.LaunchInstance {
		return hs.ctrl.Snapshot()
	})
	if snap.State != domain.InstanceStateStopping {
		t.Fatalf("state read inside the held stop = %q, want %q", snap.State, domain.InstanceStateStopping)
	}
	if snap.PID != runningPID {
		t.Fatalf("stopping view pid = %d, want the running generation's pid %d", snap.PID, runningPID)
	}
	assertLiveManagedProcess(t, &snap)

	if got := hs.ctrl.view.Load().generation; got <= runningGeneration {
		t.Fatalf("published generation = %d, want more than the running generation's %d", got, runningGeneration)
	}

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	after := hs.ctrl.Snapshot()
	if after.State != domain.InstanceStateExited {
		t.Fatalf("state after the released stop = %q, want %q", after.State, domain.InstanceStateExited)
	}
	// The terminal class of a GoAl-requested stop is "signaled" by production
	// design: Manager.wait gives an intentional stop the highest priority and
	// classifies it as signaled regardless of the OS exit code (the m.stopReq
	// branch in internal/process/manager.go). The graceful interval is therefore
	// proven by the exit code and the fixture marker below, not by the class:
	// code 0 means the child ran its own exit path after acknowledging the signal,
	// which a ForceKill cannot produce.
	if after.ExitClass != domain.InstanceExitSignaled {
		t.Fatalf("exit class after the released stop = %q, want %q", after.ExitClass, domain.InstanceExitSignaled)
	}
	if after.ExitCode == nil || *after.ExitCode != 0 {
		t.Fatalf("exit code after the released stop = %s, want 0: the child did not exit through its own signal handler path", describeExitCode(after.ExitCode))
	}

	// The fixture recorded receipt of the real OS signal, so the interval the
	// reads above landed in was a genuine graceful stopping window.
	wantSignal := "terminated"
	if runtime.GOOS == "windows" {
		wantSignal = "interrupt"
	}
	received := waitForDwellMarker(t, hs.fixture.markerPath("signal.json"), dwellSignalWaitDeadline)
	if received.Signal != wantSignal {
		t.Fatalf("signal marker signal = %q, want %q", received.Signal, wantSignal)
	}
	if received.ExitDelayMS != dwellExitDelayMS {
		t.Fatalf("signal marker exit_delay_ms = %d, want %d", received.ExitDelayMS, dwellExitDelayMS)
	}
}

// TestT2SupervisorStatusReportsStoppingWhileHeld is the T-2 Supervisor.Status
// layer.
func TestT2SupervisorStatusReportsStoppingWhileHeld(t *testing.T) {
	hs := newStoppingHarness(t)
	hs.beginStop(t)

	status := readWhileHeldErr(t, hs.h, "Supervisor.Status", func() (*domain.LaunchInstance, error) {
		return hs.sup.Status(hs.inst.ID)
	})
	if status.State != domain.InstanceStateStopping {
		t.Fatalf("Supervisor.Status state = %q, want %q", status.State, domain.InstanceStateStopping)
	}
	assertLiveManagedProcess(t, status)

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if final := hs.ctrl.Snapshot(); !final.IsTerminal() {
		t.Fatalf("state after the released stop = %q, want a terminal state", final.State)
	}
}

// TestT2SupervisorListReportsStoppingWhileHeld is the T-2 Supervisor.List layer,
// including ListActive, which must still count a stopping instance as live.
func TestT2SupervisorListReportsStoppingWhileHeld(t *testing.T) {
	hs := newStoppingHarness(t)
	hs.beginStop(t)

	listed := readWhileHeldErr(t, hs.h, "Supervisor.List", func() ([]*domain.LaunchInstance, error) {
		return hs.sup.List()
	})
	if len(listed) != 1 {
		t.Fatalf("Supervisor.List returned %d instances, want 1", len(listed))
	}
	if listed[0].ID != hs.inst.ID {
		t.Fatalf("Supervisor.List returned %q, want %q", listed[0].ID, hs.inst.ID)
	}
	if listed[0].State != domain.InstanceStateStopping {
		t.Fatalf("Supervisor.List state = %q, want %q", listed[0].State, domain.InstanceStateStopping)
	}

	active := readWhileHeldErr(t, hs.h, "Supervisor.ListActive", func() ([]*domain.LaunchInstance, error) {
		return hs.sup.ListActive()
	})
	if len(active) != 1 || active[0].State != domain.InstanceStateStopping {
		t.Fatalf("Supervisor.ListActive = %d entries %v, want the one stopping instance", len(active), activeState(active))
	}

	assertLiveManagedProcess(t, listed[0])

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func activeState(list []*domain.LaunchInstance) []domain.InstanceState {
	out := make([]domain.InstanceState, 0, len(list))
	for _, i := range list {
		out = append(out, i.State)
	}
	return out
}

// TestT3I3ReaderPrimitivesCompleteWhileHeld is the I-3 control test: each
// reader primitive named by §D5 resolves inside the held window, while the stop
// demonstrably holds ic.mu for writing.
func TestT3I3ReaderPrimitivesCompleteWhileHeld(t *testing.T) {
	hs := newStoppingHarness(t)
	runBefore := hs.ctrl.currentRun()
	hs.beginStop(t)

	// Prove the window is the real one: the stop's write lock is held, so a
	// reader that still acquired ic.mu could only answer after the release.
	if hs.ctrl.mu.TryLock() {
		hs.ctrl.mu.Unlock()
		t.Fatal("ic.mu was free while Manager.Stop was held: this run did not exercise a stop holding the controller lock")
	}

	snap := readWhileHeld(t, hs.h, "Snapshot", func() domain.LaunchInstance { return hs.ctrl.Snapshot() })
	if snap.State != domain.InstanceStateStopping {
		t.Fatalf("Snapshot state = %q, want %q", snap.State, domain.InstanceStateStopping)
	}
	if !readWhileHeld(t, hs.h, "IsRunning", func() bool { return hs.ctrl.IsRunning() }) {
		t.Fatal("IsRunning = false for a live stopping instance")
	}
	if run := readWhileHeld(t, hs.h, "currentRun", func() *instanceRunState { return hs.ctrl.currentRun() }); run != runBefore {
		t.Fatal("currentRun resolved to a different generation while the stop was held")
	}
	done := readWhileHeld(t, hs.h, "GetControllerDone", func() <-chan struct{} { return hs.ctrl.GetControllerDone() })
	if done == nil {
		t.Fatal("GetControllerDone returned nil for the live generation")
	}
	// The launch operation was released when the admission completed, so an
	// in-flight stop publishes no operation: this is the arbitration decision
	// the migrated reader must keep producing.
	if op := readWhileHeld(t, hs.h, "activeOperation", func() *launchOperation { return hs.ctrl.activeOperation() }); op != nil {
		t.Fatalf("activeOperation = %+v while a plain stop is in flight, want nil", op)
	}
	// A claim that no longer owns the controller must still be rejected from the
	// view; the value is not the subject, the completion is.
	readWhileHeld(t, hs.h, "revalidateOperation", func() error {
		return hs.ctrl.revalidateOperation(&spawnClaim{opID: 1 << 40})
	})

	assertLiveManagedProcess(t, &snap)

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if hs.ctrl.mu.TryLock() {
		hs.ctrl.mu.Unlock()
	} else {
		t.Fatal("ic.mu was still held after Stop returned")
	}
}

// TestT3CrossModelArbitrationCompletesWhileHeld is T-3: while instance A is
// inside the held stop, an arbitration-driven launch of ANOTHER model completes.
// At the baseline this stalled, because scanConflicts read every candidate
// controller under s.mu.RLock through ic.mu; §D9 records the removal of that
// read-side edge as a side benefit and §D10/RV-13 scopes it to readers. The
// assertion is ordering, not duration: the launch returns while the gate is still
// parked.
func TestT3CrossModelArbitrationCompletesWhileHeld(t *testing.T) {
	hs := newStoppingHarness(t)
	otherDir := t.TempDir()
	hs.beginStop(t)

	if hs.ctrl.mu.TryLock() {
		hs.ctrl.mu.Unlock()
		t.Fatal("ic.mu was free while Manager.Stop was held: this run did not exercise a stop holding the controller lock")
	}

	started := readWhileHeldErr(t, hs.h, "another model's AdmitAndStart during the held stop", func() (*domain.LaunchInstance, error) {
		return hs.sup.AdmitAndStart(context.Background(),
			&domain.Model{ID: "m-other", Name: "other-model", RuntimeID: "rt-other"},
			&domain.Runtime{ID: "rt-other", Name: "other-rt", Executable: hs.fixture.exePath},
			domain.ManualOwner, []string{"signal-delay", otherDir, "200"}, nil)
	})
	if started.State != domain.InstanceStateRunning {
		t.Fatalf("cross-model launch state = %q, want %q", started.State, domain.InstanceStateRunning)
	}
	// The admission did not disturb the held stop, and the stopping candidate is
	// still in flight for a conflict scan that reaches it lock-free.
	held := readWhileHeld(t, hs.h, "stopped instance during the cross-model launch", func() domain.LaunchInstance { return hs.ctrl.Snapshot() })
	if held.State != domain.InstanceStateStopping {
		t.Fatalf("held instance state = %q, want %q", held.State, domain.InstanceStateStopping)
	}
	if !readWhileHeld(t, hs.h, "in-flight classification of the stopping instance", func() bool { return held.IsInFlight() }) {
		t.Fatal("the stopping instance is not in flight: the conflict set changed")
	}

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestT4StopDuringHeldStopTakesNoSecondOwnership is T-4's Stop ∥ Stop leg,
// stated as the three separate RV-6 properties.
func TestT4StopDuringHeldStopTakesNoSecondOwnership(t *testing.T) {
	hs := newStoppingHarness(t)
	firstRun := hs.ctrl.currentRun()
	hs.beginStop(t)

	second := make(chan error, 1)
	go func() { second <- hs.ctrl.Stop(context.Background()) }()

	// (i) Stop-path serialization: the second call cannot enter Manager.Stop
	// while the first is inside it. `Stop` takes ic.lifecycleMu first
	// (supervisor.go:1705-1706) and `stopCore` then holds ic.mu across
	// manager.Stop (:1744-1747), so gate entries are the witness that no second
	// call became a stop owner — they do not isolate which of the two locks
	// serialized it, and this test makes no claim about `lifecycleMu` alone.
	if got := readWhileHeld(t, hs.h, "gate entry count during the second Stop", func() int { return hs.h.entryCount() }); got != 1 {
		t.Fatalf("Manager.Stop gate entries = %d while the second Stop was in flight, want 1: the second call became a second owner of the same run", got)
	}
	// The state has not been inverted by the concurrent call.
	snap := readWhileHeld(t, hs.h, "Snapshot during the second Stop", func() domain.LaunchInstance { return hs.ctrl.Snapshot() })
	if snap.State != domain.InstanceStateStopping {
		t.Fatalf("state during two Stops = %q, want %q", snap.State, domain.InstanceStateStopping)
	}

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("first Stop: %v", err)
	}

	// (ii) One cmd.Wait() owner: the generation completed exactly once and its
	// terminal record came from the single manager waiter.
	select {
	case <-firstRun.done:
	default:
		t.Fatal("the stopped generation's run never completed")
	}
	if got := hs.ctrl.currentRun(); got != firstRun {
		t.Fatal("the controller published a different generation during two Stops")
	}

	// (iii) The second call returns without taking stop ownership (the terminal
	// early return in stop()), still with one gate entry. This says nothing about
	// the OS-5 timeout interleaving, which is out of scope.
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("second Stop: %v", err)
		}
	case <-time.After(stopReturnDeadline):
		t.Fatal("second Stop did not return after the first completed")
	}
	if got := hs.h.entryCount(); got != 1 {
		t.Fatalf("Manager.Stop gate entries after both Stops = %d, want 1", got)
	}
}

// TestT4RestartDuringHeldStopDoesNotPublishThroughLiveRun is T-4's Stop ∥
// Restart leg: the restart's preflight is no longer stalled by an in-flight stop
// (§D9 row 2), and while the stop owns the controller no second generation
// reaches it.
func TestT4RestartDuringHeldStopDoesNotPublishThroughLiveRun(t *testing.T) {
	hs := newStoppingHarness(t)
	stoppedRun := hs.ctrl.currentRun()
	hs.beginStop(t)

	restartDone := make(chan error, 1)
	go func() {
		_, err := hs.sup.Restart(context.Background(), hs.inst.ID)
		restartDone <- err
	}()

	// (i) The restart preflight is a lock-free view load, so the stopping instance
	// is no longer a blocker for the decision: checkRestartTarget completes while
	// Manager.Stop is still parked, and allows the stopping target (§D9 row 2,
	// §D10 read-side improvement). Asserted as an ordering fact, never a duration.
	readWhileHeldErr(t, hs.h, "restart preflight against the held stop", func() (struct{}, error) {
		return struct{}{}, checkRestartTarget(hs.ctrl)
	})

	// (ii) Safety invariant during the hold: no second generation reaches this
	// controller. The restart's operation publication takes ctrl.mu
	// (arbitration.go publishClaimLocked), which the stop holds across
	// Manager.Stop — a write-side edge §D10/RV-13 records as deliberately
	// unchanged — so what this leg requires is that the live generation and the
	// published state are still the stopped run's for as long as the stop owns the
	// controller, not that a reservation is visible yet.
	during := readWhileHeld(t, hs.h, "currentRun during the pending restart", func() *instanceRunState { return hs.ctrl.currentRun() })
	if during != stoppedRun {
		t.Fatal("a restart published a new generation while Manager.Stop was held")
	}
	snap := readWhileHeld(t, hs.h, "Snapshot during the pending restart", func() domain.LaunchInstance { return hs.ctrl.Snapshot() })
	if snap.State != domain.InstanceStateStopping {
		t.Fatalf("state during the pending restart = %q, want %q", snap.State, domain.InstanceStateStopping)
	}
	heldGeneration := readWhileHeld(t, hs.h, "generation during the pending restart", func() uint64 { return hs.ctrl.view.Load().generation })

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	var restartErr error
	select {
	case restartErr = <-restartDone:
	case <-time.After(stopReturnDeadline):
		t.Fatal("the restart never finished after the stop was released")
	}
	if restartErr != nil {
		t.Fatalf("Restart after the released stop: %v", restartErr)
	}

	after := hs.ctrl.currentRun()
	if after == stoppedRun {
		t.Fatal("the restart reused the stopped generation instead of publishing a new run")
	}
	final := hs.ctrl.Snapshot()
	if final.State != domain.InstanceStateRunning {
		t.Fatalf("state after the restart = %q, want %q", final.State, domain.InstanceStateRunning)
	}
	if final.PID == snap.PID {
		t.Fatalf("the restart published pid %d, the same as the stopped generation", final.PID)
	}
	// Generation identity is monotonic across the stop and the restart, and the
	// restart's reservation is released by its own operation, not by the stop.
	afterView := hs.ctrl.view.Load()
	if afterView.generation <= heldGeneration {
		t.Fatalf("generation after the restart = %d, want more than the held-window generation %d", afterView.generation, heldGeneration)
	}
	if op := hs.ctrl.activeOperation(); op != nil {
		t.Fatalf("the completed restart left an operation published on the controller: op id=%d", op.id)
	}
}

// TestT4StartDuringHeldStopIsArbitratedAgainstStopping is T-4's Start ∥ Stop
// leg: a launch of the same model must reach its conflict decision inside the
// held window instead of blocking behind the stop.
func TestT4StartDuringHeldStopIsArbitratedAgainstStopping(t *testing.T) {
	hs := newStoppingHarness(t)
	rejectDir := t.TempDir()
	hs.beginStop(t)

	rejected := readWhileHeld(t, hs.h, "same-model AdmitAndStart during the held stop", func() error {
		_, err := hs.sup.AdmitAndStart(context.Background(),
			&domain.Model{ID: "m-stop", Name: "stop-model", RuntimeID: "rt-stop"},
			&domain.Runtime{ID: "rt-stop", Name: "stop-rt", Executable: hs.fixture.exePath},
			domain.ManualOwner, []string{"signal-delay", rejectDir, "200"}, nil)
		return err
	})
	if rejected == nil {
		t.Fatal("a same-model launch was admitted while a live stopping instance of that model existed")
	}
	var rej *AdmissionRejection
	if !errors.As(rejected, &rej) {
		t.Fatalf("same-model launch during a held stop: expected *AdmissionRejection, got %T: %v", rejected, rejected)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("rejection reason = %v, want %v", rej.Reason, RejInFlight)
	}
	if rej.ConflictID != hs.inst.ID {
		t.Fatalf("conflict instance = %q, want the stopping instance %q", rej.ConflictID, hs.inst.ID)
	}

	// The decision did not disturb the stop.
	snap := readWhileHeld(t, hs.h, "Snapshot after the rejected launch", func() domain.LaunchInstance { return hs.ctrl.Snapshot() })
	if snap.State != domain.InstanceStateStopping {
		t.Fatalf("state after the rejected launch = %q, want %q", snap.State, domain.InstanceStateStopping)
	}

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestT5DurableRecordStaysRunningWhileLiveViewIsStopping is T-5: the live view
// is ahead of the durable record in exactly the window it is ahead today, and the
// durable sequence pending → starting → running → stopping → terminal is
// preserved, with the terminal record written once by wait() and once more by the
// stop owner.
func TestT5DurableRecordStaysRunningWhileLiveViewIsStopping(t *testing.T) {
	hs := newStoppingHarness(t)
	hs.beginStop(t)

	snap := readWhileHeld(t, hs.h, "Snapshot", func() domain.LaunchInstance { return hs.ctrl.Snapshot() })
	if snap.State != domain.InstanceStateStopping {
		t.Fatalf("live view state = %q, want %q", snap.State, domain.InstanceStateStopping)
	}

	// stopCore persists "stopping" only after Manager.Stop returns, so the
	// durable record must still carry the full running identity.
	stored := readWhileHeldErr(t, hs.h, "store.Get", func() (*domain.LaunchInstanceEntry, error) {
		return hs.store.Get(string(hs.inst.ID))
	})
	if stored.State != string(domain.InstanceStateRunning) {
		t.Fatalf("durable state inside the held stop = %q, want %q", stored.State, domain.InstanceStateRunning)
	}
	if stored.PID != snap.PID || stored.Executable != snap.Executable {
		t.Fatalf("durable running identity changed: entry pid=%d exe=%q, view pid=%d exe=%q", stored.PID, stored.Executable, snap.PID, snap.Executable)
	}

	if err := hs.releaseStop(t); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	final, err := hs.store.Get(string(hs.inst.ID))
	if err != nil {
		t.Fatalf("store.Get after release: %v", err)
	}
	if final.State != string(domain.InstanceStateExited) {
		t.Fatalf("durable state after the released stop = %q, want %q", final.State, domain.InstanceStateExited)
	}

	// The terminal record is written twice, by two different owners, and both
	// writes are production behavior:
	//   - wait() persists the terminal state once it has confirmed the exit
	//     (the authoritative in-memory terminal write), and
	//   - stopCore then persists the final state again after it has synchronized
	//     with wait() (internal/process/supervisor.go, the post-done
	//     persistState), which is what makes a stop-path LastError durable.
	// The observable ordering obligation is the one above: nothing reaches the
	// store before the live view publishes it.
	want := []string{
		string(domain.InstanceStatePending),
		string(domain.InstanceStateStarting),
		string(domain.InstanceStateRunning),
		string(domain.InstanceStateStopping),
		string(domain.InstanceStateExited),
		string(domain.InstanceStateExited),
	}
	if got := hs.store.sequence(); !equalStrings(got, want) {
		t.Fatalf("durable state sequence = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func describeExitCode(code *int) string {
	if code == nil {
		return "<nil>"
	}
	return strconv.Itoa(*code)
}

// TestT6StopGateIsNilByDefault proves the seam is inert by construction: a
// Manager carries no gate unless a test in this package installs one.
func TestT6StopGateIsNilByDefault(t *testing.T) {
	if NewManager().stopGate != nil {
		t.Fatal("NewManager installed a stop gate: the seam is not nil-default")
	}
	hs := newStoppingHarness(t)
	// The harness installs the seam deliberately; a fresh controller's manager
	// must not carry one.
	fresh := NewInstanceController(&domain.LaunchInstance{ID: "i-fresh", ModelID: "m-fresh"}, hs.store, nil, nil)
	if fresh.manager.stopGate != nil {
		t.Fatal("NewInstanceController installed a stop gate: the seam is not nil-default")
	}
}

// TestT6ReleasedGateStopMatchesUngatedStop is T-6's inertness claim measured:
// the gated and ungated stops of the same fixture produce the same durable
// sequence, the same number of store writes, and the same observable outcome.
func TestT6ReleasedGateStopMatchesUngatedStop(t *testing.T) {
	ungated := newStoppingHarness(t)
	if err := ungated.stopUngated(t); err != nil {
		t.Fatalf("ungated Stop: %v", err)
	}

	gated := newStoppingHarness(t)
	gated.beginStop(t)
	if err := gated.releaseStop(t); err != nil {
		t.Fatalf("gated Stop: %v", err)
	}

	if u, g := ungated.store.writeCount(), gated.store.writeCount(); u != g {
		t.Fatalf("store writes: ungated=%d gated=%d (the seam must not add or move a persistence)", u, g)
	}
	if u, g := ungated.store.sequence(), gated.store.sequence(); !equalStrings(u, g) {
		t.Fatalf("durable sequence: ungated=%v gated=%v", u, g)
	}

	uSnap, gSnap := ungated.ctrl.Snapshot(), gated.ctrl.Snapshot()
	if uSnap.State != gSnap.State {
		t.Fatalf("final state: ungated=%q gated=%q", uSnap.State, gSnap.State)
	}
	if uSnap.ExitClass != gSnap.ExitClass {
		t.Fatalf("final exit class: ungated=%q gated=%q", uSnap.ExitClass, gSnap.ExitClass)
	}
	if uSnap.StoppedAt.IsZero() || gSnap.StoppedAt.IsZero() {
		t.Fatal("a released stop did not stamp StoppedAt")
	}
}
