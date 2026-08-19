package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMigration_V5toV6_RealFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")

	// Create a realistic v5 repository file.
	v5Fixture := map[string]interface{}{
		"schema_version": 5,
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
				"id": "p1-1234567890", "profile_id": "p1", "runtime_id": "rt1",
				"executable":        `E:\tools\llama10444\llama-server.exe`,
				"args":              []string{"--host", "0.0.0.0", "--port", "8085", "-m", `E:\models\qwen\model.gguf`},
				"working_directory": `E:\tools\llama10444`,
				"state":             "exited", "pid": 12345, "exit_code": 0,
				"started_at": "2025-01-15T10:01:00Z", "stopped_at": "2025-01-15T10:30:00Z",
				"created_at": "2025-01-15T10:01:00Z", "updated_at": "2025-01-15T10:30:00Z",
			},
		},
	}

	data, err := json.MarshalIndent(v5Fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Load the v5 file — should auto-migrate to v6.
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository (loading v5): %v", err)
	}

	// Verify runtimes preserved.
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

	// Verify old profiles became new models.
	models, err := repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model (from profile), got %d", len(models))
	}
	if models[0].ID != "p1" {
		t.Errorf("model ID = %q, want p1 (from profile ID)", models[0].ID)
	}
	if models[0].Name != "Qwen Production" {
		t.Errorf("model name = %q, want Qwen Production", models[0].Name)
	}
	if models[0].RuntimeID != "rt1" {
		t.Errorf("model runtime_id = %q, want rt1", models[0].RuntimeID)
	}
	if models[0].Host != "0.0.0.0" {
		t.Errorf("model host = %q, want 0.0.0.0", models[0].Host)
	}
	if models[0].Port != 8085 {
		t.Errorf("model port = %d, want 8085", models[0].Port)
	}
	if !models[0].Active {
		t.Error("expected model Active=true to be preserved")
	}

	// Verify old model args got folded into profile args.
	// The old model had: path=E:\models\qwen\model.gguf, mmproj=E:\models\qwen\mmproj.gguf
	// arguments=["-ngl", "999", "-c", "200000"]
	// So the new model args should be: ["-m", path, "--mmproj", mmproj, "-ngl", "999", "-c", "200000"]
	expectedArgs := []string{"-m", `E:\models\qwen\model.gguf`, "--mmproj", `E:\models\qwen\mmproj.gguf`, "-ngl", "999", "-c", "200000"}
	if len(models[0].Args) != len(expectedArgs) {
		t.Fatalf("model args = %v (len=%d), want %v (len=%d)", models[0].Args, len(models[0].Args), expectedArgs, len(expectedArgs))
	}
	for i, a := range expectedArgs {
		if models[0].Args[i] != a {
			t.Errorf("model args[%d] = %q, want %q", i, models[0].Args[i], a)
		}
	}

	// Verify instances got model_id from profile_id.
	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].ModelID != "p1" {
		t.Errorf("instance model_id = %q, want p1 (from profile_id)", instances[0].ModelID)
	}
	if instances[0].RuntimeID != "rt1" {
		t.Errorf("instance runtime_id = %q, want rt1", instances[0].RuntimeID)
	}
	if instances[0].State != "exited" {
		t.Errorf("instance state = %q, want exited", instances[0].State)
	}
	if instances[0].PID != 12345 {
		t.Errorf("instance PID = %d, want 12345", instances[0].PID)
	}

	// Save (should produce v6).
	if err := repo.Upgrade(); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// Verify file is now v6.
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
	if saved.SchemaVersion != 6 {
		t.Errorf("after Upgrade, schema_version = %d, want 6", saved.SchemaVersion)
	}

	// Reload v6 and verify all data still intact.
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reload v6: %v", err)
	}
	runtimes2, _ := repo2.ListRuntimes()
	models2, _ := repo2.ListModels()
	instances2, _ := repo2.ListInstances()

	if len(runtimes2) != 1 || len(models2) != 1 || len(instances2) != 1 {
		t.Errorf("after v6 reload: runtimes=%d models=%d instances=%d (want 1 each)",
			len(runtimes2), len(models2), len(instances2))
	}
	if models2[0].Active != true {
		t.Error("v6 model Active should still be true")
	}
	if models2[0].Name != "Qwen Production" {
		t.Errorf("v6 model name = %q, want Qwen Production", models2[0].Name)
	}
}
