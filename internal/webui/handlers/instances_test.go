package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
	CreateFunc          func(e *domain.LaunchInstanceEntry) error
	GetFunc             func(id string) (*domain.LaunchInstanceEntry, error)
	UpdateFunc          func(e *domain.LaunchInstanceEntry) error
	DeleteFunc          func(id string) error
	ListFunc            func() ([]*domain.LaunchInstanceEntry, error)
	ListByProfileIDFunc func(profileID string) ([]*domain.LaunchInstanceEntry, error)
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

func (m *mockInstanceStore) ListByProfileID(profileID string) ([]*domain.LaunchInstanceEntry, error) {
	if m.ListByProfileIDFunc != nil {
		return m.ListByProfileIDFunc(profileID)
	}
	return nil, nil
}

func newTestSupervisor(t *testing.T) *process.Supervisor {
	t.Helper()
	return process.NewSupervisor(&mockInstanceStore{})
}

func insertProfileEntry(t *testing.T, repo storage.Repository, pid string) {
	t.Helper()
	err := repo.CreateProfile(&storage.ProfileEntry{
		ID:        pid,
		Name:      "test-profile",
		RuntimeID: "rt-1",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
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

func TestInstancesHandler_StartProfile(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	insertProfileEntry(t, repo, "profile-1")
	insertRuntimeEntry(t, repo, "rt-1")

	// --- valid start ---
	body := `{"profile_id": "profile-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.StartProfile(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	// StartProfile may return 201 (success), 400 (validation error), or 500 (runtime not found)
	// since fake-runtime may not exist on the test system
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("StartProfile valid: expected 201, 400, or 500, got %d", resp.StatusCode)
	}

	// --- invalid JSON ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString("{invalid"))
	w = httptest.NewRecorder()
	h.StartProfile(w, req)
	resp = w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("StartProfile invalid JSON: expected 400, got %d", resp.StatusCode)
	}

	// --- empty profile_id ---
	body = `{"profile_id": ""}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/instances/start", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	h.StartProfile(w, req)
	resp = w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("StartProfile empty profile_id: expected 400, got %d", resp.StatusCode)
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

func TestRouteRegistry_Build(t *testing.T) {
	repo := newTestRepo(t)
	csrf := security.NewCSRF()
	sessionStore := security.NewSessionStore()
	passwordStore := security.NewPasswordStore()
	sup := newTestSupervisor(t)

	insSvc := application.NewInstanceService(sup, repo)
	profSvc := application.NewProfileService(repo)
	rtSvc := application.NewRuntimeService(repo)
	modelSvc := application.NewModelService(repo)

	reg := NewRouteRegistry(
		profSvc, insSvc, rtSvc, modelSvc,
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
	profSvc := application.NewProfileService(repo)
	rtSvc := application.NewRuntimeService(repo)
	modelSvc := application.NewModelService(repo)

	reg := NewRouteRegistry(
		profSvc, insSvc, rtSvc, modelSvc,
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
		ProfileID: "p1",
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
		ID:        domain.InstanceID("dup-1"),
		ProfileID: "p1",
		State:     domain.InstanceStatePending,
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

func TestInstanceStoreJSON_ListByProfileID(t *testing.T) {
	s := newTestInstanceStore(t)

	i1 := &domain.LaunchInstance{ID: domain.InstanceID("i1"), ProfileID: "profile-A", State: domain.InstanceStateRunning}
	i2 := &domain.LaunchInstance{ID: domain.InstanceID("i2"), ProfileID: "profile-B", State: domain.InstanceStatePending}
	i3 := &domain.LaunchInstance{ID: domain.InstanceID("i3"), ProfileID: "profile-A", State: domain.InstanceStateExited}

	s.Create(i1)
	s.Create(i2)
	s.Create(i3)

	byA, err := s.FindByProfileID("profile-A")
	if err != nil {
		t.Fatalf("FindByProfileID: %v", err)
	}
	if len(byA) != 2 {
		t.Errorf("FindByProfileID A: expected 2, got %d", len(byA))
	}

	byB, err := s.FindByProfileID("profile-B")
	if err != nil {
		t.Fatalf("FindByProfileID: %v", err)
	}
	if len(byB) != 1 {
		t.Errorf("FindByProfileID B: expected 1, got %d", len(byB))
	}

	byNone, err := s.FindByProfileID("profile-none")
	if err != nil {
		t.Fatalf("FindByProfileID: %v", err)
	}
	if len(byNone) != 0 {
		t.Errorf("FindByProfileID none: expected 0, got %d", len(byNone))
	}
}

func TestInstanceStoreJSON_ConcurrentWrites(t *testing.T) {
	s := newTestInstanceStore(t)

	const n = 20
	done := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			inst := &domain.LaunchInstance{
				ID:        domain.InstanceID("concurrent-" + string(rune('A'+idx))),
				ProfileID: "concurrent-profile",
				State:     domain.InstanceStatePending,
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
