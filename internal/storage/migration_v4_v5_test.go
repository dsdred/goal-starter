package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigration_V4toV5_RealFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")

	// Create a realistic v4 repository file
	v4Fixture := map[string]interface{}{
		"schema_version": 4,
		"runtimes": []map[string]interface{}{
			{
				"id": "rt1", "name": "llama.cpp 10444",
				"executable": "llama-server.exe", "working_directory": `E:\tools\llama10444`,
				"default_args": []string{},
				"created_at":   "2025-01-15T10:00:00Z", "updated_at": "2025-01-15T10:00:00Z",
			},
		},
		"models": []map[string]interface{}{
			{
				"id": "m1", "name": "Qwen3.8-27B",
				"path": `E:\models\qwen\model.gguf`, "mmproj": `E:\models\qwen\mmproj.gguf`,
				"format":     "gguf",
				"arguments":  []string{"-ngl", "999", "-c", "200000"},
				"runtime_id": "rt1",
				"created_at": "2025-01-15T10:00:00Z", "updated_at": "2025-01-15T10:00:00Z",
			},
		},
		"profiles": []map[string]interface{}{
			{
				"id": "p1", "name": "Qwen Production",
				"runtime_id": "rt1", "model_id": "m1",
				"host": "0.0.0.0", "port": 8085,
				"args": []string{}, "active": true,
				"created_at": "2025-01-15T10:00:00Z", "updated_at": "2025-01-15T10:00:00Z",
			},
		},
		"instances": []map[string]interface{}{
			{
				"id": "p1-1234567890", "profile_id": "p1", "runtime_id": "rt1", "model_id": "m1",
				"executable":        `E:\tools\llama10444\llama-server.exe`,
				"args":              []string{"--host", "0.0.0.0", "--port", "8085", "-m", `E:\models\qwen\model.gguf`},
				"working_directory": `E:\tools\llama10444`,
				"state":             "exited", "pid": 12345, "exit_code": 0,
				"started_at": "2025-01-15T10:01:00Z", "stopped_at": "2025-01-15T10:30:00Z",
				"created_at": "2025-01-15T10:01:00Z", "updated_at": "2025-01-15T10:30:00Z",
			},
		},
	}

	data, err := json.MarshalIndent(v4Fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Load the v4 file with the current code (which expects v5)
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository (loading v4): %v", err)
	}

	// Verify entities preserved
	runtimes, err := repo.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("expected 1 runtime, got %d", len(runtimes))
	}
	if runtimes[0].Name != "llama.cpp 10444" {
		t.Errorf("runtime name = %q", runtimes[0].Name)
	}
	if runtimes[0].Executable != "llama-server.exe" {
		t.Errorf("runtime executable = %q", runtimes[0].Executable)
	}

	models, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "Qwen3.8-27B" {
		t.Errorf("model name = %q", models[0].Name)
	}
	if len(models[0].Arguments) != 4 {
		t.Errorf("model arguments = %v, expected 4 items", models[0].Arguments)
	}

	profiles, err := repo.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Name != "Qwen Production" {
		t.Errorf("profile name = %q", profiles[0].Name)
	}
	if profiles[0].Host != "0.0.0.0" {
		t.Errorf("profile host = %q", profiles[0].Host)
	}
	if profiles[0].Port != 8085 {
		t.Errorf("profile port = %d", profiles[0].Port)
	}
	// AutostartDelay should default to 0 (absent in v4)
	if profiles[0].AutostartDelay != 0 {
		t.Errorf("expected AutostartDelay=0 for v4 profile, got %d", profiles[0].AutostartDelay)
	}
	if !profiles[0].Active {
		t.Error("expected profile Active=true to be preserved")
	}

	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].State != "exited" {
		t.Errorf("instance state = %q, want exited", instances[0].State)
	}
	if instances[0].PID != 12345 {
		t.Errorf("instance PID = %d, want 12345", instances[0].PID)
	}

	// Save (should produce v5)
	if err := repo.Upgrade(); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// Verify file is now v5
	savedData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(savedData, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.SchemaVersion != 5 {
		t.Errorf("after Upgrade, schema_version = %d, want 5", saved.SchemaVersion)
	}

	// Reload v5 and verify all data still intact
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reload v5: %v", err)
	}
	runtimes2, _ := repo2.ListRuntimes()
	models2, _ := repo2.ListModels()
	profiles2, _ := repo2.ListProfiles()
	instances2, _ := repo2.ListInstances()

	if len(runtimes2) != 1 || len(models2) != 1 || len(profiles2) != 1 || len(instances2) != 1 {
		t.Errorf("after v5 reload: runtimes=%d models=%d profiles=%d instances=%d (want 1 each)",
			len(runtimes2), len(models2), len(profiles2), len(instances2))
	}
	if profiles2[0].AutostartDelay != 0 {
		t.Errorf("v5 profile AutostartDelay = %d, want 0", profiles2[0].AutostartDelay)
	}
	if profiles2[0].Active != true {
		t.Error("v5 profile Active should still be true")
	}

	_ = time.Now() // suppress unused import if time is only in fixture
}
