package process

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// === ADR 017 corrective slice D1: BF-01 unified restart arbitration ==========
//
// BF-01 was a HIGH review-board finding: production restart reached
// restartWithRefresh -> startCore -> manager.Start without ever passing the
// model-level ADR 017 arbitration boundary, so a restart of a historical
// terminal instance could publish a second live generation of a model whose
// ownership relationship is incompatible with the generation that is already
// live.
//
// These tests pin the D1 contract:
//   - restart goes through the SAME sealed arbitration boundary as a new
//     launch and receives the SAME kind of generation-bound spawn claim;
//   - the controller-local restart reservation keeps the operation
//     conflict-visible across old-generation stop -> terminal interval -> spawn;
//   - no path can manufacture, omit, replay or reuse a spawn authorization;
//   - ADR 016 ownership and RB-015b drain semantics are unchanged.

// bf01Timeout bounds every wait performed by the D1 fixtures.
const bf01Timeout = 15 * time.Second

// bf01Harness is the shared D1 fixture: a supervisor whose spawns are counted,
// the fake runtime, and the persistence/registry readers the assertions need.
type bf01Harness struct {
	t      *testing.T
	store  *mockStore
	sup    *Supervisor
	rt     *domain.Runtime
	spawns *int32
}

func bf01New(t *testing.T, maxConcurrent int) *bf01Harness {
	t.Helper()
	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: maxConcurrent, LogBufferSize: 64})
	// The spawn counter must be installed before any controller exists: the hook
	// is applied per controller at creation time.
	spawns := withSpawnCounter(t, sup)
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	return &bf01Harness{t: t, store: store, sup: sup, rt: rt, spawns: spawns}
}

// model builds a model bound to the harness runtime. Pipeline attribution lives
// on the model because domain.LaunchResolver copies it onto the instance, which
// is what a restart's owner derivation reads from the TARGET.
func (h *bf01Harness) model(id, pipelineID, entryID string) *domain.Model {
	h.t.Helper()
	return &domain.Model{ID: id, Name: id, RuntimeID: h.rt.ID, PipelineID: pipelineID, PipelineEntryID: entryID}
}

func (h *bf01Harness) ownerOf(pipelineID, entryID string) domain.LaunchOwner {
	if pipelineID == "" {
		return domain.ManualOwner
	}
	return domain.PipelineOwner(pipelineID, entryID)
}

func (h *bf01Harness) spawnCount() int {
	h.t.Helper()
	return int(atomic.LoadInt32(h.spawns))
}

// launch performs a real AdmitAndStart and requires ADR 016 S1/S2 success
// (durable running identity) before returning.
func (h *bf01Harness) launch(m *domain.Model, owner domain.LaunchOwner, args ...string) *domain.LaunchInstance {
	h.t.Helper()
	inst, err := h.sup.AdmitAndStart(context.Background(), m, h.rt, owner, args, nil)
	if err != nil {
		h.t.Fatalf("AdmitAndStart(model=%s entry=%q): %v", m.ID, m.PipelineEntryID, err)
	}
	if inst.State != domain.InstanceStateRunning {
		h.t.Fatalf("launched instance state = %q, want running", inst.State)
	}
	return inst
}

// launchTerminal launches an instance that exits immediately and stays in the
// registry as a HISTORICAL terminal controller — the exact precondition the
// BF-01 reproduction needs.
func (h *bf01Harness) launchTerminal(m *domain.Model, owner domain.LaunchOwner) (*domain.LaunchInstance, *InstanceController) {
	h.t.Helper()
	inst := h.launch(m, owner, "exit-code", "0")
	ctrl := h.controller(inst.ID)
	if err := waitForProcess(context.Background(), ctrl, bf01Timeout); err != nil {
		h.t.Fatalf("wait for historical terminal generation: %v", err)
	}
	snap := ctrl.Snapshot()
	if !snap.IsTerminal() {
		h.t.Fatalf("historical instance state = %q, want terminal", snap.State)
	}
	return &snap, ctrl
}

func (h *bf01Harness) controller(id domain.InstanceID) *InstanceController {
	h.t.Helper()
	ctrl, err := h.sup.controllerFor(id)
	if err != nil {
		h.t.Fatalf("controllerFor(%s): %v", string(id), err)
	}
	return ctrl
}

// stopCtrl stops a generation and waits until its run is fully finalized
// (terminal state persisted, slot released).
func (h *bf01Harness) stopCtrl(ctrl *InstanceController) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), bf01Timeout)
	defer cancel()
	if err := ctrl.Stop(ctx); err != nil {
		h.t.Fatalf("stop %s: %v", string(ctrl.instanceID), err)
	}
	if err := waitForProcess(ctx, ctrl, bf01Timeout); err != nil {
		h.t.Fatalf("wait for stop of %s: %v", string(ctrl.instanceID), err)
	}
	if snap := ctrl.Snapshot(); !snap.IsTerminal() {
		h.t.Fatalf("state after stop = %q, want terminal", snap.State)
	}
}

func (h *bf01Harness) durable(id domain.InstanceID) *domain.LaunchInstanceEntry {
	h.t.Helper()
	e, err := h.store.Get(string(id))
	if err != nil {
		h.t.Fatalf("durable record %s: %v", string(id), err)
	}
	return e
}

func (h *bf01Harness) liveIDs() []domain.InstanceID {
	h.t.Helper()
	active, err := h.sup.ListActive()
	if err != nil {
		h.t.Fatalf("ListActive: %v", err)
	}
	ids := make([]domain.InstanceID, 0, len(active))
	for _, inst := range active {
		ids = append(ids, inst.ID)
	}
	return ids
}

// fabricate registers a controller in an exact in-memory state. Some of these
// states (stale, orphan, legacy-unattributed) are not reachable in memory at
// the moment a restart is requested — production records them in the repository
// only — so those cases are deliberate defense-in-depth coverage of the restart
// state contract (brief section N). The repository-side refusals are proven
// through the orphan fence instead (TestRestart_OrphanFence).
func (h *bf01Harness) fabricate(id domain.InstanceID, m *domain.Model, state domain.InstanceState) *InstanceController {
	h.t.Helper()
	inst := &domain.LaunchInstance{
		ID:              id,
		ModelID:         m.ID,
		ModelName:       m.Name,
		RuntimeID:       m.RuntimeID,
		PipelineID:      m.PipelineID,
		PipelineEntryID: m.PipelineEntryID,
		State:           state,
		Executable:      h.rt.Executable,
		Args:            []string{"-sleep", "60"},
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	ctrl := h.sup.newController(inst)
	h.sup.mu.Lock()
	h.sup.instances[id] = ctrl
	h.sup.mu.Unlock()
	return ctrl
}

// bf01BindClaim publishes a possibly forged claim as the controller's active
// operation. TEST ONLY: it reproduces exactly the state a valid arbitration
// publication produces, so each individual consumption check can be exercised
// without weakening the boundary itself.
func bf01BindClaim(ctrl *InstanceController, claim *spawnClaim, opID uint64, owner domain.LaunchOwner) {
	op := &launchOperation{id: opID, owner: owner, claim: claim}
	ctrl.mu.Lock()
	ctrl.active = op
	ctrl.mu.Unlock()
}

// bf01ArbLockFree reports whether the model's arbitration lock can be taken
// right now. Called from inside a persistence hook that runs on the restart
// goroutine, a false result means arbLock is held across lifecycle work.
func bf01ArbLockFree(sup *Supervisor, modelID string) bool {
	arb := sup.arbitrationLock(modelID)
	if !arb.TryLock() {
		return false
	}
	arb.Unlock()
	return true
}

// bf01TakeArbLock acquires and releases the model's arbitration lock from a
// helper goroutine, failing to a timeout instead of hanging the suite.
func bf01TakeArbLock(sup *Supervisor, modelID string, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		arb := sup.arbitrationLock(modelID)
		arb.Lock()
		arb.Unlock()
		done <- nil
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return errors.New("timeout: the model arbitration lock is held across restart lifecycle work")
	}
}

// bf01IsRejection returns the structured admission rejection carried by err.
func bf01IsRejection(t *testing.T, err error) *AdmissionRejection {
	t.Helper()
	if err == nil {
		t.Fatal("expected arbitration rejection, got nil error")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	return rej
}

// bf01WaitActiveReservation polls until the controller publishes an active
// operation (i.e. arbitration linearized and the lifecycle work is underway).
func bf01WaitActiveReservation(t *testing.T, ctrl *InstanceController, timeout time.Duration) *launchOperation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if op := ctrl.activeOperation(); op != nil {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for the restart reservation to be published")
	return nil
}

// === T-A1 ====================================================================

// TestRestart_DoesNotBypassAdmit runs a restart of a historical terminal
// instance and an incompatible new AdmitAndStart of the SAME ModelID
// concurrently and proves the arbitration boundary grants the spawn authority
// to at most one of them.
func TestRestart_DoesNotBypassAdmit(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-race", "", "")
	i1, ctrl1 := h.launchTerminal(model, domain.ManualOwner)

	type outcome struct {
		inst *domain.LaunchInstance
		err  error
	}
	restartRes := make(chan outcome, 1)
	admitRes := make(chan outcome, 1)
	before := h.spawnCount()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		inst, err := h.sup.Restart(context.Background(), i1.ID)
		restartRes <- outcome{inst: inst, err: err}
	}()
	go func() {
		defer wg.Done()
		inst, err := h.sup.AdmitAndStart(context.Background(), model, h.rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
		admitRes <- outcome{inst: inst, err: err}
	}()
	wg.Wait()
	close(restartRes)
	close(admitRes)

	var successes []*domain.LaunchInstance
	var rejections []error
	for _, ch := range []chan outcome{restartRes, admitRes} {
		for r := range ch {
			if r.err != nil {
				rejections = append(rejections, r.err)
				continue
			}
			successes = append(successes, r.inst)
		}
	}
	if len(successes) != 1 {
		t.Fatalf("successes = %d (want exactly 1); rejections = %v", len(successes), rejections)
	}
	if len(rejections) != 1 {
		t.Fatalf("rejections = %d, want 1", len(rejections))
	}
	// The loser must be denied by the arbitration/state contract, not by a
	// resource error.
	loser := rejections[0]
	rej := &AdmissionRejection{}
	if !errors.As(loser, &rej) && !errors.Is(loser, ErrLaunchInFlight) {
		t.Fatalf("loser error = %T: %v, want arbitration/in-flight rejection", loser, loser)
	}
	if got := h.spawnCount() - before; got != 1 {
		t.Fatalf("spawn delta = %d, want 1 (at most one incompatible spawn authority)", got)
	}
	live := h.liveIDs()
	if len(live) != 1 {
		t.Fatalf("live instances = %v, want exactly 1", live)
	}
	if live[0] != successes[0].ID {
		t.Fatalf("live instance = %s, want the winner %s", string(live[0]), string(successes[0].ID))
	}
	if op := ctrl1.activeOperation(); op != nil {
		t.Fatalf("historical controller still holds operation %d (reservation leak)", op.id)
	}
}

// === T-A2 ====================================================================

// TestRestart_TerminalTarget_ConflictRejected is the direct BF-01 regression:
// the deterministic six-step reproduction from the defect report, now expected
// to be refused by arbitration.
func TestRestart_TerminalTarget_ConflictRejected(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-repro", "", "")

	// 1-2. Start Model M -> I1, which becomes terminal but remains registered.
	i1, ctrl1 := h.launchTerminal(model, domain.ManualOwner)
	i1PID := i1.PID
	i1Durable := h.durable(i1.ID)

	// 3. Start Model M again -> I2 is admitted and becomes live.
	i2 := h.launch(model, domain.ManualOwner, "-sleep", "60")
	ctrl2 := h.controller(i2.ID)

	// 4-6. Restart historical I1: it must never reach manager.Start, so I1 and
	// I2 cannot both become live.
	before := h.spawnCount()
	_, err := h.sup.Restart(context.Background(), i1.ID)
	rej := bf01IsRejection(t, err)
	if rej.Reason != RejInFlight {
		t.Fatalf("rejection reason = %v, want %v", rej.Reason, RejInFlight)
	}
	if rej.ModelID != model.ID {
		t.Fatalf("rejection ModelID = %q, want %q", rej.ModelID, model.ID)
	}
	if rej.ConflictID != i2.ID {
		t.Fatalf("ConflictID = %q, want the live instance %q", string(rej.ConflictID), string(i2.ID))
	}

	if got := h.spawnCount() - before; got != 0 {
		t.Fatalf("restart manager.Start counter delta = %d, want 0", got)
	}
	snap2 := ctrl2.Snapshot()
	if snap2.State != domain.InstanceStateRunning || snap2.PID != i2.PID {
		t.Fatalf("live instance disturbed by the rejected restart: state=%q pid=%d (was pid=%d)", snap2.State, snap2.PID, i2.PID)
	}
	snap1 := ctrl1.Snapshot()
	if !snap1.IsTerminal() {
		t.Fatalf("historical instance state = %q, want terminal", snap1.State)
	}
	if snap1.PID != i1PID {
		t.Fatalf("historical instance PID changed by the rejected restart: %d -> %d", i1PID, snap1.PID)
	}
	if op := ctrl1.activeOperation(); op != nil {
		t.Fatalf("rejected restart left operation %d published on the target", op.id)
	}
	if n := preSpawnCount(h.sup); n != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", n)
	}
	if live := h.liveIDs(); len(live) != 1 || live[0] != i2.ID {
		t.Fatalf("live instances = %v, want only %s (no second live process)", live, string(i2.ID))
	}
	after := h.durable(i1.ID)
	if after.State != i1Durable.State || after.PID != i1Durable.PID {
		t.Fatalf("rejected restart mutated the durable record: %+v -> %+v", i1Durable, after)
	}
}

// === T-A3 / G ==============================================================

// TestRestart_PipelineOwned_ConflictMatrix walks the full ownership matrix for
// a terminal restart target against one live same-ModelID sibling: only the
// compatible() relationship may decide the outcome, and the restart owner is
// always the TARGET's attribution.
func TestRestart_PipelineOwned_ConflictMatrix(t *testing.T) {
	cases := []struct {
		name           string
		targetPipe     string
		targetEntry    string
		siblingPipe    string
		siblingEntry   string
		wantAllowed    bool
		wantConflictID string // "sibling" | "target"
	}{
		{name: "manual_target__manual_sibling", wantAllowed: false},
		{name: "manual_target__pipeline_sibling", siblingPipe: "p1", siblingEntry: "e1", wantAllowed: false},
		{name: "pipeline_target__manual_sibling", targetPipe: "p1", targetEntry: "e1", wantAllowed: false},
		{name: "cross_pipeline_rejected", targetPipe: "p1", targetEntry: "e1", siblingPipe: "p2", siblingEntry: "e2", wantAllowed: false},
		{name: "cross_pipeline_shared_entry_rejected", targetPipe: "p1", targetEntry: "e1", siblingPipe: "p2", siblingEntry: "e1", wantAllowed: false},
		{name: "same_pipeline_same_entry_rejected", targetPipe: "p1", targetEntry: "e1", siblingPipe: "p1", siblingEntry: "e1", wantAllowed: false},
		{name: "same_pipeline_distinct_entry_allowed", targetPipe: "p1", targetEntry: "e1", siblingPipe: "p1", siblingEntry: "e2", wantAllowed: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := bf01New(t, 4)
			modelID := "bf01-matrix-" + tc.name
			target := h.model(modelID, tc.targetPipe, tc.targetEntry)
			sibling := h.model(modelID, tc.siblingPipe, tc.siblingEntry)

			i1, ctrl1 := h.launchTerminal(target, h.ownerOf(tc.targetPipe, tc.targetEntry))
			i2 := h.launch(sibling, h.ownerOf(tc.siblingPipe, tc.siblingEntry), "-sleep", "60")

			before := h.spawnCount()
			restarted, err := h.sup.Restart(context.Background(), i1.ID)
			delta := h.spawnCount() - before

			if !tc.wantAllowed {
				rej := bf01IsRejection(t, err)
				if rej.Reason != RejInFlight {
					t.Fatalf("reason = %v, want %v", rej.Reason, RejInFlight)
				}
				if rej.ConflictID != i2.ID {
					t.Fatalf("ConflictID = %q, want the live sibling %q (self-exemption must not widen to ModelID/PipelineID/EntryID)", string(rej.ConflictID), string(i2.ID))
				}
				if delta != 0 {
					t.Fatalf("spawn delta = %d, want 0", delta)
				}
				if live := h.liveIDs(); len(live) != 1 || live[0] != i2.ID {
					t.Fatalf("live instances = %v, want only the sibling %s", live, string(i2.ID))
				}
				if op := ctrl1.activeOperation(); op != nil {
					t.Fatalf("rejected restart left operation %d published", op.id)
				}
				if snap := ctrl1.Snapshot(); !snap.IsTerminal() {
					t.Fatalf("terminal target state = %q, want terminal", snap.State)
				}
				return
			}

			if err != nil {
				t.Fatalf("same-pipeline distinct EntryID must stay compatible: %v", err)
			}
			if delta != 1 {
				t.Fatalf("spawn delta = %d, want 1", delta)
			}
			if restarted.ID != i1.ID {
				t.Fatalf("restart changed InstanceID: %s -> %s", string(i1.ID), string(restarted.ID))
			}
			if live := h.liveIDs(); len(live) != 2 {
				t.Fatalf("live instances = %v, want both pipeline entries", live)
			}
		})
	}
}

// TestRestart_SelfExemption_ExactInstanceIDOnly proves the arbitration scan
// exempts only the exact target InstanceID: with no other instance of the model
// the target restarts itself, and a sibling that merely shares ModelID,
// PipelineID or EntryID is still evaluated through compatible().
func TestRestart_SelfExemption_ExactInstanceIDOnly(t *testing.T) {
	t.Run("target_is_the_only_instance", func(t *testing.T) {
		h := bf01New(t, 4)
		model := h.model("bf01-self", "p1", "e1")
		i1, ctrl1 := h.launchTerminal(model, h.ownerOf("p1", "e1"))
		before := h.spawnCount()
		restarted, err := h.sup.Restart(context.Background(), i1.ID)
		if err != nil {
			t.Fatalf("self restart of the only instance: %v", err)
		}
		if h.spawnCount()-before != 1 {
			t.Fatal("self restart did not spawn a new generation")
		}
		if restarted.ID != i1.ID || ctrl1.Snapshot().ID != i1.ID {
			t.Fatalf("self restart changed instance identity: %s -> %s", string(i1.ID), string(restarted.ID))
		}
	})

	t.Run("sibling_sharing_only_entry_id_is_still_evaluated", func(t *testing.T) {
		h := bf01New(t, 4)
		modelID := "bf01-self-entry"
		i1, _ := h.launchTerminal(h.model(modelID, "p1", "e1"), h.ownerOf("p1", "e1"))
		i2 := h.launch(h.model(modelID, "p2", "e1"), h.ownerOf("p2", "e1"), "-sleep", "60")
		before := h.spawnCount()
		_, err := h.sup.Restart(context.Background(), i1.ID)
		rej := bf01IsRejection(t, err)
		if rej.ConflictID != i2.ID {
			t.Fatalf("ConflictID = %q, want %q", string(rej.ConflictID), string(i2.ID))
		}
		if h.spawnCount()-before != 0 {
			t.Fatal("shared EntryID alone must not exempt a cross-pipeline conflict")
		}
	})
}

// === T-A4 ===================================================================

// TestRestart_WithinPipeline_DuplicateModelID_Allowed preserves ADR 013: two
// entries of ONE pipeline may hold the same ModelID concurrently, and
// restarting one of them must not disturb the other.
func TestRestart_WithinPipeline_DuplicateModelID_Allowed(t *testing.T) {
	h := bf01New(t, 4)
	modelID := "bf01-dup-entry"
	pipe := "p1"

	e1, _ := h.launchTerminal(h.model(modelID, pipe, "entry-1"), h.ownerOf(pipe, "entry-1"))
	e2 := h.launch(h.model(modelID, pipe, "entry-2"), h.ownerOf(pipe, "entry-2"), "-sleep", "60")
	e2PID := e2.PID

	before := h.spawnCount()
	restarted, err := h.sup.Restart(context.Background(), e1.ID)
	if err != nil {
		t.Fatalf("restart of entry-1 while entry-2 is live: %v", err)
	}
	if h.spawnCount()-before != 1 {
		t.Fatal("expected exactly one new process generation")
	}
	if restarted.ID != e1.ID {
		t.Fatalf("InstanceID changed: %s -> %s", string(e1.ID), string(restarted.ID))
	}
	if restarted.PID <= 0 || restarted.PID == e1.PID {
		t.Fatalf("new generation PID = %d, historical PID = %d", restarted.PID, e1.PID)
	}
	snapE2 := h.controller(e2.ID).Snapshot()
	if snapE2.State != domain.InstanceStateRunning || snapE2.PID != e2PID {
		t.Fatalf("entry-2 disturbed by the entry-1 restart: state=%q pid=%d", snapE2.State, snapE2.PID)
	}
	if live := h.liveIDs(); len(live) != 2 {
		t.Fatalf("live instances = %v, want both entries", live)
	}
}

// === T-A5 ===================================================================

// TestRestart_OrphanFence uses the reachable orphan conflict: a durable orphan
// record for the model exists (recovery writes it for a live PID found at
// startup), and a restart / new admission of that model must both be denied by
// the repository fence. An orphan-target restart scenario is NOT constructed:
// orphan state exists only in the repository, never as a restart-addressable
// controller.
func TestRestart_OrphanFence(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-orphan", "", "")
	i1, ctrl1 := h.launchTerminal(model, domain.ManualOwner)

	orphanID := domain.InstanceID("bf01-orphan-record")
	orphan := &domain.LaunchInstanceEntry{ID: string(orphanID), ModelID: model.ID, State: string(domain.InstanceStateOrphan)}
	if err := h.store.Create(orphan); err != nil {
		t.Fatalf("seed orphan record: %v", err)
	}

	before := h.spawnCount()
	_, err := h.sup.Restart(context.Background(), i1.ID)
	rej := bf01IsRejection(t, err)
	if rej.Reason != RejOrphan {
		t.Fatalf("restart rejection reason = %v, want %v", rej.Reason, RejOrphan)
	}
	if rej.ConflictID != orphanID {
		t.Fatalf("ConflictID = %q, want the orphan record %q", string(rej.ConflictID), string(orphanID))
	}
	if h.spawnCount()-before != 0 {
		t.Fatal("orphan fence must not spawn")
	}
	if op := ctrl1.activeOperation(); op != nil {
		t.Fatalf("rejected restart left operation %d published", op.id)
	}

	if _, err := h.sup.AdmitAndStart(context.Background(), model, h.rt, domain.ManualOwner, []string{"-sleep", "60"}, nil); err == nil {
		t.Fatal("expected the orphan fence to reject a new admission too")
	} else {
		newRej := bf01IsRejection(t, err)
		if newRej.Reason != RejOrphan {
			t.Fatalf("admission rejection reason = %v, want %v", newRej.Reason, RejOrphan)
		}
	}
	if got := h.spawnCount() - before; got != 0 {
		t.Fatalf("spawn delta = %d, want 0 across the fenced restart and admission", got)
	}
}

// === T-A6 / K ===============================================================

// TestStartCore_RequiresClaim attempts to reach manager.Start with every
// invalid spawn authorization shape. None may spawn, publish "starting", or
// touch the durable record.
func TestStartCore_RequiresClaim(t *testing.T) {
	cases := []struct {
		name       string
		wantReason string
		bind       func(h *bf01Harness, ctrl *InstanceController) *spawnClaim
	}{
		{
			name:       "nil_claim",
			wantReason: "no spawn authorization presented",
			bind:       func(h *bf01Harness, ctrl *InstanceController) *spawnClaim { return nil },
		},
		{
			name:       "foreign_supervisor_claim",
			wantReason: "different supervisor",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				other := bf01New(h.t, 4)
				claim := other.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
				return claim
			},
		},
		{
			name:       "wrong_instance_id",
			wantReason: "claim instance does not match",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				opID := h.sup.nextOperationID()
				claim := &spawnClaim{sup: h.sup, modelID: ctrl.modelID, owner: domain.ManualOwner, instanceID: domain.InstanceID("someone-else"), opID: opID}
				bf01BindClaim(ctrl, claim, opID, domain.ManualOwner)
				return claim
			},
		},
		{
			name:       "wrong_model_id",
			wantReason: "claim model does not match",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				opID := h.sup.nextOperationID()
				claim := &spawnClaim{sup: h.sup, modelID: "other-model", owner: domain.ManualOwner, instanceID: ctrl.instanceID, opID: opID}
				bf01BindClaim(ctrl, claim, opID, domain.ManualOwner)
				return claim
			},
		},
		{
			name:       "wrong_operation_generation",
			wantReason: "belongs to a different generation",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				stale := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
				// The controller is then handed a NEWER generation by a fresh
				// arbitration-equivalent publication.
				current := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
				if current.opID <= stale.opID {
					h.t.Fatalf("operation IDs are not monotonic: %d then %d", stale.opID, current.opID)
				}
				return stale
			},
		},
		{
			name:       "stale_claim_no_operation",
			wantReason: "no operation currently owns",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				opID := h.sup.nextOperationID()
				claim := &spawnClaim{sup: h.sup, modelID: ctrl.modelID, owner: domain.ManualOwner, instanceID: ctrl.instanceID, opID: opID}
				// Deliberately NOT bound: the claim outlived its operation.
				return claim
			},
		},
		{
			name:       "claim_operation_identity_mismatch",
			wantReason: "operation identity mismatch",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				opID := h.sup.nextOperationID()
				claim := &spawnClaim{sup: h.sup, modelID: ctrl.modelID, owner: domain.ManualOwner, instanceID: ctrl.instanceID, opID: opID}
				bf01BindClaim(ctrl, claim, opID+1000, domain.ManualOwner)
				return claim
			},
		},
		{
			name:       "claim_owner_mismatch",
			wantReason: "owner does not match",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				opID := h.sup.nextOperationID()
				claim := &spawnClaim{sup: h.sup, modelID: ctrl.modelID, owner: domain.ManualOwner, instanceID: ctrl.instanceID, opID: opID}
				bf01BindClaim(ctrl, claim, opID, domain.PipelineOwner("p-x", "e-x"))
				return claim
			},
		},
		{
			name:       "already_consumed_claim",
			wantReason: "claim already consumed",
			bind: func(h *bf01Harness, ctrl *InstanceController) *spawnClaim {
				claim := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
				claim.consumed.Store(true)
				return claim
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := bf01New(t, 4)
			model := h.model("bf01-claim", "", "")
			_, ctrl := h.launchTerminal(model, domain.ManualOwner)
			before := h.spawnCount()
			durableBefore := h.durable(ctrl.instanceID)

			claim := tc.bind(h, ctrl)
			_, err := ctrl.startWithReservation(context.Background(), nil, claim)
			if claim != nil {
				defer ctrl.releaseLaunchOperation(claim.opID)
			}

			if !errors.Is(err, errSpawnClaimRejected) {
				t.Fatalf("error = %v, want it to carry %v", err, errSpawnClaimRejected)
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("error = %q, want it to report %q", err.Error(), tc.wantReason)
			}
			if got := h.spawnCount() - before; got != 0 {
				t.Fatalf("manager.Start executed %d time(s) without a valid spawn claim", got)
			}
			if snap := ctrl.Snapshot(); snap.State != domain.InstanceStateExited {
				t.Fatalf("state = %q, want exited (a rejected claim must not publish starting)", snap.State)
			}
			after := h.durable(ctrl.instanceID)
			if after.State != durableBefore.State || after.PID != durableBefore.PID {
				t.Fatalf("durable record mutated by a rejected claim: %+v -> %+v", durableBefore, after)
			}
			if n := preSpawnCount(h.sup); n != 0 {
				t.Fatalf("preSpawnInFlight = %d, want 0", n)
			}
		})
	}

	t.Run("valid_claim_is_accepted", func(t *testing.T) {
		h := bf01New(t, 4)
		model := h.model("bf01-claim-ok", "", "")
		_, ctrl := h.launchTerminal(model, domain.ManualOwner)
		before := h.spawnCount()
		claim := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
		defer ctrl.releaseLaunchOperation(claim.opID)
		if _, err := ctrl.startWithReservation(context.Background(), nil, claim); err != nil {
			t.Fatalf("valid claim rejected: %v", err)
		}
		if h.spawnCount()-before != 1 {
			t.Fatal("a valid claim must authorize exactly one spawn")
		}
		if snap := ctrl.Snapshot(); snap.State != domain.InstanceStateRunning {
			t.Fatalf("state = %q, want running", snap.State)
		}
	})
}

// === T-A7 ===================================================================

// TestRestart_AbortAfterClaim_ReleasesReservation forces a pre-spawn restart
// failure AFTER the claim was published and proves the reservation does not
// leak, no zombie in-flight restart remains, the abandoned claim authorizes
// nothing, and a later fresh arbitration can proceed.
func TestRestart_AbortAfterClaim_ReleasesReservation(t *testing.T) {
	h := bf01New(t, 1)
	model := h.model("bf01-abort", "", "")
	i1, ctrl1 := h.launchTerminal(model, domain.ManualOwner)

	// Occupy the only slot by removing the token directly: the restart must
	// block in slot acquisition, after its reservation is already published.
	<-h.sup.semaphore

	var firstOpID uint64
	res := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := h.sup.Restart(ctx, i1.ID)
		res <- err
	}()

	// While the restart is blocked, its reservation must keep the model
	// conflict-visible: this is the terminal-interval gap BF-01 used to open.
	firstOp := bf01WaitActiveReservation(t, ctrl1, bf01Timeout)
	firstOpID = firstOp.id
	orphanedClaim := firstOp.claim
	_, err := h.sup.AdmitAndStart(context.Background(), model, h.rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	rej := bf01IsRejection(t, err)
	if rej.Reason != RejInFlight || rej.ConflictID != i1.ID {
		t.Fatalf("in-flight restart not conflict-visible: %+v", rej)
	}

	select {
	case err := <-res:
		if err == nil {
			t.Fatal("expected the slot-starved restart to fail")
		}
		if !strings.Contains(err.Error(), "acquire concurrency slot") {
			t.Fatalf("error = %v, want the bounded slot acquisition failure", err)
		}
	case <-time.After(bf01Timeout):
		t.Fatal("restart did not return after its request context expired")
	}

	if op := ctrl1.activeOperation(); op != nil {
		t.Fatalf("failed restart leaked operation %d (published %d)", op.id, firstOpID)
	}
	// The failed attempt never reached commit C, so its claim was never spent —
	// but with its operation gone it authorizes nothing either: replaying it is
	// rejected and cannot spawn. Only the fresh arbitration below can.
	if orphanedClaim.consumed.Load() {
		t.Fatal("a claim from a pre-commit failure was spent without committing")
	}
	beforeReplay := h.spawnCount()
	if _, err := ctrl1.startWithReservation(context.Background(), nil, orphanedClaim); !errors.Is(err, errSpawnClaimRejected) {
		t.Fatalf("orphaned claim replay error = %v, want %v", err, errSpawnClaimRejected)
	}
	if got := h.spawnCount() - beforeReplay; got != 0 {
		t.Fatalf("an orphaned claim spawned %d process(es)", got)
	}
	if snap := ctrl1.Snapshot(); !snap.IsTerminal() {
		t.Fatalf("target state = %q, want the terminal state to remain final", snap.State)
	}
	if n := preSpawnCount(h.sup); n != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", n)
	}

	// A fresh arbitration must be able to proceed once the resource is free.
	h.sup.semaphore <- struct{}{}
	before := h.spawnCount()
	restarted, err := h.sup.Restart(context.Background(), i1.ID)
	if err != nil {
		t.Fatalf("restart after a failed restart: %v", err)
	}
	if h.spawnCount()-before != 1 {
		t.Fatal("the retried restart did not spawn")
	}
	if restarted.ID != i1.ID {
		t.Fatalf("retry changed InstanceID: %s -> %s", string(i1.ID), string(restarted.ID))
	}
	if restarted.PID == i1.PID {
		t.Fatalf("retry reused the old PID %d", restarted.PID)
	}
}

// === T-B1 / T (RB-015b) =====================================================

// TestRestart_DuringShutdown_DrainWins covers the shutdown races for a restart:
// drain before arbitration (no usable claim at all), drain while the restart is
// still pre-spawn (abort, claim permanently unusable, reservation released only
// after the final state), and the deterministic drain-before-commit-C ordering
// (commit C spends the claim, then aborts without manager.Start).
func TestRestart_DuringShutdown_DrainWins(t *testing.T) {
	t.Run("drain_before_arbitration", func(t *testing.T) {
		h := bf01New(t, 4)
		model := h.model("bf01-drain-a", "", "")
		i1, ctrl1 := h.launchTerminal(model, domain.ManualOwner)

		h.sup.beginDrain()
		before := h.spawnCount()
		_, err := h.sup.Restart(context.Background(), i1.ID)
		rej := bf01IsRejection(t, err)
		if rej.Reason != RejShuttingDown {
			t.Fatalf("reason = %v, want %v", rej.Reason, RejShuttingDown)
		}
		if h.spawnCount()-before != 0 {
			t.Fatal("manager.Start executed after drain-start")
		}
		if op := ctrl1.activeOperation(); op != nil {
			t.Fatalf("rejected restart published operation %d", op.id)
		}
	})

	t.Run("drain_after_claim_before_commit", func(t *testing.T) {
		h := bf01New(t, 1)
		model := h.model("bf01-drain-c", "", "")
		i1, ctrl1 := h.launchTerminal(model, domain.ManualOwner)
		durableBefore := h.durable(i1.ID)

		<-h.sup.semaphore // starve the restart's slot acquisition

		res := make(chan error, 1)
		go func() {
			_, err := h.sup.Restart(context.Background(), i1.ID)
			res <- err
		}()
		firstOp := bf01WaitActiveReservation(t, ctrl1, bf01Timeout)
		spentClaim := firstOp.claim

		h.sup.beginDrain()            // D wins the pre-spawn window
		h.sup.semaphore <- struct{}{} // hand the slot back so C can be attempted

		select {
		case err := <-res:
			if !errors.Is(err, ErrLaunchAbortedByShutdown) {
				t.Fatalf("error = %v, want %v", err, ErrLaunchAbortedByShutdown)
			}
		case <-time.After(bf01Timeout):
			t.Fatal("the aborted restart never returned")
		}

		if got := h.spawnCount(); got != 1 {
			t.Fatalf("spawn count = %d, want 1 (only the historical generation ever spawned; manager.Start must not run after D)", got)
		}
		if n := preSpawnCount(h.sup); n != 0 {
			t.Fatalf("preSpawnInFlight = %d, want 0 (a restart never registers a pre-spawn token)", n)
		}
		if got := len(h.sup.semaphore); got != 1 {
			t.Fatalf("available slots = %d, want 1 (the acquired slot must be returned)", got)
		}
		if op := ctrl1.activeOperation(); op != nil {
			t.Fatalf("aborted restart leaked operation %d", op.id)
		}
		// The aborted attempt leaves its authorization permanently unusable: either
		// commit C spent it and the D<C abort kept it spent, or drain beat C and
		// the claim lost its operation. Both are proven behaviorally — replaying
		// this very claim may never spawn (drain_while_… below pins the first
		// branch deterministically).
		replayBefore := h.spawnCount()
		if _, err := ctrl1.startWithReservation(context.Background(), nil, spentClaim); !errors.Is(err, errSpawnClaimRejected) {
			t.Fatalf("the aborted restart's claim still authorizes a spawn: %v", err)
		}
		if got := h.spawnCount() - replayBefore; got != 0 {
			t.Fatalf("an aborted restart's claim spawned %d process(es)", got)
		}
		snap := ctrl1.Snapshot()
		if !snap.IsTerminal() {
			t.Fatalf("target state = %q, want the previous generation's terminal state to remain final", snap.State)
		}
		after := h.durable(i1.ID)
		if after.State != durableBefore.State {
			t.Fatalf("durable state changed by the aborted restart: %s -> %s", durableBefore.State, after.State)
		}

		// The spent claim cannot be retried; only fresh arbitration can proceed,
		// so the model's arbitration lock must be free again.
		if err := bf01TakeArbLock(h.sup, model.ID, 2*time.Second); err != nil {
			t.Fatal(err)
		}
	})

	// Deterministic "C attempts" ordering: the restart is held strictly between
	// its slot acquisition and commit C by an old generation whose completion the
	// test controls, so the drain is already latched when commit C is reached.
	t.Run("drain_while_restart_awaits_old_generation", func(t *testing.T) {
		h := bf01New(t, 0) // no slot accounting, so acquireSlot cannot race the drain
		model := h.model("bf01-drain-commit-c", "", "")
		target := h.fabricate(domain.InstanceID("bf01-drain-commit-c-target"), model, domain.InstanceStateStopping)
		run := newInstanceRunState(nil)
		target.mu.Lock()
		target.run = run
		target.mu.Unlock()

		res := make(chan error, 1)
		go func() {
			_, err := h.sup.Restart(context.Background(), target.instanceID)
			res <- err
		}()
		op := bf01WaitActiveReservation(t, target, bf01Timeout)
		claim := op.claim

		// D latches while the restart still waits for the existing stop owner,
		// i.e. strictly BEFORE commit C is attempted.
		h.sup.beginDrain()
		run.complete()

		select {
		case err := <-res:
			if !errors.Is(err, ErrLaunchAbortedByShutdown) {
				t.Fatalf("error = %v, want %v", err, ErrLaunchAbortedByShutdown)
			}
		case <-time.After(bf01Timeout):
			t.Fatal("the aborted restart never returned")
		}

		// C attempted after D: the claim was consumed at commit C and stays SPENT.
		if !claim.consumed.Load() {
			t.Fatal("commit C did not spend the spawn claim before aborting")
		}
		if got := h.spawnCount(); got != 0 {
			t.Fatalf("spawn count = %d, want 0 (manager.Start must not run after D)", got)
		}
		if op := target.activeOperation(); op != nil {
			t.Fatalf("aborted restart leaked operation %d", op.id)
		}
		// The old generation's finalization belongs to its own owner: the abort
		// must not rewrite it, and a restart never registers a pre-spawn token.
		if snap := target.Snapshot(); snap.State != domain.InstanceStateStopping {
			t.Fatalf("state = %q, want stopping (the abort must not become a second stop owner)", snap.State)
		}
		if n := preSpawnCount(h.sup); n != 0 {
			t.Fatalf("preSpawnInFlight = %d, want 0", n)
		}
	})
}

// TestRestart_CommitBeforeSpawnClaimConsumed stays internal to the package to
// assert the spent-claim rule directly: a consumed claim is never restored by
// an abort.
func TestRestart_CommitBeforeSpawnClaimConsumed(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-spent", "", "")
	_, ctrl := h.launchTerminal(model, domain.ManualOwner)

	claim := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
	defer ctrl.releaseLaunchOperation(claim.opID)
	if _, err := ctrl.startWithReservation(context.Background(), nil, claim); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if !claim.consumed.Load() {
		t.Fatal("the committed claim was not spent")
	}
	h.stopCtrl(ctrl)
	if _, err := ctrl.startWithReservation(context.Background(), nil, claim); !errors.Is(err, errSpawnClaimRejected) {
		t.Fatalf("a spent claim authorized another spawn: %v", err)
	}
}

// === T-B2 ===================================================================

// TestRestart_CommittedBeforeDrain_IsStoppable proves the C<D ordering for a
// restart: once the new generation commits it is lifecycle-visible, so
// Shutdown owns and stops it instead of returning success over a live process.
func TestRestart_CommittedBeforeDrain_IsStoppable(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-c-before-d", "", "")
	i1 := h.launch(model, domain.ManualOwner, "-sleep", "60")

	restarted, err := h.sup.Restart(context.Background(), i1.ID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if restarted.State != domain.InstanceStateRunning || restarted.PID <= 0 {
		t.Fatalf("restarted generation incomplete: %+v", restarted)
	}
	ctrl := h.controller(i1.ID)

	if err := shutdownWithTimeout(t, h.sup, bf01Timeout); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	snap := ctrl.Snapshot()
	if !snap.IsTerminal() {
		t.Fatalf("state after shutdown = %q, want terminal", snap.State)
	}
	if err := waitForProcess(context.Background(), ctrl, bf01Timeout); err != nil {
		t.Fatal(err)
	}
	if n := preSpawnCount(h.sup); n != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", n)
	}
	if !isDraining(h.sup) {
		t.Fatal("draining latch must be one-way and still set")
	}
}

// === T-B3 / H, I ============================================================

// TestArbitration_DoesNotHoldAcrossRestartStop proves the restart reservation
// design does not serialize the model (or another model) behind the lifecycle
// work: arbLock is released before stop, persistence, run completion waits and
// spawn.
func TestArbitration_DoesNotHoldAcrossRestartStop(t *testing.T) {
	t.Run("arbLock_free_while_restart_persists_stopping", func(t *testing.T) {
		store := &concurrentHookStore{mockStore: newMockStore()}
		sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
		spawns := withSpawnCounter(t, sup)
		rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
		model := &domain.Model{ID: "bf01-nohold", Name: "bf01", RuntimeID: "rt"}

		stopping := make(chan struct{})
		release := make(chan struct{})
		var stopOnce sync.Once
		var probeErr error
		store.updateHook = func(e *domain.LaunchInstanceEntry) error {
			if e.State == string(domain.InstanceStateStopping) {
				stopOnce.Do(func() {
					if !bf01ArbLockFree(sup, e.ModelID) {
						probeErr = errors.New("arbLock is held across the restart's stop/persistence work")
					}
					close(stopping)
				})
				<-release
			}
			return nil
		}

		inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"graceful"}, nil)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if atomic.LoadInt32(spawns) != 1 {
			t.Fatalf("spawn count after start = %d, want 1", atomic.LoadInt32(spawns))
		}

		type outcome struct {
			inst *domain.LaunchInstance
			err  error
		}
		res := make(chan outcome, 1)
		go func() {
			inst, err := sup.Restart(context.Background(), inst.ID)
			res <- outcome{inst: inst, err: err}
		}()

		select {
		case <-stopping:
		case <-time.After(bf01Timeout):
			t.Fatal("the restart never reached the stopping persist (fixture assumption broken)")
		}
		if probeErr != nil {
			t.Error(probeErr)
		}
		close(release)

		select {
		case r := <-res:
			if r.err != nil {
				t.Fatalf("restart after the blocked stop: %v", r.err)
			}
			if r.inst.PID == inst.PID {
				t.Fatalf("restart reused the old PID %d", r.inst.PID)
			}
		case <-time.After(bf01Timeout):
			t.Fatal("restart did not complete after the stop persist was released")
		}
	})

	t.Run("other_admission_proceeds_while_restart_waits_for_old_generation", func(t *testing.T) {
		h := bf01New(t, 0)
		waiting := h.model("bf01-wait", "", "")
		target := h.fabricate(domain.InstanceID("bf01-wait-target"), waiting, domain.InstanceStateStopping)
		run := newInstanceRunState(nil)
		target.mu.Lock()
		target.run = run
		target.mu.Unlock()

		type outcome struct {
			inst *domain.LaunchInstance
			err  error
		}
		res := make(chan outcome, 1)
		go func() {
			inst, err := h.sup.Restart(context.Background(), target.instanceID)
			res <- outcome{inst: inst, err: err}
		}()
		bf01WaitActiveReservation(t, target, bf01Timeout)

		if err := bf01TakeArbLock(h.sup, waiting.ID, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		other := h.model("bf01-wait-other", "", "")
		if inst := h.launch(other, domain.ManualOwner, "-sleep", "60"); inst.State != domain.InstanceStateRunning {
			t.Fatalf("unrelated model did not start: %+v", inst)
		}

		run.complete()
		select {
		case r := <-res:
			if r.err != nil {
				t.Fatalf("restart after the old generation completed: %v", r.err)
			}
			if r.inst.ID != target.instanceID {
				t.Fatalf("restart changed InstanceID: %s -> %s", string(target.instanceID), string(r.inst.ID))
			}
		case <-time.After(bf01Timeout):
			t.Fatal("restart never continued after the old generation completed")
		}
		if op := target.activeOperation(); op != nil {
			t.Fatalf("completed restart leaked operation %d", op.id)
		}
	})
}

// === T-G1 / T-G2 ============================================================

// TestSpawnClaim_SingleConsumption races eight consumers over one claim: at
// most one spawn may be authorized. This test is part of the Linux -race CI
// coverage (local CGO/gcc is unavailable).
func TestSpawnClaim_SingleConsumption(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-single", "", "")
	_, ctrl := h.launchTerminal(model, domain.ManualOwner)
	before := h.spawnCount()

	claim := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
	defer ctrl.releaseLaunchOperation(claim.opID)

	const consumers = 8
	var wg sync.WaitGroup
	results := make([]error, consumers)
	start := make(chan struct{})
	wg.Add(consumers)
	for i := 0; i < consumers; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := ctrl.startWithReservation(context.Background(), nil, claim)
			results[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	var accepted, rejected, other int
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, errSpawnClaimRejected):
			rejected++
		default:
			other++
			t.Errorf("unexpected consumer error: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted consumers = %d, want exactly 1", accepted)
	}
	if rejected != consumers-1 {
		t.Fatalf("claim-rejected consumers = %d, want %d", rejected, consumers-1)
	}
	if other != 0 {
		t.Fatalf("%d consumer(s) failed for an unrelated reason", other)
	}
	if got := h.spawnCount() - before; got != 1 {
		t.Fatalf("spawn delta = %d, want 1 (manager.Start at most once per claim)", got)
	}
	if !claim.consumed.Load() {
		t.Fatal("the winning claim was not spent")
	}
	if n := preSpawnCount(h.sup); n != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", n)
	}
}

// TestSpawnClaim_CannotAuthorizeNextRestartGeneration proves the generation
// binding: claim N cannot authorize generation N+1, and a claim authorizes at
// most one transition.
func TestSpawnClaim_CannotAuthorizeNextRestartGeneration(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-generations", "", "")
	_, ctrl := h.launchTerminal(model, domain.ManualOwner)

	gen1 := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
	if _, err := ctrl.startWithReservation(context.Background(), nil, gen1); err != nil {
		t.Fatalf("generation 1 spawn: %v", err)
	}
	h.stopCtrl(ctrl)
	ctrl.releaseLaunchOperation(gen1.opID)
	if op := ctrl.activeOperation(); op != nil {
		t.Fatalf("released operation %d is still published", op.id)
	}

	gen2 := h.sup.mintTestSpawnClaim(ctrl, domain.ManualOwner)
	defer ctrl.releaseLaunchOperation(gen2.opID)
	if gen2.opID <= gen1.opID {
		t.Fatalf("operation identity is not monotonic: %d then %d", gen1.opID, gen2.opID)
	}

	before := h.spawnCount()
	if _, err := ctrl.startWithReservation(context.Background(), nil, gen1); !errors.Is(err, errSpawnClaimRejected) {
		t.Fatalf("generation-%d claim authorized generation %d: %v", gen1.opID, gen2.opID, err)
	}
	if got := h.spawnCount() - before; got != 0 {
		t.Fatalf("stale generation claim spawned %d time(s)", got)
	}

	if _, err := ctrl.startWithReservation(context.Background(), nil, gen2); err != nil {
		t.Fatalf("generation 2 spawn: %v", err)
	}
	if got := h.spawnCount() - before; got != 1 {
		t.Fatalf("spawn delta = %d, want 1", got)
	}
	if _, err := ctrl.startWithReservation(context.Background(), nil, gen2); !errors.Is(err, errSpawnClaimRejected) {
		t.Fatalf("replayed claim accepted: %v", err)
	}
	if got := h.spawnCount() - before; got != 1 {
		t.Fatalf("replayed claim spawned: delta = %d, want 1", got)
	}
	h.stopCtrl(ctrl)
}

// TestSpawnClaim_IsPointerOnlyNoCopyType pins the no-copy marker that makes
// `go vet` (copylocks) reject any by-value copy of a spawn claim, so the
// authority can never be duplicated. The marker must live in the struct BY
// VALUE: a *noCopy field would not be detected by copylocks, and the claim is
// only ever threaded as a pointer.
func TestSpawnClaim_IsPointerOnlyNoCopyType(t *testing.T) {
	// Compile-time proof that *spawnClaim itself exposes the lock-style marker,
	// which is exactly the condition copylocks uses.
	var marker interface {
		Lock()
		Unlock()
	} = (*spawnClaim)(nil)
	if marker == nil {
		t.Fatal("spawnClaim carries no no-copy marker")
	}

	// reflect through the pointer type: taking a spawnClaim VALUE here would
	// itself trip copylocks.
	f, ok := reflect.TypeOf((*spawnClaim)(nil)).Elem().FieldByName("noCopy")
	if !ok {
		t.Fatal("spawnClaim has no embedded noCopy field")
	}
	if f.Type.Kind() != reflect.Struct {
		t.Fatalf("noCopy field is %s, want a by-value struct (a pointer field escapes copylocks)", f.Type.Kind())
	}
	if f.Anonymous != true {
		t.Fatal("the marker must be embedded so copies of the claim are flagged")
	}

	claim := &spawnClaim{}
	if claim.opID != 0 || claim.sup != nil || claim.consumed.Load() {
		t.Fatal("a zero claim must carry no binding")
	}
}

// === T-R1 / T-R2 ============================================================

// TestRestart_Stopping_AwaitsExistingGeneration proves the STOPPING rule: the
// restart establishes its reservation first and then waits for the existing
// stop/run owner — it never becomes a second stop owner, and exactly one new
// generation starts afterwards.
func TestRestart_Stopping_AwaitsExistingGeneration(t *testing.T) {
	h := bf01New(t, 0)
	target := h.model("bf01-stopping", "", "")
	ctrl := h.fabricate(domain.InstanceID("bf01-stopping-target"), target, domain.InstanceStateStopping)
	run := newInstanceRunState(nil)
	ctrl.mu.Lock()
	ctrl.run = run
	ctrl.mu.Unlock()
	pidBefore := ctrl.Snapshot().PID

	type outcome struct {
		inst *domain.LaunchInstance
		err  error
	}
	res := make(chan outcome, 1)
	go func() {
		inst, err := h.sup.Restart(context.Background(), ctrl.instanceID)
		res <- outcome{inst: inst, err: err}
	}()
	bf01WaitActiveReservation(t, ctrl, bf01Timeout)

	// Nothing may be terminalized, stopped or spawned while the old generation
	// is still being finalized by its own owner.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		snap := ctrl.Snapshot()
		if snap.State != domain.InstanceStateStopping {
			t.Fatalf("state during the wait = %q, want stopping (the restart became a second stop owner)", snap.State)
		}
		if snap.PID != pidBefore {
			t.Fatalf("PID changed during the wait: %d -> %d", pidBefore, snap.PID)
		}
		if got := h.spawnCount(); got != 0 {
			t.Fatalf("spawn count = %d, want 0 while the old generation is live", got)
		}
		if op := ctrl.activeOperation(); op == nil {
			t.Fatal("the restart reservation must stay visible while it waits")
		}
		time.Sleep(20 * time.Millisecond)
	}

	run.complete()
	select {
	case r := <-res:
		if r.err != nil {
			t.Fatalf("restart after the existing generation completed: %v", r.err)
		}
		if r.inst.ID != ctrl.instanceID {
			t.Fatalf("InstanceID changed: %s -> %s", string(ctrl.instanceID), string(r.inst.ID))
		}
		if r.inst.PID <= 0 || r.inst.PID == pidBefore {
			t.Fatalf("new generation PID = %d, want a fresh PID (was %d)", r.inst.PID, pidBefore)
		}
	case <-time.After(bf01Timeout):
		t.Fatal("restart never continued after the old generation completed")
	}
	if got := h.spawnCount(); got != 1 {
		t.Fatalf("spawn count = %d, want exactly one new generation", got)
	}
	if n := preSpawnCount(h.sup); n != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", n)
	}
}

// TestRestart_StartingRejected proves the STARTING rule of the restart state
// contract: the generation is inside the ADR 016 ownership-establishment window
// and may still become a C/D residual, so restart refuses without stopping or
// spawning.
func TestRestart_StartingRejected(t *testing.T) {
	h := bf01New(t, 4)
	model := h.model("bf01-starting", "", "")
	for _, state := range []domain.InstanceState{domain.InstanceStateStarting, domain.InstanceStatePending} {
		state := state
		t.Run(string(state), func(t *testing.T) {
			id := domain.InstanceID("bf01-" + string(state))
			ctrl := h.fabricate(id, model, state)
			before := h.spawnCount()

			_, err := h.sup.Restart(context.Background(), id)
			if !errors.Is(err, ErrLaunchInFlight) {
				t.Fatalf("error = %v, want %v", err, ErrLaunchInFlight)
			}
			if h.spawnCount()-before != 0 {
				t.Fatal("manager.Start executed for an in-flight target")
			}
			snap := ctrl.Snapshot()
			if snap.State != state {
				t.Fatalf("state = %q, want %q (no stop must be issued)", snap.State, state)
			}
			if op := ctrl.activeOperation(); op != nil {
				t.Fatalf("refused restart published operation %d", op.id)
			}
			if err := h.sup.PreflightRestart([]domain.InstanceID{id}); !errors.Is(err, ErrLaunchInFlight) {
				t.Fatalf("preflight error = %v, want %v", err, ErrLaunchInFlight)
			}
		})
	}

	t.Run("unrecoverable_states_refused", func(t *testing.T) {
		for _, state := range []domain.InstanceState{domain.InstanceStateStale, domain.InstanceStateOrphan} {
			id := domain.InstanceID("bf01-" + string(state))
			ctrl := h.fabricate(id, model, state)
			before := h.spawnCount()
			_, err := h.sup.Restart(context.Background(), id)
			if !errors.Is(err, ErrNotRestartable) {
				t.Fatalf("restart of a %s target: error = %v, want %v", state, err, ErrNotRestartable)
			}
			if h.spawnCount()-before != 0 {
				t.Fatalf("manager.Start executed for a %s target", state)
			}
			if op := ctrl.activeOperation(); op != nil {
				t.Fatalf("refused restart published operation %d", op.id)
			}
		}
	})

	t.Run("legacy_unattributed_pipeline_target_refused", func(t *testing.T) {
		legacy := h.model("bf01-legacy", "p1", "")
		ctrl := h.fabricate(domain.InstanceID("bf01-legacy"), legacy, domain.InstanceStateExited)
		before := h.spawnCount()
		_, err := h.sup.Restart(context.Background(), ctrl.instanceID)
		if !errors.Is(err, ErrNotRestartable) {
			t.Fatalf("error = %v, want %v", err, ErrNotRestartable)
		}
		if !strings.Contains(err.Error(), "legacy attribution") {
			t.Fatalf("error = %q, want the conservative legacy-attribution refusal", err.Error())
		}
		if h.spawnCount()-before != 0 {
			t.Fatal("manager.Start executed for an unattributable target")
		}
		if op := ctrl.activeOperation(); op != nil {
			t.Fatalf("refused restart published operation %d", op.id)
		}
		if err := h.sup.PreflightRestart([]domain.InstanceID{ctrl.instanceID}); !errors.Is(err, ErrNotRestartable) {
			t.Fatalf("preflight error = %v, want %v", err, ErrNotRestartable)
		}
	})
}

// === T-R3 ===================================================================

// TestPreflightRestart_CompleteSet audits every restartable and non-restartable
// state through the model/runtime pre-flight and proves a refusal never
// mutates or reserves any member of the selected set.
func TestPreflightRestart_CompleteSet(t *testing.T) {
	h := bf01New(t, 4)
	modelID := "bf01-preflight"

	okIDs := map[domain.InstanceState]domain.InstanceID{}
	for _, state := range []domain.InstanceState{
		domain.InstanceStateRunning, domain.InstanceStateStopping,
		domain.InstanceStateExited, domain.InstanceStateFailed,
	} {
		id := domain.InstanceID("bf01-ok-" + string(state))
		h.fabricate(id, h.model(modelID, "", ""), state)
		okIDs[state] = id
		if err := h.sup.PreflightRestart([]domain.InstanceID{id}); err != nil {
			t.Fatalf("preflight of a %s target: %v", state, err)
		}
	}

	var refused []domain.InstanceID
	for _, state := range []domain.InstanceState{
		domain.InstanceStatePending, domain.InstanceStateStarting,
		domain.InstanceStateStale, domain.InstanceStateOrphan,
	} {
		id := domain.InstanceID("bf01-no-" + string(state))
		h.fabricate(id, h.model(modelID, "", ""), state)
		err := h.sup.PreflightRestart([]domain.InstanceID{id})
		if err == nil {
			t.Fatalf("preflight accepted a %s target", state)
		}
		if !errors.Is(err, ErrLaunchInFlight) && !errors.Is(err, ErrNotRestartable) {
			t.Fatalf("preflight of a %s target returned %v, want a bounded in-flight/not-restartable error", state, err)
		}
		refused = append(refused, id)
	}
	legacyID := domain.InstanceID("bf01-no-legacy")
	h.fabricate(legacyID, h.model(modelID, "p1", ""), domain.InstanceStateExited)
	if err := h.sup.PreflightRestart([]domain.InstanceID{legacyID}); !errors.Is(err, ErrNotRestartable) {
		t.Fatalf("legacy preflight error = %v, want %v", err, ErrNotRestartable)
	}
	if err := h.sup.PreflightRestart([]domain.InstanceID{"bf01-missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown instance preflight error = %v, want a bounded not-found error", err)
	}

	// One selected set mixing every restartable target with every non-restartable
	// target: the whole set must be refused before any target is touched.
	set := []domain.InstanceID{okIDs[domain.InstanceStateRunning], okIDs[domain.InstanceStateStopping],
		okIDs[domain.InstanceStateExited], okIDs[domain.InstanceStateFailed], legacyID}
	set = append(set, refused...)
	before := h.spawnCount()
	if err := h.sup.PreflightRestart(set); err == nil {
		t.Fatal("preflight applied a set containing non-restartable targets")
	}
	if got := h.spawnCount() - before; got != 0 {
		t.Fatalf("spawn delta = %d, want 0 (pre-flight must not restart anything)", got)
	}
	h.sup.mu.RLock()
	controllers := make([]*InstanceController, 0, len(h.sup.instances))
	for _, ctrl := range h.sup.instances {
		controllers = append(controllers, ctrl)
	}
	h.sup.mu.RUnlock()
	for _, ctrl := range controllers {
		if op := ctrl.activeOperation(); op != nil {
			t.Errorf("instance %s holds operation %d after a refused preflight (reservation leaked)", string(ctrl.instanceID), op.id)
		}
	}
	if n := preSpawnCount(h.sup); n != 0 {
		t.Fatalf("preSpawnInFlight = %d, want 0", n)
	}
}

// === T-D3 ===================================================================

// TestRestart_ADR016_RunningIdentityDurable re-proves ADR 016 across the D1
// restart path: success still requires the durable running identity, and a
// running-persistence failure still fails closed with confirmed termination.
func TestRestart_ADR016_RunningIdentityDurable(t *testing.T) {
	t.Run("success_requires_durable_running_identity", func(t *testing.T) {
		h := bf01New(t, 4)
		model := h.model("bf01-durable", "p1", "e1")
		i1 := h.launch(model, h.ownerOf("p1", "e1"), "-sleep", "60")
		before := h.spawnCount()
		created := i1.CreatedAt
		ctrl := h.controller(i1.ID)
		ctrlBefore := ctrl

		restarted, err := h.sup.Restart(context.Background(), i1.ID)
		if err != nil {
			t.Fatalf("restart: %v", err)
		}
		if h.spawnCount()-before != 1 {
			t.Fatal("expected exactly one new process generation")
		}
		if restarted.ID != i1.ID {
			t.Fatalf("InstanceID changed: %s -> %s", string(i1.ID), string(restarted.ID))
		}
		if h.controller(i1.ID) != ctrlBefore {
			t.Fatal("restart replaced the controller (identity must be preserved)")
		}
		if !restarted.CreatedAt.Equal(created) {
			t.Fatalf("CreatedAt changed on restart: %v -> %v", created, restarted.CreatedAt)
		}
		if restarted.PID == i1.PID || restarted.PID <= 0 {
			t.Fatalf("PID = %d, want a fresh PID (previous %d)", restarted.PID, i1.PID)
		}
		if restarted.StartedAt.IsZero() {
			t.Fatal("new generation has no StartedAt")
		}
		if restarted.StartedAt.Before(i1.StartedAt) {
			t.Fatalf("StartedAt went backwards: %v -> %v", i1.StartedAt, restarted.StartedAt)
		}
		if restarted.PipelineID != "p1" || restarted.PipelineEntryID != "e1" {
			t.Fatalf("pipeline attribution lost: %q/%q", restarted.PipelineID, restarted.PipelineEntryID)
		}
		// ADR 016 S1/S2: the durable record carries the full running identity.
		e := h.durable(i1.ID)
		if e.State != string(domain.InstanceStateRunning) {
			t.Fatalf("durable state = %q, want running", e.State)
		}
		if e.PID != restarted.PID {
			t.Fatalf("durable PID = %d, want the new generation PID %d", e.PID, restarted.PID)
		}
		if e.PipelineID != "p1" || e.PipelineEntryID != "e1" || e.ModelID != model.ID {
			t.Fatalf("durable attribution changed: %+v", e)
		}
		if !e.CreatedAt.Equal(created) {
			t.Fatalf("durable CreatedAt changed: %v -> %v", created, e.CreatedAt)
		}
		if op := ctrl.activeOperation(); op != nil {
			t.Fatalf("successful restart leaked operation %d", op.id)
		}
	})

	t.Run("running_persist_failure_still_fails_closed", func(t *testing.T) {
		store := newMockStore()
		var armed atomic.Bool
		store.updateFn = func(e *domain.LaunchInstanceEntry) error {
			if e.State == string(domain.InstanceStateRunning) && armed.Load() {
				return testUpdateErr
			}
			store.storeAccepted(e)
			return nil
		}
		sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64})
		spawns := withSpawnCounter(t, sup)
		rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
		model := &domain.Model{ID: "bf01-durable-fail", Name: "bf01", RuntimeID: "rt"}

		// The first generation is long-lived and stopped to a terminal state, so
		// the restarted generation is a live process the ADR 016 rollback contract
		// must kill (an immediately-exiting target would race its own exit with
		// the rollback and is not what this row pins).
		i1, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		ctrl, err := sup.controllerFor(i1.ID)
		if err != nil {
			t.Fatalf("controllerFor: %v", err)
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bf01Timeout)
		defer stopCancel()
		if err := ctrl.Stop(stopCtx); err != nil {
			t.Fatalf("stop first generation: %v", err)
		}
		if err := waitForProcess(context.Background(), ctrl, bf01Timeout); err != nil {
			t.Fatalf("wait for terminal generation: %v", err)
		}
		armed.Store(true)

		_, err = sup.Restart(context.Background(), i1.ID)
		if err == nil {
			t.Fatal("expected the ADR 016 fail-closed error, got nil")
		}
		if !errors.Is(err, ErrPersistenceFailure) {
			t.Fatalf("error = %v, want %v", err, ErrPersistenceFailure)
		}
		if errors.Is(err, ErrTerminationUnconfirmed) || errors.Is(err, ErrRollbackFailed) {
			t.Fatalf("Outcome A must not carry residual sentinels, got %v", err)
		}
		if atomic.LoadInt32(spawns) != 2 {
			t.Fatalf("spawn count = %d, want 2 (the restart did spawn before the persist failed)", atomic.LoadInt32(spawns))
		}
		// The restart attempt is final, so its reservation is already released.
		if op := ctrl.activeOperation(); op != nil {
			t.Fatalf("failed restart leaked operation %d", op.id)
		}
		// Wait for the run's sole completion owner to finalize, then require a
		// terminal outcome (ADR 016 F1: a non-durable running identity can never
		// be reported as success).
		waitForRunDone(t, sup, i1.ID, bf01Timeout)
		if snap := ctrl.Snapshot(); !isTerminalState(snap.State) {
			t.Fatalf("state = %q, want terminal", snap.State)
		}
		waitForSlotFree(t, sup, 1, bf01Timeout)
		e, err := store.Get(string(i1.ID))
		if err != nil {
			t.Fatalf("durable record: %v", err)
		}
		if !isTerminalState(domain.InstanceState(e.State)) {
			t.Fatalf("durable state = %q, want terminal and never running", e.State)
		}
	})
}
