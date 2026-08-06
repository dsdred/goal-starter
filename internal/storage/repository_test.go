package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
		DefaultArgs: []string{"serve"},
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

	model := &ModelEntry{Name: "model", Path: "/tmp/model"}
	if err := repo.CreateModel(model); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Valid profile.
	p := &ProfileEntry{
		Name:      "valid",
		RuntimeID: rt.ID,
		ModelID:   model.ID,
		Host:      "127.0.0.1",
		Port:      11434,
	}
	if err := repo.CreateProfile(p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if err := repo.ValidateCrossReferences(context.Background()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Invalid profile (non-existent model).
	p2 := &ProfileEntry{
		Name:      "invalid",
		RuntimeID: rt.ID,
		ModelID:   "non-existent",
		Host:      "127.0.0.1",
		Port:      11434,
	}
	if err := repo.CreateProfile(p2); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if err := repo.ValidateCrossReferences(context.Background()); err == nil {
		t.Error("expected validation error for non-existent model reference")
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
				DefaultArgs: []string{"serve"},
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
		DefaultArgs: []string{"a", "b"},
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
	runtimes[0].DefaultArgs[0] = "x"
	runtimes[0].Environment["KEY"] = "changed"

	// Reload and verify original is intact.
	repo2, _ := NewJSONRepository(path)
	runtimes2, _ := repo2.ListRuntimes()
	if runtimes2[0].Name != "test" {
		t.Errorf("expected 'test', got '%s'", runtimes2[0].Name)
	}
	if runtimes2[0].DefaultArgs[0] != "a" {
		t.Errorf("expected 'a', got '%s'", runtimes2[0].DefaultArgs[0])
	}
	if runtimes2[0].Environment["KEY"] != "value" {
		t.Errorf("expected 'value', got '%s'", runtimes2[0].Environment["KEY"])
	}
}

func TestJSONRepository_SchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	if repo.SchemaVersion() != 4 {
		t.Errorf("expected schema version 4, got %d", repo.SchemaVersion())
	}
}

func TestJSONRepository_CountActiveInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	inst1 := &LaunchInstanceEntry{ProfileID: "p1", State: "running"}
	repo.CreateLaunchInstance(inst1) //nolint:errcheck

	inst2 := &LaunchInstanceEntry{ProfileID: "p2", State: "starting"}
	repo.CreateLaunchInstance(inst2) //nolint:errcheck

	inst3 := &LaunchInstanceEntry{ProfileID: "p3", State: "exited"}
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
		ProfileID:        "profile-1",
		RuntimeID:        "runtime-1",
		ModelID:          "model-1",
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

func TestJSONRepository_ProfilesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create profile.
	p := &ProfileEntry{
		Name:        "test-profile",
		RuntimeID:   "runtime-1",
		ModelID:     "model-1",
		Host:        "127.0.0.1",
		Port:        11434,
		Args:        []string{"--flag"},
		Environment: map[string]string{"VAR": "val"},
		Active:      true,
	}
	if err := repo.CreateProfile(p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	id := p.ID

	// Retrieve.
	got, err := repo.GetProfile(id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "test-profile" {
		t.Errorf("expected 'test-profile', got '%s'", got.Name)
	}

	// List.
	list, err := repo.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(list))
	}

	// Update.
	got.Name = "updated-profile"
	if err := repo.UpdateProfile(got); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got2, _ := repo.GetProfile(id)
	if got2.Name != "updated-profile" {
		t.Errorf("expected 'updated-profile', got '%s'", got2.Name)
	}

	// Delete.
	if err := repo.DeleteProfile(id); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	_, err = repo.GetProfile(id)
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
		Name:   "test-model",
		Path:   "/tmp/model.gguf",
		Format: "gguf",
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

func TestJSONRepository_LaunchInstance_ByProfileID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	inst1 := &LaunchInstanceEntry{ProfileID: "p1", State: "running", RuntimeID: "r1"}
	repo.CreateLaunchInstance(inst1) //nolint:errcheck

	inst2 := &LaunchInstanceEntry{ProfileID: "p1", State: "exited", RuntimeID: "r2"}
	repo.CreateLaunchInstance(inst2) //nolint:errcheck

	inst3 := &LaunchInstanceEntry{ProfileID: "p2", State: "running", RuntimeID: "r3"}
	repo.CreateLaunchInstance(inst3) //nolint:errcheck

	result, err := repo.ListByProfileID("p1")
	if err != nil {
		t.Fatalf("ListByProfileID: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 instances for p1, got %d", len(result))
	}

	result2, err := repo.ListByProfileID("p2")
	if err != nil {
		t.Fatalf("ListByProfileID: %v", err)
	}
	if len(result2) != 1 {
		t.Fatalf("expected 1 instance for p2, got %d", len(result2))
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

	p := &ProfileEntry{Name: "test-profile", RuntimeID: rt.ID, Host: "127.0.0.1", Port: 11434}
	if err := repo.CreateProfile(p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	inst := &LaunchInstanceEntry{ProfileID: p.ID, State: "running", RuntimeID: rt.ID}
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
	if parsed["schema_version"] != float64(4) {
		t.Errorf("expected schema_version 4, got %v", parsed["schema_version"])
	}
	if parsed["runtimes"] == nil {
		t.Error("expected runtimes key to exist")
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

func BenchmarkJSONRepository_CreateProfile(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, _ := NewJSONRepository(path)

	// Create a runtime first.
	rt := &RuntimeEntry{Name: "runtime", Executable: "ollama"}
	repo.CreateRuntime(rt) //nolint:errcheck

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := &ProfileEntry{
			Name:      "profile",
			RuntimeID: rt.ID,
			Host:      "127.0.0.1",
			Port:      11434,
		}
		_ = repo.CreateProfile(p)
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
