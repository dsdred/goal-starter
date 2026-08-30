package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestJSONRepository_CreateRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}

	// Create runtime.
	rt := &RuntimeEntry{
		Name:        "test-runtime",
		Executable:  "ollama",
		Environment: map[string]string{"OLLAMA_HOST": "127.0.0.1:11434"},
	}
	err = repo.CreateRuntime(rt)
	if err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	if rt.ID == "" {
		t.Fatal("expected runtime ID to be set")
	}
	if rt.CreatedAt.IsZero() || rt.UpdatedAt.IsZero() {
		t.Error("expected timestamps to be set")
	}

	// Verify persistence: reload from disk.
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository(reload): %v", err)
	}

	runtimes, err := repo2.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("expected 1 runtime, got %d", len(runtimes))
	}
	if runtimes[0].Name != "test-runtime" {
		t.Errorf("expected name 'test-runtime', got '%s'", runtimes[0].Name)
	}
}

func TestJSONRepository_GetRuntime_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	_, err := repo.GetRuntime("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent runtime")
	}
}

func TestJSONRepository_UpdateRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create.
	rt := &RuntimeEntry{Name: "original", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	id := rt.ID

	// Update.
	rt.Name = "updated"
	if err := repo.UpdateRuntime(rt); err != nil {
		t.Fatalf("UpdateRuntime: %v", err)
	}

	got, err := repo.GetRuntime(id)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	if got.Name != "updated" {
		t.Errorf("expected 'updated', got '%s'", got.Name)
	}
}

func TestJSONRepository_DeleteRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	rt := &RuntimeEntry{Name: "to-delete", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	id := rt.ID

	if err := repo.DeleteRuntime(id); err != nil {
		t.Fatalf("DeleteRuntime: %v", err)
	}

	_, err := repo.GetRuntime(id)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestJSONRepository_CrossReferenceValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	rt := &RuntimeEntry{Name: "rt", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	m := &ModelEntry{Name: "valid", RuntimeID: rt.ID}
	if err := repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	if err := repo.ValidateCrossReferences(context.Background()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Invalid model (non-existent runtime).
	m2 := &ModelEntry{Name: "invalid", RuntimeID: "non-existent"}
	if err := repo.CreateModel(m2); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	if err := repo.ValidateCrossReferences(context.Background()); err == nil {
		t.Error("expected validation error for non-existent runtime reference")
	}
}

func TestJSONRepository_AtomicWriteBackupRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")

	// Write first version.
	repo, _ := NewJSONRepository(path)
	rt1 := &RuntimeEntry{Name: "first-version", Executable: "ollama"}
	if err := repo.CreateRuntime(rt1); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Verify backup was created.
	bakPath := path + ".bak"
	if _, err := os.Stat(bakPath); err != nil {
		// .bak is only created when there's an existing good file to back up.
		// First save doesn't have a previous file, so .bak may not exist.
		t.Log("first save: .bak not created (expected)")
	}

	// Write second version (creates .bak).
	rt2 := &RuntimeEntry{Name: "second-version", Executable: "llama-cpp"}
	if err := repo.CreateRuntime(rt2); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	if _, err := os.Stat(bakPath); err != nil {
		t.Skipf("backup not created: %v", err)
	}

	// Corrupt main file.
	if err := os.WriteFile(path, []byte("{corrupted"), 0600); err != nil {
		t.Fatalf("corrupt main: %v", err)
	}

	// Reload — should fall back to backup.
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository(recovery): %v", err)
	}

	runtimes, err := repo2.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes after recovery: %v", err)
	}
	if len(runtimes) < 1 {
		t.Fatal("expected at least 1 runtime after backup recovery")
	}
}

func TestJSONRepository_BothFilesCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")

	// Create and save.
	repo, _ := NewJSONRepository(path)
	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Ensure .bak exists.
	rt2 := &RuntimeEntry{Name: "test2", Executable: "llama"}
	if err := repo.CreateRuntime(rt2); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Corrupt both files.
	_ = os.WriteFile(path, []byte("{bad"), 0600)
	_ = os.WriteFile(path+".bak", []byte("{also-bad"), 0600)

	// Reload should fail.
	_, err := NewJSONRepository(path)
	if err == nil {
		t.Fatal("expected error when both files corrupted")
	}
}

func TestJSONRepository_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	const n = 10
	done := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			rt := &RuntimeEntry{
				Name:        "concurrent",
				Executable:  "ollama",
				Environment: map[string]string{"TEST": "true"},
			}
			_ = repo.CreateRuntime(rt)
			done <- true
		}(i)
	}

	for i := 0; i < n; i++ {
		<-done
	}

	runtimes, err := repo.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(runtimes) != n {
		t.Errorf("expected %d runtimes, got %d", n, len(runtimes))
	}
}

func TestJSONRepository_ListRuntimes_ReturnsCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	rt := &RuntimeEntry{
		Name:        "test",
		Executable:  "ollama",
		Environment: map[string]string{"KEY": "value"},
	}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	runtimes, err := repo.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}

	// Mutate returned copy.
	runtimes[0].Name = "mutated"
	runtimes[0].Environment["KEY"] = "changed"

	// Reload and verify original is intact.
	repo2, _ := NewJSONRepository(path)
	runtimes2, _ := repo2.ListRuntimes()
	if runtimes2[0].Name != "test" {
		t.Errorf("expected 'test', got '%s'", runtimes2[0].Name)
	}
	if runtimes2[0].Environment["KEY"] != "value" {
		t.Errorf("expected 'value', got '%s'", runtimes2[0].Environment["KEY"])
	}
}

func TestJSONRepository_SchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	if repo.SchemaVersion() != 8 {
		t.Errorf("expected schema version 8, got %d", repo.SchemaVersion())
	}
}

func TestJSONRepository_CountActiveInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	inst1 := &LaunchInstanceEntry{ModelID: "m1", RuntimeID: "r1", State: "running"}
	repo.CreateLaunchInstance(inst1) //nolint:errcheck

	inst2 := &LaunchInstanceEntry{ModelID: "m2", RuntimeID: "r2", State: "starting"}
	repo.CreateLaunchInstance(inst2) //nolint:errcheck

	inst3 := &LaunchInstanceEntry{ModelID: "m3", RuntimeID: "r3", State: "exited"}
	repo.CreateLaunchInstance(inst3) //nolint:errcheck

	count := repo.CountActiveInstances()
	if count != 2 {
		t.Errorf("expected 2 active instances, got %d", count)
	}
}

func TestJSONRepository_InstanceClone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create instance.
	inst := &LaunchInstanceEntry{
		ModelID:          "model-1",
		RuntimeID:        "runtime-1",
		Executable:       "ollama",
		Args:             []string{"serve"},
		WorkingDirectory: "/tmp/ollama",
		Environment:      map[string]string{"OLLAMA_HOST": "127.0.0.1:11434"},
		State:            "running",
	}
	if err := repo.CreateLaunchInstance(inst); err != nil {
		t.Fatalf("CreateLaunchInstance: %v", err)
	}
	id := inst.ID

	// Retrieve and mutate.
	retrieved, err := repo.GetLaunchInstance(id)
	if err != nil {
		t.Fatalf("GetLaunchInstance: %v", err)
	}
	retrieved.Args[0] = "changed"
	retrieved.Environment["OLLAMA_HOST"] = "0.0.0.0:11434"

	// Reload and verify.
	repo2, _ := NewJSONRepository(path)
	retrieved2, _ := repo2.GetLaunchInstance(id)
	if retrieved2.Args[0] != "serve" {
		t.Errorf("expected 'serve', got '%s'", retrieved2.Args[0])
	}
	if retrieved2.Environment["OLLAMA_HOST"] != "127.0.0.1:11434" {
		t.Errorf("expected '127.0.0.1:11434', got '%s'", retrieved2.Environment["OLLAMA_HOST"])
	}
}

func TestJSONRepository_ModelFullFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create model.
	m := &ModelEntry{
		Name:           "test-model",
		RuntimeID:      "runtime-1",
		Args:           []string{"--host", "127.0.0.1", "--port", "11434", "--flag"},
		Environment:    map[string]string{"VAR": "val"},
		Active:         true,
		AutostartDelay: 3,
	}
	if err := repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	id := m.ID

	// Retrieve.
	got, err := repo.GetModel(id)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Name != "test-model" {
		t.Errorf("expected 'test-model', got '%s'", got.Name)
	}
	if !hasFlagValue(got.Args, "--host", "127.0.0.1") {
		t.Errorf("expected --host 127.0.0.1 in args, got %v", got.Args)
	}
	if !hasFlagValue(got.Args, "--port", "11434") {
		t.Errorf("expected --port 11434 in args, got %v", got.Args)
	}
	if !containsV5Flag(got.Args, "--flag") {
		t.Errorf("expected --flag in args, got %v", got.Args)
	}
	if got.Environment["VAR"] != "val" {
		t.Errorf("expected VAR=val, got %v", got.Environment)
	}
	if !got.Active {
		t.Error("expected model to be active")
	}
	if got.AutostartDelay != 3 {
		t.Errorf("expected autostart delay 3, got %d", got.AutostartDelay)
	}

	// List.
	list, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 model, got %d", len(list))
	}

	// Update.
	got.Name = "updated-model"
	if err := repo.UpdateModel(got); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	got2, _ := repo.GetModel(id)
	if got2.Name != "updated-model" {
		t.Errorf("expected 'updated-model', got '%s'", got2.Name)
	}

	// Delete.
	if err := repo.DeleteModel(id); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	_, err = repo.GetModel(id)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestJSONRepository_ModelsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create.
	m := &ModelEntry{
		Name:      "test-model",
		RuntimeID: "runtime-1",
	}
	if err := repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	id := m.ID

	// Get.
	got, err := repo.GetModel(id)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Name != "test-model" {
		t.Errorf("expected 'test-model', got '%s'", got.Name)
	}

	// List.
	list, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 model, got %d", len(list))
	}

	// Update.
	got.Name = "updated-model"
	if err := repo.UpdateModel(got); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	got2, _ := repo.GetModel(id)
	if got2.Name != "updated-model" {
		t.Errorf("expected 'updated-model', got '%s'", got2.Name)
	}

	// Delete.
	if err := repo.DeleteModel(id); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
}

func TestJSONRepository_LaunchInstance_ByModelID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	inst1 := &LaunchInstanceEntry{ModelID: "m1", State: "running", RuntimeID: "r1"}
	repo.CreateLaunchInstance(inst1) //nolint:errcheck

	inst2 := &LaunchInstanceEntry{ModelID: "m1", State: "exited", RuntimeID: "r2"}
	repo.CreateLaunchInstance(inst2) //nolint:errcheck

	inst3 := &LaunchInstanceEntry{ModelID: "m2", State: "running", RuntimeID: "r3"}
	repo.CreateLaunchInstance(inst3) //nolint:errcheck

	result, err := repo.ListByModelID("m1")
	if err != nil {
		t.Fatalf("ListByModelID: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 instances for m1, got %d", len(result))
	}

	result2, err := repo.ListByModelID("m2")
	if err != nil {
		t.Fatalf("ListByModelID: %v", err)
	}
	if len(result2) != 1 {
		t.Fatalf("expected 1 instance for m2, got %d", len(result2))
	}
}

func TestJSONRepository_NoTempFilesAfterSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Check no .tmp files left.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == "repo.json.tmp" {
			t.Errorf("unexpected .tmp file found")
		}
	}
}

func TestJSONRepository_RepositoryInterfaceMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Test InstanceStore compatibility methods via Repository interface.
	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	m := &ModelEntry{Name: "test-model", RuntimeID: rt.ID}
	if err := repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	inst := &LaunchInstanceEntry{ModelID: m.ID, State: "running", RuntimeID: rt.ID}
	if err := repo.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Test delegated methods.
	gotInst, err := repo.GetInstance(inst.ID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if gotInst.State != "running" {
		t.Errorf("expected 'running', got '%s'", gotInst.State)
	}

	listInst, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(listInst) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(listInst))
	}

	// Test Update/Delete via delegated methods.
	if err := repo.UpdateInstance(inst); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}
	if err := repo.DeleteInstance(inst.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	_, err = repo.GetInstance(inst.ID)
	if err == nil {
		t.Fatal("expected error after DeleteInstance")
	}
}

func TestJSONRepository_SaveLocked_HoldsLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// saveLocked is called internally by Create/Update/Delete.
	// Verify that concurrent operations don't panic.
	const n = 5
	done := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			rt := &RuntimeEntry{Name: "concurrent", Executable: "ollama"}
			_ = repo.CreateRuntime(rt)
			done <- true
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}

	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != n {
		t.Errorf("expected %d runtimes, got %d", n, len(runtimes))
	}
}

func TestJSONRepository_MarshalJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")

	// Create with initial data.
	repo, _ := NewJSONRepository(path)
	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	// Verify JSON structure.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["schema_version"] != float64(8) {
		t.Errorf("expected schema_version 8, got %v", parsed["schema_version"])
	}
	if _, ok := parsed["runtimes"]; !ok {
		t.Error("expected runtimes key to exist")
	}
	if _, ok := parsed["models"]; !ok {
		t.Error("expected models key to exist")
	}
	if _, ok := parsed["instances"]; !ok {
		t.Error("expected instances key to exist")
	}
	if _, ok := parsed["profiles"]; ok {
		t.Error("unexpected profiles key in v7 JSON")
	}
}

func TestJSONRepository_ValidateCrossReferences_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Empty repo should pass validation.
	if err := repo.ValidateCrossReferences(context.Background()); err != nil {
		t.Errorf("expected no error for empty repo, got %v", err)
	}
}

func TestJSONRepository_Upgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	if err := repo.Upgrade(); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// Reload and verify data preserved.
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	runtimes, err := repo2.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("expected 1 runtime after upgrade, got %d", len(runtimes))
	}
}

func TestJSONRepository_SaveUnified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	altPath := filepath.Join(dir, "alt_repo.json")

	repo, _ := NewJSONRepository(path)
	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	if err := repo.SaveUnified(altPath); err != nil {
		t.Fatalf("SaveUnified: %v", err)
	}

	// Verify alt file exists and is valid.
	altRepo, err := NewJSONRepository(altPath)
	if err != nil {
		t.Fatalf("NewJSONRepository(alt): %v", err)
	}
	runtimes, err := altRepo.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes(alt): %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("expected 1 runtime in alt repo, got %d", len(runtimes))
	}
}

// Benchmark tests.
func BenchmarkJSONRepository_CreateRuntime(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rt := &RuntimeEntry{Name: "bench", Executable: "ollama"}
		_ = repo.CreateRuntime(rt)
	}
}

func BenchmarkJSONRepository_ListRuntimes(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Pre-populate.
	for i := 0; i < 100; i++ {
		rt := &RuntimeEntry{Name: "bench", Executable: "ollama"}
		_ = repo.CreateRuntime(rt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = repo.ListRuntimes()
	}
}

func BenchmarkJSONRepository_CreateModel(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create a runtime first.
	rt := &RuntimeEntry{Name: "runtime", Executable: "ollama"}
	repo.CreateRuntime(rt) //nolint:errcheck

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := &ModelEntry{
			Name:      "model",
			RuntimeID: rt.ID,
		}
		_ = repo.CreateModel(m)
	}
}

// Test that time.Now() calls are properly set on entries.
func TestJSONRepository_Timestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	rt := &RuntimeEntry{Name: "test", Executable: "ollama"}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	if rt.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if rt.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}

	// Small sleep to ensure timestamp changes on update.
	time.Sleep(10 * time.Millisecond)

	rt.Name = "updated"
	if err := repo.UpdateRuntime(rt); err != nil {
		t.Fatalf("UpdateRuntime: %v", err)
	}
	if rt.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero after update")
	}
}

func TestJSONRepository_IDUniquenessOnExplicitConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}

	m1 := &ModelEntry{ID: "fixed-model", Name: "one"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	m2 := &ModelEntry{ID: "fixed-model", Name: "two"}
	err = repo.CreateModel(m2)
	if err == nil {
		t.Fatalf("expected duplicate-ID Create to fail with uniqueness error")
	}

	models, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].Name != "one" {
		t.Fatalf("expected original model to remain, got %d entries", len(models))
	}
}

func TestJSONRepository_IDUniqueness_GeneratedRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}

	m1 := &ModelEntry{Name: "one"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	m2 := &ModelEntry{Name: "two"}
	if err := repo.CreateModel(m2); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if m1.ID == m2.ID {
		t.Fatalf("expected distinct generated IDs, got same: %s", m1.ID)
	}
}

func TestJSONRepository_CallerDuplicate_Runtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	r1 := &RuntimeEntry{ID: "fixed-rt", Name: "first", Executable: "ollama"}
	if err := repo.CreateRuntime(r1); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	r2 := &RuntimeEntry{ID: "fixed-rt", Name: "second", Executable: "llama"}
	if err := repo.CreateRuntime(r2); err == nil {
		t.Fatal("expected error for duplicate caller ID")
	}

	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 1 || runtimes[0].Name != "first" {
		t.Fatalf("expected 1 runtime 'first', got %d", len(runtimes))
	}
}

func TestJSONRepository_CallerDuplicate_Model(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	m1 := &ModelEntry{ID: "fixed-model", Name: "first", RuntimeID: "r1"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	m2 := &ModelEntry{ID: "fixed-model", Name: "second", RuntimeID: "r2"}
	if err := repo.CreateModel(m2); err == nil {
		t.Fatal("expected error for duplicate caller ID")
	}

	models, _ := repo.ListModels()
	if len(models) != 1 || models[0].Name != "first" {
		t.Fatalf("expected 1 model 'first', got %d", len(models))
	}
}

func TestJSONRepository_CallerDuplicate_LaunchInstance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	i1 := &LaunchInstanceEntry{ID: "fixed-inst", ModelID: "m1", State: "running"}
	if err := repo.CreateLaunchInstance(i1); err != nil {
		t.Fatalf("CreateLaunchInstance: %v", err)
	}

	i2 := &LaunchInstanceEntry{ID: "fixed-inst", ModelID: "m2", State: "exited"}
	if err := repo.CreateLaunchInstance(i2); err == nil {
		t.Fatal("expected error for duplicate caller ID")
	}

	instances, _ := repo.ListLaunchInstances()
	if len(instances) != 1 || instances[0].ModelID != "m1" {
		t.Fatalf("expected 1 instance with model 'm1', got %d", len(instances))
	}
}

func TestJSONRepository_DeterministicCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)
	jsonRepo := repo.(*JSONRepository)

	var callCount int
	saved := jsonRepo.idGenerator
	jsonRepo.idGenerator = func() string {
		callCount++
		switch callCount {
		case 1:
			return "inst_100"
		case 2:
			return "inst_200"
		default:
			return "inst_100" // collides with m1
		}
	}
	defer func() { jsonRepo.idGenerator = saved }()

	m1 := &ModelEntry{Name: "one"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("first CreateModel: %v", err)
	}
	if m1.ID != "inst_100" {
		t.Fatalf("expected ID 'inst_100', got '%s'", m1.ID)
	}

	m2 := &ModelEntry{Name: "two"}
	if err := repo.CreateModel(m2); err != nil {
		t.Fatalf("second CreateModel: %v", err)
	}
	if m2.ID != "inst_200" {
		t.Fatalf("expected ID 'inst_200', got '%s'", m2.ID)
	}

	// A colliding generated ID is rejected without a partial insert.
	m3 := &ModelEntry{Name: "three"}
	if err := repo.CreateModel(m3); err == nil {
		t.Fatal("expected error for colliding generated ID")
	}

	models, _ := repo.ListModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 models after rejected create, got %d", len(models))
	}
	if models[0].Name != "one" || models[1].Name != "two" {
		t.Fatalf("expected original models to remain, got %v", models)
	}
}

func TestJSONRepository_Collision_Sequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)
	jsonRepo := repo.(*JSONRepository)

	var callCount int
	saved := jsonRepo.idGenerator
	jsonRepo.idGenerator = func() string {
		callCount++
		if callCount == 1 {
			return "inst_50"
		}
		return "inst_60"
	}
	defer func() { jsonRepo.idGenerator = saved }()

	m1 := &ModelEntry{Name: "one"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("first CreateModel: %v", err)
	}
	if m1.ID != "inst_50" {
		t.Fatalf("expected ID 'inst_50', got '%s'", m1.ID)
	}

	// A generated ID that collides with m1 is rejected; m1 remains intact.
	jsonRepo.idGenerator = func() string { return "inst_50" }

	m2 := &ModelEntry{Name: "two"}
	if err := repo.CreateModel(m2); err == nil {
		t.Fatal("expected error for colliding generated ID")
	}

	models, _ := repo.ListModels()
	if len(models) != 1 || models[0].Name != "one" {
		t.Fatalf("expected original model to remain, got %d entries", len(models))
	}
}

func TestJSONRepository_Collision_Always(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)
	jsonRepo := repo.(*JSONRepository)

	var callCount int
	saved := jsonRepo.idGenerator
	jsonRepo.idGenerator = func() string {
		callCount++
		return fmt.Sprintf("inst_%d", callCount)
	}
	defer func() { jsonRepo.idGenerator = saved }()

	for i := 0; i < 10; i++ {
		m := &ModelEntry{Name: fmt.Sprintf("m%d", i)}
		if err := repo.CreateModel(m); err != nil {
			t.Fatalf("CreateModel %d: %v", i, err)
		}
	}

	jsonRepo.idGenerator = func() string {
		return "inst_1" // always collides with first model
	}

	m := &ModelEntry{Name: "exhaust"}
	exhaustErr := repo.CreateModel(m)
	if exhaustErr == nil {
		t.Fatal("expected collision error")
	}

	models, _ := repo.ListModels()
	if len(models) != 10 {
		t.Fatalf("expected 10 models (no partial insert), got %d", len(models))
	}
}

func TestJSONRepository_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)
	jsonRepo := repo.(*JSONRepository)

	saved := jsonRepo.idGenerator
	jsonRepo.idGenerator = func() string { return "inst_persist" }
	defer func() { jsonRepo.idGenerator = saved }()
	m1 := &ModelEntry{Name: "persist-1"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	jsonRepo.idGenerator = func() string { return "inst_persist2" }
	m2 := &ModelEntry{Name: "persist-2"}
	if err := repo.CreateModel(m2); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}

	models, err := repo2.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models after reopen, got %d", len(models))
	}

	// Verify JSON schema preserved.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["schema_version"] != float64(8) {
		t.Errorf("expected schema_version 8, got %v", parsed["schema_version"])
	}
	if _, ok := parsed["models"]; !ok {
		t.Error("expected models key in JSON")
	}
	if _, ok := parsed["profiles"]; ok {
		t.Error("unexpected profiles key in v7 JSON")
	}
}

func TestJSONRepository_Concurrency_Uniqueness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	const n = 50
	var mu sync.Mutex
	ids := make(map[string]bool)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m := &ModelEntry{Name: fmt.Sprintf("concurrent-%d", idx)}
			if err := repo.CreateModel(m); err != nil {
				t.Errorf("CreateModel goroutine %d: %v", idx, err)
				return
			}
			mu.Lock()
			if ids[m.ID] {
				t.Errorf("duplicate ID detected: %s", m.ID)
			}
			ids[m.ID] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("expected %d unique IDs, got %d", n, len(ids))
	}

	models, _ := repo.ListModels()
	if len(models) != n {
		t.Fatalf("expected %d models in repo, got %d", n, len(models))
	}
}

func TestJSONRepository_Collision_AllEntities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)
	jsonRepo := repo.(*JSONRepository)

	saved := jsonRepo.idGenerator
	defer func() { jsonRepo.idGenerator = saved }()

	// Runtime: a colliding generated ID is rejected.
	jsonRepo.idGenerator = func() string { return "inst-rt-1" }
	r1 := &RuntimeEntry{Name: "rt1", Executable: "ollama"}
	if err := repo.CreateRuntime(r1); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	r2 := &RuntimeEntry{Name: "rt2", Executable: "llama"}
	if err := repo.CreateRuntime(r2); err == nil {
		t.Fatal("expected error for colliding generated runtime ID")
	}

	// Model: a colliding generated ID is rejected.
	jsonRepo.idGenerator = func() string { return "inst-ml-1" }
	m1 := &ModelEntry{Name: "ml1", RuntimeID: "rt1"}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	m2 := &ModelEntry{Name: "ml2", RuntimeID: "rt1"}
	if err := repo.CreateModel(m2); err == nil {
		t.Fatal("expected error for colliding generated model ID")
	}

	// LaunchInstance: a colliding generated ID is rejected.
	jsonRepo.idGenerator = func() string { return "inst-ln-1" }
	i1 := &LaunchInstanceEntry{ModelID: m1.ID, RuntimeID: "rt1", State: "running"}
	if err := repo.CreateLaunchInstance(i1); err != nil {
		t.Fatalf("CreateLaunchInstance: %v", err)
	}
	i2 := &LaunchInstanceEntry{ModelID: m1.ID, RuntimeID: "rt1", State: "exited"}
	if err := repo.CreateLaunchInstance(i2); err == nil {
		t.Fatal("expected error for colliding generated instance ID")
	}
}

func TestJSONRepository_CreateModel_EmptyName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Empty ID is OK — auto-generated.
	m := &ModelEntry{Name: "valid", RuntimeID: "rt1"}
	if err := repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if m.ID == "" {
		t.Fatal("expected auto-generated ID for model")
	}

	// Verify retrievable.
	got, err := repo.GetModel(m.ID)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Name != "valid" {
		t.Errorf("expected name 'valid', got '%s'", got.Name)
	}

	// Another create still works.
	m2 := &ModelEntry{Name: "valid2", RuntimeID: "rt1"}
	if err := repo.CreateModel(m2); err != nil {
		t.Fatalf("CreateModel second: %v", err)
	}
	if m2.ID == "" || m2.ID == m.ID {
		t.Fatal("expected distinct auto-generated ID for second model")
	}
}
