package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/storage"
)

// TestModelDelete_UnknownIDReturns404 pins DELETE /api/v1/models/{id} for an id
// that does not exist: the repository's plain "model not found" error must reach
// the handler as a CodeNotFound APIError instead of falling through to the
// generic 500 branch.
func TestModelDelete_UnknownIDReturns404(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: "test.exe"}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m-present", Name: "Present", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model: %v", err)
	}
	h := NewModelsHandler(application.NewModelService(repo), nil, nil, repo, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/models/m-absent", nil)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", body["code"])
	}
	if _, err := repo.GetModel("m-present"); err != nil {
		t.Errorf("unrelated model was removed: %v", err)
	}
}
