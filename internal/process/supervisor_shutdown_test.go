package process

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// === RB-015b deterministic shutdown / pre-spawn race regression tests ===
//
// These tests exercise the A (admission registration) / D (drain-start) / C
// (spawn commit) linearization on launchMu. No test relies on scheduler luck
// for its correctness assertion: ordering is forced through the concurrency
// slot, the lifecycle context, or synchronous call sequencing. A spawn
// counter (managerStartHook seam) makes the "no manager.Start" guarantee
// observable.

func newShutdownTestSupervisor(t *testing.T, store InstanceStore, maxConcurrent int, lctx context.Context) *Supervisor {
	t.Helper()
	sup := NewSupervisorWithContext(lctx, store)
	sup.maxConcurrent = maxConcurrent
	if maxConcurrent > 0 {
		sup.semaphore = newSemaphore(maxConcurrent)
	}
	sup.broker = NewLogBroker(64)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sup.mu.RLock()
		controllers := make([]*InstanceController, 0, len(sup.instances))
		for _, c := range sup.instances {
			controllers = append(controllers, c)
		}
		sup.mu.RUnlock()
		for _, c := range controllers {
			if c.IsRunning() {
				c.Stop(ctx)
			}
			if done := c.GetControllerDone(); done != nil {
				select {
				case <-done:
				case <-ctx.Done():
				}
			}
		}
	})
	return sup
}

// withSpawnCounter installs a start override that counts spawn attempts and
// lets the real spawn proceed. It is wired via the managerStartHook seam
// (nil in production) and must be set before AdmitAndStart.
func withSpawnCounter(t *testing.T, sup *Supervisor) *int32 {
	t.Helper()
	counter := new(int32)
	sup.managerStartHook = func(m *Manager) {
		m.SetStartOverride(func(spec CommandSpec) error {
			atomic.AddInt32(counter, 1)
			return nil
		})
	}
	return counter
}

// withSpawnFail installs a start override that counts and FAILS the spawn
// (manager.Start returns an error after commit C).
func withSpawnFail(t *testing.T, sup *Supervisor) *int32 {
	t.Helper()
	counter := new(int32)
	sup.managerStartHook = func(m *Manager) {
		m.SetStartOverride(func(spec CommandSpec) error {
			atomic.AddInt32(counter, 1)
			return errors.New("spawn failed (test)")
		})
	}
	return counter
}

func preSpawnCount(sup *Supervisor) int {
	sup.launchMu.Lock()
	defer sup.launchMu.Unlock()
	return sup.preSpawnInFlight
}

func isDraining(sup *Supervisor) bool {
	sup.launchMu.Lock()
	defer sup.launchMu.Unlock()
	return sup.draining
}

func waitForPreSpawn(t *testing.T, sup *Supervisor, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if preSpawnCount(sup) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("preSpawnInFlight = %d, want %d (timed out)", preSpawnCount(sup), want)
}

func waitForDraining(t *testing.T, sup *Supervisor, want bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isDraining(sup) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("draining = %v, want %v (timed out)", isDraining(sup), want)
}

func shutdownWithTimeout(t *testing.T, sup *Supervisor, d time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return sup.Shutdown(ctx)
}

// releaseTestSlot returns a cleanup func that returns one concurrency token to
// the semaphore only if a slot is actually free (non-blocking), so it cannot
// deadlock on a double release when the test body already returned the slot.
func releaseTestSlot(sup *Supervisor) func() {
	return func() {
		select {
		case sup.semaphore <- struct{}{}:
		default:
		}
	}
}

func t1ModelRT(t *testing.T) (*domain.Model, *domain.Runtime) {
	t.Helper()
	model := &domain.Model{ID: "m", Name: "m", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	return model, rt
}

// T1: an already-admitted launch blocked on the concurrency slot is aborted
// when shutdown begins (lifecycle cancellation); manager.Start never runs and
// Shutdown succeeds.
func TestRB015b_T1_SlotBlockedAbortsOnShutdown(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 1, lctx)
	counter := withSpawnCounter(t, sup)
	t.Cleanup(releaseTestSlot(sup))
	<-sup.semaphore // occupy the only slot; the launch will block in acquireSlot

	model, rt := t1ModelRT(t)
	var launchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, launchErr = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	}()

	waitForPreSpawn(t, sup, 1, 5*time.Second) // A completed, launch pre-spawn
	lcancel()                                 // shutdown begins; slot waiter wakes

	if serr := shutdownWithTimeout(t, sup, 15*time.Second); serr != nil {
		t.Fatalf("Shutdown must succeed after the pre-spawn abort drains: %v", serr)
	}
	<-done

	if !errors.Is(launchErr, ErrLaunchAbortedByShutdown) {
		t.Fatalf("launch error = %v, want ErrLaunchAbortedByShutdown", launchErr)
	}
	if n := atomic.LoadInt32(counter); n != 0 {
		t.Fatalf("manager.Start attempts = %d, want 0", n)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", c)
	}
}

// T2: after a SUCCESSFUL Shutdown return, no already-admitted launch can reach
// manager.Start, and no new admission is reopened.
func TestRB015b_T2_SuccessfulShutdownGuarantee(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 1, lctx)
	counter := withSpawnCounter(t, sup)
	t.Cleanup(releaseTestSlot(sup))
	<-sup.semaphore

	model, rt := t1ModelRT(t)
	var launchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, launchErr = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	}()

	waitForPreSpawn(t, sup, 1, 5*time.Second)
	lcancel()

	if serr := shutdownWithTimeout(t, sup, 15*time.Second); serr != nil {
		t.Fatalf("Shutdown must succeed: %v", serr)
	}
	<-done
	if !errors.Is(launchErr, ErrLaunchAbortedByShutdown) {
		t.Fatalf("launch error = %v, want ErrLaunchAbortedByShutdown", launchErr)
	}

	// Post-success: the drain latch stays armed, so no new launch is admitted.
	if _, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil); err == nil {
		t.Fatal("admission after successful shutdown must be rejected")
	} else {
		var rej *AdmissionRejection
		if !errors.As(err, &rej) || rej.Reason != RejShuttingDown {
			t.Fatalf("post-shutdown admission = %v, want RejShuttingDown", err)
		}
	}

	sup.mu.RLock()
	live := 0
	for _, c := range sup.instances {
		if c.IsRunning() {
			live++
		}
	}
	sup.mu.RUnlock()
	if live != 0 {
		t.Fatalf("live instances after successful shutdown = %d, want 0", live)
	}
	if n := atomic.LoadInt32(counter); n != 0 {
		t.Fatalf("manager.Start attempts = %d, want 0", n)
	}
}

// T3: drain wins admission (D < A). A launch that passes the early lifecycle
// fence but is admitted after D is rejected, with no insertion, no token, and
// no spawn.
func TestRB015b_T3_DrainWinsAdmission(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background()) // NOT cancelled during the test: early fence passes
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 2, lctx)
	counter := withSpawnCounter(t, sup)

	sup.beginDrain() // D wins before A; draining=true, lifecycle still active

	model, rt := t1ModelRT(t)
	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	var rej *AdmissionRejection
	if !errors.As(err, &rej) || rej.Reason != RejShuttingDown {
		t.Fatalf("admission = %v, want RejShuttingDown", err)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0 (no token)", c)
	}
	sup.mu.RLock()
	instances := len(sup.instances)
	sup.mu.RUnlock()
	if instances != 0 {
		t.Fatalf("instances = %d, want 0 (no insertion)", instances)
	}
	if n := atomic.LoadInt32(counter); n != 0 {
		t.Fatalf("manager.Start attempts = %d, want 0", n)
	}
}

// T4: registration wins before drain (A < D). The drain accounts for the
// already-admitted launch; no untracked PRE-SPAWN work remains.
func TestRB015b_T4_RegistrationWinsBeforeDrain(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 1, lctx)
	counter := withSpawnCounter(t, sup)
	t.Cleanup(releaseTestSlot(sup))
	<-sup.semaphore

	model, rt := t1ModelRT(t)
	var launchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, launchErr = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	}()

	waitForPreSpawn(t, sup, 1, 5*time.Second) // A completed before D
	sup.beginDrain()                          // D: drain accounts for the launch (count==1)
	lcancel()                                 // slot waiter wakes

	if serr := shutdownWithTimeout(t, sup, 15*time.Second); serr != nil {
		t.Fatalf("Shutdown must succeed: %v", serr)
	}
	<-done
	if !errors.Is(launchErr, ErrLaunchAbortedByShutdown) {
		t.Fatalf("launch error = %v, want ErrLaunchAbortedByShutdown", launchErr)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", c)
	}
	if n := atomic.LoadInt32(counter); n != 0 {
		t.Fatalf("manager.Start attempts = %d, want 0", n)
	}
}

// T5: D wins C. A launch admitted and then allowed to reach the commit
// boundary aborts PRE-SPAWN because draining was set first; manager.Start
// never runs and the controller is cleaned up.
func TestRB015b_T5_DrainWinsCommit(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background()) // not cancelled: launch waits on the slot, not lc
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 1, lctx)
	counter := withSpawnCounter(t, sup)
	t.Cleanup(releaseTestSlot(sup))
	<-sup.semaphore

	model, rt := t1ModelRT(t)
	var launchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, launchErr = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	}()

	waitForPreSpawn(t, sup, 1, 5*time.Second) // A completed, launch blocked on slot

	shutdownDone := make(chan error)
	go func() {
		shutdownDone <- shutdownWithTimeout(t, sup, 15*time.Second)
	}()
	waitForDraining(t, sup, true, 5*time.Second) // D established

	sup.semaphore <- struct{}{} // release the slot: launch proceeds to C, sees draining

	serr := <-shutdownDone
	<-done

	if serr != nil {
		t.Fatalf("Shutdown must succeed after the commit abort drains: %v", serr)
	}
	if !errors.Is(launchErr, ErrLaunchAbortedByShutdown) {
		t.Fatalf("launch error = %v, want ErrLaunchAbortedByShutdown", launchErr)
	}
	if n := atomic.LoadInt32(counter); n != 0 {
		t.Fatalf("manager.Start attempts = %d, want 0", n)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", c)
	}
	sup.mu.RLock()
	instances := len(sup.instances)
	sup.mu.RUnlock()
	if instances != 0 {
		t.Fatalf("instances after commit abort = %d, want 0 (controller removed)", instances)
	}
}

// T6: C wins D. A launch commits to starting (token consumed COMMITTED, real
// spawn) before Shutdown; the drain treats it as lifecycle-visible and the
// existing Stop/ADR 016 path owns it.
func TestRB015b_T6_CommitWinsDrain(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 2, lctx)
	counter := withSpawnCounter(t, sup)

	model := &domain.Model{ID: "m", Name: "m", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("commit-first launch must succeed: %v", err)
	}
	if inst.State != domain.InstanceStateRunning {
		t.Fatalf("state = %q, want running (C committed)", inst.State)
	}
	if n := atomic.LoadInt32(counter); n != 1 {
		t.Fatalf("manager.Start attempts = %d, want 1", n)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0 (token committed)", c)
	}

	// Shutdown now: committed launch is lifecycle-visible, not pre-spawn.
	if serr := shutdownWithTimeout(t, sup, 20*time.Second); serr != nil {
		t.Fatalf("Shutdown must succeed: %v", serr)
	}
	ctrl := waitForRunDone(t, sup, inst.ID, 20*time.Second)
	if snap := ctrl.Snapshot(); !snap.IsTerminal() {
		t.Fatalf("state after Shutdown = %q, want terminal (Stopped)", snap.State)
	}
}

// T7: starting-persistence failure AFTER C. No manager.Start, no second token
// decrement, no stuck controller/slot.
func TestRB015b_T7_StartingPersistFailAfterCommit(t *testing.T) {
	store := newMockStore()
	// Fail the FIRST update (the "starting" persist); let the terminal persist
	// succeed so the cleanup persists cleanly.
	seen := false
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if !seen {
			seen = true
			return testUpdateErr
		}
		store.storeAccepted(e)
		return nil
	}
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 2, lctx)
	counter := withSpawnCounter(t, sup)

	model, rt := t1ModelRT(t)
	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected a start error from the failing starting-persist")
	}
	if n := atomic.LoadInt32(counter); n != 0 {
		t.Fatalf("manager.Start attempts = %d, want 0 (persist-starting failed before spawn)", n)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0 (no double decrement)", c)
	}
	sup.mu.RLock()
	instances := len(sup.instances)
	sup.mu.RUnlock()
	if instances != 0 {
		t.Fatalf("instances = %d, want 0 (controller removed)", instances)
	}
	if got := len(sup.semaphore); got != sup.maxConcurrent {
		t.Fatalf("free slots = %d, want %d (slot released)", got, sup.maxConcurrent)
	}
}

// T8: manager.Start failure AFTER C. The token was already committed at C and
// must NOT be decremented again; existing startCore/ADR 016 cleanup applies.
func TestRB015b_T8_SpawnFailAfterCommit(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 2, lctx)
	counter := withSpawnFail(t, sup)

	model, rt := t1ModelRT(t)
	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected a start error from the failing spawn")
	}
	if n := atomic.LoadInt32(counter); n != 1 {
		t.Fatalf("manager.Start attempts = %d, want 1 (spawn was attempted after C)", n)
	}
	if c := preSpawnCount(sup); c != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0 (no post-C decrement)", c)
	}
	sup.mu.RLock()
	instances := len(sup.instances)
	sup.mu.RUnlock()
	if instances != 0 {
		t.Fatalf("instances = %d, want 0 (controller removed)", instances)
	}
}

// T9: Shutdown begins with preSpawnInFlight == 0. The drain signals immediately
// and does not hang.
func TestRB015b_T9_DrainZeroStartsImmediate(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 2, lctx)

	if preSpawnCount(sup) != 0 {
		t.Fatalf("precondition: preSpawnInFlight = %d, want 0", preSpawnCount(sup))
	}
	// No instances at all: drain must close immediately and return.
	if serr := shutdownWithTimeout(t, sup, 5*time.Second); serr != nil {
		t.Fatalf("Shutdown with no pre-spawn work must succeed immediately: %v", serr)
	}
}

// T10: Shutdown drain context timeout returns an error, not a false success.
func TestRB015b_T10_DrainTimeoutReturnsError(t *testing.T) {
	store := newMockStore()
	// A cancelled lifecycle context is required to abort the pre-spawn launch;
	// we do NOT cancel it, so the admitted launch stays pre-spawn and the
	// bounded shutdown context times out.
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 1, lctx)
	<-sup.semaphore

	model, rt := t1ModelRT(t)
	var launchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, launchErr = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	}()
	waitForPreSpawn(t, sup, 1, 5*time.Second)

	// Very short shutdown context: the drain cannot complete (no lc cancel,
	// slot never released) so it must time out with an error.
	err := shutdownWithTimeout(t, sup, 200*time.Millisecond)
	if err == nil {
		t.Fatal("Shutdown must return an error on drain timeout, not false success")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain timeout error = %v, want context.DeadlineExceeded", err)
	}

	// Unblock the launch (release the slot); it aborts at commit C because the
	// drain latch is now armed, then the supervisor settles for cleanup.
	sup.semaphore <- struct{}{}
	<-done
	if !errors.Is(launchErr, ErrLaunchAbortedByShutdown) {
		t.Fatalf("launch error = %v, want ErrLaunchAbortedByShutdown", launchErr)
	}
}

// T11: repeated defensive Shutdown does not recreate the drain generation,
// does not panic on double close, and does not reopen admission.
func TestRB015b_T11_RepeatedShutdown(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 2, lctx)

	if err := shutdownWithTimeout(t, sup, 5*time.Second); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := shutdownWithTimeout(t, sup, 5*time.Second); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if !isDraining(sup) {
		t.Fatal("draining latch must remain armed after Shutdown")
	}
	model, rt := t1ModelRT(t)
	if _, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil); err == nil {
		t.Fatal("admission must remain rejected after Shutdown")
	}
}

// T12: lock-order regression. The admission (A: launchMu->s.mu), the
// s.mu->ic.mu read path (ListActive), the commit (C: launchMu->ic.mu), and the
// drain (D) run concurrently and must complete without deadlocking. Reintroducing
// the forbidden ic.mu->launchMu ordering would form the wait cycle
// launchMu -> s.mu -> ic.mu -> launchMu and hang this bounded test.
func TestRB015b_T12_LockOrderNoDeadlock(t *testing.T) {
	store := newMockStore()
	lctx, lcancel := context.WithCancel(context.Background())
	defer lcancel()
	sup := newShutdownTestSupervisor(t, store, 4, lctx)
	counter := withSpawnCounter(t, sup)

	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	var wg sync.WaitGroup
	// Concurrent admissions (A) — distinct model IDs so they do not reject each
	// other on the in-flight check, exercising launchMu -> s.mu repeatedly.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			m := &domain.Model{ID: id, Name: id, RuntimeID: "rt"}
			sup.AdmitAndStart(context.Background(), m, rt, domain.ManualOwner, []string{"-sleep", "1"}, nil)
		}(string(rune('a' + i)))
	}
	// Concurrent s.mu -> ic.mu read path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			sup.ListActive()
			sup.List()
		}
	}()
	// Concurrent drain (D) + shutdown (drain wait, then stop lifecycle-visible).
	wg.Add(1)
	go func() {
		defer wg.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sup.Shutdown(sctx)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("T12 deadlocked: concurrent A/ListActive/C/D did not complete (lock cycle reintroduced?)")
	}
	_ = counter
}
