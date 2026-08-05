package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

func TestInstanceStoreJSON_CreateAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Now().UTC()
	inst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-001"),
		ProfileID: "profile-1",
		RuntimeID: "runtime-1",
		ModelID:   "model-1",
		PID:       1234,
		State:     domain.InstanceStateRunning,
		StartedAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	got, err := store.Get(domain.InstanceID("test-001"))
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}

	if got.ID != inst.ID {
		t.Errorf("expected ID %s, got %s", inst.ID, got.ID)
	}
	if got.PID != inst.PID {
		t.Errorf("expected PID %d, got %d", inst.PID, got.PID)
	}
	if got.State != inst.State {
		t.Errorf("expected state %s, got %s", inst.State, got.State)
	}
}

func TestInstanceStoreJSON_Update(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Now().UTC()
	inst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-002"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	// Update state to exited.
	stoppedAt := now.Add(1 * time.Minute)
	inst.State = domain.InstanceStateExited
	inst.StoppedAt = stoppedAt
	inst.ExitCode = ptrInt(0)

	if err := store.Update(inst); err != nil {
		t.Fatalf("update instance: %v", err)
	}

	got, err := store.Get(domain.InstanceID("test-002"))
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}

	if got.State != domain.InstanceStateExited {
		t.Errorf("expected state exited, got %s", got.State)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", got.ExitCode)
	}
}

func TestInstanceStoreJSON_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Now().UTC()
	inst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-003"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if err := store.Delete(domain.InstanceID("test-003")); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	_, err = store.Get(domain.InstanceID("test-003"))
	if err == nil {
		t.Fatal("expected error for deleted instance")
	}
}

func TestInstanceStoreJSON_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		inst := &domain.LaunchInstance{
			ID:        domain.InstanceID("test-list-" + string(rune('a'+i))),
			ProfileID: "profile-1",
			State:     domain.InstanceStateExited,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.Create(inst); err != nil {
			t.Fatalf("create instance %d: %v", i, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}

	if len(list) != 5 {
		t.Errorf("expected 5 instances, got %d", len(list))
	}
}

func TestInstanceStoreJSON_FindByProfileID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Now().UTC()

	// Create instances for different profiles.
	profile1 := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-profile1-1"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
		CreatedAt: now,
		UpdatedAt: now,
	}
	profile2 := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-profile2-1"),
		ProfileID: "profile-2",
		State:     domain.InstanceStateExited,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Create(profile1); err != nil {
		t.Fatalf("create profile1 instance: %v", err)
	}
	if err := store.Create(profile2); err != nil {
		t.Fatalf("create profile2 instance: %v", err)
	}

	result, err := store.FindByProfileID("profile-1")
	if err != nil {
		t.Fatalf("find by profile id: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 instance for profile-1, got %d", len(result))
	}
	if len(result) > 0 && result[0].ProfileID != "profile-1" {
		t.Errorf("expected profile-1, got %s", result[0].ProfileID)
	}
}

func TestInstanceStoreJSON_CountActive(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Now().UTC()

	// Create active instances.
	active1 := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-active-1"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	active2 := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-active-2"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Create exited instance.
	exited := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-exited-1"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
		CreatedAt: now,
		UpdatedAt: now,
	}

	store.Create(active1)
	store.Create(active2)
	store.Create(exited)

	count := store.CountActive()
	if count != 2 {
		t.Errorf("expected 2 active instances, got %d", count)
	}
}

func TestInstanceStoreJSON_Persistence(t *testing.T) {
	dir := t.TempDir()
	filename := "instances.json"

	now := time.Now().UTC()
	inst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-persist-1"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
		ExitCode:  ptrInt(1),
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Create and persist.
	store1, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  filename,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store1.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	// Reopen store.
	store2, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  filename,
	})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	got, err := store2.Get(domain.InstanceID("test-persist-1"))
	if err != nil {
		t.Fatalf("get instance after reopen: %v", err)
	}

	if got.ID != inst.ID {
		t.Errorf("expected ID %s, got %s", inst.ID, got.ID)
	}
	if got.ExitCode == nil || *got.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %v", got.ExitCode)
	}
	if got.State != domain.InstanceStateExited {
		t.Errorf("expected state exited, got %s", got.State)
	}
}

func TestInstanceStoreJSON_CleanupTerminal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	oldTime := time.Now().UTC().Add(-2 * time.Hour)
	now := time.Now().UTC()

	// Create old terminal instance.
	oldInst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-old"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
		StoppedAt: oldTime,
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Create recent terminal instance.
	recentInst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-recent"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
		StoppedAt: now.Add(-5 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Create active instance.
	activeInst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-active"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}

	store.Create(oldInst)
	store.Create(recentInst)
	store.Create(activeInst)

	// Cleanup instances older than 1 hour.
	cleaned := store.CleanupTerminal(1 * time.Hour)
	if cleaned != 1 {
		t.Errorf("expected 1 cleaned instance, got %d", cleaned)
	}

	// Verify old instance is gone.
	_, err = store.Get(domain.InstanceID("test-old"))
	if err == nil {
		t.Fatal("expected old instance to be cleaned up")
	}

	// Verify recent instance still exists.
	_, err = store.Get(domain.InstanceID("test-recent"))
	if err != nil {
		t.Errorf("expected recent instance to still exist: %v", err)
	}

	// Verify active instance still exists.
	_, err = store.Get(domain.InstanceID("test-active"))
	if err != nil {
		t.Errorf("expected active instance to still exist: %v", err)
	}
}

func TestInstanceStoreJSON_Error_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  "instances.json",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_, err = store.Get(domain.InstanceID("non-existent"))
	if err == nil {
		t.Fatal("expected error for non-existent instance")
	}

	err = store.Update(&domain.LaunchInstance{
		ID:        domain.InstanceID("non-existent"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateExited,
	})
	if err == nil {
		t.Fatal("expected error when updating non-existent instance")
	}

	err = store.Delete(domain.InstanceID("non-existent"))
	if err == nil {
		t.Fatal("expected error when deleting non-existent instance")
	}
}

func TestInstanceStoreJSON_EnvironmentToList(t *testing.T) {
	now := time.Now().UTC()
	inst := &domain.LaunchInstance{
		ID:        domain.InstanceID("test-env"),
		ProfileID: "profile-1",
		State:     domain.InstanceStateRunning,
		Environment: map[string]string{
			"PATH": "/usr/bin",
			"HOME": "/root",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	result := inst.EnvironmentToList()

	// Result should have exactly len(Environment) items.
	if len(result) != len(inst.Environment) {
		t.Errorf("expected %d items, got %d", len(inst.Environment), len(result))
	}

	// Check that all keys are present in the result.
	foundKeys := make(map[string]bool)
	for _, item := range result {
		for k := range inst.Environment {
			prefix := k + "="
			if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
				foundKeys[k] = true
			}
		}
	}

	for k := range inst.Environment {
		if !foundKeys[k] {
			t.Errorf("expected key %s in result", k)
		}
	}
}

func TestInstanceStoreJSON_InstanceState(t *testing.T) {
	// Test IsActive.
	running := &domain.LaunchInstance{
		State: domain.InstanceStateRunning,
	}
	if !running.IsActive() {
		t.Error("running instance should be active")
	}

	exited := &domain.LaunchInstance{
		State: domain.InstanceStateExited,
	}
	if exited.IsActive() {
		t.Error("exited instance should not be active")
	}

	// Test IsTerminal.
	if !exited.IsTerminal() {
		t.Error("exited instance should be terminal")
	}
	if running.IsTerminal() {
		t.Error("running instance should not be terminal")
	}
}

func TestInstanceStoreJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	filename := "instances.json"

	// Write invalid JSON.
	invalidPath := filepath.Join(dir, filename)
	if err := os.WriteFile(invalidPath, []byte("{invalid json}"), 0o644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}

	_, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  filename,
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON file")
	}
}

func TestInstanceStoreJSON_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	filename := "instances.json"

	// Write empty file.
	emptyPath := filepath.Join(dir, filename)
	if err := os.WriteFile(emptyPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write empty json: %v", err)
	}

	store, err := NewInstanceStoreJSON(InstanceStoreOptions{
		Directory: dir,
		Filename:  filename,
	})
	if err != nil {
		t.Fatalf("create store from empty json: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 instances, got %d", len(list))
	}
}

// ptrInt is a helper to create *int.
func ptrInt(i int) *int {
	return &i
}
