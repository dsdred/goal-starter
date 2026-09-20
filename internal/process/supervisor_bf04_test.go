package process

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// BF-04 (ADR 017 corrective slice D0): the post-admission failure cleanup in
// startPostAdmit terminalizes the controller-owned *domain.LaunchInstance, which
// is the same pointer that Snapshot/List/ListActive/Status/ActiveInstances read
// under ic.mu. These tests pin the synchronization contract, the persist-before
// -forget ordering, and the rule that a successfully spawned process is never
// forgotten by that cleanup.

// bf04ProbeTimeout bounds the read probe performed from inside a persistence
// hook. A controller lock held across the persist surfaces as a timeout here,
// never as a hung suite.
const bf04ProbeTimeout = 5 * time.Second

// bf04HalfApplied reports a snapshot that exposes a half-applied Fail() (the
// terminal state visible while the failure detail is still missing), which is
// only possible when the mutation is not synchronized with the reader.
func bf04HalfApplied(snap *domain.LaunchInstance) bool {
	return snap != nil && snap.State == domain.InstanceStateFailed &&
		(snap.LastError == "" || snap.StoppedAt.IsZero())
}

// bf04Readers hammers every registry-visible read path until the returned stop
// func is called, so the readers overlap the whole failing launch.
func bf04Readers(t *testing.T, sup *Supervisor) func() {
	t.Helper()
	done := make(chan struct{})
	var wg sync.WaitGroup
	var torn atomic.Int32
	check := func(snap *domain.LaunchInstance) {
		if bf04HalfApplied(snap) {
			torn.Add(1)
		}
	}
	forward := func() bool {
		select {
		case <-done:
			return false
		default:
			return true
		}
	}
	workers := []func(){
		func() {
			for forward() {
				insts, err := sup.List()
				if err != nil {
					return
				}
				for _, inst := range insts {
					check(inst)
				}
			}
		},
		func() {
			for forward() {
				insts, err := sup.ListActive()
				if err != nil {
					return
				}
				for _, inst := range insts {
					check(inst)
				}
			}
		},
		func() {
			for forward() {
				sup.ActiveInstances()
			}
		},
		func() {
			for forward() {
				sup.mu.RLock()
				var ctrl *InstanceController
				for _, c := range sup.instances {
					ctrl = c
					break
				}
				sup.mu.RUnlock()
				if ctrl != nil {
					snap := ctrl.Snapshot()
					check(&snap)
					_ = ctrl.IsRunning()
				}
			}
		},
	}
	for _, w := range workers {
		wg.Add(1)
		go func(w func()) {
			defer wg.Done()
			w()
		}(w)
	}
	return func() {
		close(done)
		wg.Wait()
		if n := torn.Load(); n > 0 {
			t.Errorf("concurrent readers observed %d half-applied terminal snapshot(s) — unsynchronized shared-instance mutation", n)
		}
	}
}

// bf04SingleEntry returns the only durable record stored for modelID.
func bf04SingleEntry(t *testing.T, store *mockStore, modelID string) *domain.LaunchInstanceEntry {
	t.Helper()
	entries, err := store.ListByModelID(modelID)
	if err != nil {
		t.Fatalf("list by model: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("durable records for %s = %d, want 1", modelID, len(entries))
	}
	return entries[0]
}

func bf04RegistryCount(sup *Supervisor) int {
	sup.mu.RLock()
	defer sup.mu.RUnlock()
	return len(sup.instances)
}

// TestBF04_SpawnFailure_TerminalizeIsSynchronized verifies the BF-04 fix on the
// manager.Start failure branch: the controller-owned instance is terminalized
// under ic.mu while concurrent readers exercise every registry read path, the
// durable record ends terminal, and the controller is forgotten. Under -race
// this is the deterministic regression guard for the previously unlocked
// inst.Fail(...) / ToStorageEntry(inst) pair.
func TestBF04_SpawnFailure_TerminalizeIsSynchronized(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{LogBufferSize: 64})
	model := &domain.Model{ID: "bf04-spawn-fail", Name: "bf04", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: "/nonexistent/bf04-runtime"}

	stopReaders := bf04Readers(t, sup)
	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	stopReaders()

	if err == nil {
		t.Fatal("expected spawn failure, got nil")
	}
	e := bf04SingleEntry(t, store, model.ID)
	if e.State != string(domain.InstanceStateFailed) {
		t.Fatalf("durable state = %q, want failed", e.State)
	}
	if e.LastError == "" || e.StoppedAt.IsZero() {
		t.Fatalf("terminal record incomplete: last_error=%q stopped_at=%v", e.LastError, e.StoppedAt)
	}
	if e.ExitClass != string(domain.InstanceExitError) {
		t.Fatalf("exit class = %q, want %q", e.ExitClass, domain.InstanceExitError)
	}
	if e.PID != 0 {
		t.Fatalf("durable PID = %d, want 0 (the process was never spawned)", e.PID)
	}
	if n := bf04RegistryCount(sup); n != 0 {
		t.Fatalf("registry holds %d controller(s), want 0", n)
	}
}

// TestBF04_PersistStartingFailure_TerminalizeIsSynchronized verifies the other
// branch that reaches the cleanup: persist(starting) fails before any spawn, so
// the cleanup is the ONLY terminalizer of the durable pending record. The
// intended durable outcome must survive the synchronization correction.
func TestBF04_PersistStartingFailure_TerminalizeIsSynchronized(t *testing.T) {
	store := rejectingStore(string(domain.InstanceStateStarting))
	sup := newTestSupervisor(t, store, SupervisorConfig{LogBufferSize: 64})
	model := &domain.Model{ID: "bf04-persist-starting", Name: "bf04", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	stopReaders := bf04Readers(t, sup)
	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
	stopReaders()

	if err == nil {
		t.Fatal("expected persist(starting) failure, got nil")
	}
	if !strings.Contains(err.Error(), "persist starting state") {
		t.Fatalf("error = %v, want it to report the persist(starting) failure", err)
	}
	e := bf04SingleEntry(t, store, model.ID)
	if e.State != string(domain.InstanceStateFailed) {
		t.Fatalf("durable state = %q, want failed (the pending record must be terminalized)", e.State)
	}
	if !strings.Contains(e.LastError, "persist starting state") {
		t.Fatalf("durable last_error = %q, want the spawn failure reason", e.LastError)
	}
	if e.PID != 0 {
		t.Fatalf("durable PID = %d, want 0 (never spawned)", e.PID)
	}
	if n := bf04RegistryCount(sup); n != 0 {
		t.Fatalf("registry holds %d controller(s), want 0", n)
	}
}

// TestBF04_CleanupPersistRunsOutsideControllerLock pins the ordering half of the
// fix that abortPreSpawnCleanup does not provide: the storage snapshot is built
// under ic.mu, but the repository write itself must happen after the unlock and
// before the registry forget.
func TestBF04_CleanupPersistRunsOutsideControllerLock(t *testing.T) {
	store := newMockStore()
	var failedUpdates atomic.Int32
	var probeErr atomic.Value
	sup := newTestSupervisor(t, store, SupervisorConfig{LogBufferSize: 64})

	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateFailed) && failedUpdates.Add(1) == 2 {
			// Update #1 is startCore's persist under ic.mu; update #2 is the
			// startPostAdmit cleanup, which must NOT hold ic.mu and must still
			// find the controller registered.
			if err := bf04ProbeReadable(sup, domain.InstanceID(e.ID)); err != nil {
				probeErr.Store(err)
			}
		}
		store.storeAccepted(e)
		return nil
	}

	model := &domain.Model{ID: "bf04-lock-order", Name: "bf04", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: "/nonexistent/bf04-runtime"}
	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected spawn failure, got nil")
	}
	if got := failedUpdates.Load(); got != 2 {
		t.Fatalf("failed persists = %d, want 2 (startCore persist + idempotent cleanup persist)", got)
	}
	if v := probeErr.Load(); v != nil {
		t.Errorf("cleanup persist ordering violated: %v", v)
	}
}

// bf04ProbeReadable reads the instance back through the supervisor while a
// persistence hook is on the stack. Blocking here means the persist was issued
// with ic.mu (or s.mu) still held.
func bf04ProbeReadable(sup *Supervisor, id domain.InstanceID) error {
	type result struct {
		snap *domain.LaunchInstance
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		snap, err := sup.Status(id)
		ch <- result{snap: snap, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if r.snap.State != domain.InstanceStateFailed || r.snap.LastError == "" {
			return errors.New("cleanup persisted a non-terminal snapshot")
		}
		return nil
	case <-time.After(bf04ProbeTimeout):
		return errors.New("repository write issued while the controller/registry lock was held")
	}
}

// TestBF04_SuccessfulSpawnIsNotForgotten guards the helper's precondition: the
// cleanup must never run for a launch that produced a supervised process.
func TestBF04_SuccessfulSpawnIsNotForgotten(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{LogBufferSize: 64})
	model := &domain.Model{ID: "bf04-spawned", Name: "bf04", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if inst.State != domain.InstanceStateRunning || inst.PID <= 0 {
		t.Fatalf("launched instance incomplete: state=%q pid=%d", inst.State, inst.PID)
	}
	sup.mu.RLock()
	ctrl := sup.instances[inst.ID]
	sup.mu.RUnlock()
	if ctrl == nil {
		t.Fatal("successfully spawned controller was removed from the registry")
	}
	if snap := ctrl.Snapshot(); snap.State != domain.InstanceStateRunning {
		t.Fatalf("in-memory state = %q, want running", snap.State)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sup.Stop(ctx, inst.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := waitForProcess(ctx, ctrl, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if snap := ctrl.Snapshot(); !snap.IsTerminal() {
		t.Fatalf("state after stop = %q, want terminal", snap.State)
	}
	e := bf04SingleEntry(t, store, model.ID)
	if e.PID <= 0 {
		t.Fatalf("durable record lost its PID: %+v", e)
	}
}
