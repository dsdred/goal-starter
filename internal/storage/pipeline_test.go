package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeV7 writes a schema v7 repository file (no pipelines key).
func writeV7(t *testing.T, dir string, runtimes, models, instances []map[string]interface{}) string {
	t.Helper()
	fixture := map[string]interface{}{
		"schema_version": 7,
		"runtimes":       runtimes,
		"models":         models,
		"instances":      instances,
	}
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

func v7Runtime(id string) map[string]interface{} {
	return map[string]interface{}{
		"id":                id,
		"name":              "RT " + id,
		"executable":        "rt.exe",
		"working_directory": "",
		"created_at":        "2025-06-01T00:00:00Z",
		"updated_at":        "2025-06-01T00:00:00Z",
	}
}

func v7Model(id string) map[string]interface{} {
	return map[string]interface{}{
		"id":         id,
		"name":       "M " + id,
		"runtime_id": "rt1",
		"args":       []string{"-m", "model.gguf"},
		"active":     true,
		"created_at": "2025-06-02T00:00:00Z",
		"updated_at": "2025-06-02T00:00:00Z",
	}
}

func v7Instance(id, modelID string, state string) map[string]interface{} {
	return map[string]interface{}{
		"id":         id,
		"model_id":   modelID,
		"runtime_id": "rt1",
		"executable": "rt.exe",
		"args":       []string{"-m", "model.gguf"},
		"state":      state,
		"created_at": "2025-06-03T00:00:00Z",
		"updated_at": "2025-06-03T01:00:00Z",
	}
}

func readRepoFile(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repo file: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal repo file: %v", err)
	}
	return out
}

// ADR 010 acceptance 1: a v7 file loads unchanged (runtimes/models/instances
// intact) and saves as v8 with an empty pipelines list.
func TestPipeline_MigrationV7toV8(t *testing.T) {
	dir := t.TempDir()
	path := writeV7(t, dir,
		[]map[string]interface{}{v7Runtime("rt1")},
		[]map[string]interface{}{v7Model("m1")},
		[]map[string]interface{}{v7Instance("inst1", "m1", "exited")},
	)

	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}

	runtimes, _ := repo.ListRuntimes()
	models, _ := repo.ListModels()
	instances, _ := repo.ListInstances()
	pipelines, _ := repo.ListPipelines()

	if len(runtimes) != 1 || runtimes[0].ID != "rt1" || runtimes[0].Name != "RT rt1" {
		t.Fatalf("runtimes not preserved: %+v", runtimes)
	}
	if len(models) != 1 || models[0].ID != "m1" || !models[0].Active {
		t.Fatalf("models not preserved: %+v", models)
	}
	if !reflect.DeepEqual(models[0].Args, []string{"-m", "model.gguf"}) {
		t.Fatalf("model args not preserved: %v", models[0].Args)
	}
	if len(instances) != 1 || instances[0].ID != "inst1" || instances[0].State != "exited" {
		t.Fatalf("instances not preserved: %+v", instances)
	}
	if instances[0].PipelineID != "" {
		t.Fatalf("v7 instance must have empty pipeline_id, got %q", instances[0].PipelineID)
	}
	if len(pipelines) != 0 {
		t.Fatalf("v7 file must load with an empty pipeline list, got %d", len(pipelines))
	}

	if repo.SchemaVersion() != 8 {
		t.Fatalf("SchemaVersion = %d, want 8", repo.SchemaVersion())
	}

	// Save upgrades the file to v8 with an explicit empty pipelines list.
	if err := repo.Upgrade(); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	file := readRepoFile(t, path)
	if file["schema_version"] != float64(8) {
		t.Fatalf("saved schema_version = %v, want 8", file["schema_version"])
	}
	pipelinesRaw, ok := file["pipelines"]
	if !ok {
		t.Fatal("saved file must carry the pipelines key")
	}
	if list, _ := pipelinesRaw.([]interface{}); list == nil || len(list) != 0 {
		t.Fatalf("saved pipelines = %v, want empty list", pipelinesRaw)
	}

	// Reload: everything still intact.
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := repo2.ListModels(); len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("models lost after reload: %+v", got)
	}
	if got, _ := repo2.ListPipelines(); len(got) != 0 {
		t.Fatalf("pipelines after reload = %d, want 0", len(got))
	}
}

// ADR 010 acceptance 1: v8 round-trips, including active and per-entry
// auto_start; absent fields load as false.
func TestPipeline_V8RoundTrip(t *testing.T) {
	dir := t.TempDir()
	fixture := map[string]interface{}{
		"schema_version": 8,
		"runtimes":       []map[string]interface{}{v7Runtime("rt1")},
		"models":         []map[string]interface{}{v7Model("m1"), v7Model("m2")},
		"instances": []map[string]interface{}{{
			"id":          "inst-owned",
			"model_id":    "m1",
			"runtime_id":  "rt1",
			"pipeline_id": "pipe-1",
			"state":       "exited",
			"created_at":  "2025-06-03T00:00:00Z",
			"updated_at":  "2025-06-03T01:00:00Z",
		}},
		"pipelines": []map[string]interface{}{{
			"id":     "pipe-1",
			"name":   "Local cluster",
			"active": true,
			"models": []map[string]interface{}{
				{"model_id": "m1", "auto_start": true},
				{"model_id": "m2", "args": []string{"-m", "other.gguf"}, "auto_start": false},
			},
			"created_at": "2025-06-04T00:00:00Z",
			"updated_at": "2025-06-05T00:00:00Z",
		}},
	}
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goal_repo.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	p, err := repo.GetPipeline("pipe-1")
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if p.Name != "Local cluster" || !p.Active {
		t.Fatalf("pipeline fields not preserved: %+v", p)
	}
	if len(p.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(p.Models))
	}
	if !p.Models[0].AutoStart || p.Models[0].ModelID != "m1" || len(p.Models[0].Args) != 0 {
		t.Fatalf("entry 1 = %+v, want m1 auto_start=true empty args", p.Models[0])
	}
	if p.Models[1].AutoStart || !reflect.DeepEqual(p.Models[1].Args, []string{"-m", "other.gguf"}) {
		t.Fatalf("entry 2 = %+v, want override args auto_start=false", p.Models[1])
	}
	if !p.CreatedAt.Equal(mustTime(t, "2025-06-04T00:00:00Z")) || !p.UpdatedAt.Equal(mustTime(t, "2025-06-05T00:00:00Z")) {
		t.Fatalf("timestamps not preserved: %+v", p)
	}

	// Instance attribution round-trips too.
	inst, err := repo.GetInstance("inst-owned")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.PipelineID != "pipe-1" {
		t.Fatalf("instance pipeline_id = %q, want pipe-1", inst.PipelineID)
	}

	// Round-trip: save and reload.
	if err := repo.Upgrade(); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	p2, err := repo2.GetPipeline("pipe-1")
	if err != nil {
		t.Fatalf("reloaded GetPipeline: %v", err)
	}
	if !reflect.DeepEqual(p2.Models, p.Models) || p2.Active != p.Active || p2.Name != p.Name {
		t.Fatalf("round-trip mismatch: %+v vs %+v", p2, p)
	}
}

// Absent active / auto_start values load as false (a pipeline never
// autostarts by accident).
func TestPipeline_AbsentFlagFieldsLoadAsFalse(t *testing.T) {
	dir := t.TempDir()
	fixture := map[string]interface{}{
		"schema_version": 8,
		"runtimes":       []interface{}{},
		"models":         []interface{}{},
		"instances":      []interface{}{},
		"pipelines": []map[string]interface{}{{
			"id":         "pipe-2",
			"name":       "Defaults",
			"models":     []map[string]interface{}{{"model_id": "m1"}},
			"created_at": "2025-06-04T00:00:00Z",
			"updated_at": "2025-06-04T00:00:00Z",
		}},
	}
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goal_repo.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	p, err := repo.GetPipeline("pipe-2")
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if p.Active {
		t.Error("absent active must load as false")
	}
	if p.Models[0].AutoStart {
		t.Error("absent auto_start must load as false")
	}
}

func TestPipeline_CRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}

	e := &PipelineEntry{
		Name:   "cluster",
		Active: true,
		Models: []PipelineModel{
			{ModelID: "m1", AutoStart: true},
			{ModelID: "m2", Args: []string{"-x", "1"}},
		},
	}
	if err := repo.CreatePipeline(e); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	if e.ID == "" {
		t.Fatal("CreatePipeline must generate an ID")
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", e)
	}

	// Duplicate ID is rejected.
	if err := repo.CreatePipeline(&PipelineEntry{ID: e.ID, Name: "dup"}); err == nil {
		t.Fatal("duplicate pipeline ID must be rejected")
	}

	got, err := repo.GetPipeline(e.ID)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if got.Name != "cluster" || !got.Active || len(got.Models) != 2 {
		t.Fatalf("GetPipeline mismatch: %+v", got)
	}

	// List order is creation order.
	if err := repo.CreatePipeline(&PipelineEntry{Name: "second", Models: []PipelineModel{{ModelID: "m1"}}}); err != nil {
		t.Fatalf("CreatePipeline second: %v", err)
	}
	list, err := repo.ListPipelines()
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	if len(list) != 2 || list[0].ID != e.ID || list[1].Name != "second" {
		t.Fatalf("ListPipelines order/content: %+v", list)
	}

	// Unknown ID.
	if _, err := repo.GetPipeline("missing"); err == nil {
		t.Fatal("GetPipeline(missing) must fail")
	}
	if err := repo.UpdatePipeline(&PipelineEntry{ID: "missing", Name: "x"}); err == nil {
		t.Fatal("UpdatePipeline(missing) must fail")
	}
	if err := repo.DeletePipeline("missing"); err == nil {
		t.Fatal("DeletePipeline(missing) must fail")
	}

	// Update persists and preserves the entry's CreatedAt.
	created := got.CreatedAt
	got.Name = "renamed"
	got.Models[0].Args = []string{"-y", "2"}
	if err := repo.UpdatePipeline(got); err != nil {
		t.Fatalf("UpdatePipeline: %v", err)
	}
	repo2, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	again, err := repo2.GetPipeline(e.ID)
	if err != nil {
		t.Fatalf("reloaded GetPipeline: %v", err)
	}
	if again.Name != "renamed" || !reflect.DeepEqual(again.Models[0].Args, []string{"-y", "2"}) {
		t.Fatalf("update not persisted: %+v", again)
	}
	if !again.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt changed by update: %v vs %v", again.CreatedAt, created)
	}

	// Delete removes exactly one.
	if err := repo2.DeletePipeline(e.ID); err != nil {
		t.Fatalf("DeletePipeline: %v", err)
	}
	repo3, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	rest, err := repo3.ListPipelines()
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	if len(rest) != 1 || rest[0].Name != "second" {
		t.Fatalf("after delete: %+v", rest)
	}
}

// D1.6: the CRUD follows the P0 contract — durable write (.bak present) and
// in-memory rollback on save failure.
func TestPipeline_DurabilityAndRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	e := &PipelineEntry{Name: "durable", Models: []PipelineModel{{ModelID: "m1"}}}
	if err := repo.CreatePipeline(e); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	bak, err := os.Stat(path + ".bak")
	if err != nil || bak.Size() == 0 {
		t.Fatalf("backup file missing after pipeline write: %v", err)
	}

	// Rollback on save failure: the in-memory state is unchanged.
	broken := &JSONRepository{filePath: filepath.Join(dir, "no-such-dir", "repo.json"), idGenerator: generateID}
	if err := broken.CreatePipeline(&PipelineEntry{Name: "ghost", Models: []PipelineModel{{ModelID: "m1"}}}); err == nil {
		t.Fatal("expected save failure, got nil")
	}
	if got, _ := broken.ListPipelines(); len(got) != 0 {
		t.Fatalf("pipelines after failed create = %d, want 0", len(got))
	}
	if err := broken.UpdatePipeline(&PipelineEntry{Name: "ghost"}); err == nil {
		t.Fatal("expected update failure")
	}
	if err := broken.DeletePipeline("ghost"); err == nil {
		t.Fatal("expected delete failure")
	}

	// The good repository file is untouched by the broken save attempts.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "durable") {
		t.Fatal("original file corrupted")
	}
}

// pipelineIDFormat guards the ID generation contract used by the UI.
func TestPipeline_IDGeneration(t *testing.T) {
	dir := t.TempDir()
	repo, err := NewJSONRepository(filepath.Join(dir, "goal_repo.json"))
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	e := &PipelineEntry{Name: "ids", Models: []PipelineModel{{ModelID: "m"}}}
	if err := repo.CreatePipeline(e); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	if !strings.HasPrefix(e.ID, "ent_") {
		t.Fatalf("pipeline ID = %q, want ent_ prefix", e.ID)
	}
}
