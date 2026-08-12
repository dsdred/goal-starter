package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/storage"
)

func TestProfilesHandler_Create_EmptyName(t *testing.T) {
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	profileSvc := application.NewProfileService(repo)
	sup := newTestSupervisor(t)
	h := NewProfilesHandler(profileSvc, nil, sup, nil)

	body := `{"name":"","runtime_id":"rt1","host":"127.0.0.1","port":11434}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Fatal("expected 'error' in response")
	}

	// Verify repository was not mutated.
	profiles, _ := repo.ListProfiles()
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles in repo, got %d", len(profiles))
	}
}

func TestProfilesHandler_Create_Valid(t *testing.T) {
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	profileSvc := application.NewProfileService(repo)
	sup := newTestSupervisor(t)
	h := NewProfilesHandler(profileSvc, nil, sup, nil)

	body := `{"name":"test","runtime_id":"rt1","host":"127.0.0.1","port":11434}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var resp storage.ProfileEntry
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected ID in response")
	}

	// Verify persisted.
	got, err := repo.GetProfile(resp.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("expected name 'test', got '%s'", got.Name)
	}
}

func TestProfilesHandler_Create_JSONDecodeError(t *testing.T) {
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	profileSvc := application.NewProfileService(repo)
	sup := newTestSupervisor(t)
	h := NewProfilesHandler(profileSvc, nil, sup, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(`{invalid`))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	// Verify repository was not mutated.
	profiles, _ := repo.ListProfiles()
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles in repo, got %d", len(profiles))
	}
}
