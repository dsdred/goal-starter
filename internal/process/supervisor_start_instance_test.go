package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// ADR 024 D-12: Supervisor.StartInstance — the spawn path of the instance.start
// command. Every test below asserts not only the response/error class but the
// ABSENCE of unwanted lifecycle side effects: no PID change on a refusal, no
// extra spawn, no second instance record, no mutation of the live generation.

// startManualRunning starts one manual fake-runtime instance and waits for it
// to reach running; it returns the running snapshot.
func startManualRunning(t *testing.T, sup *Supervisor, model *domain.Model, rt *domain.Runtime, args []string) *domain.LaunchInstance {
	t.Helper()
	ctx := context.Background()
	inst, err := sup.start(ctx, model, rt, args, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := waitForState(sup, inst.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running: %v", err)
	}
	snap, err := sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return snap
}

// stopAndWaitTerminal stops the instance and waits for a terminal state.
func stopAndWaitTerminal(t *testing.T, sup *Supervisor, id domain.InstanceID) domain.InstanceState {
	t.Helper()
	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sup.Stop(stopCtx, id); err != nil {
		t.Fatalf("stop: %v", err)
	}
	sup.mu.RLock()
	ctrl := sup.instances[id]
	sup.mu.RUnlock()
	if err := waitForProcess(context.Background(), ctrl, 15*time.Second); err != nil {
		t.Fatalf("wait process: %v", err)
	}
	snap, err := sup.Status(id)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !snap.IsTerminal() {
		t.Fatalf("state = %q, want terminal", snap.State)
	}
	return snap.State
}

// TestStartInstance_Exited_RestartsSameID pins the D-12 success leg: a terminal
// (exited) instance starts on its SAME InstanceID, with a NEW PID, the preserved
// record identity and a real new spawn — and no second instance record.
func TestStartInstance_Exited_RestartsSameID(t *testing.T) {
	tmp := t.TempDir()
	argvPath := filepath.Join(tmp, "argv.txt")

	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"argv-file", argvPath, "--mark"}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	running := startManualRunning(t, sup, model, rt, nil)
	firstPID := running.PID
	created := running.CreatedAt
	if err := waitForFileContentContaining(argvPath, "--mark", 5*time.Second); err != nil {
		t.Fatalf("argv not captured: %v", err)
	}
	firstMtime := fileMtime(t, argvPath)

	// To terminal.
	if stopAndWaitTerminal(t, sup, running.ID) != domain.InstanceStateExited {
		t.Fatal("want exited terminal state")
	}

	inst, err := sup.StartInstance(context.Background(), running.ID, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("StartInstance on exited instance: %v", err)
	}
	if inst.ID != running.ID {
		t.Fatalf("InstanceID changed: %s -> %s", running.ID, inst.ID)
	}
	if inst.PID == firstPID {
		t.Fatalf("PID did not change: %d", firstPID)
	}
	if !inst.CreatedAt.Equal(created) {
		t.Fatalf("record identity (CreatedAt) changed: %v -> %v", created, inst.CreatedAt)
	}
	if err := waitForState(sup, inst.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running after start: %v", err)
	}

	// A real new spawn: the argv file was rewritten by the new generation.
	waitForFileRewrite(t, argvPath, firstMtime, 10*time.Second)

	// No second instance record: exactly one controller for the model.
	sup.mu.RLock()
	n := 0
	for _, c := range sup.instances {
		if c.Snapshot().ModelID == "m1" {
			n++
		}
	}
	sup.mu.RUnlock()
	if n != 1 {
		t.Fatalf("controllers for m1 = %d, want 1 (no second instance record)", n)
	}
}

// TestStartInstance_Failed_RestartsSameID pins the D-12 success leg for the
// other allowed terminal state: a failed (non-zero exit) instance starts on its
// SAME InstanceID.
func TestStartInstance_Failed_RestartsSameID(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"exit-code", "1"}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ctx := context.Background()
	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	firstPID := inst.PID
	if err := waitForState(sup, inst.ID, domain.InstanceStateFailed, 10*time.Second); err != nil {
		t.Fatalf("wait failed state: %v", err)
	}

	started, err := sup.StartInstance(ctx, inst.ID, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("StartInstance on failed instance: %v", err)
	}
	if started.ID != inst.ID {
		t.Fatalf("InstanceID changed: %s -> %s", inst.ID, started.ID)
	}
	if started.PID == firstPID {
		t.Fatalf("PID did not change: %d", firstPID)
	}
	if err := waitForState(sup, started.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running after start: %v", err)
	}
}

// TestStartInstance_Running_Rejected_NoMutation pins the atomic running →
// ErrAlreadyRunning contract with the side-effect absence asserted: the refused
// start neither changes the PID, changes the state, nor spawns a new generation
// (the argv file is not rewritten).
func TestStartInstance_Running_Rejected_NoMutation(t *testing.T) {
	tmp := t.TempDir()
	argvPath := filepath.Join(tmp, "argv.txt")

	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"argv-file", argvPath, "--mark"}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	running := startManualRunning(t, sup, model, rt, nil)
	if err := waitForFileContentContaining(argvPath, "--mark", 5*time.Second); err != nil {
		t.Fatalf("argv not captured: %v", err)
	}
	firstPID := running.PID
	firstMtime := fileMtime(t, argvPath)

	_, err := sup.StartInstance(context.Background(), running.ID, model, rt, nil, nil)
	if err == nil {
		t.Fatal("StartInstance on a running instance: expected ErrAlreadyRunning, got nil")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("error = %v, want ErrAlreadyRunning", err)
	}

	// No unwanted side effect: same PID, still running, no new spawn.
	snap, err := sup.Status(running.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.PID != firstPID {
		t.Fatalf("PID changed by a refused start: %d -> %d", firstPID, snap.PID)
	}
	if snap.State != domain.InstanceStateRunning {
		t.Fatalf("state = %q, want running", snap.State)
	}
	time.Sleep(200 * time.Millisecond)
	if st, err := os.Stat(argvPath); err == nil && !st.ModTime().Equal(firstMtime) {
		t.Fatal("a new generation was spawned by a refused start")
	}
}

// TestStartInstance_Stopping_Rejected pins the stopping → ErrAlreadyRunning
// half of the contract: while a live stop is finalizing the generation, a start
// is refused and does not touch the live process.
func TestStartInstance_Stopping_Rejected(t *testing.T) {
	markerDir := t.TempDir()

	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"signal-delay", markerDir, "3000"}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	running := startManualRunning(t, sup, model, rt, nil)
	firstPID := running.PID

	// ready.json is written only after the child installed its signal handler,
	// so a stop requested after this point is a deliverable graceful stop.
	ready := waitForDwellMarker(t, filepath.Join(markerDir, "ready.json"), 20*time.Second)
	if ready.Event != "ready" {
		t.Fatalf("ready marker event = %q, want ready", ready.Event)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	go func() {
		_ = sup.Stop(stopCtx, running.ID)
	}()
	if err := waitForState(sup, running.ID, domain.InstanceStateStopping, 10*time.Second); err != nil {
		t.Fatalf("wait stopping: %v", err)
	}

	_, err := sup.StartInstance(context.Background(), running.ID, model, rt, nil, nil)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("error = %v, want ErrAlreadyRunning (stopping)", err)
	}

	// The stop proceeds to terminal unmutated by the refused start.
	snap, err := sup.Status(running.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.PID != firstPID {
		t.Fatalf("PID changed during a live stop: %d -> %d", firstPID, snap.PID)
	}
	if err := waitForState(sup, running.ID, domain.InstanceStateExited, 15*time.Second); err != nil {
		t.Fatalf("wait exited after stop: %v", err)
	}
}

// TestStartInstance_Terminal_Concurrent_ExactlyOneAdmitted pins the double
// concurrent Start: exactly one of two racing starts is admitted; the loser is
// refused with a bounded token, and the final state is a single running
// generation on the same InstanceID.
func TestStartInstance_Terminal_Concurrent_ExactlyOneAdmitted(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"argv-file", filepath.Join(t.TempDir(), "argv.txt")}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ctx := context.Background()
	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	stopAndWaitTerminal(t, sup, inst.ID)

	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = sup.StartInstance(ctx, inst.ID, model, rt, nil, nil)
		}(i)
	}
	close(start)
	wg.Wait()

	var admitted, refused int
	for _, r := range results {
		if r == nil {
			admitted++
			continue
		}
		if errors.Is(r, ErrLaunchInFlight) || errors.Is(r, ErrAlreadyRunning) {
			refused++
			continue
		}
		t.Fatalf("unbounded concurrent-start refusal: %v", r)
	}
	if admitted != 1 || refused != 1 {
		t.Fatalf("admitted=%d refused=%d, want exactly one of each: %v", admitted, refused, results)
	}

	snap, err := sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := waitForState(sup, inst.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running: %v", err)
	}
	if !snap.State.IsRunningOrStarting() {
		t.Fatalf("final state = %q, want running", snap.State)
	}
}

// TestStartInstance_VsRestart_SameBoundary pins the Start-vs-Restart
// serialization through the single arbitration boundary: a start of a terminal
// instance followed by a restart of the now-running instance both pass through
// the same boundary, preserve the InstanceID and leave exactly one running
// generation — start never breaks the restart contract.
func TestStartInstance_VsRestart_SameBoundary(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"argv-file", filepath.Join(t.TempDir(), "argv.txt")}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ctx := context.Background()
	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	stopAndWaitTerminal(t, sup, inst.ID)

	started, err := sup.StartInstance(ctx, inst.ID, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if started.ID != inst.ID {
		t.Fatalf("InstanceID changed: %s -> %s", inst.ID, started.ID)
	}
	startPID := started.PID
	if err := waitForState(sup, inst.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running after start: %v", err)
	}

	// A restart of the now-running instance remains fully legitimate.
	restarted, err := sup.Restart(ctx, inst.ID)
	if err != nil {
		t.Fatalf("Restart after StartInstance: %v", err)
	}
	if restarted.ID != inst.ID {
		t.Fatalf("InstanceID changed on restart: %s -> %s", inst.ID, restarted.ID)
	}
	if restarted.PID == startPID {
		t.Fatalf("restart produced no new generation: %d", startPID)
	}
	if err := waitForState(sup, inst.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running after restart: %v", err)
	}

	sup.mu.RLock()
	n := 0
	for _, c := range sup.instances {
		if c.Snapshot().ModelID == "m1" {
			n++
		}
	}
	sup.mu.RUnlock()
	if n != 1 {
		t.Fatalf("controllers for m1 = %d, want 1", n)
	}
}

// TestStartInstance_StartThenStop_NoResidue pins the Start-vs-Stop leg: a
// started terminal instance stops cleanly to terminal with the same
// InstanceID, no second spawn and no in-flight residue.
func TestStartInstance_StartThenStop_NoResidue(t *testing.T) {
	tmp := t.TempDir()
	argvPath := filepath.Join(tmp, "argv.txt")

	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", Args: []string{"argv-file", argvPath, "--mark"}}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ctx := context.Background()
	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	stopAndWaitTerminal(t, sup, inst.ID)

	started, err := sup.StartInstance(ctx, inst.ID, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if err := waitForFileContentContaining(argvPath, "--mark", 5*time.Second); err != nil {
		t.Fatalf("argv not captured after start: %v", err)
	}
	startMtime := fileMtime(t, argvPath)
	if err := waitForState(sup, inst.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait running: %v", err)
	}

	if stopAndWaitTerminal(t, sup, inst.ID) != domain.InstanceStateExited {
		t.Fatal("want exited terminal state after stop")
	}
	time.Sleep(200 * time.Millisecond)
	if st, err := os.Stat(argvPath); err == nil && !st.ModTime().Equal(startMtime) {
		t.Fatal("a generation was spawned after the stop")
	}
	if started.ID != inst.ID {
		t.Fatalf("InstanceID changed: %s -> %s", inst.ID, started.ID)
	}
}

// TestStartInstance_DuplicateModelIsolation pins the repeatable-model leg: a
// model may have two terminal instances (as two pipeline components do);
// instance.start names ONE InstanceID. Starting one never touches the other
// instance (no PID/state change), and a start of the OTHER instance while the
// first is running is refused by the existing conflict scan (RejInFlight), not
// silently admitted.
func TestStartInstance_DuplicateModelIsolation(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	args := func() []string { return []string{"argv-file", filepath.Join(t.TempDir(), "argv.txt")} }

	ctx := context.Background()
	a, err := sup.start(ctx, model, rt, args(), nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	stopAndWaitTerminal(t, sup, a.ID)
	aPID := a.PID

	b, err := sup.start(ctx, model, rt, args(), nil)
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	stopAndWaitTerminal(t, sup, b.ID)

	// Start exactly B; A must be untouched.
	startedB, err := sup.StartInstance(ctx, b.ID, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("StartInstance(B): %v", err)
	}
	if err := waitForState(sup, b.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait B running: %v", err)
	}

	aSnap, err := sup.Status(a.ID)
	if err != nil {
		t.Fatalf("status A: %v", err)
	}
	if aSnap.PID != aPID {
		t.Fatalf("sibling A PID changed by a start of B: %d -> %d", aPID, aSnap.PID)
	}
	if aSnap.State != domain.InstanceStateExited {
		t.Fatalf("sibling A state = %q, want untouched exited", aSnap.State)
	}

	// A start of A while B (same model, running) is live is refused by the
	// existing conflict scan — the same ADR 013 matrix a new launch uses.
	if _, err := sup.StartInstance(ctx, a.ID, model, rt, nil, nil); err == nil {
		t.Fatal("StartInstance(A) while B running: expected a conflict refusal, got nil")
	} else {
		var rej *AdmissionRejection
		if !errors.As(err, &rej) || rej.Reason != RejInFlight {
			t.Fatalf("refusal = %v, want RejInFlight", err)
		}
	}

	bSnap, err := sup.Status(b.ID)
	if err != nil {
		t.Fatalf("status B: %v", err)
	}
	if bSnap.PID != startedB.PID {
		t.Fatalf("B PID changed by the refused start of A: %d -> %d", startedB.PID, bSnap.PID)
	}
}

// TestStartInstance_UnknownID_ReturnsInstanceNotFound pins the bounded 404
// class on the process surface: an unknown (or cleaned) InstanceID is
// ErrInstanceNotFound — the same class the per-object routes return. This is
// also the ordinary-operation answer for a stale ID (no registered controller).
func TestStartInstance_UnknownID_ReturnsInstanceNotFound(t *testing.T) {
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	_, err := sup.StartInstance(context.Background(), domain.InstanceID("no-such-instance"), model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected ErrInstanceNotFound, got nil")
	}
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("error = %v, want ErrInstanceNotFound", err)
	}
}

// TestStartInstance_OrphanFence_Rejected pins the orphan leg: a repository
// orphan of the same model is authoritative — a start of a terminal instance of
// that model is refused with RejOrphan, the same fence a new launch uses.
func TestStartInstance_OrphanFence_Rejected(t *testing.T) {
	store := newMockStore()
	if err := store.Create(&domain.LaunchInstanceEntry{
		ID:      "orphan-1",
		ModelID: "m1",
		State:   string(domain.InstanceStateOrphan),
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ctx := context.Background()
	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	stopAndWaitTerminal(t, sup, inst.ID)

	_, err = sup.StartInstance(ctx, inst.ID, model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected an orphan refusal, got nil")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) || rej.Reason != RejOrphan {
		t.Fatalf("refusal = %v, want RejOrphan", err)
	}
	if rej.ConflictID != "orphan-1" {
		t.Fatalf("ConflictID = %q, want orphan-1", rej.ConflictID)
	}
}

// TestStartInstance_Shutdown_Rejected pins the shutdown leg: after the
// supervisor lifecycle begins, a start is refused with RejShuttingDown before
// any reservation, and the terminal instance is untouched.
func TestStartInstance_Shutdown_Rejected(t *testing.T) {
	store := newMockStore()
	ctx, cancel := context.WithCancel(context.Background())
	sup := NewSupervisorWithContext(ctx, store)
	sup.maxConcurrent = 2
	sup.semaphore = newSemaphore(2)
	sup.broker = NewLogBroker(64)
	sup.arbLocks = make(map[string]*sync.Mutex)
	t.Cleanup(func() {
		cancel()
		sup.mu.RLock()
		controllers := make([]*InstanceController, 0, len(sup.instances))
		for _, c := range sup.instances {
			controllers = append(controllers, c)
		}
		sup.mu.RUnlock()
		for _, c := range controllers {
			if done := c.GetControllerDone(); done != nil {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
			}
		}
	})

	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	firstPID := inst.PID
	stopAndWaitTerminal(t, sup, inst.ID)

	cancel()

	_, err = sup.StartInstance(context.Background(), inst.ID, model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected a shutdown refusal, got nil")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) || rej.Reason != RejShuttingDown {
		t.Fatalf("refusal = %v, want RejShuttingDown", err)
	}

	snap, err := sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.PID != firstPID || snap.State.IsInFlight() {
		t.Fatalf("refused start mutated the terminal instance: pid=%d state=%q", snap.PID, snap.State)
	}
}

// TestStartInstance_PendingVisibleThroughGate pins the pending/starting leg at
// the process surface: a target whose launch is in flight is refused with
// ErrLaunchInFlight — the gate sees the in-flight state, not a stale terminal
// view.
func TestStartInstance_PendingVisibleThroughGate(t *testing.T) {
	// MaxConcurrent 1: the first holder of the slot keeps the second launch in
	// pending, so the pending gate branch of checkStartTarget is reachable.
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64})
	modelA := &domain.Model{ID: "mA", Name: "mA", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "mB", Name: "mB", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ctx := context.Background()
	// mA sleeps and holds the only slot.
	instA, err := sup.start(ctx, modelA, rt, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start holder: %v", err)
	}
	if err := waitForState(sup, instA.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait holder running: %v", err)
	}

	// mB's launch is in flight (pending: slot acquisition). startInFlight owns
	// the pending start goroutine and cancels it in cleanup.
	instBID := startInFlight(t, sup, ctx, modelB, rt, []string{"-sleep", "60"})

	_, err = sup.StartInstance(ctx, instBID, modelB, rt, nil, nil)
	if err == nil {
		t.Fatal("expected ErrLaunchInFlight, got nil")
	}
	if !errors.Is(err, ErrLaunchInFlight) {
		t.Fatalf("error = %v, want ErrLaunchInFlight", err)
	}

	// Side-effect absence: mB is still pending, no generation spawned for it.
	snap, err := sup.Status(instBID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.State != domain.InstanceStatePending {
		t.Fatalf("mB state = %q, want still pending", snap.State)
	}
	if snap.PID > 0 {
		t.Fatalf("mB PID = %d after a refused start, want none", snap.PID)
	}
}
