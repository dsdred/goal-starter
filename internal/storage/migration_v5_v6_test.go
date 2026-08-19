package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeV5(t *testing.T, dir string, fixture map[string]interface{}) string {
	t.Helper()
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goal_repo.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadMigrated(t *testing.T, path string) (Repository, []*RuntimeEntry, []*ModelEntry, []*LaunchInstanceEntry) {
	t.Helper()
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	runtimes, _ := repo.ListRuntimes()
	models, _ := repo.ListModels()
	instances, _ := repo.ListInstances()
	return repo, runtimes, models, instances
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

func sameArgs(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args = %#v, want %#v", got, want)
	}
}

func rtFixture(id string) map[string]interface{} {
	return map[string]interface{}{
		"id":                id,
		"name":              "RT " + id,
		"executable":        "rt.exe",
		"working_directory": "",
		"default_args":      []string{},
		"created_at":        "2025-01-01T00:00:00Z",
		"updated_at":        "2025-01-01T00:00:00Z",
	}
}

func pFixture(id string, extra map[string]interface{}) map[string]interface{} {
	base := map[string]interface{}{
		"id":         id,
		"name":       "P " + id,
		"runtime_id": "rt1",
		"host":       "127.0.0.1",
		"port":       8080,
		"args":       []string{},
		"active":     false,
		"created_at": "2025-01-10T00:00:00Z",
		"updated_at": "2025-01-10T00:00:00Z",
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func mFixture(id string, extra map[string]interface{}) map[string]interface{} {
	base := map[string]interface{}{
		"id":         id,
		"name":       "M " + id,
		"runtime_id": "rt1",
		"created_at": "2025-01-10T00:00:00Z",
		"updated_at": "2025-01-10T00:00:00Z",
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func baseFixture(runtimes, models, profiles, instances []map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"schema_version": 5,
		"runtimes":       runtimes,
		"models":         models,
		"profiles":       profiles,
		"instances":      instances,
	}
}

func TestMigration_V5toV6_EdgeCases(t *testing.T) {
	t.Run("Case1_ProfileWithoutModelID", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{"model_id": ""})},
			[]map[string]interface{}{},
		))
		_, runtimes, models, _ := loadMigrated(t, path)
		if len(runtimes) != 1 {
			t.Fatalf("expected 1 runtime, got %d", len(runtimes))
		}
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		if models[0].ID != "p1" {
			t.Errorf("model ID = %q, want p1", models[0].ID)
		}
		if len(models[0].Args) != 0 {
			t.Errorf("expected no folded args, got %v", models[0].Args)
		}
	})

	t.Run("Case2_ProfileReferencingMissingModel", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{
				"model_id": "ghost",
				"args":     []string{"-flag", "x"},
			})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		sameArgs(t, models[0].Args, []string{"-flag", "x"})
	})

	t.Run("Case3_MultipleProfilesShareSameModel", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":      `E:\models\shared.gguf`,
				"mmproj":    `E:\models\shared-mm.gguf`,
				"arguments": []string{"-ngl", "99"},
			})},
			[]map[string]interface{}{
				pFixture("p1", map[string]interface{}{"model_id": "m1"}),
				pFixture("p2", map[string]interface{}{"model_id": "m1"}),
			},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 2 {
			t.Fatalf("expected 2 models, got %d", len(models))
		}
		ids := map[string]bool{models[0].ID: true, models[1].ID: true}
		if !ids["p1"] || !ids["p2"] {
			t.Errorf("model IDs = %v %v, want p1 and p2", models[0].ID, models[1].ID)
		}
		expected := []string{"-m", `E:\models\shared.gguf`, "--mmproj", `E:\models\shared-mm.gguf`, "-ngl", "99"}
		sameArgs(t, models[0].Args, expected)
		sameArgs(t, models[1].Args, expected)
	})

	t.Run("Case4_UnreferencedOldModelsDiscarded", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{
				mFixture("m1", map[string]interface{}{"path": `E:\models\used.gguf`}),
				mFixture("m2", map[string]interface{}{"path": `E:\models\orphan.gguf`}),
			},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{"model_id": "m1"})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model (orphan discarded), got %d", len(models))
		}
		if models[0].ID == "m2" {
			t.Errorf("orphan old model m2 must not appear in migrated models")
		}
		if models[0].ID != "p1" {
			t.Errorf("model ID = %q, want p1", models[0].ID)
		}
		sameArgs(t, models[0].Args, []string{"-m", `E:\models\used.gguf`})
	})

	t.Run("Case5_EmptyModelPathSkipsMFilg", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":      "",
				"mmproj":    `E:\models\mm.gguf`,
				"arguments": []string{"-ngl", "1"},
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{"model_id": "m1"})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		sameArgs(t, models[0].Args, []string{"--mmproj", `E:\models\mm.gguf`, "-ngl", "1"})
		for _, a := range models[0].Args {
			if a == "-m" {
				t.Errorf("unexpected -m flag in args %v", models[0].Args)
			}
		}
	})

	t.Run("Case6_EmptyMMProjSkipsMMProjFlag", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":      `E:\models\m.gguf`,
				"mmproj":    "",
				"arguments": []string{"-c", "100"},
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{"model_id": "m1"})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		sameArgs(t, models[0].Args, []string{"-m", `E:\models\m.gguf`, "-c", "100"})
		for _, a := range models[0].Args {
			if a == "--mmproj" {
				t.Errorf("unexpected --mmproj flag in args %v", models[0].Args)
			}
		}
	})

	t.Run("Case7_EmptyModelArguments", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":      `E:\models\m.gguf`,
				"mmproj":    `E:\models\mm.gguf`,
				"arguments": []string{},
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{"model_id": "m1"})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		sameArgs(t, models[0].Args, []string{"-m", `E:\models\m.gguf`, "--mmproj", `E:\models\mm.gguf`})
	})

	t.Run("Case8_ProfileArgsComeFirst", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":      `E:\models\m.gguf`,
				"mmproj":    `E:\models\mm.gguf`,
				"arguments": []string{"-ngl", "99"},
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{
				"model_id": "m1",
				"args":     []string{"-temp", "1.0"},
			})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		sameArgs(t, models[0].Args, []string{"-temp", "1.0", "-m", `E:\models\m.gguf`, "--mmproj", `E:\models\mm.gguf`, "-ngl", "99"})
	})

	t.Run("Case9_DuplicateMFilgNotDeduplicated", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path": `E:\models\from-old-model.gguf`,
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{
				"model_id": "m1",
				"args":     []string{"-m", `E:\models\custom.gguf`},
			})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		// Migration does not deduplicate: profile's -m stays, old model path appended.
		sameArgs(t, models[0].Args, []string{"-m", `E:\models\custom.gguf`, "-m", `E:\models\from-old-model.gguf`})
	})

	t.Run("Case10_InstanceProfileIDBecomesModelID", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{},
			[]map[string]interface{}{pFixture("p1", nil)},
			[]map[string]interface{}{{
				"id":                "inst1",
				"profile_id":        "p1",
				"runtime_id":        "rt1",
				"model_id":          "old-m1",
				"executable":        `E:\rt\rt.exe`,
				"args":              []string{"--host", "0.0.0.0"},
				"working_directory": `E:\rt`,
				"environment":       map[string]string{"KEY": "VAL"},
				"state":             "exited",
				"pid":               4321,
				"exit_code":         7,
				"exit_class":        "crash",
				"last_error":        "boom",
				"started_at":        "2025-01-11T00:00:00Z",
				"stopped_at":        "2025-01-11T01:00:00Z",
				"created_at":        "2025-01-11T00:00:00Z",
				"updated_at":        "2025-01-11T01:00:00Z",
			}},
		))
		_, _, models, instances := loadMigrated(t, path)
		if len(instances) != 1 {
			t.Fatalf("expected 1 instance, got %d", len(instances))
		}
		inst := instances[0]
		if inst.ModelID != "p1" {
			t.Errorf("instance model_id = %q, want p1 (from profile_id)", inst.ModelID)
		}
		if inst.ID != "inst1" {
			t.Errorf("instance ID = %q, want inst1", inst.ID)
		}
		if inst.RuntimeID != "rt1" {
			t.Errorf("instance runtime_id = %q, want rt1", inst.RuntimeID)
		}
		if inst.Executable != `E:\rt\rt.exe` {
			t.Errorf("instance executable = %q", inst.Executable)
		}
		sameArgs(t, inst.Args, []string{"--host", "0.0.0.0"})
		if inst.WorkingDirectory != `E:\rt` {
			t.Errorf("instance working_directory = %q", inst.WorkingDirectory)
		}
		if !reflect.DeepEqual(inst.Environment, map[string]string{"KEY": "VAL"}) {
			t.Errorf("instance environment = %v", inst.Environment)
		}
		if inst.State != "exited" {
			t.Errorf("instance state = %q, want exited", inst.State)
		}
		if inst.PID != 4321 {
			t.Errorf("instance pid = %d, want 4321", inst.PID)
		}
		if inst.ExitCode != 7 {
			t.Errorf("instance exit_code = %d, want 7", inst.ExitCode)
		}
		if inst.ExitClass != "crash" {
			t.Errorf("instance exit_class = %q, want crash", inst.ExitClass)
		}
		if inst.LastError != "boom" {
			t.Errorf("instance last_error = %q, want boom", inst.LastError)
		}
		if !inst.StartedAt.Equal(mustTime(t, "2025-01-11T00:00:00Z")) {
			t.Errorf("instance started_at = %v", inst.StartedAt)
		}
		if !inst.StoppedAt.Equal(mustTime(t, "2025-01-11T01:00:00Z")) {
			t.Errorf("instance stopped_at = %v", inst.StoppedAt)
		}
		if !inst.CreatedAt.Equal(mustTime(t, "2025-01-11T00:00:00Z")) {
			t.Errorf("instance created_at = %v", inst.CreatedAt)
		}
		if !inst.UpdatedAt.Equal(mustTime(t, "2025-01-11T01:00:00Z")) {
			t.Errorf("instance updated_at = %v", inst.UpdatedAt)
		}
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
	})

	t.Run("Case11_EnvironmentPreserved", func(t *testing.T) {
		dir := t.TempDir()
		env := map[string]string{"LLAMA_ARG1": "a", "PATH_EXT": `C:\bin`, "EMPTY": ""}
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{"environment": env})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		if !reflect.DeepEqual(models[0].Environment, env) {
			t.Errorf("model environment = %v, want %v", models[0].Environment, env)
		}
	})

	t.Run("Case12_AutostartFieldsPreserved", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{
				"active":          true,
				"autostart_delay": 30,
			})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		if !models[0].Active {
			t.Error("expected Active=true preserved")
		}
		if models[0].AutostartDelay != 30 {
			t.Errorf("AutostartDelay = %d, want 30", models[0].AutostartDelay)
		}
	})

	t.Run("Case13_UpdatedAtTakesLaterModelTime", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":       `E:\models\m.gguf`,
				"updated_at": "2025-02-01T00:00:00Z",
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{
				"model_id":   "m1",
				"created_at": "2025-01-01T00:00:00Z",
				"updated_at": "2025-01-15T00:00:00Z",
			})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		if !models[0].UpdatedAt.Equal(mustTime(t, "2025-02-01T00:00:00Z")) {
			t.Errorf("model updated_at = %v, want 2025-02-01 (later model time)", models[0].UpdatedAt)
		}
		if !models[0].CreatedAt.Equal(mustTime(t, "2025-01-01T00:00:00Z")) {
			t.Errorf("model created_at = %v, want profile created_at", models[0].CreatedAt)
		}
	})

	t.Run("Case14_UpdatedAtTakesProfileTimeWhenLater", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{rtFixture("rt1")},
			[]map[string]interface{}{mFixture("m1", map[string]interface{}{
				"path":       `E:\models\m.gguf`,
				"updated_at": "2025-02-01T00:00:00Z",
			})},
			[]map[string]interface{}{pFixture("p1", map[string]interface{}{
				"model_id":   "m1",
				"created_at": "2025-01-01T00:00:00Z",
				"updated_at": "2025-03-01T00:00:00Z",
			})},
			[]map[string]interface{}{},
		))
		_, _, models, _ := loadMigrated(t, path)
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		if !models[0].UpdatedAt.Equal(mustTime(t, "2025-03-01T00:00:00Z")) {
			t.Errorf("model updated_at = %v, want 2025-03-01 (later profile time)", models[0].UpdatedAt)
		}
	})

	t.Run("Case15_EmptyRepository", func(t *testing.T) {
		dir := t.TempDir()
		path := writeV5(t, dir, baseFixture(
			[]map[string]interface{}{},
			[]map[string]interface{}{},
			[]map[string]interface{}{},
			[]map[string]interface{}{},
		))
		repo, runtimes, models, instances := loadMigrated(t, path)
		if len(runtimes) != 0 {
			t.Errorf("expected 0 runtimes, got %d", len(runtimes))
		}
		if len(models) != 0 {
			t.Errorf("expected 0 models, got %d", len(models))
		}
		if len(instances) != 0 {
			t.Errorf("expected 0 instances, got %d", len(instances))
		}
		if repo.SchemaVersion() != 6 {
			t.Errorf("SchemaVersion = %d, want 6", repo.SchemaVersion())
		}
		if err := repo.Upgrade(); err != nil {
			t.Fatalf("Upgrade: %v", err)
		}
		repo2, runtimes2, models2, instances2 := loadMigrated(t, path)
		if repo2.SchemaVersion() != 6 {
			t.Errorf("reloaded SchemaVersion = %d, want 6", repo2.SchemaVersion())
		}
		if len(runtimes2) != 0 || len(models2) != 0 || len(instances2) != 0 {
			t.Errorf("after reload: runtimes=%d models=%d instances=%d (want 0 each)",
				len(runtimes2), len(models2), len(instances2))
		}
	})
}
