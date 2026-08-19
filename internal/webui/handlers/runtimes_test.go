package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

func TestRuntimesHandler_Replace(t *testing.T) {
	repo, handler := newV23RuntimeHandler(t)

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "old-rt", Name: "Old", Executable: "old.exe"}); err != nil {
		t.Fatalf("CreateRuntime old: %v", err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "new-rt", Name: "New", Executable: "new.exe"}); err != nil {
		t.Fatalf("CreateRuntime new: %v", err)
	}
	for _, id := range []string{"m-1", "m-2"} {
		if err := repo.CreateModel(&storage.ModelEntry{ID: id, Name: id, RuntimeID: "old-rt"}); err != nil {
			t.Fatalf("CreateModel %s: %v", id, err)
		}
	}

	// --- success ---
	body := `{"new_runtime_id":"new-rt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/old-rt/replace", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.Replace(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Replace: expected 200, got %d, body %s", resp.StatusCode, w.Body.String())
	}
	var out struct {
		Status      string `json:"status"`
		ModelsMoved int    `json:"models_moved"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Status != "replaced" || out.ModelsMoved != 2 {
		t.Fatalf("unexpected response: %#v", out)
	}
	if _, err := repo.GetRuntime("old-rt"); err == nil {
		t.Fatal("old runtime should be deleted")
	}
	m, err := repo.GetModel("m-1")
	if err != nil {
		t.Fatalf("model m-1 should still exist: %v", err)
	}
	if m.RuntimeID != "new-rt" {
		t.Fatalf("model m-1: runtime_id = %q, want %q", m.RuntimeID, "new-rt")
	}

	// --- old runtime not found (404) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/missing/replace", strings.NewReader(`{"new_runtime_id":"new-rt"}`))
	w = httptest.NewRecorder()
	handler.Replace(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Replace missing old: expected 404, got %d", w.Code)
	}

	// --- new runtime not found (404) ---
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-x", Name: "X", Executable: "x.exe"}); err != nil {
		t.Fatalf("CreateRuntime rt-x: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/rt-x/replace", strings.NewReader(`{"new_runtime_id":"missing"}`))
	w = httptest.NewRecorder()
	handler.Replace(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Replace missing new: expected 404, got %d", w.Code)
	}

	// --- empty new_runtime_id (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/rt-x/replace", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	handler.Replace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Replace empty new_runtime_id: expected 400, got %d", w.Code)
	}

	// --- invalid JSON (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/rt-x/replace", strings.NewReader(`{invalid`))
	w = httptest.NewRecorder()
	handler.Replace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Replace invalid JSON: expected 400, got %d", w.Code)
	}

	// --- empty ID (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes//replace", strings.NewReader(`{"new_runtime_id":"new-rt"}`))
	w = httptest.NewRecorder()
	handler.Replace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Replace empty ID: expected 400, got %d", w.Code)
	}
}

func TestRuntimesHandler_CascadeDelete(t *testing.T) {
	repo, handler := newV23RuntimeHandler(t)

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-1", Name: "RT1", Executable: "rt1.exe"}); err != nil {
		t.Fatalf("CreateRuntime rt-1: %v", err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-2", Name: "RT2", Executable: "rt2.exe"}); err != nil {
		t.Fatalf("CreateRuntime rt-2: %v", err)
	}
	for _, id := range []string{"m-1", "m-2"} {
		if err := repo.CreateModel(&storage.ModelEntry{ID: id, Name: id, RuntimeID: "rt-1"}); err != nil {
			t.Fatalf("CreateModel %s: %v", id, err)
		}
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m-3", Name: "m-3", RuntimeID: "rt-2"}); err != nil {
		t.Fatalf("CreateModel m-3: %v", err)
	}
	if err := repo.CreateInstance(&storage.LaunchInstanceEntry{
		ID: "inst-1", ModelID: "m-1", RuntimeID: "rt-1", State: "exited",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// --- success ---
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/rt-1/cascade-delete", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	handler.CascadeDelete(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CascadeDelete: expected 200, got %d, body %s", resp.StatusCode, w.Body.String())
	}
	var out struct {
		Status        string `json:"status"`
		ModelsDeleted int    `json:"models_deleted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Status != "deleted" || out.ModelsDeleted != 2 {
		t.Fatalf("unexpected response: %#v", out)
	}
	if _, err := repo.GetRuntime("rt-1"); err == nil {
		t.Fatal("runtime rt-1 should be deleted")
	}
	for _, id := range []string{"m-1", "m-2"} {
		if _, err := repo.GetModel(id); err == nil {
			t.Errorf("model %s should be deleted", id)
		}
	}
	if _, err := repo.GetModel("m-3"); err != nil {
		t.Fatalf("model m-3 should still exist: %v", err)
	}
	if _, err := repo.GetRuntime("rt-2"); err != nil {
		t.Fatalf("runtime rt-2 should still exist: %v", err)
	}
	inst, err := repo.GetInstance("inst-1")
	if err != nil {
		t.Fatalf("instance history should be preserved: %v", err)
	}
	if inst.ModelID != "m-1" {
		t.Errorf("instance model_id = %q, want %q", inst.ModelID, "m-1")
	}

	// --- runtime not found (404) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/missing/cascade-delete", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	handler.CascadeDelete(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("CascadeDelete missing: expected 404, got %d", w.Code)
	}

	// --- empty ID (400) ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes//cascade-delete", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	handler.CascadeDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("CascadeDelete empty ID: expected 400, got %d", w.Code)
	}
}
