package storage

import (
	"path/filepath"
	"testing"
)

// newBrokenRepo builds a repository whose save target is in a nonexistent
// directory, so every saveLocked fails deterministically on all platforms.
func newBrokenRepo(t *testing.T) *JSONRepository {
	t.Helper()
	dir := t.TempDir()
	return &JSONRepository{
		filePath:    filepath.Join(dir, "no-such-dir", "repo.json"),
		idGenerator: generateID,
	}
}

func (r *JSONRepository) setGoodPath(t *testing.T) {
	t.Helper()
	r.filePath = filepath.Join(t.TempDir(), "repo.json")
}

func TestConsistency_CreateRollsBackOnSaveFailure(t *testing.T) {
	r := newBrokenRepo(t)

	if err := r.CreateRuntime(&RuntimeEntry{Name: "rt", Executable: "x"}); err == nil {
		t.Fatal("expected save failure, got nil")
	}
	if got, _ := r.ListRuntimes(); len(got) != 0 {
		t.Fatalf("runtimes after failed create = %d, want 0", len(got))
	}

	if err := r.CreateModel(&ModelEntry{Name: "m", RuntimeID: "rt"}); err == nil {
		t.Fatal("expected save failure, got nil")
	}
	if got, _ := r.ListModels(); len(got) != 0 {
		t.Fatalf("models after failed create = %d, want 0", len(got))
	}

	if err := r.CreateInstance(&LaunchInstanceEntry{State: "exited"}); err == nil {
		t.Fatal("expected save failure, got nil")
	}
	if got, _ := r.ListInstances(); len(got) != 0 {
		t.Fatalf("instances after failed create = %d, want 0", len(got))
	}

	r.setGoodPath(t)
	if err := r.CreateRuntime(&RuntimeEntry{Name: "rt2", Executable: "x"}); err != nil {
		t.Fatalf("create after recovery: %v", err)
	}
	if got, _ := r.ListRuntimes(); len(got) != 1 {
		t.Fatalf("runtimes after recovery = %d, want 1", len(got))
	}
}

func TestConsistency_UpdateRollsBackOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	r := &JSONRepository{filePath: filepath.Join(dir, "repo.json"), idGenerator: generateID}
	rt := &RuntimeEntry{Name: "orig", Executable: "x"}
	if err := r.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}

	r.filePath = filepath.Join(dir, "no-such-dir", "repo.json")
	if err := r.UpdateRuntime(&RuntimeEntry{ID: rt.ID, Name: "changed", Executable: "x"}); err == nil {
		t.Fatal("expected save failure, got nil")
	}

	got, err := r.GetRuntime(rt.ID)
	if err != nil {
		t.Fatalf("entity lost after failed update: %v", err)
	}
	if got.Name != "orig" {
		t.Fatalf("rollback failed: name = %q, want %q", got.Name, "orig")
	}
}

func TestConsistency_DeleteRollsBackOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	r := &JSONRepository{filePath: filepath.Join(dir, "repo.json"), idGenerator: generateID}
	first := &RuntimeEntry{Name: "first", Executable: "x"}
	second := &RuntimeEntry{Name: "second", Executable: "x"}
	if err := r.CreateRuntime(first); err != nil {
		t.Fatalf("CreateRuntime first: %v", err)
	}
	if err := r.CreateRuntime(second); err != nil {
		t.Fatalf("CreateRuntime second: %v", err)
	}

	r.filePath = filepath.Join(dir, "no-such-dir", "repo.json")
	if err := r.DeleteRuntime(first.ID); err == nil {
		t.Fatal("expected save failure, got nil")
	}

	list, err := r.ListRuntimes()
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("runtimes after failed delete = %d, want 2", len(list))
	}
	if list[0].Name != "first" || list[1].Name != "second" {
		t.Fatalf("rollback corrupted slice: %q, %q", list[0].Name, list[1].Name)
	}
}

func TestConsistency_CreateExistingInstanceRollsBackOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	r := &JSONRepository{filePath: filepath.Join(dir, "repo.json"), idGenerator: generateID}
	inst := &LaunchInstanceEntry{State: "running", ModelID: "m1"}
	if err := r.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	r.filePath = filepath.Join(dir, "no-such-dir", "repo.json")
	if err := r.Create(&LaunchInstanceEntry{ID: inst.ID, State: "exited", ModelID: "m1"}); err == nil {
		t.Fatal("expected save failure, got nil")
	}

	got, err := r.GetInstance(inst.ID)
	if err != nil {
		t.Fatalf("entity lost after failed create-update: %v", err)
	}
	if got.State != "running" {
		t.Fatalf("rollback failed: state = %q, want %q", got.State, "running")
	}
}
