package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

func setupDeleteTest(t *testing.T) (*ModelService, *RuntimeService, *ProfileService, storage.Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "goal.json"))
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	modelSvc := NewModelService(repo)
	runtimeSvc := NewRuntimeService(repo)
	profileSvc := NewProfileService(repo)
	return modelSvc, runtimeSvc, profileSvc, repo
}

// Test A: Model M used by Profile P → Delete M should FAIL, M remains, P remains.
func TestDeleteModel_ReferencedByProfile_Fails(t *testing.T) {
	modelSvc, runtimeSvc, profileSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	// Create Runtime
	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Create Model
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1", Path: "test.gguf"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Create Profile referencing the model
	p := &storage.ProfileEntry{ID: "p-1", Name: "Profile", RuntimeID: "rt-1", ModelID: "m-1", Host: "127.0.0.1", Port: 8080}
	if err := profileSvc.CreateProfile(ctx, p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Attempt to delete Model → should FAIL
	err := modelSvc.DeleteModel(ctx, "m-1")
	if err == nil {
		t.Fatal("expected error when deleting model referenced by profile, got nil")
	}

	// Model must still exist
	if _, err := repo.GetModel("m-1"); err != nil {
		t.Fatalf("model should still exist after failed delete: %v", err)
	}

	// Profile must still exist
	if _, err := repo.GetProfile("p-1"); err != nil {
		t.Fatalf("profile should still exist after failed delete: %v", err)
	}
}

// Test B: Delete Profile P first, then Delete Model M → should SUCCEED.
func TestDeleteModel_AfterProfileRemoved_Succeeds(t *testing.T) {
	modelSvc, runtimeSvc, profileSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1", Path: "test.gguf"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	p := &storage.ProfileEntry{ID: "p-1", Name: "Profile", RuntimeID: "rt-1", ModelID: "m-1", Host: "127.0.0.1", Port: 8080}
	if err := profileSvc.CreateProfile(ctx, p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Delete Profile first
	if err := profileSvc.DeleteProfile(ctx, "p-1"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	// Now delete Model → should SUCCEED
	if err := modelSvc.DeleteModel(ctx, "m-1"); err != nil {
		t.Fatalf("DeleteModel after profile removal: %v", err)
	}

	// Model must be gone
	if _, err := repo.GetModel("m-1"); err == nil {
		t.Fatal("model should be deleted")
	}
}

// Test C: Runtime dependency — deleting a runtime used by model/profile is blocked.
func TestDeleteRuntime_Referenced_Fails(t *testing.T) {
	modelSvc, runtimeSvc, profileSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	// Create Runtime
	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Create Model referencing the runtime
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1", Path: "test.gguf"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Create Profile referencing the runtime
	p := &storage.ProfileEntry{ID: "p-1", Name: "Profile", RuntimeID: "rt-1", ModelID: "m-1", Host: "127.0.0.1", Port: 8080}
	if err := profileSvc.CreateProfile(ctx, p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Attempt to delete Runtime → should FAIL
	err := runtimeSvc.DeleteRuntime(ctx, "rt-1")
	if err == nil {
		t.Fatal("expected error when deleting runtime referenced by model/profile, got nil")
	}

	// Runtime must still exist
	if _, err := repo.GetRuntime("rt-1"); err != nil {
		t.Fatalf("runtime should still exist after failed delete: %v", err)
	}
}

// Test D: Failure path atomicity — a failed delete does not modify the repository file.
func TestDeleteModel_FailedDoesNotModifyRepo(t *testing.T) {
	modelSvc, runtimeSvc, profileSvc, repo := setupDeleteTest(t)
	ctx := context.Background()

	rt := &storage.RuntimeEntry{ID: "rt-1", Name: "RT", Executable: "test.exe"}
	if err := runtimeSvc.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	m := &storage.ModelEntry{ID: "m-1", Name: "Model", RuntimeID: "rt-1", Path: "test.gguf"}
	if err := modelSvc.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	p := &storage.ProfileEntry{ID: "p-1", Name: "Profile", RuntimeID: "rt-1", ModelID: "m-1", Host: "127.0.0.1", Port: 8080}
	if err := profileSvc.CreateProfile(ctx, p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Snapshot: count all entities
	modelsBefore, _ := repo.ListModels()
	profilesBefore, _ := repo.ListProfiles()
	runtimesBefore, _ := repo.ListRuntimes()

	// Failed delete
	_ = modelSvc.DeleteModel(ctx, "m-1")

	// Verify: same count, same entities
	modelsAfter, _ := repo.ListModels()
	profilesAfter, _ := repo.ListProfiles()
	runtimesAfter, _ := repo.ListRuntimes()

	if len(modelsAfter) != len(modelsBefore) {
		t.Fatalf("model count changed: before=%d after=%d", len(modelsBefore), len(modelsAfter))
	}
	if len(profilesAfter) != len(profilesBefore) {
		t.Fatalf("profile count changed: before=%d after=%d", len(profilesBefore), len(profilesAfter))
	}
	if len(runtimesAfter) != len(runtimesBefore) {
		t.Fatalf("runtime count changed: before=%d after=%d", len(runtimesBefore), len(runtimesAfter))
	}

	// Verify model content unchanged
	for _, m := range modelsAfter {
		if m.ID == "m-1" && (m.Name != "Model" || m.Path != "test.gguf") {
			t.Fatal("model content was modified by failed delete")
		}
	}
}
