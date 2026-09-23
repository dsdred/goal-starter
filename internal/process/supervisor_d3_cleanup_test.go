package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// === ADR 017 corrective slice D3: BF-07c cleanup ↔ registry coherence =========
//
// These tests pin the registry side of an explicit instance cleanup:
//   - a controller whose history record is gone and whose ownership is provably
//     finished is removed (identity-checked);
//   - a controller with ANY remaining lifecycle / process / restart ownership is
//     kept, even when its record is gone;
//   - a terminal controller whose record still exists is NEVER touched (no
//     automatic eviction — D3 Owner decision);
//   - the primitive never writes to the repository.

// d3ControllerWithState builds a controller in one fixed state with a fully
// finalized run, i.e. the shape a terminal historical instance has after wait()
// released its slot and completed its run.
func d3ControllerWithState(id domain.InstanceID, state domain.InstanceState, runClosed bool) *InstanceController {
	inst := &domain.LaunchInstance{
		ID:        id,
		ModelID:   "model-" + string(id),
		RuntimeID: "rt",
		State:     state,
		CreatedAt: time.Now(),
	}
	ctrl := NewInstanceController(inst, nil, nil, nil)
	run := newInstanceRunState(nil)
	if runClosed {
		run.complete()
	}
	ctrl.run = run
	return ctrl
}

// d3Supervisor builds a Supervisor for the synthetic-registry tests: none of
// the controllers registered there owns a real process, so the shutdown
// cleanup that newTestSupervisor performs is not applicable.
func d3Supervisor(store InstanceStore, maxConcurrent int) *Supervisor {
	return NewSupervisorWithConfig(store, SupervisorConfig{MaxConcurrent: maxConcurrent})
}

func d3Register(sup *Supervisor, ctrl *InstanceController) {
	sup.mu.Lock()
	sup.instances[ctrl.instanceID] = ctrl
	sup.mu.Unlock()
}

func d3Registered(sup *Supervisor, id domain.InstanceID) (*InstanceController, bool) {
	sup.mu.RLock()
	defer sup.mu.RUnlock()
	ctrl, ok := sup.instances[id]
	return ctrl, ok
}

// d3SeedRecord puts a durable record for id in the mock store, so a controller
// with the same ID is NOT a cleanup candidate.
func d3SeedRecord(t *testing.T, store *mockStore, id domain.InstanceID, state domain.InstanceState) {
	t.Helper()
	if err := store.Create(&domain.LaunchInstanceEntry{ID: string(id), ModelID: "model-" + string(id), RuntimeID: "rt", State: string(state)}); err != nil {
		t.Fatalf("seed record %s: %v", id, err)
	}
}

// T-D3-1 (primitive half): the record is gone, the terminal controller holds no
// ownership → the exact controller is removed and cannot be resurrected.
func TestD3Cleanup_RecordlessTerminalControllerIsRemoved(t *testing.T) {
	store := newMockStore()
	sup := d3Supervisor(store, 2)

	terminal := d3ControllerWithState("d3-gone", domain.InstanceStateExited, true)
	d3Register(sup, terminal)
	// A record for an unrelated instance: cleanup of one instance must not
	// evict another instance's controller.
	d3SeedRecord(t, store, "d3-kept", domain.InstanceStateFailed)
	other := d3ControllerWithState("d3-kept", domain.InstanceStateFailed, true)
	d3Register(sup, other)

	if err := store.Delete("d3-gone"); err != nil {
		t.Fatalf("store.Delete: %v", err)
	}

	if err := sup.ForgetCleanedControllers(); err != nil {
		t.Fatalf("ForgetCleanedControllers: %v", err)
	}

	if _, ok := d3Registered(sup, "d3-gone"); ok {
		t.Error("cleaned controller is still Supervisor-addressable")
	}
	if _, err := sup.Status("d3-gone"); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("Status after cleanup = %v, want ErrInstanceNotFound", err)
	}
	if ctrl, ok := d3Registered(sup, "d3-kept"); !ok || ctrl != other {
		t.Error("a controller whose record still exists must not be touched")
	}
	// Registry-only: nothing was written back to the repository.
	if entries, err := store.List(); err != nil {
		t.Fatalf("store.List: %v", err)
	} else if len(entries) != 1 || entries[0].ID != "d3-kept" {
		t.Errorf("durable records after cleanup = %v, want only d3-kept", entries)
	}
}

// T-D3-3: every in-flight state is refused by the fence, even when its record is
// gone (pending = admitted launch whose Create has not happened yet).
func TestD3Cleanup_InFlightStatesSurvive(t *testing.T) {
	for _, state := range []domain.InstanceState{
		domain.InstanceStatePending,
		domain.InstanceStateStarting,
		domain.InstanceStateRunning,
		domain.InstanceStateStopping,
		domain.InstanceStateUnknown,
		domain.InstanceStateOrphan,
	} {
		t.Run(string(state), func(t *testing.T) {
			sup := d3Supervisor(newMockStore(), 2)

			id := domain.InstanceID("d3-" + string(state))
			ctrl := d3ControllerWithState(id, state, false)
			d3Register(sup, ctrl)

			if err := sup.ForgetCleanedControllers(); err != nil {
				t.Fatalf("ForgetCleanedControllers: %v", err)
			}
			if got, ok := d3Registered(sup, id); !ok || got != ctrl {
				t.Fatalf("controller in state %q was removed", state)
			}
			if snap := ctrl.Snapshot(); snap.State != state {
				t.Errorf("cleanup changed lifecycle state: %q -> %q", state, snap.State)
			}
		})
	}
}

// T-D3-4: ADR 016 residual ownership (Outcome C: kill accepted, exit
// unconfirmed; Outcome D: kill refused) is never removable by cleanup, even when
// the durable record is deleted out from under it. The slot stays held, so
// ShutdownWithPersistence keeps its retry authority.
func TestD3Cleanup_ADR016ResidualSurvives(t *testing.T) {
	tests := []struct {
		name string
		// stopFirst distinguishes the two residuals: Outcome C's process exits
		// on its own, Outcome D's refused kill leaves a live process that an
		// operator stop must still be able to take down.
		stopFirst bool
		killErr   error
		args      []string
	}{
		{name: "outcomeC", killErr: nil, args: []string{"-sleep", "2"}},
		{name: "outcomeD", stopFirst: true, killErr: errors.New("simulated OS refusal"), args: []string{"-sleep", "60"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withRollbackConfirmWindow(t, 300*time.Millisecond)
			store := newMockStore()
			cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
			sup := newTestSupervisor(t, store, cfg)
			store.updateFn = setKillOverrideOnFirstRunningPersist(t, sup, store, func() error { return tc.killErr })
			model := &domain.Model{ID: "d3-residual-" + tc.name, Name: tc.name, RuntimeID: "rt"}
			rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

			inst, err := sup.start(context.Background(), model, rt, tc.args, nil)
			if err == nil {
				t.Fatal("expected the fail-closed residual error, got nil")
			}
			if !errors.Is(err, ErrPersistenceFailure) {
				t.Fatalf("err = %v, want ErrPersistenceFailure residual ownership", err)
			}
			if inst == nil {
				t.Fatal("residual outcome must keep the instance")
			}
			// Worst case for the fence: the history record is gone while
			// ownership is still held.
			if err := store.Delete(string(inst.ID)); err != nil {
				t.Fatalf("store.Delete: %v", err)
			}

			if err := sup.ForgetCleanedControllers(); err != nil {
				t.Fatalf("ForgetCleanedControllers: %v", err)
			}

			snap, err := sup.Status(inst.ID)
			if err != nil {
				t.Fatalf("status after cleanup: %v (residual controller was lost)", err)
			}
			if snap.State != domain.InstanceStateStarting {
				t.Fatalf("state after cleanup = %q, want starting (residual ownership intact)", snap.State)
			}
			if got := len(sup.semaphore); got != 0 {
				t.Fatalf("free slots after cleanup = %d, want 0 (cleanup must not release run/slot ownership)", got)
			}
			// Ownership is still held by wait(): the instance stays stoppable
			// (ShutdownWithPersistence retry authority), and the exit
			// confirmation releases the slot exactly once.
			if tc.stopFirst {
				stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := sup.Stop(stopCtx, inst.ID); err != nil {
					t.Fatalf("stop residual instance: %v", err)
				}
			}
			waitForRunDone(t, sup, inst.ID, 15*time.Second)
			if got := len(sup.semaphore); got != 1 {
				t.Fatalf("free slots after run completion = %d, want 1", got)
			}
		})
	}
}

// T-D3-5: an active D1 restart reservation / launch operation blocks removal —
// both when it is already published and when it is published after the registry
// scan (the restart-vs-cleanup interleaving named in the D3 concurrency
// contract).
func TestD3Cleanup_ActiveOperationAndReservationSurvive(t *testing.T) {
	t.Run("published_operation", func(t *testing.T) {
		sup := d3Supervisor(newMockStore(), 2)
		ctrl := d3ControllerWithState("d3-active", domain.InstanceStateExited, true)
		ctrl.active = &launchOperation{id: 7, owner: domain.ManualOwner}
		d3Register(sup, ctrl)

		if err := sup.ForgetCleanedControllers(); err != nil {
			t.Fatalf("ForgetCleanedControllers: %v", err)
		}
		if got, ok := d3Registered(sup, "d3-active"); !ok || got != ctrl {
			t.Fatal("a controller with an active launch/restart operation was removed")
		}
	})

	t.Run("pre_spawn_token", func(t *testing.T) {
		sup := d3Supervisor(newMockStore(), 2)
		ctrl := d3ControllerWithState("d3-token", domain.InstanceStateExited, true)
		d3Register(sup, ctrl)
		sup.launchMu.Lock()
		sup.preSpawnInFlight++
		ctrl.preSpawnToken = true
		sup.launchMu.Unlock()

		if err := sup.ForgetCleanedControllers(); err != nil {
			t.Fatalf("ForgetCleanedControllers: %v", err)
		}
		if _, ok := d3Registered(sup, "d3-token"); !ok {
			t.Fatal("a controller with an unconsumed pre-spawn launch authority was removed")
		}
	})

	t.Run("reservation_published_after_the_scan", func(t *testing.T) {
		sup := d3Supervisor(newMockStore(), 2)
		ctrl := d3ControllerWithState("d3-race", domain.InstanceStateExited, true)
		d3Register(sup, ctrl)
		// Deterministic stand-in for a restart that wins arbitration after the
		// scan: the hook runs outside every lock, and publishes the reservation
		// the way publishClaimLocked does.
		sup.cleanupScanHook = func() {
			ctrl.mu.Lock()
			ctrl.active = &launchOperation{id: 11, owner: domain.ManualOwner}
			ctrl.mu.Unlock()
		}

		if err := sup.ForgetCleanedControllers(); err != nil {
			t.Fatalf("ForgetCleanedControllers: %v", err)
		}
		if got, ok := d3Registered(sup, "d3-race"); !ok || got != ctrl {
			t.Fatal("cleanup removed a controller that acquired a restart reservation mid-cleanup")
		}
	})
}

// T-D3-6: the removal is identity-checked, not ID-keyed. When the registry holds
// a different controller at a validated ID at the linearization point, the
// replacement survives and the stale candidate cannot delete it.
func TestD3Cleanup_ReplacementControllerSurvivesIdentityRace(t *testing.T) {
	sup := d3Supervisor(newMockStore(), 2)

	stale := d3ControllerWithState("d3-id", domain.InstanceStateExited, true)
	replacement := d3ControllerWithState("d3-id", domain.InstanceStateExited, true)
	d3Register(sup, stale)

	sup.cleanupScanHook = func() { d3Register(sup, replacement) }

	if err := sup.ForgetCleanedControllers(); err != nil {
		t.Fatalf("ForgetCleanedControllers: %v", err)
	}
	got, ok := d3Registered(sup, "d3-id")
	if !ok {
		t.Fatal("the replacement controller was deleted on a stale candidate's behalf")
	}
	if got != replacement {
		t.Fatalf("registry holds %p, want the replacement %p", got, replacement)
	}
}

// T-D3-10: terminal-controller retention is the Owner decision, not a defect.
// (a) A terminal controller whose record exists is never a cleanup candidate.
// (b) Ordinary terminalization without an explicit cleanup keeps the controller
// addressable — the process-scoped historical restart contract stays intact.
func TestD3Cleanup_NoAutomaticEviction(t *testing.T) {
	t.Run("terminal_with_record", func(t *testing.T) {
		store := newMockStore()
		sup := d3Supervisor(store, 2)
		d3SeedRecord(t, store, "d3-retained", domain.InstanceStateExited)
		ctrl := d3ControllerWithState("d3-retained", domain.InstanceStateExited, true)
		d3Register(sup, ctrl)

		if err := sup.ForgetCleanedControllers(); err != nil {
			t.Fatalf("ForgetCleanedControllers: %v", err)
		}
		if got, ok := d3Registered(sup, "d3-retained"); !ok || got != ctrl {
			t.Fatal("a terminal controller with a history record was evicted")
		}
	})

	t.Run("terminalization_without_cleanup", func(t *testing.T) {
		store := newMockStore()
		cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
		sup := newTestSupervisor(t, store, cfg)
		model := &domain.Model{ID: "d3-wait", Name: "wait", RuntimeID: "rt"}
		rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

		inst, err := sup.start(context.Background(), model, rt, []string{"exit-code", "0"}, nil)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		ctrl := waitForRunDone(t, sup, inst.ID, 15*time.Second)
		snap := ctrl.Snapshot()
		if !snap.IsTerminal() {
			t.Fatalf("state = %q, want terminal", snap.State)
		}

		// No cleanup ran, so nothing may have been evicted — not even by the
		// terminal persistence path inside wait().
		if list, err := sup.List(); err != nil {
			t.Fatalf("List: %v", err)
		} else if len(list) != 1 || list[0].ID != inst.ID {
			t.Fatalf("terminal instance disappeared from the registry without cleanup: %v", list)
		}
		if got, ok := d3Registered(sup, inst.ID); !ok || got != ctrl {
			t.Fatal("wait() removed the terminal controller from the registry")
		}
	})
}

// The primitive is inert without a durable view: a Supervisor with no store has
// nothing to prove a record is gone, so it removes nothing.
func TestD3Cleanup_NilStoreRemovesNothing(t *testing.T) {
	sup := d3Supervisor(nil, 2)
	ctrl := d3ControllerWithState("d3-nil", domain.InstanceStateExited, true)
	d3Register(sup, ctrl)

	if err := sup.ForgetCleanedControllers(); err != nil {
		t.Fatalf("ForgetCleanedControllers: %v", err)
	}
	if _, ok := d3Registered(sup, "d3-nil"); !ok {
		t.Fatal("a store-less Supervisor evicted a controller")
	}
}

// A repository read failure must surface (the caller reports an unreconciled
// cleanup) and must not delete anything.
func TestD3Cleanup_StoreReadFailureKeepsRegistry(t *testing.T) {
	store := &d3FailingListStore{mockStore: newMockStore()}
	sup := d3Supervisor(store, 2)
	ctrl := d3ControllerWithState("d3-readerr", domain.InstanceStateExited, true)
	d3Register(sup, ctrl)

	if err := sup.ForgetCleanedControllers(); err == nil {
		t.Fatal("expected the store read error to be reported")
	}
	if got, ok := d3Registered(sup, "d3-readerr"); !ok || got != ctrl {
		t.Fatal("controllers were removed despite the failed record read")
	}
}

type d3FailingListStore struct {
	*mockStore
}

func (s *d3FailingListStore) List() ([]*domain.LaunchInstanceEntry, error) {
	return nil, errors.New("simulated record read failure")
}

// BF-07e: an unknown InstanceID at the restart preflight is the documented
// bounded not-found class, not an unclassified server failure.
func TestD3Preflight_UnknownInstanceIsSentinelNotFound(t *testing.T) {
	sup := d3Supervisor(newMockStore(), 2)

	err := sup.PreflightRestart([]domain.InstanceID{"d3-missing"})
	if err == nil {
		t.Fatal("expected a bounded error for an unknown instance")
	}
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("err = %v, want ErrInstanceNotFound", err)
	}
	if errors.Is(err, ErrLaunchInFlight) || errors.Is(err, ErrNotRestartable) {
		t.Fatalf("not-found must not be classified as a conflict: %v", err)
	}
	// The rendering stays identical to every other controller lookup.
	if got, want := err.Error(), "instance not found: d3-missing"; got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
}
