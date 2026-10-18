package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

func newMockInstanceStoreForTest() *mockInstanceStore {
	entries := make(map[string]*domain.LaunchInstanceEntry)
	m := &mockInstanceStore{}
	m.CreateFunc = func(e *domain.LaunchInstanceEntry) error {
		entries[e.ID] = e
		return nil
	}
	m.GetFunc = func(id string) (*domain.LaunchInstanceEntry, error) {
		if e, ok := entries[id]; ok {
			return e, nil
		}
		return nil, errors.New("not found")
	}
	m.UpdateFunc = func(e *domain.LaunchInstanceEntry) error {
		entries[e.ID] = e
		return nil
	}
	m.ListFunc = func() ([]*domain.LaunchInstanceEntry, error) {
		result := make([]*domain.LaunchInstanceEntry, 0, len(entries))
		for _, e := range entries {
			result = append(result, e)
		}
		return result, nil
	}
	m.ListByModelIDFunc = func(modelID string) ([]*domain.LaunchInstanceEntry, error) {
		var result []*domain.LaunchInstanceEntry
		for _, e := range entries {
			if e.ModelID == modelID {
				result = append(result, e)
			}
		}
		return result, nil
	}
	return m
}

// newPendingWindowFixture creates a handler with a slot-limited supervisor
// and a real pending instance (second model waiting for the only slot).
func newPendingWindowFixture(t *testing.T) (*InstancesHandler, *ModelsHandler, *process.Supervisor, domain.InstanceID, domain.InstanceID) {
	t.Helper()
	repo := newTestRepo(t)
	insStore := newMockInstanceStoreForTest()

	cfg := process.SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := process.NewSupervisorWithConfig(insStore, cfg)

	fakePath := fakeruntime.Path(t)
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}
	modelA := &domain.Model{ID: "mA", Name: "A", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "mB", Name: "B", RuntimeID: "rt"}

	ctx := context.Background()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mA", Name: "A", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model A: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mB", Name: "B", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model B: %v", err)
	}

	instA, err := sup.AdmitAndStart(ctx, modelA, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := waitForSupervisorState(t, sup, instA.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}

	// Start B in a goroutine (blocks on slot acquisition).
	// The pendingStartCtx is cancelled before Shutdown so the goroutine
	// cannot spawn a process after cleanup begins.
	pendingStartCtx, cancelPendingStart := context.WithCancel(ctx)
	pendingDone := make(chan struct{})
	go func() {
		defer close(pendingDone)
		_, _ = sup.AdmitAndStart(pendingStartCtx, modelB, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	}()

	t.Cleanup(func() {
		cancelPendingStart()
		select {
		case <-pendingDone:
		case <-time.After(5 * time.Second):
			t.Errorf("timeout waiting for pending start goroutine to finish")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(shutdownCtx)
	})

	// Wait for B to reach pending.
	var instBID domain.InstanceID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		insts, _ := sup.List()
		for _, inst := range insts {
			if inst.ModelID == "mB" && inst.State == domain.InstanceStatePending {
				instBID = inst.ID
				break
			}
		}
		if instBID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if instBID == "" {
		t.Fatal("timeout waiting for B to reach pending")
	}

	insSvc := application.NewInstanceService(sup, repo)
	modelSvc := application.NewModelService(repo)
	csrf := security.NewCSRF()
	hInst := NewInstancesHandler(insSvc, csrf)
	hModels := NewModelsHandler(modelSvc, insSvc, sup, repo, csrf)

	return hInst, hModels, sup, instA.ID, instBID
}

func waitForSupervisorState(t *testing.T, sup *process.Supervisor, id domain.InstanceID, target domain.InstanceState, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		snap, err := sup.Status(id)
		if err != nil {
			return err
		}
		if snap.State == target {
			return nil
		}
		<-ticker.C
	}
	return errors.New("timeout waiting for state " + string(target))
}

func TestInstancesHandler_Stop_Pending_Returns409(t *testing.T) {
	hInst, _, _, _, instBID := newPendingWindowFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/"+string(instBID)+"/stop", nil)
	w := httptest.NewRecorder()
	hInst.StopInstance(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}
}

func TestInstancesHandler_Restart_Pending_Returns409(t *testing.T) {
	hInst, _, _, _, instBID := newPendingWindowFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/"+string(instBID)+"/restart", nil)
	w := httptest.NewRecorder()
	hInst.RestartInstance(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}
}

func TestModelsHandler_Start_DuplicateInFlight_Returns409(t *testing.T) {
	_, hModels, _, _, _ := newPendingWindowFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mB/start", nil)
	w := httptest.NewRecorder()
	hModels.Start(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}
}

func TestModelsHandler_Start_NoInFlight_Allowed(t *testing.T) {
	repo := newTestRepo(t)
	fakePath := fakeruntime.Path(t)
	insStore := newMockInstanceStoreForTest()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mC", Name: "C", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
	})

	insSvc := application.NewInstanceService(sup, repo)
	modelSvc := application.NewModelService(repo)
	csrf := security.NewCSRF()
	hModels := NewModelsHandler(modelSvc, insSvc, sup, repo, csrf)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mC/start", nil)
	w := httptest.NewRecorder()
	hModels.Start(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestModelsHandler_Start_ConcurrentDuplicate_OnlyOneAccepted(t *testing.T) {
	repo := newTestRepo(t)
	fakePath := fakeruntime.Path(t)
	insStore := newMockInstanceStoreForTest()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mX", Name: "X", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
	})

	insSvc := application.NewInstanceService(sup, repo)
	modelSvc := application.NewModelService(repo)
	csrf := security.NewCSRF()
	hModels := NewModelsHandler(modelSvc, insSvc, sup, repo, csrf)

	type result struct {
		code int
		body string
	}
	ch := make(chan result, 2)

	for i := 0; i < 2; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mX/start", nil)
			w := httptest.NewRecorder()
			hModels.Start(w, req)
			ch <- result{code: w.Code, body: w.Body.String()}
		}()
	}

	var codes []int
	for i := 0; i < 2; i++ {
		r := <-ch
		codes = append(codes, r.code)
	}

	accepted := 0
	rejected := 0
	for _, c := range codes {
		if c == http.StatusOK {
			accepted++
		} else if c == http.StatusConflict {
			rejected++
		}
	}
	if accepted != 1 {
		t.Errorf("expected exactly 1 accepted, got %d (codes: %v)", accepted, codes)
	}
	if rejected != 1 {
		t.Errorf("expected exactly 1 rejected with 409, got %d (codes: %v)", rejected, codes)
	}

	insts, _ := sup.List()
	var mXCount int
	for _, inst := range insts {
		if inst.ModelID == "mX" && inst.IsInFlight() {
			mXCount++
		}
	}
	if mXCount != 1 {
		t.Errorf("expected exactly 1 in-flight instance of mX, got %d", mXCount)
	}
}

func TestModelsHandler_Stop_Pending_Returns409(t *testing.T) {
	_, hModels, sup, _, instBID := newPendingWindowFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mB/stop", nil)
	w := httptest.NewRecorder()
	hModels.Stop(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}

	snap, _ := sup.Status(instBID)
	if snap.State != domain.InstanceStatePending {
		t.Errorf("pending instance state = %q, want pending (unchanged)", snap.State)
	}
}

func TestModelsHandler_Restart_Pending_Returns409(t *testing.T) {
	_, hModels, sup, _, instBID := newPendingWindowFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mB/restart", nil)
	w := httptest.NewRecorder()
	hModels.Restart(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}

	insts, _ := sup.List()
	var mBCount int
	for _, inst := range insts {
		if inst.ModelID == "mB" {
			mBCount++
		}
	}
	if mBCount != 1 {
		t.Errorf("expected 1 instance of mB (no new launch), got %d", mBCount)
	}
	snap, _ := sup.Status(instBID)
	if snap.State != domain.InstanceStatePending {
		t.Errorf("pending instance state = %q, want pending (unchanged)", snap.State)
	}
}

func TestModelsHandler_Stop_NonPending_Running_OK(t *testing.T) {
	repo := newTestRepo(t)
	fakePath := fakeruntime.Path(t)
	insStore := newMockInstanceStoreForTest()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mR", Name: "R", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
	})

	ctx := context.Background()
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}
	model := &domain.Model{ID: "mR", Name: "R", RuntimeID: "rt"}
	inst, err := sup.AdmitAndStart(ctx, model, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := waitForSupervisorState(t, sup, inst.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait running: %v", err)
	}

	insSvc := application.NewInstanceService(sup, repo)
	modelSvc := application.NewModelService(repo)
	csrf := security.NewCSRF()
	hModels := NewModelsHandler(modelSvc, insSvc, sup, repo, csrf)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mR/stop", nil)
	w := httptest.NewRecorder()
	hModels.Stop(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestF8LegB_ModelRoute_Returns409AgainstPipelineOwner closes the F-8 Leg B
// evidence gap: an ordered, real-HTTP scenario where a pipeline is started
// first (two entries on the SAME model, both launch as pipeline-owned
// processes), and a manual model-route Start is then requested against that
// already-running model and must be rejected with HTTP 409 launch_in_flight
// WITHOUT persisting a third instance, WITHOUT a third supervisor instance and
// WITHOUT spawning an additional OS process — while both pipeline instances
// stay running and unchanged.
//
// Three observation channels are kept distinct:
//   - persisted instance store (the supervisor's InstanceStore),
//   - in-memory supervisor state (sup.List / sup.Status),
//   - real process-spawn attempts (per-instance argv-file fingerprints).
//
// The argv-file channel is a per-spawn side-effect indicator, NOT a full OS
// process census; the test does not enumerate surviving PIDs by image. The 409
// is raised by the owner-agnostic handler preflight (models.go Start) before
// StartModel, so this certifies the handler barrier for a pipeline-owned
// in-flight; the supervisor compatible(ManualOwner, pipeline) barrier is a
// separate, defense-in-depth guard covered by the ADR 017 supervisor tests.
func TestF8LegB_ModelRoute_Returns409AgainstPipelineOwner(t *testing.T) {
	repo := newTestRepo(t)
	insStore := newMockInstanceStoreForTest()
	fakePath := fakeruntime.Path(t)

	// MaxConcurrent >= 3: a third launch is blocked only by owner-aware
	// arbitration, never by the concurrency semaphore. This isolates the
	// barrier under test.
	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	// Cleanup is registered before any launch so it runs even if an
	// intermediate assertion fails; a failed shutdown or a leaked live
	// instance is reported as a test failure.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sup.Shutdown(ctx); err != nil {
			t.Errorf("supervisor shutdown returned error: %v", err)
		}
		rest, err := sup.List()
		if err != nil {
			t.Errorf("supervisor list after shutdown: %v", err)
		}
		for _, inst := range rest {
			if inst.IsLive() {
				t.Errorf("instance %s left live after shutdown (state=%q)", inst.ID, inst.State)
			}
		}
	})

	dir := t.TempDir()
	pipe1File := filepath.Join(dir, "entry1.argv")
	pipe2File := filepath.Join(dir, "entry2.argv")
	manualFile := filepath.Join(dir, "manual.argv")

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	// The model's own args are the MANUAL-route fingerprint. StartModel
	// resolves model args with customArgs=nil, so a manual spawn would write
	// manualFile. The preflight must reject before StartModel, so this file
	// must never appear.
	if err := repo.CreateModel(&storage.ModelEntry{
		ID: "m1", Name: "M1", RuntimeID: "rt",
		Args: []string{"argv-file", manualFile, "MANUAL"},
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	// Pipeline: two entries referencing the SAME model, each with its own
	// argv-file fingerprint, so each real pipeline spawn is independently
	// observable. The pipeline start path overrides the model args per entry
	// (pipeline_service.go), so neither entry can ever touch manualFile.
	pipeEntry := &storage.PipelineEntry{
		Name: "leg-b",
		Models: []storage.PipelineModel{
			{ModelID: "m1", Args: []string{"argv-file", pipe1File, "ENTRY-1"}},
			{ModelID: "m1", Args: []string{"argv-file", pipe2File, "ENTRY-2"}},
		},
	}
	pipelineSvc := application.NewPipelineService(sup, repo)
	if err := pipelineSvc.CreatePipeline(context.Background(), pipeEntry); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	pipelineID := pipeEntry.ID
	stored, err := repo.GetPipeline(pipelineID)
	if err != nil {
		t.Fatalf("get pipeline: %v", err)
	}
	if len(stored.Models) != 2 || stored.Models[0].ID == "" || stored.Models[1].ID == "" {
		t.Fatalf("expected two pipeline entries with allocated ids, got %+v", stored.Models)
	}
	if stored.Models[0].ID == stored.Models[1].ID {
		t.Fatalf("pipeline entry ids must differ, both = %q", stored.Models[0].ID)
	}
	wantEntryIDs := map[string]bool{stored.Models[0].ID: true, stored.Models[1].ID: true}

	insSvc := application.NewInstanceService(sup, repo)
	modelSvc := application.NewModelService(repo)
	csrf := security.NewCSRF()
	hPipe := NewPipelineHandler(pipelineSvc, repo, insSvc, csrf)
	hModels := NewModelsHandler(modelSvc, insSvc, sup, repo, csrf)

	// ── Channel readers (kept explicit and distinct) ──
	supM1 := func() []*domain.LaunchInstance {
		t.Helper()
		insts, err := sup.List()
		if err != nil {
			t.Fatalf("supervisor list: %v", err)
		}
		var out []*domain.LaunchInstance
		for _, inst := range insts {
			if inst.ModelID == "m1" {
				out = append(out, inst)
			}
		}
		return out
	}
	persistedM1Count := func() int {
		t.Helper()
		entries, err := insStore.List()
		if err != nil {
			t.Fatalf("instance store list: %v", err)
		}
		n := 0
		for _, e := range entries {
			if e.ModelID == "m1" {
				n++
			}
		}
		return n
	}
	mustArgv := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read argv-file %s: %v (a real child process was expected to spawn and write it)", path, err)
		}
		return strings.TrimSpace(string(data))
	}
	// waitArgv polls until the child process has written its fingerprint. The
	// supervisor marks an instance running at spawn, which is a hair before the
	// child's WriteFile lands, so an immediate read would race the child. This
	// is the same bounded-wait technique the permanent restart suite uses for
	// argv-file assertions.
	waitArgv := func(path, needle string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), needle) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("argv-file %s did not contain %q within timeout (no real child process launched?)", path, needle)
	}

	// ── Step 1: real HTTP pipeline Start (PipelineHandler) ──
	pw := httptest.NewRecorder()
	hPipe.Start(pw, httptest.NewRequest(http.MethodPost, "/api/v1/pipelines/"+pipelineID+"/start", nil))
	if pw.Code != http.StatusOK {
		t.Fatalf("pipeline start: expected 200, got %d: %s", pw.Code, pw.Body.String())
	}
	var startRes application.PipelineStartResult
	if err := json.NewDecoder(pw.Body).Decode(&startRes); err != nil {
		t.Fatalf("decode pipeline start: %v", err)
	}
	if startRes.PipelineID != pipelineID {
		t.Errorf("pipeline_id = %q, want %q", startRes.PipelineID, pipelineID)
	}
	if len(startRes.Results) != 2 {
		t.Fatalf("expected 2 entry results, got %d: %+v", len(startRes.Results), startRes.Results)
	}
	instIDs := make([]domain.InstanceID, 0, 2)
	for _, r := range startRes.Results {
		if r.ModelID != "m1" {
			t.Errorf("result model_id = %q, want m1", r.ModelID)
		}
		if !wantEntryIDs[r.EntryID] {
			t.Errorf("result entry_id = %q, not one of the allocated pipeline entries", r.EntryID)
		}
		if r.Status != application.OutcomeStarted {
			t.Fatalf("pipeline entry %q status = %q, want started: both entries must actually launch", r.EntryID, r.Status)
		}
		if r.InstanceID == "" {
			t.Fatalf("started entry %q has no instance_id", r.EntryID)
		}
		instIDs = append(instIDs, domain.InstanceID(r.InstanceID))
	}
	if instIDs[0] == instIDs[1] {
		t.Fatalf("the two pipeline entries must own distinct instances, both = %q", instIDs[0])
	}

	// ── Step 2: require BOTH pipeline-owned instances to reach RUNNING ──
	for _, id := range instIDs {
		if err := waitForSupervisorState(t, sup, id, domain.InstanceStateRunning, 10*time.Second); err != nil {
			t.Fatalf("instance %s did not reach running: %v", id, err)
		}
	}

	// ── Step 3: real-process fingerprints (argv-files) ──
	// These prove two distinct child processes actually executed their
	// resolved argv; a mocked/absent process cannot produce them.
	waitArgv(pipe1File, "ENTRY-1")
	waitArgv(pipe2File, "ENTRY-2")

	// ── Step 4: capture the full pre-request state across all channels ──
	if n := persistedM1Count(); n != 2 {
		t.Fatalf("persisted m1 instances = %d before manual POST, want 2", n)
	}
	preSup := supM1()
	if len(preSup) != 2 {
		t.Fatalf("supervisor m1 instances = %d before manual POST, want 2", len(preSup))
	}
	type snap struct {
		state       domain.InstanceState
		pid         int
		pipelineID  string
		pipelineEnt string
	}
	pre := make(map[domain.InstanceID]snap, 2)
	for _, inst := range preSup {
		if inst.PipelineID != pipelineID || !wantEntryIDs[inst.PipelineEntryID] {
			t.Errorf("instance %s is not pipeline-owned: pipeline_id=%q entry_id=%q", inst.ID, inst.PipelineID, inst.PipelineEntryID)
		}
		if inst.PID <= 0 {
			t.Errorf("instance %s has no live PID (pid=%d): a real process was expected", inst.ID, inst.PID)
		}
		pre[inst.ID] = snap{inst.State, inst.PID, inst.PipelineID, inst.PipelineEntryID}
	}
	if len(pre) != 2 {
		t.Fatalf("expected 2 distinct pipeline-owned m1 instances captured, got %d", len(pre))
	}

	// ── Step 5: EXACTLY ONE manual HTTP POST against the pipeline owner ──
	mw := httptest.NewRecorder()
	hModels.Start(mw, httptest.NewRequest(http.MethodPost, "/api/v1/models/m1/start", nil))
	if mw.Code != http.StatusConflict {
		t.Fatalf("manual start: expected 409, got %d: %s", mw.Code, mw.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(mw.Body).Decode(&body); err != nil {
		t.Fatalf("decode manual start body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("manual error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("manual code = %q, want conflict", body["code"])
	}

	// ── Step 6: nothing changed, held over a bounded settling window to
	// detect delayed or transient divergence ──
	deadline := time.Now().Add(2 * time.Second)
	for {
		if n := persistedM1Count(); n != 2 {
			t.Fatalf("persisted m1 instances = %d after manual POST, want unchanged 2", n)
		}
		postSup := supM1()
		if len(postSup) != 2 {
			t.Fatalf("supervisor m1 instances = %d after manual POST, want unchanged 2", len(postSup))
		}
		for _, inst := range postSup {
			s, ok := pre[inst.ID]
			if !ok {
				t.Fatalf("unexpected new instance %s appeared after manual POST", inst.ID)
			}
			if inst.State != domain.InstanceStateRunning {
				t.Errorf("pipeline instance %s state = %q, want still running", inst.ID, inst.State)
			}
			if inst.PID != s.pid {
				t.Errorf("pipeline instance %s PID = %d, want unchanged %d", inst.ID, inst.PID, s.pid)
			}
			if inst.PipelineID != s.pipelineID || inst.PipelineEntryID != s.pipelineEnt {
				t.Errorf("pipeline instance %s ownership changed: pipeline_id=%q entry_id=%q", inst.ID, inst.PipelineID, inst.PipelineEntryID)
			}
		}
		if _, err := os.Stat(manualFile); !os.IsNotExist(err) {
			t.Fatalf("manual argv-file appeared (stat err=%v): the manual route spawned a process instead of returning 409 before StartModel", err)
		}
		if got := mustArgv(pipe1File); got != "ENTRY-1" {
			t.Fatalf("entry1 argv-file changed to %q after manual POST", got)
		}
		if got := mustArgv(pipe2File); got != "ENTRY-2" {
			t.Fatalf("entry2 argv-file changed to %q after manual POST", got)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}
