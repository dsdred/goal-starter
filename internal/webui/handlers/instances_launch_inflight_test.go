package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
	})

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

	instA, err := sup.Start(ctx, modelA, rt, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := waitForSupervisorState(t, sup, instA.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}

	// Start B in a goroutine (blocks on slot acquisition).
	go func() {
		_, _ = sup.Start(ctx, modelB, rt, []string{"-sleep", "60"}, nil)
	}()

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
	inst, err := sup.Start(ctx, model, rt, []string{"-sleep", "60"}, nil)
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
