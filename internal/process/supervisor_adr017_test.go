package process

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// TestAdmitAndStart_SameModelID_SerialAdmission verifies that sequential
// AdmitAndStart calls for the same model with the first still in-flight are
// rejected with RejInFlight.
func TestAdmitAndStart_SameModelID_SerialAdmission(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst1, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("first AdmitAndStart: %v", err)
	}
	if inst1 == nil {
		t.Fatal("first AdmitAndStart returned nil instance")
	}

	_, err = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("second AdmitAndStart: expected rejection, got nil error")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("reason = %v, want RejInFlight", rej.Reason)
	}
	if rej.ModelID != "m1" {
		t.Fatalf("ModelID = %q, want m1", rej.ModelID)
	}
	if rej.ConflictID != inst1.ID {
		t.Fatalf("ConflictID = %q, want %q", rej.ConflictID, inst1.ID)
	}
}

// TestAdmitAndStart_SameModelID_Concurrent verifies that two concurrent
// AdmitAndStart calls for the same model result in exactly one admission
// and one rejection.
func TestAdmitAndStart_SameModelID_Concurrent(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	var wg sync.WaitGroup
	type result struct {
		inst *domain.LaunchInstance
		err  error
	}
	results := make([]result, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
			results[idx] = result{inst: inst, err: err}
		}(i)
	}
	wg.Wait()

	var successes, rejections int
	var rejected *AdmissionRejection
	for _, r := range results {
		if r.err == nil {
			successes++
		} else {
			rejections++
			var rej *AdmissionRejection
			if errors.As(r.err, &rej) {
				rejected = rej
			}
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if rejections != 1 {
		t.Fatalf("rejections = %d, want 1", rejections)
	}
	if rejected == nil {
		t.Fatal("expected *AdmissionRejection on the failed attempt")
	}
	if rejected.Reason != RejInFlight {
		t.Fatalf("rejection reason = %v, want RejInFlight", rejected.Reason)
	}
}

// blockingStore wraps mockStore and blocks Create for a specific modelID
// until released. Used to hold Model A inside startPostAdmit (after
// admission) while proving Model B's admission completes independently.
type blockingStore struct {
	*mockStore
	blockModel string
	blockCh    chan struct{}
	released   chan struct{}
}

func (s *blockingStore) Create(e *domain.LaunchInstanceEntry) error {
	if e.ModelID == s.blockModel {
		close(s.released)
		<-s.blockCh
	}
	return s.mockStore.Create(e)
}

// TestAdmitAndStart_DifferentModelIDs_Concurrent proves that concurrent
// AdmitAndStart calls for DIFFERENT model IDs are not serialized by one
// global lock. It blocks Model A inside startPostAdmit (after admission
// linearization, during store.Create) and verifies Model B's full
// AdmitAndStart (admission + startPostAdmit) completes while A is blocked.
// With a global arbitration lock held across the entire AdmitAndStart call,
// B would be unable to proceed and would time out.
func TestAdmitAndStart_DifferentModelIDs_Concurrent(t *testing.T) {
	store := &blockingStore{
		mockStore:  newMockStore(),
		blockModel: "model-a",
		blockCh:    make(chan struct{}),
		released:   make(chan struct{}),
	}
	cfg := SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	modelA := &domain.Model{ID: "model-a", Name: "a", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "model-b", Name: "b", RuntimeID: "rt"}

	type result struct {
		inst *domain.LaunchInstance
		err  error
	}
	resA := make(chan result, 1)
	resB := make(chan result, 1)

	go func() {
		inst, err := sup.AdmitAndStart(context.Background(), modelA, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
		resA <- result{inst: inst, err: err}
	}()
	go func() {
		inst, err := sup.AdmitAndStart(context.Background(), modelB, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
		resB <- result{inst: inst, err: err}
	}()

	// Wait for Model A to enter store.Create (admission completed, now blocked
	// in startPostAdmit).
	select {
	case <-store.released:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Model A to enter store.Create")
	}

	// While Model A is blocked in startPostAdmit, Model B must complete its
	// full AdmitAndStart (admission + slot + store.Create + spawn). With a
	// global lock held by A across the whole call, B would never proceed.
	select {
	case rB := <-resB:
		if rB.err != nil {
			t.Fatalf("model-b AdmitAndStart failed while model-a was blocked in startPostAdmit: %v", rB.err)
		}
		if rB.inst == nil {
			t.Fatal("model-b returned nil instance")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("model-b did not complete while model-a was blocked in startPostAdmit — suggests global lock serialization")
	}

	// Release Model A.
	close(store.blockCh)

	select {
	case rA := <-resA:
		if rA.err != nil {
			t.Fatalf("model-a AdmitAndStart: %v", rA.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Model A to complete after release")
	}
}

// TestAdmitAndStart_OrphanConflict verifies that AdmitAndStart is rejected
// when an unresolved orphan exists for the model in the repository.
func TestAdmitAndStart_OrphanConflict(t *testing.T) {
	store := newMockStore()
	orphan := &domain.LaunchInstanceEntry{
		ID:      "orphan-1",
		ModelID: "m1",
		State:   string(domain.InstanceStateOrphan),
	}
	if err := store.Create(orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected orphan rejection, got nil")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejOrphan {
		t.Fatalf("reason = %v, want RejOrphan", rej.Reason)
	}
	if rej.ConflictID != "orphan-1" {
		t.Fatalf("ConflictID = %q, want orphan-1", rej.ConflictID)
	}
}

// TestAdmitAndStart_DismissedOrphanNoLongerBlocks verifies that a stale
// (dismissed) orphan does not block admission.
func TestAdmitAndStart_DismissedOrphanNoLongerBlocks(t *testing.T) {
	store := newMockStore()
	stale := &domain.LaunchInstanceEntry{
		ID:      "orphan-1",
		ModelID: "m1",
		State:   string(domain.InstanceStateStale),
	}
	if err := store.Create(stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("expected success (stale orphan does not block), got: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instance, got nil")
	}
}

// TestAdmitAndStart_ResidualStartingRejected verifies that a residual
// instance in starting state (ADR 016 C/D) blocks new admission.
func TestAdmitAndStart_ResidualStartingRejected(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	// Simulate a residual: manually insert a controller in starting state.
	residualID := domain.InstanceID("residual-1")
	residual := &domain.LaunchInstance{
		ID:      residualID,
		ModelID: "m1",
		State:   domain.InstanceStateStarting,
	}
	ctrl := NewInstanceController(residual, store, sup.resolver, sup.broker)
	ctrl.supervisorRef = sup
	sup.mu.Lock()
	sup.instances[residualID] = ctrl
	sup.mu.Unlock()

	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected rejection for residual starting instance")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("reason = %v, want RejInFlight", rej.Reason)
	}
	if rej.ConflictID != residualID {
		t.Fatalf("ConflictID = %q, want %q", rej.ConflictID, residualID)
	}
}

// TestAdmitAndStart_PendingVisibility verifies that once a pending instance
// is in s.instances, a second admission is rejected even before the
// repository Create completes.
func TestAdmitAndStart_PendingVisibility(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	// Insert a pending instance directly (simulates the state immediately
	// after linearization but before store.Create).
	pendingID := domain.InstanceID("pending-1")
	pending := &domain.LaunchInstance{
		ID:      pendingID,
		ModelID: "m1",
		State:   domain.InstanceStatePending,
	}
	ctrl := NewInstanceController(pending, store, sup.resolver, sup.broker)
	ctrl.supervisorRef = sup
	sup.mu.Lock()
	sup.instances[pendingID] = ctrl
	sup.mu.Unlock()

	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected rejection for pending instance")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("reason = %v, want RejInFlight", rej.Reason)
	}
	if rej.ConflictID != pendingID {
		t.Fatalf("ConflictID = %q, want %q", rej.ConflictID, pendingID)
	}
}

// TestAdmitAndStart_StructuredRejectionFields verifies the structured
// rejection carries the correct reason and conflict ID.
func TestAdmitAndStart_StructuredRejectionFields(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst1, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	_, err = sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T", err)
	}
	if rej.Reason != RejInFlight {
		t.Errorf("Reason = %v, want RejInFlight", rej.Reason)
	}
	if rej.ModelID != "m1" {
		t.Errorf("ModelID = %q, want m1", rej.ModelID)
	}
	if rej.ConflictID != inst1.ID {
		t.Errorf("ConflictID = %q, want %q", rej.ConflictID, inst1.ID)
	}
	if rej.Error() == "" {
		t.Error("Error() returned empty string")
	}
}

// TestAdmitAndStart_Shutdown verifies that AdmitAndStart is rejected when
// the lifecycle context is cancelled.
func TestAdmitAndStart_Shutdown(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	ctx, cancel := context.WithCancel(context.Background())
	sup := NewSupervisorWithContext(ctx, store)
	sup.maxConcurrent = cfg.MaxConcurrent
	sup.semaphore = newSemaphore(cfg.MaxConcurrent)
	sup.broker = NewLogBroker(cfg.LogBufferSize)
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

	cancel()

	_, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, nil, nil)
	if err == nil {
		t.Fatal("expected shutdown rejection")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejShuttingDown {
		t.Fatalf("reason = %v, want RejShuttingDown", rej.Reason)
	}
}

// TestStart_TransitionalStillWorks verifies the old Supervisor.Start entry
// point still works for transitional callers (Slice B/C not yet migrated).
func TestStart_TransitionalStillWorks(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	inst, err := sup.Start(context.Background(), model, rt, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("transitional Start: %v", err)
	}
	if inst == nil {
		t.Fatal("transitional Start returned nil instance")
	}
}

// TestStart_PostAdmitError_CleansInstances verifies that when startPostAdmit
// fails (e.g. slot acquisition timeout), the instance is removed from
// s.instances.
func TestStart_PostAdmitError_CleansInstances(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	// Exhaust the single slot.
	res, _ := sup.acquireSlot(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := sup.Start(ctx, model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected slot timeout error")
	}

	sup.mu.RLock()
	count := len(sup.instances)
	sup.mu.RUnlock()
	if count != 0 {
		t.Fatalf("s.instances count = %d, want 0 (instance should be cleaned up)", count)
	}
	res.Release()
}

// TestAdmitAndStart_Pipeline_SamePipeline_DifferentEntries_SameModel verifies
// ADR 013: repeated ModelID entries inside ONE pipeline remain independently
// launchable.
func TestAdmitAndStart_Pipeline_SamePipeline_DifferentEntries_SameModel(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "e1"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	owner1 := domain.PipelineOwner("p1", "e1")
	inst1, err := sup.AdmitAndStart(context.Background(), model, rt, owner1, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("entry e1: %v", err)
	}
	if inst1 == nil {
		t.Fatal("entry e1: nil instance")
	}

	model2 := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "e2"}
	owner2 := domain.PipelineOwner("p1", "e2")
	inst2, err := sup.AdmitAndStart(context.Background(), model2, rt, owner2, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("entry e2 (same pipeline, different entry): expected success, got: %v", err)
	}
	if inst2 == nil {
		t.Fatal("entry e2: nil instance")
	}
}

// TestAdmitAndStart_Pipeline_SameEntry_Rejected verifies that a second
// admission for the same pipeline+entry is rejected.
func TestAdmitAndStart_Pipeline_SameEntry_Rejected(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "e1"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	owner := domain.PipelineOwner("p1", "e1")

	inst1, err := sup.AdmitAndStart(context.Background(), model, rt, owner, []string{"-sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	_, err = sup.AdmitAndStart(context.Background(), model, rt, owner, nil, nil)
	if err == nil {
		t.Fatal("second same-entry: expected rejection")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("reason = %v, want RejInFlight", rej.Reason)
	}
	if rej.PipelineID != "p1" || rej.EntryID != "e1" {
		t.Fatalf("conflict owner = (%q, %q), want (p1, e1)", rej.PipelineID, rej.EntryID)
	}
	_ = inst1
}

// TestAdmitAndStart_Pipeline_CrossPipeline_SameModel verifies that two
// different pipelines launching the same model result in exactly one
// admission and one rejection.
func TestAdmitAndStart_Pipeline_CrossPipeline_SameModel(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	owner1 := domain.PipelineOwner("p1", "e1")
	owner2 := domain.PipelineOwner("p2", "e2")

	var wg sync.WaitGroup
	type result struct {
		inst *domain.LaunchInstance
		err  error
	}
	results := make([]result, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		inst, err := sup.AdmitAndStart(context.Background(), model, rt, owner1, []string{"-sleep", "30"}, nil)
		results[0] = result{inst: inst, err: err}
	}()
	go func() {
		defer wg.Done()
		inst, err := sup.AdmitAndStart(context.Background(), model, rt, owner2, []string{"-sleep", "30"}, nil)
		results[1] = result{inst: inst, err: err}
	}()
	wg.Wait()

	var successes, rejections int
	for _, r := range results {
		if r.err == nil {
			successes++
		} else {
			rejections++
			var rej *AdmissionRejection
			if errors.As(r.err, &rej) && rej.Reason != RejInFlight {
				t.Fatalf("unexpected rejection reason: %v", rej.Reason)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if rejections != 1 {
		t.Fatalf("rejections = %d, want 1", rejections)
	}
}

// TestAdmitAndStart_Manual_Pipeline_SameModel verifies the manual × pipeline
// race: exactly one incompatible owner passes admission.
func TestAdmitAndStart_Manual_Pipeline_SameModel(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}

	ownerPipeline := domain.PipelineOwner("p1", "e1")

	var wg sync.WaitGroup
	type result struct {
		inst *domain.LaunchInstance
		err  error
	}
	results := make([]result, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		inst, err := sup.AdmitAndStart(context.Background(), model, rt, domain.ManualOwner, []string{"-sleep", "30"}, nil)
		results[0] = result{inst: inst, err: err}
	}()
	go func() {
		defer wg.Done()
		inst, err := sup.AdmitAndStart(context.Background(), model, rt, ownerPipeline, []string{"-sleep", "30"}, nil)
		results[1] = result{inst: inst, err: err}
	}()
	wg.Wait()

	var successes, rejections int
	for _, r := range results {
		if r.err == nil {
			successes++
		} else {
			rejections++
			var rej *AdmissionRejection
			if errors.As(r.err, &rej) && rej.Reason != RejInFlight {
				t.Fatalf("unexpected rejection reason: %v", rej.Reason)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if rejections != 1 {
		t.Fatalf("rejections = %d, want 1", rejections)
	}
}

// TestAdmitAndStart_Pipeline_Orphan verifies that a pipeline launch is
// rejected when an unresolved orphan exists for the model.
func TestAdmitAndStart_Pipeline_Orphan(t *testing.T) {
	store := newMockStore()
	orphan := &domain.LaunchInstanceEntry{
		ID: "orphan-p", ModelID: "m1", State: string(domain.InstanceStateOrphan),
	}
	if err := store.Create(orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "e1"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	owner := domain.PipelineOwner("p1", "e1")

	_, err := sup.AdmitAndStart(context.Background(), model, rt, owner, nil, nil)
	if err == nil {
		t.Fatal("expected orphan rejection")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejOrphan {
		t.Fatalf("reason = %v, want RejOrphan", rej.Reason)
	}
	if rej.ConflictID != "orphan-p" {
		t.Fatalf("ConflictID = %q, want orphan-p", rej.ConflictID)
	}
}

// TestAdmitAndStart_Pipeline_ResidualStarting verifies that a pipeline
// admission is rejected while a residual starting instance exists.
func TestAdmitAndStart_Pipeline_ResidualStarting(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "e1"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	owner := domain.PipelineOwner("p1", "e1")

	residualID := domain.InstanceID("residual-p")
	residual := &domain.LaunchInstance{
		ID: residualID, ModelID: "m1", State: domain.InstanceStateStarting,
		PipelineID: "p1", PipelineEntryID: "e1",
	}
	ctrl := NewInstanceController(residual, store, sup.resolver, sup.broker)
	ctrl.supervisorRef = sup
	sup.mu.Lock()
	sup.instances[residualID] = ctrl
	sup.mu.Unlock()

	_, err := sup.AdmitAndStart(context.Background(), model, rt, owner, nil, nil)
	if err == nil {
		t.Fatal("expected rejection for residual")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("reason = %v, want RejInFlight", rej.Reason)
	}
}

// TestAdmitAndStart_Pipeline_LegacyUnattributed verifies that a legacy
// unattributed pipeline instance (empty PipelineEntryID) blocks a new
// pipeline entry admission for the same model.
func TestAdmitAndStart_Pipeline_LegacyUnattributed(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := newTestSupervisor(t, store, cfg)
	model := &domain.Model{ID: "m1", Name: "m1", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "e1"}
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: buildFakeRuntimeForTest(t)}
	owner := domain.PipelineOwner("p1", "e1")

	legacyID := domain.InstanceID("legacy-1")
	legacy := &domain.LaunchInstance{
		ID: legacyID, ModelID: "m1", State: domain.InstanceStateRunning,
		PipelineID: "p1", PipelineEntryID: "",
	}
	ctrl := NewInstanceController(legacy, store, sup.resolver, sup.broker)
	ctrl.supervisorRef = sup
	sup.mu.Lock()
	sup.instances[legacyID] = ctrl
	sup.mu.Unlock()

	_, err := sup.AdmitAndStart(context.Background(), model, rt, owner, nil, nil)
	if err == nil {
		t.Fatal("expected rejection for legacy unattributed instance")
	}
	var rej *AdmissionRejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *AdmissionRejection, got %T: %v", err, err)
	}
	if rej.Reason != RejInFlight {
		t.Fatalf("reason = %v, want RejInFlight", rej.Reason)
	}
	if rej.ConflictID != legacyID {
		t.Fatalf("ConflictID = %q, want %q", rej.ConflictID, legacyID)
	}
}

// TestCompatible_Symmetry verifies that compatible(A, B) == compatible(B, A)
// for all relevant owner combinations.
func TestCompatible_Symmetry(t *testing.T) {
	owners := []domain.LaunchOwner{
		domain.ManualOwner,
		domain.PipelineOwner("p1", "e1"),
		domain.PipelineOwner("p1", "e2"),
		domain.PipelineOwner("p2", "e1"),
	}
	for i, a := range owners {
		for j, b := range owners {
			// Build a fake existing instance for owner b.
			existing := &domain.LaunchInstance{ID: "x", ModelID: "m"}
			if b.Kind == domain.OwnerPipeline {
				existing.PipelineID = b.PipelineID
				existing.PipelineEntryID = b.PipelineEntryID
			}
			// For symmetry, also build existing for a.
			existingA := &domain.LaunchInstance{ID: "x", ModelID: "m"}
			if a.Kind == domain.OwnerPipeline {
				existingA.PipelineID = a.PipelineID
				existingA.PipelineEntryID = a.PipelineEntryID
			}
			ab := compatible(a, existing)
			ba := compatible(b, existingA)
			if ab != ba {
				t.Errorf("compatible not symmetric: owners[%d]=%+v, owners[%d]=%+v: a->b=%v b->a=%v", i, a, j, b, ab, ba)
			}
		}
	}
}
