package application

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/errors"
)

func setupReplaceTest(t *testing.T) (*RuntimeService, *ModelService, storage.Repository) {
	t.Helper()
	modelSvc, runtimeSvc, repo := setupDeleteTest(t)
	return runtimeSvc, modelSvc, repo
}

func TestReplaceRuntime_RebindsModelsAndDeletesOld(t *testing.T) {
	runtimeSvc, modelSvc, repo := setupReplaceTest(t)
	ctx := context.Background()

	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "old-rt", Name: "Old", Executable: "old.exe"}); err != nil {
		t.Fatalf("CreateRuntime old: %v", err)
	}
	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "new-rt", Name: "New", Executable: "new.exe"}); err != nil {
		t.Fatalf("CreateRuntime new: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-1", Name: "M1", RuntimeID: "old-rt"}); err != nil {
		t.Fatalf("CreateModel m-1: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-2", Name: "M2", RuntimeID: "old-rt"}); err != nil {
		t.Fatalf("CreateModel m-2: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-3", Name: "M3", RuntimeID: "new-rt"}); err != nil {
		t.Fatalf("CreateModel m-3: %v", err)
	}

	moved, err := runtimeSvc.ReplaceRuntime(ctx, "old-rt", "new-rt")
	if err != nil {
		t.Fatalf("ReplaceRuntime: %v", err)
	}
	if moved != 2 {
		t.Fatalf("models moved: got %d, want 2", moved)
	}

	if _, err := repo.GetRuntime("old-rt"); err == nil {
		t.Fatal("old runtime should be deleted")
	}
	for _, id := range []string{"m-1", "m-2"} {
		m, err := repo.GetModel(id)
		if err != nil {
			t.Fatalf("model %s should still exist: %v", id, err)
		}
		if m.RuntimeID != "new-rt" {
			t.Errorf("model %s: runtime_id = %q, want %q", id, m.RuntimeID, "new-rt")
		}
	}
	m3, err := repo.GetModel("m-3")
	if err != nil {
		t.Fatalf("model m-3 should still exist: %v", err)
	}
	if m3.RuntimeID != "new-rt" {
		t.Errorf("model m-3: runtime_id = %q, want %q", m3.RuntimeID, "new-rt")
	}
}

func TestReplaceRuntime_NoModels(t *testing.T) {
	runtimeSvc, _, _ := setupReplaceTest(t)
	ctx := context.Background()

	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "old-rt", Name: "Old", Executable: "old.exe"}); err != nil {
		t.Fatalf("CreateRuntime old: %v", err)
	}
	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "new-rt", Name: "New", Executable: "new.exe"}); err != nil {
		t.Fatalf("CreateRuntime new: %v", err)
	}

	moved, err := runtimeSvc.ReplaceRuntime(ctx, "old-rt", "new-rt")
	if err != nil {
		t.Fatalf("ReplaceRuntime: %v", err)
	}
	if moved != 0 {
		t.Fatalf("models moved: got %d, want 0", moved)
	}
}

func TestReplaceRuntime_OldNotFound(t *testing.T) {
	runtimeSvc, _, repo := setupReplaceTest(t)
	ctx := context.Background()

	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "new-rt", Name: "New", Executable: "new.exe"}); err != nil {
		t.Fatalf("CreateRuntime new: %v", err)
	}

	_, err := runtimeSvc.ReplaceRuntime(ctx, "missing-rt", "new-rt")
	if err == nil {
		t.Fatal("expected error when old runtime not found")
	}
	var apiErr *errors.APIError
	if !stderrors.As(err, &apiErr) || apiErr.Code != errors.CodeInvalidRuntime {
		t.Fatalf("expected not-found API error, got: %v", err)
	}

	// new runtime must be untouched
	if _, err := repo.GetRuntime("new-rt"); err != nil {
		t.Fatalf("new runtime should still exist: %v", err)
	}
}

func TestReplaceRuntime_NewNotFound(t *testing.T) {
	runtimeSvc, modelSvc, repo := setupReplaceTest(t)
	ctx := context.Background()

	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "old-rt", Name: "Old", Executable: "old.exe"}); err != nil {
		t.Fatalf("CreateRuntime old: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-1", Name: "M1", RuntimeID: "old-rt"}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	_, err := runtimeSvc.ReplaceRuntime(ctx, "old-rt", "missing-rt")
	if err == nil {
		t.Fatal("expected error when new runtime not found")
	}
	var apiErr *errors.APIError
	if !stderrors.As(err, &apiErr) || apiErr.Code != errors.CodeInvalidRuntime {
		t.Fatalf("expected not-found API error, got: %v", err)
	}

	// Nothing should have changed: old runtime and model intact, still bound.
	if _, err := repo.GetRuntime("old-rt"); err != nil {
		t.Fatalf("old runtime should still exist: %v", err)
	}
	m, err := repo.GetModel("m-1")
	if err != nil {
		t.Fatalf("model should still exist: %v", err)
	}
	if m.RuntimeID != "old-rt" {
		t.Fatalf("model was rebound despite failed replace: %q", m.RuntimeID)
	}
}

func TestReplaceRuntime_InvalidIDs(t *testing.T) {
	runtimeSvc, _, _ := setupReplaceTest(t)
	ctx := context.Background()

	if _, err := runtimeSvc.ReplaceRuntime(ctx, "", "new-rt"); err == nil {
		t.Fatal("expected error for empty old ID")
	}
	if _, err := runtimeSvc.ReplaceRuntime(ctx, "old-rt", ""); err == nil {
		t.Fatal("expected error for empty new ID")
	}
}

func TestCascadeDeleteRuntime_DeletesRuntimeAndModels(t *testing.T) {
	runtimeSvc, modelSvc, repo := setupReplaceTest(t)
	ctx := context.Background()

	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "rt-1", Name: "RT1", Executable: "rt1.exe"}); err != nil {
		t.Fatalf("CreateRuntime rt-1: %v", err)
	}
	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "rt-2", Name: "RT2", Executable: "rt2.exe"}); err != nil {
		t.Fatalf("CreateRuntime rt-2: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-1", Name: "M1", RuntimeID: "rt-1"}); err != nil {
		t.Fatalf("CreateModel m-1: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-2", Name: "M2", RuntimeID: "rt-1"}); err != nil {
		t.Fatalf("CreateModel m-2: %v", err)
	}
	if err := modelSvc.CreateModel(ctx, &storage.ModelEntry{ID: "m-3", Name: "M3", RuntimeID: "rt-2"}); err != nil {
		t.Fatalf("CreateModel m-3: %v", err)
	}
	// Instance history must survive the cascade.
	if err := repo.CreateInstance(&storage.LaunchInstanceEntry{
		ID: "inst-1", ModelID: "m-1", RuntimeID: "rt-1", State: "exited",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	deleted, err := runtimeSvc.CascadeDeleteRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("CascadeDeleteRuntime: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("models deleted: got %d, want 2", deleted)
	}

	if _, err := repo.GetRuntime("rt-1"); err == nil {
		t.Fatal("runtime rt-1 should be deleted")
	}
	for _, id := range []string{"m-1", "m-2"} {
		if _, err := repo.GetModel(id); err == nil {
			t.Errorf("model %s should be deleted", id)
		}
	}
	// Unrelated model untouched.
	if _, err := repo.GetModel("m-3"); err != nil {
		t.Fatalf("model m-3 should still exist: %v", err)
	}
	if _, err := repo.GetRuntime("rt-2"); err != nil {
		t.Fatalf("runtime rt-2 should still exist: %v", err)
	}
	// Instance history preserved with dangling model_id.
	inst, err := repo.GetInstance("inst-1")
	if err != nil {
		t.Fatalf("instance history should be preserved: %v", err)
	}
	if inst.ModelID != "m-1" {
		t.Errorf("instance model_id = %q, want %q", inst.ModelID, "m-1")
	}
}

func TestCascadeDeleteRuntime_NoModels(t *testing.T) {
	runtimeSvc, _, _ := setupReplaceTest(t)
	ctx := context.Background()

	if err := runtimeSvc.CreateRuntime(ctx, &storage.RuntimeEntry{ID: "rt-1", Name: "RT1", Executable: "rt1.exe"}); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	deleted, err := runtimeSvc.CascadeDeleteRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("CascadeDeleteRuntime: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("models deleted: got %d, want 0", deleted)
	}
}

func TestCascadeDeleteRuntime_NotFound(t *testing.T) {
	runtimeSvc, _, repo := setupReplaceTest(t)
	ctx := context.Background()

	_, err := runtimeSvc.CascadeDeleteRuntime(ctx, "missing-rt")
	if err == nil {
		t.Fatal("expected error when runtime not found")
	}
	var apiErr *errors.APIError
	if !stderrors.As(err, &apiErr) || apiErr.Code != errors.CodeInvalidRuntime {
		t.Fatalf("expected not-found API error, got: %v", err)
	}

	runtimes, err := repo.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(runtimes) != 0 {
		t.Errorf("runtimes should be unchanged, got %d", len(runtimes))
	}
}

func TestCascadeDeleteRuntime_EmptyID(t *testing.T) {
	runtimeSvc, _, _ := setupReplaceTest(t)
	ctx := context.Background()

	if _, err := runtimeSvc.CascadeDeleteRuntime(ctx, ""); err == nil {
		t.Fatal("expected error for empty ID")
	}
}
