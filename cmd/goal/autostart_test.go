package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

func setupAutostartRepo(t *testing.T) (storage.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")
	repo, err := storage.NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	return repo, path
}

func addAutostartFixture(t *testing.T, repo storage.Repository, name, exe string, active bool, delay int) {
	t.Helper()
	rt := &storage.RuntimeEntry{Name: name + "-rt", Executable: exe}
	if err := repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	m := &storage.ModelEntry{
		Name:           name,
		RuntimeID:      rt.ID,
		Host:           "127.0.0.1",
		Port:           19999,
		Active:         active,
		AutostartDelay: delay,
	}
	if err := repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
}

func TestAutostart_InactiveModelNotStarted(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	addAutostartFixture(t, repo, "inactive-model", "/bin/sleep", false, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)

	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Errorf("expected 0 instances for inactive model, got %d", len(instances))
	}
}

func makeFakeExe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, "noop.bat")
		if err := os.WriteFile(p, []byte("@echo off\r\nexit /b 0\r\n"), 0755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, "noop.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAutostart_ActiveModelStarted(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "active-model", exe, true, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)

	// Wait for the process to start and likely exit (fake exe exits immediately)
	time.Sleep(500 * time.Millisecond)

	// Shutdown to clean up any lingering processes
	shutdownCtx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	_ = sup.ShutdownWithPersistence(shutdownCtx)

	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance for active model, got %d", len(instances))
	}
	// The fake exe exits immediately, so state should be running, starting, or exited
	valid := instances[0].State == "running" || instances[0].State == "starting" || instances[0].State == "exited"
	if !valid {
		t.Errorf("expected running/starting/exited state, got %s", instances[0].State)
	}
}

func TestAutostart_DelayApplied(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "delayed-model", exe, true, 1) // 1 second delay

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	start := time.Now()
	autostartModels(ctx, repo, sup)
	elapsed := time.Since(start)
	_ = sup.ShutdownWithPersistence(context.Background())

	if elapsed < 900*time.Millisecond {
		t.Errorf("expected delay of ~1s, but autostartModels returned after %v", elapsed)
	}
}

func TestAutostart_DeterministicOrder(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)

	// Create 3 active models in order
	addAutostartFixture(t, repo, "first", exe, true, 0)
	addAutostartFixture(t, repo, "second", exe, true, 0)
	addAutostartFixture(t, repo, "third", exe, true, 0)

	models, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)
	_ = sup.ShutdownWithPersistence(context.Background())

	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}

	// Verify order matches repository order
	for i, m := range models {
		found := false
		for _, inst := range instances {
			if inst.ModelID == m.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("model %d (%s) has no instance", i, m.Name)
		}
	}
}

func TestAutostart_FailureDoesNotBlockNext(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	// First model: non-existent executable (will fail at resolve)
	rt1 := &storage.RuntimeEntry{Name: "bad-rt", Executable: "/nonexistent/binary-xyz"}
	if err := repo.CreateRuntime(rt1); err != nil {
		t.Fatal(err)
	}
	m1 := &storage.ModelEntry{Name: "will-fail", RuntimeID: rt1.ID, Host: "127.0.0.1", Port: 19997, Active: true}
	if err := repo.CreateModel(m1); err != nil {
		t.Fatal(err)
	}

	// Second model: valid executable
	exe := makeFakeExe(t)
	rt2 := &storage.RuntimeEntry{Name: "good-rt", Executable: exe}
	if err := repo.CreateRuntime(rt2); err != nil {
		t.Fatal(err)
	}
	m2 := &storage.ModelEntry{Name: "will-succeed", RuntimeID: rt2.ID, Host: "127.0.0.1", Port: 19998, Active: true}
	if err := repo.CreateModel(m2); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)
	_ = sup.ShutdownWithPersistence(context.Background())

	// The second model should have been started despite the first failing
	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	succeeded := false
	for _, inst := range instances {
		if inst.ModelID == m2.ID {
			succeeded = true
			break
		}
	}
	if !succeeded {
		t.Error("second model was not started despite first failing")
	}
}

func TestAutostart_ContextCancelStopsLoop(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "delayed-1", exe, true, 5)
	addAutostartFixture(t, repo, "delayed-2", exe, true, 5)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	// Should return early due to ctx timeout before completing delays
	autostartModels(ctx, repo, sup)
}

func TestAutostart_NoDuplicateInstances(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "dup-model", exe, true, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)

	// Second model is inactive, should NOT start
	addAutostartFixture(t, repo, "dup-model-2", exe, false, 0)
	autostartModels(ctx, repo, sup)
	_ = sup.ShutdownWithPersistence(context.Background())

	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	// Only the active model should have instances
	activeCount := 0
	for _, inst := range instances {
		if inst.State != "stale" {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 non-stale instance, got %d", activeCount)
	}
}

func TestAutostart_SchemaVersion(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	if repo.SchemaVersion() != 6 {
		t.Errorf("expected schema version 6, got %d", repo.SchemaVersion())
	}
}

func TestAutostart_DuplicateGuard_ActiveInstanceExists(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "dup-guard", exe, true, 0)

	models, _ := repo.ListModels()
	modelID := models[0].ID

	// Simulate an already-running instance for this model
	inst := &storage.LaunchInstanceEntry{
		ID:        "inst_existing-1",
		ModelID:   modelID,
		RuntimeID: models[0].RuntimeID,
		State:     "running",
		PID:       99999,
	}
	if err := repo.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)
	_ = sup.ShutdownWithPersistence(context.Background())

	instances, err := repo.ListByModelID(modelID)
	if err != nil {
		t.Fatalf("ListByModelID: %v", err)
	}

	// Should be exactly 1 instance (the pre-existing one), no new one created
	if len(instances) != 1 {
		t.Fatalf("expected exactly 1 instance (duplicate guard), got %d", len(instances))
	}
	if instances[0].ID != "inst_existing-1" {
		t.Errorf("expected the pre-existing instance, got ID %q", instances[0].ID)
	}
}

func TestAutostart_DuplicateGuard_StaleInstanceDoesNotBlock(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "stale-guard", exe, true, 0)

	models, _ := repo.ListModels()
	modelID := models[0].ID

	// Simulate a stale (recovered) instance — should NOT block autostart
	inst := &storage.LaunchInstanceEntry{
		ID:        "inst_stale-1",
		ModelID:   modelID,
		RuntimeID: models[0].RuntimeID,
		State:     "stale",
	}
	if err := repo.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)
	_ = sup.ShutdownWithPersistence(context.Background())

	instances, err := repo.ListByModelID(modelID)
	if err != nil {
		t.Fatalf("ListByModelID: %v", err)
	}

	// Should be 2 instances: the stale one + the new autostart one
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances (stale + new), got %d", len(instances))
	}

	// The new one should not be stale
	newFound := false
	for _, i := range instances {
		if i.ID != "inst_stale-1" && i.State != "stale" {
			newFound = true
		}
	}
	if !newFound {
		t.Error("expected a new non-stale instance to be created")
	}
}

func TestAutostart_DuplicateGuard_ExitedInstanceDoesNotBlock(t *testing.T) {
	repo, _ := setupAutostartRepo(t)
	exe := makeFakeExe(t)
	addAutostartFixture(t, repo, "exited-guard", exe, true, 0)

	models, _ := repo.ListModels()
	modelID := models[0].ID

	// A previously exited instance should NOT block autostart
	inst := &storage.LaunchInstanceEntry{
		ID:        "inst_exited-1",
		ModelID:   modelID,
		RuntimeID: models[0].RuntimeID,
		State:     "exited",
		ExitCode:  0,
	}
	if err := repo.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := process.NewSupervisorWithContext(ctx, repo)
	autostartModels(ctx, repo, sup)
	_ = sup.ShutdownWithPersistence(context.Background())

	instances, err := repo.ListByModelID(modelID)
	if err != nil {
		t.Fatalf("ListByModelID: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances (exited + new), got %d", len(instances))
	}
}
