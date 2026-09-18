package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// startInFlight starts an instance in a background goroutine (the call
// blocks on slot acquisition) and returns the instance ID once it reaches
// the pending state. The goroutine is owned by the fixture: a t.Cleanup is
// registered that cancels the pending start and joins the goroutine before
// the supervisor Shutdown runs.
func startInFlight(t *testing.T, sup *Supervisor, ctx context.Context, model *domain.Model, rt *domain.Runtime, args []string) domain.InstanceID {
	t.Helper()
	type result struct {
		id  domain.InstanceID
		err error
	}
	ch := make(chan result, 1)

	startCtx, cancelStart := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		inst, err := sup.start(startCtx, model, rt, args, nil)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{id: inst.ID}
	}()

	t.Cleanup(func() {
		cancelStart()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("timeout waiting for pending start goroutine to finish")
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		insts, _ := sup.List()
		for _, inst := range insts {
			if inst.ModelID == model.ID && inst.State == domain.InstanceStatePending {
				return inst.ID
			}
		}
	}
	t.Fatal("timeout waiting for pending instance")
	return ""
}

// TestSupervisor_PendingWindow_StopRefused verifies that Stop during the
// pending window (slot acquisition not yet complete) returns
// ErrLaunchInFlight and does NOT transition the instance to a terminal state.
func TestSupervisor_PendingWindow_StopRefused(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)

	fakePath := fakeruntime.Path(t)
	modelA := &domain.Model{ID: "mA", Name: "A", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "mB", Name: "B", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}

	ctx := context.Background()

	instA, err := sup.start(ctx, modelA, rt, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := waitForState(sup, instA.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}

	instBID := startInFlight(t, sup, ctx, modelB, rt, []string{"-sleep", "60"})

	snapB, err := sup.Status(instBID)
	if err != nil {
		t.Fatalf("status B: %v", err)
	}
	if snapB.State != domain.InstanceStatePending {
		t.Fatalf("B state = %q, want pending", snapB.State)
	}

	stopErr := sup.Stop(ctx, instBID)
	if !errors.Is(stopErr, ErrLaunchInFlight) {
		t.Fatalf("stop B: expected ErrLaunchInFlight, got %v", stopErr)
	}

	snapB2, err := sup.Status(instBID)
	if err != nil {
		t.Fatalf("status B after stop: %v", err)
	}
	if snapB2.State != domain.InstanceStatePending {
		t.Fatalf("B state after refused stop = %q, want pending", snapB2.State)
	}
	if snapB2.IsTerminal() {
		t.Fatal("B must not be terminal after refused stop")
	}
}

// TestSupervisor_PendingWindow_RestartRefused verifies that Restart during
// the pending window returns ErrLaunchInFlight and does NOT create a second
// lifecycle.
func TestSupervisor_PendingWindow_RestartRefused(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)

	fakePath := fakeruntime.Path(t)
	modelA := &domain.Model{ID: "mA", Name: "A", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "mB", Name: "B", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}

	ctx := context.Background()

	instA, err := sup.start(ctx, modelA, rt, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := waitForState(sup, instA.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}

	instBID := startInFlight(t, sup, ctx, modelB, rt, []string{"-sleep", "60"})

	snapB, err := sup.Status(instBID)
	if err != nil {
		t.Fatalf("status B: %v", err)
	}
	if snapB.State != domain.InstanceStatePending {
		t.Fatalf("B state = %q, want pending", snapB.State)
	}

	_, restartErr := sup.Restart(ctx, instBID)
	if !errors.Is(restartErr, ErrLaunchInFlight) {
		t.Fatalf("restart B: expected ErrLaunchInFlight, got %v", restartErr)
	}

	insts, _ := sup.List()
	var mBCount int
	for _, inst := range insts {
		if inst.ModelID == "mB" {
			mBCount++
			if inst.State != domain.InstanceStatePending {
				t.Fatalf("B state = %q, want pending", inst.State)
			}
		}
	}
	if mBCount != 1 {
		t.Fatalf("expected 1 instance of mB, got %d", mBCount)
	}
}

// TestSupervisor_PendingWindow_RecoveryAfterSlotRelease verifies that after
// the blocking instance is stopped, the pending instance transitions to
// running (recovery semantics intact).
func TestSupervisor_PendingWindow_RecoveryAfterSlotRelease(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)

	fakePath := fakeruntime.Path(t)
	modelA := &domain.Model{ID: "mA", Name: "A", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "mB", Name: "B", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}

	ctx := context.Background()

	instA, err := sup.start(ctx, modelA, rt, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := waitForState(sup, instA.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}

	instBID := startInFlight(t, sup, ctx, modelB, rt, []string{"-sleep", "60"})

	snapB, _ := sup.Status(instBID)
	if snapB.State != domain.InstanceStatePending {
		t.Fatalf("B state = %q, want pending", snapB.State)
	}

	if err := sup.Stop(ctx, instA.ID); err != nil {
		t.Fatalf("stop A: %v", err)
	}

	if err := waitForState(sup, instBID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait B running after A stopped: %v", err)
	}
}

// TestSupervisor_PendingWindow_PendingNotTerminal verifies that a pending
// instance is not classified as terminal by any predicate.
func TestSupervisor_PendingWindow_PendingNotTerminal(t *testing.T) {
	inst := &domain.LaunchInstance{
		ID:    "test-pending",
		State: domain.InstanceStatePending,
	}
	if inst.IsTerminal() {
		t.Error("pending must not be terminal")
	}
	if inst.IsLive() {
		t.Error("pending must not be live")
	}
	if !inst.IsInFlight() {
		t.Error("pending must be in-flight")
	}
	if inst.IsRunningOrStarting() {
		t.Error("pending must not be running-or-starting")
	}
}
