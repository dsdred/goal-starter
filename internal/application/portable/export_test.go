package portable

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

func newTestRepo(t *testing.T) storage.Repository {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func seedGraph(t *testing.T, repo storage.Repository) {
	t.Helper()
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT One", Executable: "/bin/one", Environment: map[string]string{"TOKEN": "secret", "ALPHA": "val"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt2", Name: "RT Two", Executable: "/bin/two"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "Model 1", RuntimeID: "rt1", Args: []string{"-m", "model1.gguf"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m2", Name: "Model 2", RuntimeID: "rt2"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePipeline(&storage.PipelineEntry{
		ID:     "p1",
		Name:   "Pipeline 1",
		Active: true,
		Models: []storage.PipelineModel{
			{ID: "e1", ModelID: "m1"},
			{ID: "e2", ModelID: "m2"},
			{ID: "e3", ModelID: "m1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExportAll(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2", len(b.Runtimes))
	}
	if len(b.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(b.Models))
	}
	if len(b.Pipelines) != 1 {
		t.Fatalf("pipelines = %d, want 1", len(b.Pipelines))
	}
	if len(b.Pipelines[0].Models) != 3 {
		t.Fatalf("pipeline entries = %d, want 3", len(b.Pipelines[0].Models))
	}
}

func TestExport_RuntimeRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	if len(b.Runtimes) != 1 || b.Runtimes[0].ID != "rt1" {
		t.Fatalf("unexpected runtimes: %+v", b.Runtimes)
	}
	if len(b.Models) != 0 {
		t.Fatalf("models should be empty, got %d", len(b.Models))
	}
	if len(b.Pipelines) != 0 {
		t.Fatalf("pipelines should be empty, got %d", len(b.Pipelines))
	}
}

func TestExport_ModelRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	if len(b.Runtimes) != 1 || b.Runtimes[0].ID != "rt1" {
		t.Fatalf("expected runtime rt1, got %+v", b.Runtimes)
	}
	if len(b.Models) != 1 || b.Models[0].ID != "m1" {
		t.Fatalf("expected model m1, got %+v", b.Models)
	}
}

func TestExport_PipelineRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{PipelineID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	if len(b.Pipelines) != 1 || b.Pipelines[0].ID != "p1" {
		t.Fatalf("unexpected pipelines: %+v", b.Pipelines)
	}
	if len(b.Models) != 2 {
		t.Fatalf("models = %d, want 2 (m1, m2 deduped)", len(b.Models))
	}
	if len(b.Runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2 (rt1, rt2)", len(b.Runtimes))
	}
	// Entry order preserved.
	if b.Pipelines[0].Models[0].ID != "e1" || b.Pipelines[0].Models[2].ID != "e3" {
		t.Fatalf("entry order not preserved: %+v", b.Pipelines[0].Models)
	}
	// Repeated model entries preserved.
	if b.Pipelines[0].Models[0].ModelID != "m1" || b.Pipelines[0].Models[2].ModelID != "m1" {
		t.Fatal("repeated model entries lost")
	}
}

func TestExport_UnknownRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	_, err := ExportBundle(repo, ExportRoot{ModelID: "nonexistent"})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if _, ok := err.(*ErrNotFound); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestExport_MultipleRoots(t *testing.T) {
	repo := newTestRepo(t)
	_, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1", ModelID: "m1"})
	if err == nil {
		t.Fatal("expected multiple roots error")
	}
}

func TestExport_EnvironmentKeys(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s", Environment: map[string]string{"ZED": "1", "ALPHA": "2"}}); err != nil {
		t.Fatal(err)
	}

	data, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	ek := b.Runtimes[0].EnvironmentKeys
	if len(ek) != 2 || ek[0] != "ALPHA" || ek[1] != "ZED" {
		t.Fatalf("environment_keys = %v, want [ALPHA ZED]", ek)
	}
}

func TestExport_NoEnvironmentValues(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s", Environment: map[string]string{"SECRET": "top-secret-value"}}); err != nil {
		t.Fatal(err)
	}

	data, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Contains(got, "top-secret-value") {
		t.Fatal("secret value leaked in bundle")
	}
}
