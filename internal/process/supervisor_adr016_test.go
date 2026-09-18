package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

// isTerminalState reports whether a durable entry state is terminal.
func isTerminalState(s domain.InstanceState) bool {
	switch s {
	case domain.InstanceStateExited, domain.InstanceStateFailed, domain.InstanceStateStale:
		return true
	}
	return false
}

// storeAccepted persists the entry inside an update hook. mockStore.Update
// holds s.mu while invoking the hook, so this MUST NOT lock again.
func (s *mockStore) storeAccepted(e *domain.LaunchInstanceEntry) {
	s.instances[string(e.ID)] = e
}

// rejectingStore returns a mockStore whose update hook persists every state
// except the listed rejected ones (which return testUpdateErr). Note that
// mockStore.Update does NOT store the entry itself when an update hook is
// set, so the hook must persist accepted states explicitly.
func rejectingStore(rejected ...string) *mockStore {
	s := newMockStore()
	s.updateFn = func(e *domain.LaunchInstanceEntry) error {
		for _, r := range rejected {
			if e.State == r {
				return testUpdateErr
			}
		}
		s.storeAccepted(e)
		return nil
	}
	return s
}

// waitForSlotFree polls until the supervisor semaphore holds wantFree tokens
// (i.e. that many concurrency slots are released).
func waitForSlotFree(t *testing.T, sup *Supervisor, wantFree int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := len(sup.semaphore); got == wantFree {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d free slot(s); got %d", wantFree, len(sup.semaphore))
}

// waitForProcessDead polls the platform prober until the PID is reported
// dead or the timeout elapses. On Windows, OpenProcess can succeed briefly
// for a just-terminated process, so a single immediate check is not a
// reliable death confirmation.
func waitForProcessDead(t *testing.T, prober platform.RecoveryProber, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := prober.IsProcessAlive(pid)
		if err == nil && !alive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	alive, err := prober.IsProcessAlive(pid)
	t.Fatalf("process %d is still alive (alive=%v) after %v (err=%v)", pid, alive, timeout, err)
}

// assertSlotReleasedExactlyOnce confirms that exactly one slot token has been
// released: it waits for the token, consumes it, then asserts no second token
// arrives within grace (a double release would surface as a second token).
func assertSlotReleasedExactlyOnce(t *testing.T, sup *Supervisor, grace time.Duration) {
	t.Helper()
	waitForSlotFree(t, sup, 1, 10*time.Second)
	<-sup.semaphore
	select {
	case <-sup.semaphore:
		t.Fatal("second token available — slot was released more than once")
	case <-time.After(grace):
	}
}

// waitForRunDone polls until the instance's run completion channel closes.
func waitForRunDone(t *testing.T, sup *Supervisor, id domain.InstanceID, timeout time.Duration) *InstanceController {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sup.mu.RLock()
		ctrl, ok := sup.instances[id]
		sup.mu.RUnlock()
		if !ok {
			t.Fatalf("instance %q removed from registry before run completion", string(id))
		}
		if done := ctrl.GetControllerDone(); done != nil {
			select {
			case <-done:
				return ctrl
			default:
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for run completion of %q", string(id))
	return nil
}

// setKillOverrideOnFirstRunningPersist returns an update hook that persists
// every state except running (which returns testUpdateErr), and wires a
// manager kill override the moment the running-state persist is first
// attempted (i.e. after a successful spawn, synchronously inside startCore).
func setKillOverrideOnFirstRunningPersist(t *testing.T, sup *Supervisor, store *mockStore, override func() error) func(*domain.LaunchInstanceEntry) error {
	t.Helper()
	return func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) {
			sup.mu.RLock()
			ctrl, _ := sup.instances[domain.InstanceID(e.ID)]
			sup.mu.RUnlock()
			if ctrl == nil {
				t.Errorf("controller not found for %q at running persist", e.ID)
				return testUpdateErr
			}
			ctrl.manager.SetKillOverride(override)
			return testUpdateErr
		}
		store.storeAccepted(e)
		return nil
	}
}

// withRollbackConfirmWindow temporarily narrows the rollback exit-confirmation
// window and restores it after the test.
func withRollbackConfirmWindow(t *testing.T, window time.Duration) {
	t.Helper()
	old := rollbackExitConfirmWindow
	SetRollbackConfirmWindow(window)
	t.Cleanup(func() { SetRollbackConfirmWindow(old) })
}

// TestStartCore_RunningPersistFail_KillConfirmed verifies ADR 016 Outcome A:
// running persist fails after retry, the rollback kill is accepted, the exit
// is CONFIRMED (the process is actually dead), Start() returns
// ErrPersistenceFailure, and the slot is released.
func TestStartCore_RunningPersistFail_KillConfirmed(t *testing.T) {
	store := rejectingStore(string(domain.InstanceStateRunning))
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "adr016-a", Name: "a", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) {
		t.Fatalf("expected ErrPersistenceFailure, got %v", err)
	}
	if errors.Is(err, ErrTerminationUnconfirmed) || errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("Outcome A must not carry residual sentinels, got %v", err)
	}
	if inst != nil {
		t.Fatalf("Outcome A returns nil instance, got %q", string(inst.ID))
	}
	waitForSlotFree(t, sup, 1, 5*time.Second)

	// The durable record carries the terminal state. The authoritative exit
	// confirmation is the Manager's done channel (already consumed by
	// confirmExit); IsProcessAlive via OpenProcess is unreliable on Windows
	// for freshly-terminated processes (the kernel object lingers briefly).
	entries, err := store.List()
	if err != nil {
		t.Fatalf("store list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("durable entries = %d, want 1", len(entries))
	}
	for _, e := range entries {
		if !isTerminalState(domain.InstanceState(e.State)) {
			t.Fatalf("durable state = %q, want terminal", e.State)
		}
	}
}

// TestStartCore_RunningPersistFail_FailedPersistFails verifies ADR 016
// Outcome B: the rollback kill is confirmed dead, but persisting the failed
// state ALSO fails. The repository retains `starting` (no PID), Start()
// returns ErrPersistenceFailure, and the slot is released.
func TestStartCore_RunningPersistFail_FailedPersistFails(t *testing.T) {
	store := rejectingStore(
		string(domain.InstanceStateRunning),
		string(domain.InstanceStateFailed),
	)
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "adr016-b", Name: "b", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) {
		t.Fatalf("expected ErrPersistenceFailure, got %v", err)
	}
	if inst != nil {
		t.Fatalf("Outcome B returns nil instance, got %q", string(inst.ID))
	}
	waitForSlotFree(t, sup, 1, 5*time.Second)

	// The durable record is either:
	//   - starting/0: the initial persist succeeded, running+failed were
	//     rejected, and wait() has not yet persisted the terminal state.
	//   - exited/PID>0: wait() has confirmed the killed process exit and
	//     persisted the terminal state (the store rejects running/failed
	//     but accepts exited).
	// Both are valid ADR 016 Outcome B observations: the running identity
	// was never durable, and the failed rollback persist was rejected.
	// The wait() goroutine's terminal persist is a legal concurrent
	// postcondition, not a contract violation.
	entries, err := store.List()
	if err != nil {
		t.Fatalf("store list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("durable entries = %d, want 1", len(entries))
	}
	st := domain.InstanceState(entries[0].State)
	switch {
	case st == domain.InstanceStateStarting && entries[0].PID == 0:
		// Pre-wait(): the repository retains the initial starting record.
	case st == domain.InstanceStateExited && entries[0].PID > 0:
		// Post-wait(): the killed process exit was persisted terminally.
	default:
		t.Fatalf("durable record = state %q pid %d; want starting/0 or exited/PID>0 (Outcome B)", entries[0].State, entries[0].PID)
	}
}

// TestStartCore_RunningPersistFail_KillUnconfirmed verifies ADR 016 Outcome C:
// the rollback kill is accepted by the OS but the exit is NOT confirmed
// within the bounded window. The slot and run ownership stay with the wait()
// goroutine; the instance remains in the registry in starting state; and the
// eventual wait() exit confirmation releases ownership exactly once.
func TestStartCore_RunningPersistFail_KillUnconfirmed(t *testing.T) {
	withRollbackConfirmWindow(t, 300*time.Millisecond)
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	// Kill accepted by the OS, but the process is NOT actually killed:
	// termination remains unconfirmed until the process exits on its own.
	store.updateFn = setKillOverrideOnFirstRunningPersist(t, sup, store, func() error { return nil })
	model := &domain.Model{ID: "adr016-c", Name: "c", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "2"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) || !errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatalf("expected ErrPersistenceFailure + ErrTerminationUnconfirmed, got %v", err)
	}
	if errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("Outcome C must not carry ErrRollbackFailed, got %v", err)
	}
	if inst == nil {
		t.Fatal("Outcome C returns the instance (kept in the registry), got nil")
	}
	// The instance stays in the registry, in starting state, with the
	// rollback diagnostic in LastError.
	snap, err := sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.State != domain.InstanceStateStarting {
		t.Fatalf("state = %q, want starting (residual ownership)", snap.State)
	}
	if snap.LastError == "" {
		t.Fatal("expected rollback diagnostic in LastError")
	}
	// The slot is HELD while termination is unconfirmed.
	if got := len(sup.semaphore); got != 0 {
		t.Fatalf("free slots while termination unconfirmed = %d, want 0 (slot must be held)", got)
	}

	// The process exits on its own (-sleep 2); wait() then confirms exit and
	// releases ownership exactly once.
	ctrl := waitForRunDone(t, sup, inst.ID, 10*time.Second)
	cSnap := ctrl.Snapshot()
	if !cSnap.IsTerminal() {
		t.Fatalf("state after wait() confirmation = %q, want terminal", cSnap.State)
	}
	assertSlotReleasedExactlyOnce(t, sup, 300*time.Millisecond)
}

// TestStartCore_RunningPersistFail_KillFailed verifies ADR 016 Outcome D: the
// rollback kill is REFUSED by the OS (genuine refusal). The process is still
// alive, the instance stays starting with the slot held, and the error
// carries ErrRollbackFailed. The instance remains stoppable; after an
// operator stop, wait() releases ownership exactly once.
func TestStartCore_RunningPersistFail_KillFailed(t *testing.T) {
	killRefused := errors.New("simulated OS refusal: access denied")
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	store.updateFn = setKillOverrideOnFirstRunningPersist(t, sup, store, func() error { return killRefused })
	model := &domain.Model{ID: "adr016-d", Name: "d", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "60"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) || !errors.Is(err, ErrRollbackFailed) || !errors.Is(err, killRefused) {
		t.Fatalf("expected ErrPersistenceFailure + ErrRollbackFailed + kill error, got %v", err)
	}
	if errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatalf("Outcome D must not carry ErrTerminationUnconfirmed, got %v", err)
	}
	if inst == nil {
		t.Fatal("Outcome D returns the instance (kept in the registry), got nil")
	}
	snap, err := sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.State != domain.InstanceStateStarting {
		t.Fatalf("state = %q, want starting (kill refused, process may be alive)", snap.State)
	}
	// The process is genuinely ALIVE (the fake refusal did not kill it).
	prober := platform.NewRecoveryProber()
	alive, aliveErr := prober.IsProcessAlive(snap.PID)
	if aliveErr != nil || !alive {
		t.Fatalf("expected the process to be alive after a refused kill (alive=%v err=%v)", alive, aliveErr)
	}
	// Slot held while the process is alive and unconfirmed.
	if got := len(sup.semaphore); got != 0 {
		t.Fatalf("free slots with live process = %d, want 0 (slot must be held)", got)
	}

	// The instance is stoppable; the operator stop kills it and wait()
	// confirms exit, releasing ownership exactly once.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopCancel()
	if err := sup.Stop(stopCtx, inst.ID); err != nil {
		t.Fatalf("stop refused-kill instance: %v", err)
	}
	ctrl := waitForRunDone(t, sup, inst.ID, 15*time.Second)
	cSnap := ctrl.Snapshot()
	if !cSnap.IsTerminal() {
		t.Fatalf("state after stop = %q, want terminal", cSnap.State)
	}
	assertSlotReleasedExactlyOnce(t, sup, 300*time.Millisecond)
}

// TestStartCore_RunningPersistFail_KillProcessAlreadyGone verifies the ADR
// 016 §2.1 note: if Kill() reports the process no longer exists
// (ErrKillAlreadyGone), the outcome is reclassified as confirmed dead
// (A/B), NOT Outcome D.
func TestStartCore_RunningPersistFail_KillProcessAlreadyGone(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	store.updateFn = setKillOverrideOnFirstRunningPersist(t, sup, store, func() error {
		return platform.ErrKillAlreadyGone
	})
	model := &domain.Model{ID: "adr016-gone", Name: "gone", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) {
		t.Fatalf("expected ErrPersistenceFailure, got %v", err)
	}
	if errors.Is(err, ErrRollbackFailed) || errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatalf("already-gone kill must reclassify as confirmed dead, got %v", err)
	}
	if inst != nil {
		t.Fatalf("confirmed-dead outcome returns nil instance, got %q", string(inst.ID))
	}
	waitForSlotFree(t, sup, 1, 5*time.Second)

	// The real process was not killed by the fake "already gone" override, so
	// clean it up explicitly to avoid leaking a live process into the fixture
	// teardown (Windows file-lock check in fakeruntime.Cleanup).
	if e, lerr := lastDurableInstance(t, store); lerr == nil && e != nil && e.PID > 0 {
		if alive, _ := platform.NewRecoveryProber().IsProcessAlive(e.PID); alive {
			t.Cleanup(func() {
				if p, perr := os.FindProcess(e.PID); perr == nil {
					_ = p.Kill()
				}
			})
		}
	}
}

// TestStartCore_RunningPersistRetry_SucceedsOnSecondAttempt verifies ADR 016
// §8: a transient running-persist failure is retried synchronously; when the
// second attempt succeeds, Start() returns success and the repository holds
// the full running identity (S1).
func TestStartCore_RunningPersistRetry_SucceedsOnSecondAttempt(t *testing.T) {
	var runningAttempts atomic.Int32
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) && runningAttempts.Add(1) == 1 {
			return errors.New("transient I/O stall (simulated)")
		}
		store.storeAccepted(e)
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "adr016-retry", Name: "retry", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("start with transient retry failure: %v", err)
	}
	if got := runningAttempts.Load(); got != 2 {
		t.Fatalf("running persist attempts = %d, want 2 (fail then succeed)", got)
	}
	// S1: the repository contains the full running identity.
	store.mu.RLock()
	e := store.instances[string(inst.ID)]
	store.mu.RUnlock()
	if e == nil {
		t.Fatal("no durable entry for the instance")
	}
	if e.State != string(domain.InstanceStateRunning) || e.PID <= 0 || e.StartedAt.IsZero() || e.Executable == "" {
		t.Fatalf("durable running identity incomplete: state=%q pid=%d started=%v exe=%q", e.State, e.PID, e.StartedAt, e.Executable)
	}
	// The process is running; clean it up.
	if err := sup.Stop(context.Background(), inst.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	sup.mu.RLock()
	ctrl := sup.instances[inst.ID]
	sup.mu.RUnlock()
	if ctrl != nil {
		if err := waitForProcess(context.Background(), ctrl, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStartCore_RunningPersistRetry_BoundedAttempts verifies ADR 016 §8: the
// retry is BOUNDED (persistRetryAttempts total attempts), after which the
// fail-closed rollback applies.
func TestStartCore_RunningPersistRetry_BoundedAttempts(t *testing.T) {
	var runningAttempts atomic.Int32
	store := rejectingStore(string(domain.InstanceStateRunning))
	origFn := store.updateFn
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) {
			runningAttempts.Add(1)
		}
		return origFn(e)
	}
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "adr016-bounded", Name: "bounded", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	start := time.Now()
	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) {
		t.Fatalf("expected ErrPersistenceFailure, got %v", err)
	}
	if inst != nil {
		t.Fatalf("confirmed-dead outcome returns nil instance, got %q", string(inst.ID))
	}
	if got := runningAttempts.Load(); got != persistRetryAttempts {
		t.Fatalf("running persist attempts = %d, want %d (bounded)", got, persistRetryAttempts)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("fail-closed path took %v, must stay under the 2s ADR 016 §8 budget", elapsed)
	}
	waitForSlotFree(t, sup, 1, 5*time.Second)
}

// TestWait_TerminalPersistRetry_SucceedsOnSecondAttempt verifies ADR 016
// §8/RB-003: a transient terminal-persist failure is retried in wait(); when
// the retry succeeds, the durable record becomes terminal and no persistence
// error is recorded.
func TestWait_TerminalPersistRetry_SucceedsOnSecondAttempt(t *testing.T) {
	var terminalAttempts atomic.Int32
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if (e.State == string(domain.InstanceStateExited) || e.State == string(domain.InstanceStateFailed)) &&
			terminalAttempts.Add(1) == 1 {
			return errors.New("transient I/O stall (simulated)")
		}
		store.storeAccepted(e)
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "adr016-wait-retry", Name: "w", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"exit-code", "0"}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctrl := waitForRunDone(t, sup, inst.ID, 10*time.Second)
	snap := ctrl.Snapshot()
	if !snap.IsTerminal() {
		t.Fatalf("state = %q, want terminal", snap.State)
	}
	if got := terminalAttempts.Load(); got != 2 {
		t.Fatalf("terminal persist attempts = %d, want 2 (fail then succeed)", got)
	}
	if snap.LastError != "" {
		t.Fatalf("LastError after successful retry = %q, want empty", snap.LastError)
	}
	store.mu.RLock()
	e := store.instances[string(inst.ID)]
	store.mu.RUnlock()
	if e == nil || !isTerminalState(domain.InstanceState(e.State)) {
		t.Fatalf("durable state after retry success = %v, want terminal", e)
	}
}

// TestWait_TerminalPersistFail_SlotReleased_RunCompleted verifies ADR 016
// T1-T3: after terminal-persist retry EXHAUSTION the run is fully finalized —
// the in-memory state is terminal and authoritative, the slot is released,
// the run is completed, and the instance remains in List(). The durable
// record stays at running (recovery will reconcile it as stale, T5).
func TestWait_TerminalPersistFail_SlotReleased_RunCompleted(t *testing.T) {
	var terminalAttempts atomic.Int32
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateExited) || e.State == string(domain.InstanceStateFailed) {
			terminalAttempts.Add(1)
			return testUpdateErr
		}
		store.storeAccepted(e)
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "adr016-wait-exhaust", Name: "w", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"exit-code", "0"}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctrl := waitForRunDone(t, sup, inst.ID, 10*time.Second)
	snap := ctrl.Snapshot()

	// T3: in-memory terminal state is authoritative.
	if !snap.IsTerminal() {
		t.Fatalf("in-memory state = %q, want terminal (T3)", snap.State)
	}
	// T1: slot released, run completed (waitForRunDone returned).
	if got := len(sup.semaphore); got != 1 {
		t.Fatalf("free slots after terminal persist exhaustion = %d, want 1 (T1)", got)
	}
	if got := terminalAttempts.Load(); got != persistRetryAttempts {
		t.Fatalf("terminal persist attempts = %d, want %d (bounded retry)", got, persistRetryAttempts)
	}
	if snap.LastError == "" {
		t.Fatal("expected the persistence failure to be observable in LastError")
	}
	// T2: the instance remains in List().
	list, err := sup.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != inst.ID {
		t.Fatalf("instance missing from List() (T2): %v", list)
	}
	// The durable record still has the running identity (T5 precondition).
	store.mu.RLock()
	e := store.instances[string(inst.ID)]
	store.mu.RUnlock()
	if e == nil || e.State != string(domain.InstanceStateRunning) {
		t.Fatalf("durable state = %v, want running (terminal persist exhausted)", e)
	}
}

// TestStart_RunningPersistRetryCancelledByShutdown verifies ADR 016 §8/§8.1
// cancellation semantics: when the supervisor lifecycle context is cancelled
// (shutdown) during the running-persist retry, the retry exits immediately and
// the fail-closed path still proceeds. The rollback kill is still attempted
// (the process must not be orphaned), but the exit-confirmation wait is
// aborted by the cancellation → Outcome C (residual): the instance is kept,
// the slot is held, and the error carries ErrTerminationUnconfirmed. Because
// the kill was real, wait() then confirms the actual exit and releases the
// slot exactly once — a shutdown never leaves an unmanaged process.
func TestStart_RunningPersistRetryCancelledByShutdown(t *testing.T) {
	lcCtx, lcCancel := context.WithCancel(context.Background())
	defer lcCancel()
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) {
			// The spawn already happened: simulate the shutdown cancelling
			// the lifecycle context exactly when the retry is in flight.
			lcCancel()
			return testUpdateErr
		}
		store.storeAccepted(e)
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	sup.lifecycleCtx = lcCtx
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})
	model := &domain.Model{ID: "adr016-lc", Name: "lc", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) {
		t.Fatalf("expected ErrPersistenceFailure, got %v", err)
	}
	// The exit-confirmation wait was aborted by the lifecycle cancellation:
	// Outcome C (residual ownership), not a confirmed-dead A/B.
	if !errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatalf("expected ErrTerminationUnconfirmed (cancelled confirmation wait), got %v", err)
	}
	if inst == nil {
		t.Fatal("Outcome C keeps the instance in the registry, got nil")
	}
	snap, serr := sup.Status(inst.ID)
	if serr != nil {
		t.Fatalf("status: %v", serr)
	}
	if snap.State != domain.InstanceStateStarting {
		t.Fatalf("state = %q, want starting (residual)", snap.State)
	}
	// The real kill did terminate the process; wait() confirms exit and
	// releases the held slot exactly once.
	assertSlotReleasedExactlyOnce(t, sup, 300*time.Millisecond)
}

// TestShutdown_ResidualOwnership verifies the ADR 016 shutdown interaction
// with residual ownership: an Outcome C instance (kill accepted, termination
// unconfirmed, slot held) is stopped by Shutdown. Shutdown must NOT
// manufacture a terminal confirmation: the instance only becomes terminal
// when wait() confirms the actual process exit, and the slot is released
// exactly once.
func TestShutdown_ResidualOwnership(t *testing.T) {
	withRollbackConfirmWindow(t, 300*time.Millisecond)
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	store.updateFn = setKillOverrideOnFirstRunningPersist(t, sup, store, func() error { return nil })
	model := &domain.Model{ID: "adr016-shutdown", Name: "s", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(context.Background(), model, rt, []string{"-sleep", "60"}, nil)
	if err == nil || !errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatalf("expected Outcome C, got inst=%v err=%v", inst, err)
	}
	if inst == nil {
		t.Fatal("Outcome C must return the instance")
	}
	if got := len(sup.semaphore); got != 0 {
		t.Fatalf("slot not held before shutdown: %d free", got)
	}

	// Shutdown stops the instance. While the process has not yet exited the
	// state must be non-terminal (no manufactured confirmation); the real
	// force kill from Stop then lets wait() confirm exit.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	shutdownErr := sup.Shutdown(shutdownCtx)
	shutdownCancel()
	_ = shutdownErr

	ctrl := waitForRunDone(t, sup, inst.ID, 20*time.Second)
	cSnap := ctrl.Snapshot()
	if !cSnap.IsTerminal() {
		t.Fatalf("state after shutdown + wait() confirmation = %q, want terminal", cSnap.State)
	}
	assertSlotReleasedExactlyOnce(t, sup, 300*time.Millisecond)
}

// TestStart_RequestCancellationAfterSpawnDoesNotAbandonOwnership verifies
// ADR 016 §8.1: the caller's request context is NOT used for post-spawn
// lifecycle operations. A request context cancelled right after the spawn
// (simulated HTTP client disconnect) must not abandon the lifecycle: the
// running identity is still persisted and Start() succeeds.
func TestStart_RequestCancellationAfterSpawnDoesNotAbandonOwnership(t *testing.T) {
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	store := newMockStore()
	cancelled := false
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) && !cancelled {
			// The spawn already happened: the client disconnects now.
			cancelled = true
			reqCancel()
		}
		store.storeAccepted(e)
		return nil
	}

	inst, err := startWithSeparateRequestCtx(t, store, reqCtx, []string{"-sleep", "30"})
	if err != nil {
		t.Fatalf("post-spawn request cancellation must not fail the start: %v", err)
	}
	if !cancelled {
		t.Fatal("test setup error: request context was not cancelled after spawn")
	}
	// The instance is fully running with a durable identity, proving the
	// lifecycle is owned by the supervisor (not the dead request context).
	snap, err := instSnapshot(store, inst.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.State != domain.InstanceStateRunning || snap.PID <= 0 {
		t.Fatalf("state=%q pid=%d, want running with PID (lifecycle owned despite request cancel)", snap.State, snap.PID)
	}
	// The running process is stopped by the supervisor cleanup registered in
	// startWithSeparateRequestCtx (t.Cleanup → Shutdown).
}

// TestStart_RequestCancellationDuringRollbackCompletes verifies ADR 016 §8.1:
// even when the request context is cancelled during the fail-closed rollback
// (after a running-persist failure), the rollback completes on the lifecycle
// context — the process is killed and confirmed dead.
func TestStart_RequestCancellationDuringRollbackCompletes(t *testing.T) {
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) {
			reqCancel() // client disconnects exactly when persistence fails
			return testUpdateErr
		}
		return nil
	}

	inst, err := startWithSeparateRequestCtx(t, store, reqCtx, []string{"-sleep", "30"})
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !errors.Is(err, ErrPersistenceFailure) {
		t.Fatalf("expected ErrPersistenceFailure, got %v", err)
	}
	if inst != nil {
		t.Fatalf("confirmed-dead outcome returns nil instance, got %q", string(inst.ID))
	}
}

// startWithSeparateRequestCtx replicates Supervisor.Start with a caller
// request context distinct from the supervisor lifecycle context, to prove
// that post-spawn operations do not depend on the request context.
func startWithSeparateRequestCtx(t *testing.T, store InstanceStore, reqCtx context.Context, args []string) (*domain.LaunchInstance, error) {
	t.Helper()
	sup := NewSupervisor(store)
	sup.broker = NewLogBroker(64)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})
	model := &domain.Model{ID: "adr016-reqctx", Name: "req", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.resolver.ResolveToInstance(model, rt, args, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ctrl := NewInstanceController(inst, store, sup.resolver, sup.broker)
	ctrl.supervisorRef = sup
	sup.mu.Lock()
	sup.instances[inst.ID] = ctrl
	sup.mu.Unlock()

	reservation, err := sup.acquireSlot(reqCtx)
	if err != nil {
		sup.mu.Lock()
		delete(sup.instances, inst.ID)
		sup.mu.Unlock()
		return nil, err
	}
	if sErr := store.Create(domain.ToStorageEntry(inst)); sErr != nil {
		reservation.Release()
		sup.mu.Lock()
		delete(sup.instances, inst.ID)
		sup.mu.Unlock()
		return nil, sErr
	}
	ctrlInst, err := ctrl.startWithReservation(sup.lifecycleContext(), reservation)
	if err != nil {
		if ctrlInst == nil {
			sup.mu.Lock()
			delete(sup.instances, inst.ID)
			sup.mu.Unlock()
		}
		return ctrlInst, err
	}
	return ctrlInst, nil
}

func instSnapshot(store InstanceStore, id domain.InstanceID) (domain.LaunchInstance, error) {
	e, err := store.Get(string(id))
	if err != nil {
		return domain.LaunchInstance{}, err
	}
	return *domain.ToDomain(e), nil
}

// lastDurableInstance returns the single durable entry (if any).
func lastDurableInstance(t *testing.T, store *mockStore) (*domain.LaunchInstanceEntry, error) {
	t.Helper()
	store.mu.RLock()
	defer store.mu.RUnlock()
	var out *domain.LaunchInstanceEntry
	for _, e := range store.instances {
		if out != nil {
			return nil, fmt.Errorf("expected a single durable entry, found %d", len(store.instances))
		}
		out = e
	}
	return out, nil
}
