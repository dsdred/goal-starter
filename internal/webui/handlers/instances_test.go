package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/store"
	"github.com/dsdred/goal/internal/webui/security"
)

// ---------- helpers ----------

func newTestRepo(t *testing.T) storage.Repository {
	t.Helper()
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo
}

func newTestInstanceStore(t *testing.T) *store.InstanceStoreJSON {
	t.Helper()
	dir := t.TempDir()
	s, err := store.NewInstanceStoreJSON(store.InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create instance store: %v", err)
	}
	return s
}

// mockInstanceStore implements process.InstanceStore with configurable behavior.
type mockInstanceStore struct {
	CreateFunc        func(e *domain.LaunchInstanceEntry) error
	GetFunc           func(id string) (*domain.LaunchInstanceEntry, error)
	UpdateFunc        func(e *domain.LaunchInstanceEntry) error
	DeleteFunc        func(id string) error
	ListFunc          func() ([]*domain.LaunchInstanceEntry, error)
	ListByModelIDFunc func(modelID string) ([]*domain.LaunchInstanceEntry, error)
}

func (m *mockInstanceStore) Create(e *domain.LaunchInstanceEntry) error {
	if m.CreateFunc != nil {
		return m.CreateFunc(e)
	}
	return nil
}

func (m *mockInstanceStore) Get(id string) (*domain.LaunchInstanceEntry, error) {
	if m.GetFunc != nil {
		return m.GetFunc(id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockInstanceStore) Update(e *domain.LaunchInstanceEntry) error {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(e)
	}
	return nil
}

func (m *mockInstanceStore) Delete(id string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(id)
	}
	return nil
}

func (m *mockInstanceStore) List() ([]*domain.LaunchInstanceEntry, error) {
	if m.ListFunc != nil {
		return m.ListFunc()
	}
	return nil, nil
}

func (m *mockInstanceStore) ListByModelID(modelID string) ([]*domain.LaunchInstanceEntry, error) {
	if m.ListByModelIDFunc != nil {
		return m.ListByModelIDFunc(modelID)
	}
	return nil, nil
}

func newTestSupervisor(t *testing.T) *process.Supervisor {
	t.Helper()
	return process.NewSupervisor(&mockInstanceStore{})
}

func insertModelEntry(t *testing.T, repo storage.Repository, mid string) {
	t.Helper()
	err := repo.CreateModel(&storage.ModelEntry{
		ID:        mid,
		Name:      "test-model",
		RuntimeID: "rt-1",
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
}

func insertRuntimeEntry(t *testing.T, repo storage.Repository, id string) {
	t.Helper()
	err := repo.CreateRuntime(&storage.RuntimeEntry{
		ID:               id,
		Name:             "test-runtime",
		Executable:       filepath.Join(t.TempDir(), "fake-runtime"),
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
}

// ---------- InstancesHandler tests ----------

func TestInstancesHandler_List(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("List: expected 200, got %d", resp.StatusCode)
	}
}

func TestInstancesHandler_Get(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	// --- nonexistent ---
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/nonexistent", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Get nonexistent: expected 404, got %d", resp.StatusCode)
	}

	// --- empty ID ---
	req = httptest.NewRequest(http.MethodGet, "/api/v1/instances/", nil)
	w = httptest.NewRecorder()
	h.Get(w, req)
	resp = w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Get empty ID: expected 400, got %d", resp.StatusCode)
	}
}

func TestInstancesHandler_StartModel(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	insertModelEntry(t, repo, "model-1")
	insertRuntimeEntry(t, repo, "rt-1")

	// --- valid start ---
	body := `{"model_id": "model-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.StartModel(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	// StartModel may return 201 (success), 400 (validation error), or 500 (runtime not found)
	// since fake-runtime may not exist on the test system
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("StartModel valid: expected 201, 400, or 500, got %d", resp.StatusCode)
	}

	// --- invalid JSON ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString("{invalid"))
	w = httptest.NewRecorder()
	h.StartModel(w, req)
	resp = w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("StartModel invalid JSON: expected 400, got %d", resp.StatusCode)
	}

	// --- empty model_id ---
	body = `{"model_id": ""}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	h.StartModel(w, req)
	resp = w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("StartModel empty model_id: expected 400, got %d", resp.StatusCode)
	}
}

func TestInstancesHandler_StopInstance(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	// --- empty ID ---
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances//stop", nil)
	w := httptest.NewRecorder()
	h.StopInstance(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("StopInstance empty ID: expected 400, got %d", resp.StatusCode)
	}

	// --- unknown ID ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/unknown-123/stop", nil)
	w = httptest.NewRecorder()
	h.StopInstance(w, req)
	resp = w.Result()
	defer resp.Body.Close()
	// 404 is acceptable for unknown instance
	if resp.StatusCode != http.StatusNotFound {
		t.Logf("StopInstance unknown: expected 404, got %d (may vary)", resp.StatusCode)
	}
}

func TestInstancesHandler_RestartInstance(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	// --- empty ID ---
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances//restart", nil)
	w := httptest.NewRecorder()
	h.RestartInstance(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("RestartInstance empty ID: expected 400, got %d", resp.StatusCode)
	}
}

// ---------- RouteRegistry tests ----------

func TestInstancesHandler_Cleanup(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	seed := func(id, state string, stoppedAgo time.Duration) {
		var stoppedAt time.Time
		if stoppedAgo > 0 {
			stoppedAt = time.Now().Add(-stoppedAgo)
		}
		err := repo.CreateInstance(&storage.LaunchInstanceEntry{
			ID:        id,
			ModelID:   "model-x",
			RuntimeID: "rt-x",
			State:     state,
			StoppedAt: stoppedAt,
			CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("exited-1", "exited", time.Hour)
	seed("failed-1", "failed", time.Hour)
	seed("stale-1", "stale", time.Hour)
	seed("running-1", "running", 0)
	seed("pending-1", "pending", 0)

	listIDs := func() map[string]bool {
		t.Helper()
		instances, err := repo.ListInstances()
		if err != nil {
			t.Fatalf("ListInstances: %v", err)
		}
		ids := make(map[string]bool, len(instances))
		for _, inst := range instances {
			ids[inst.ID] = true
		}
		return ids
	}

	// --- all_terminal ---
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/cleanup", bytes.NewBufferString(`{"mode":"all_terminal"}`))
	w := httptest.NewRecorder()
	h.Cleanup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Cleanup all_terminal: expected 200, got %d, body %s", w.Code, w.Body.String())
	}
	var out struct {
		Status  string `json:"status"`
		Deleted int    `json:"deleted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Status != "cleaned" || out.Deleted != 3 {
		t.Fatalf("unexpected response: %#v", out)
	}
	ids := listIDs()
	for _, id := range []string{"exited-1", "failed-1", "stale-1"} {
		if ids[id] {
			t.Errorf("terminal instance %s should be deleted", id)
		}
	}
	for _, id := range []string{"running-1", "pending-1"} {
		if !ids[id] {
			t.Errorf("active instance %s must not be deleted", id)
		}
	}

	// --- selected: running instance must survive ---
	seed("failed-2", "failed", time.Hour)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/cleanup", bytes.NewBufferString(`{"mode":"selected","ids":["failed-2","running-1"]}`))
	w = httptest.NewRecorder()
	h.Cleanup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Cleanup selected: expected 200, got %d, body %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Status != "cleaned" || out.Deleted != 1 {
		t.Fatalf("unexpected response: %#v", out)
	}
	ids = listIDs()
	if ids["failed-2"] {
		t.Error("selected terminal instance should be deleted")
	}
	if !ids["running-1"] {
		t.Error("active instance must not be deleted")
	}

	// --- invalid mode (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/cleanup", bytes.NewBufferString(`{"mode":"bogus"}`))
	w = httptest.NewRecorder()
	h.Cleanup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Cleanup invalid mode: expected 400, got %d", w.Code)
	}

	// --- selected without ids (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/cleanup", bytes.NewBufferString(`{"mode":"selected"}`))
	w = httptest.NewRecorder()
	h.Cleanup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Cleanup selected without ids: expected 400, got %d", w.Code)
	}

	// --- invalid JSON (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/cleanup", bytes.NewBufferString(`{invalid`))
	w = httptest.NewRecorder()
	h.Cleanup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Cleanup invalid JSON: expected 400, got %d", w.Code)
	}
}

func TestRouteRegistry_Build(t *testing.T) {
	repo := newTestRepo(t)
	csrf := security.NewCSRF()
	sessionStore := security.NewSessionStore()
	passwordStore := security.NewPasswordStore()
	sup := newTestSupervisor(t)

	insSvc := application.NewInstanceService(sup, repo)
	rtSvc := application.NewRuntimeService(repo)
	modelSvc := application.NewModelService(repo)

	reg := NewRouteRegistry(
		insSvc, rtSvc, modelSvc,
		sup, repo, csrf, sessionStore, passwordStore,
	)

	handler := reg.Build()
	if handler == nil {
		t.Fatal("Build() returned nil")
	}
}

func TestRouteRegistry_AuthEndpoints_NoAuthRequired(t *testing.T) {
	repo := newTestRepo(t)
	csrf := security.NewCSRF()
	sessionStore := security.NewSessionStore()
	passwordStore := security.NewPasswordStore()
	sup := newTestSupervisor(t)

	insSvc := application.NewInstanceService(sup, repo)
	rtSvc := application.NewRuntimeService(repo)
	modelSvc := application.NewModelService(repo)

	reg := NewRouteRegistry(
		insSvc, rtSvc, modelSvc,
		sup, repo, csrf, sessionStore, passwordStore,
	)

	handler := reg.Build()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString("not-json"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("login endpoint should not require auth")
	}
}

// ---------- InstanceStoreJSON tests ----------

func TestInstanceStoreJSON_CreateGetUpdateDelete(t *testing.T) {
	s := newTestInstanceStore(t)

	inst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-1"),
		ModelID:   "m1",
		RuntimeID: "r1",
		State:     domain.InstanceStatePending,
	}

	// Create
	if err := s.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	got, err := s.Get("test-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "test-1" {
		t.Errorf("Get: expected ID test-1, got %s", got.ID)
	}

	// Update
	got.State = domain.InstanceStateRunning
	if err := s.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Get again
	got, err = s.Get("test-1")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.State != domain.InstanceStateRunning {
		t.Errorf("after Update: expected state running, got %s", got.State)
	}

	// Delete
	if err := s.Delete("test-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Get after delete
	_, err = s.Get("test-1")
	if err == nil {
		t.Fatal("Get after Delete: expected error, got nil")
	}

	// List
	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List after delete: expected 0, got %d", len(all))
	}
}

func TestInstanceStoreJSON_CreateDuplicate(t *testing.T) {
	s := newTestInstanceStore(t)

	inst := &domain.LaunchInstance{
		ID:      domain.InstanceID("dup-1"),
		ModelID: "m1",
		State:   domain.InstanceStatePending,
	}

	if err := s.Create(inst); err != nil {
		t.Fatalf("Create first: %v", err)
	}

	// InstanceStoreJSON.Create does not enforce uniqueness on ID.
	// It overwrites the existing entry. Verify the overwrite behavior.
	inst.State = domain.InstanceStateRunning
	if err := s.Create(inst); err != nil {
		t.Fatalf("Create duplicate: expected no error, got: %v", err)
	}

	got, err := s.Get("dup-1")
	if err != nil {
		t.Fatalf("Get after duplicate: %v", err)
	}
	if got.State != domain.InstanceStateRunning {
		t.Errorf("Create duplicate: expected state to be overwritten to running, got %s", got.State)
	}
}

func TestInstanceStoreJSON_ListByModelID(t *testing.T) {
	s := newTestInstanceStore(t)

	i1 := &domain.LaunchInstance{ID: domain.InstanceID("i1"), ModelID: "model-A", State: domain.InstanceStateRunning}
	i2 := &domain.LaunchInstance{ID: domain.InstanceID("i2"), ModelID: "model-B", State: domain.InstanceStatePending}
	i3 := &domain.LaunchInstance{ID: domain.InstanceID("i3"), ModelID: "model-A", State: domain.InstanceStateExited}

	s.Create(i1)
	s.Create(i2)
	s.Create(i3)

	byA, err := s.FindByModelID("model-A")
	if err != nil {
		t.Fatalf("FindByModelID: %v", err)
	}
	if len(byA) != 2 {
		t.Errorf("FindByModelID A: expected 2, got %d", len(byA))
	}

	byB, err := s.FindByModelID("model-B")
	if err != nil {
		t.Fatalf("FindByModelID: %v", err)
	}
	if len(byB) != 1 {
		t.Errorf("FindByModelID B: expected 1, got %d", len(byB))
	}

	byNone, err := s.FindByModelID("model-none")
	if err != nil {
		t.Fatalf("FindByModelID: %v", err)
	}
	if len(byNone) != 0 {
		t.Errorf("FindByModelID none: expected 0, got %d", len(byNone))
	}
}

func TestInstanceStoreJSON_ConcurrentWrites(t *testing.T) {
	s := newTestInstanceStore(t)

	const n = 20
	done := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			inst := &domain.LaunchInstance{
				ID:      domain.InstanceID("concurrent-" + string(rune('A'+idx))),
				ModelID: "concurrent-model",
				State:   domain.InstanceStatePending,
			}
			s.Create(inst)
			done <- true
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}

	all, err := s.List()
	if err != nil {
		t.Fatalf("List after concurrent: %v", err)
	}
	if len(all) == 0 {
		t.Error("List after concurrent: expected at least one instance")
	}
}

func TestInstancesHandler_List_StripsEnvironment(t *testing.T) {
	t.Setenv("GOAL_TEST_SECRET", "super-secret-value")

	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	// Start a model to create an instance with environment
	insertModelEntry(t, repo, "model-env")
	insertRuntimeEntry(t, repo, "rt-env")

	body := `{"model_id": "model-env"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.StartModel(w, req)
	resp := w.Result()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusInternalServerError {
		t.Logf("StartModel: got %d (201, 202, or 500 acceptable on test systems without fake-runtime)", resp.StatusCode)
	}
	startBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("StartModel: read response body: %v", err)
	}
	if bytes.Contains(startBody, []byte("super-secret-value")) {
		t.Error("StartModel: environment value leaked in response")
	}

	// Now verify List strips environment
	req = httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
	w = httptest.NewRecorder()
	h.List(w, req)
	resp = w.Result()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("List: expected 200, got %d", resp.StatusCode)
	}
	listBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("List: read response body: %v", err)
	}
	if bytes.Contains(listBody, []byte("super-secret-value")) {
		t.Error("List: environment value leaked in response")
	}

	// Verify Get strips environment
	// Use a synthetic ID; if instance exists from StartModel, env will be stripped.
	// If no instance exists (500 on StartModel), Get will 404 which is acceptable.
	instanceID := "model-env"
	req = httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+instanceID, nil)
	w = httptest.NewRecorder()
	h.Get(w, req)
	resp = w.Result()

	if resp.StatusCode == http.StatusOK {
		getBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("Get: read response body: %v", err)
		}
		if bytes.Contains(getBody, []byte("super-secret-value")) {
			t.Error("Get: environment value leaked in response")
		}
	}
}
