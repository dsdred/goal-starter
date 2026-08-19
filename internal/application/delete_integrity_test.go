package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

func setupDeleteTest(t *testing.T) (*ModelService, *RuntimeService, storage.Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "goal.json"))
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	modelSvc := NewModelService(repo)
	runtimeSvc := NewRuntimeService(repo)
	return modelSvc, runtimeSvc, repo
}

// Test A: Model M used by Runtime dependency — deleting a model is straightforward.
func TestDeleteModel_Success(t *testing.T) {
	modelSvc, runtimeSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	// Create Runtime
	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Create Model
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Delete Model → should SUCCEED
	if err := modelSvc.DeleteModel(ctx, "m-1"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}

	// Model must be gone
	if _, err := repo.GetModel("m-1"); err == nil {
		t.Fatal("model should be deleted")
	}
}

// Test B: Model still exists after failed delete (e.g., already deleted).
func TestDeleteModel_NotFound(t *testing.T) {
	modelSvc, _, repo := setupDeleteTest(t)
	ctx := context.Background()

	// Attempt to delete non-existent model
	err := modelSvc.DeleteModel(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected error when deleting non-existent model")
	}

	// Repository still has no models
	models, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

// Test C: Runtime dependency — deleting a runtime used by model is blocked.
func TestDeleteRuntime_Referenced_Fails(t *testing.T) {
	modelSvc, runtimeSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	// Create Runtime
	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Create Model referencing the runtime
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Attempt to delete Runtime → should FAIL
	err := runtimeSvc.DeleteRuntime(ctx, "rt-1")
	if err == nil {
		t.Fatal("expected error when deleting runtime referenced by model, got nil")
	}

	// Runtime must still exist
	if _, err := repo.GetRuntime("rt-1"); err != nil {
		t.Fatalf("runtime should still exist after failed delete: %v", err)
	}
}

// Test D: Failure path atomicity — a failed delete does not modify the repository file.
func TestDeleteModel_FailedDoesNotModifyRepo(t *testing.T) {
	modelSvc, runtimeSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Snapshot: count all entities
	modelsBefore, _ := repo.ListModels()
	runtimesBefore, _ := repo.ListRuntimes()

	// Failed delete (non-existent)
	_ = modelSvc.DeleteModel(ctx, "non-existent")

	// Verify: same count, same entities
	modelsAfter, _ := repo.ListModels()
	runtimesAfter, _ := repo.ListRuntimes()

	if len(modelsAfter) != len(modelsBefore) {
		t.Fatalf("model count changed: before=%d after=%d", len(modelsBefore), len(modelsAfter))
	}
	if len(runtimesAfter) != len(runtimesBefore) {
		t.Fatalf("runtime count changed: before=%d after=%d", len(runtimesBefore), len(runtimesAfter))
	}

	// Verify model content unchanged
	for _, m := range modelsAfter {
		if m.ID == "m-1" && m.Name != "Model" {
			t.Fatal("model content was modified by failed delete")
		}
	}
}
