package portable

import (
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

func TestImport_EmptyRepo_Success(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Active: true}},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Runtimes != 1 || res.Models != 1 || res.Pipelines != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}

	rt, err := repo.GetRuntime("rt1")
	if err != nil || rt.Name != "RT" {
		t.Fatalf("runtime not imported: %v %v", rt, err)
	}
	m, err := repo.GetModel("m1")
	if err != nil || !m.Active {
		t.Fatalf("model not imported or not active: %v %v", m, err)
	}
}

func TestImport_EnvironmentKeys_NotImported(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s", EnvironmentKeys: []string{"TOKEN", "API_KEY"}}},
	}
	if _, err := orch.Import(bundle, false); err != nil {
		t.Fatal(err)
	}

	rt, _ := repo.GetRuntime("rt1")
	if rt.Environment != nil {
		t.Fatalf("environment should be nil/empty, got: %v", rt.Environment)
	}
}

func TestImport_IDCollision(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "Existing", Executable: "/bin/existing"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "New", Executable: "/bin/new"}},
	}
	_, err := orch.Import(bundle, false)
	if err == nil {
		t.Fatal("expected conflict")
	}
	ce, ok := err.(*ErrConflict)
	if !ok {
		t.Fatalf("wrong error type: %T", err)
	}
	if len(ce.Conflicts) == 0 {
		t.Fatal("no conflicts reported")
	}
	// Zero writes: the existing runtime is unchanged.
	rt, _ := repo.GetRuntime("rt1")
	if rt.Name != "Existing" {
		t.Fatal("existing runtime was modified")
	}
}

func TestImport_NameCollision(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "Llama.cpp", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt2", Name: "llama.cpp", Executable: "/bin/b"}},
	}
	_, err := orch.Import(bundle, false)
	if err == nil {
		t.Fatal("expected name collision")
	}
}

func TestImport_DryRun_Success(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
	}
	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Runtimes != 1 {
		t.Fatalf("dry-run result: %+v", res)
	}
	// Zero writes.
	if _, err := repo.GetRuntime("rt1"); err == nil {
		t.Fatal("dry-run should not write")
	}
}

func TestImport_DryRun_Conflict(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
	}
	_, err := orch.Import(bundle, true)
	if err == nil {
		t.Fatal("expected conflict in dry-run")
	}
	if _, ok := err.(*ErrConflict); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestImport_InvalidBundle(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:  FormatIdentity,
		Version: 1,
		Models:  []PortableModel{{ID: "m1", Name: "M", RuntimeID: "nonexistent"}},
	}
	_, err := orch.Import(bundle, false)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if _, ok := err.(*ErrValidation); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestImport_ActiveNotLaunched(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:    FormatIdentity,
		Version:   1,
		Runtimes:  []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/nonexistent/binary"}},
		Models:    []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Active: true, AutostartDelay: 0}},
		Pipelines: []PortablePipeline{{ID: "p1", Name: "P", Active: true, Models: []PortableEntry{{ID: "e1", ModelID: "m1", AutoStart: true}}}},
	}
	if _, err := orch.Import(bundle, false); err != nil {
		t.Fatal(err)
	}

	// Verify Active is preserved.
	m, _ := repo.GetModel("m1")
	if !m.Active {
		t.Fatal("model Active not preserved")
	}
	p, _ := repo.GetPipeline("p1")
	if !p.Active {
		t.Fatal("pipeline Active not preserved")
	}

	// Verify no instance was created (no launch occurred).
	instances, _ := repo.ListInstances()
	if len(instances) != 0 {
		t.Fatalf("import should not launch: %d instances found", len(instances))
	}
}

func TestImport_RoundTrip_WithEnvironment(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s", Environment: map[string]string{"TOKEN": "secret123"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--port", "8080"}}); err != nil {
		t.Fatal(err)
	}

	// Export.
	data1, err := ExportBundle(repo, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := ParseBundle(data1)
	// Verify environment_keys present.
	if len(b1.Runtimes[0].EnvironmentKeys) != 1 || b1.Runtimes[0].EnvironmentKeys[0] != "TOKEN" {
		t.Fatalf("environment_keys = %v", b1.Runtimes[0].EnvironmentKeys)
	}
	// Verify no secret in bundle.
	if strings.Contains(string(data1), "secret123") {
		t.Fatal("secret leaked in export")
	}

	// Import into a fresh repo.
	repo2 := newTestRepo(t)
	orch := NewImportOrchestrator(repo2)
	if _, err := orch.Import(b1, false); err != nil {
		t.Fatal(err)
	}

	// Verify imported entity has empty environment.
	rt2, _ := repo2.GetRuntime("rt1")
	if rt2.Environment != nil {
		t.Fatalf("imported environment should be nil, got %v", rt2.Environment)
	}

	// Re-export from repo2.
	data2, err := ExportBundle(repo2, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := ParseBundle(data2)
	// environment_keys should be empty (no env configured on imported entity).
	if len(b2.Runtimes[0].EnvironmentKeys) != 0 {
		t.Fatalf("re-export environment_keys = %v, want empty", b2.Runtimes[0].EnvironmentKeys)
	}

	// NOT byte-identical by design (env keys lost).
	if string(data1) == string(data2) {
		t.Fatal("bundles should differ (env keys not persisted)")
	}
}

func TestImport_DryRunTOCTOU_RealImportRejects(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
	}

	// Step 1: actual application dry-run on empty repo — succeeds.
	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatalf("dry-run should succeed on empty repo: %v", err)
	}
	if res.Runtimes != 1 {
		t.Fatalf("dry-run result: %+v", res)
	}

	// Step 2: competing repository mutation between dry-run and real import.
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "Competing", Executable: "/bin/competing"}); err != nil {
		t.Fatal(err)
	}

	// Step 3: real Import — locked collision check must reject.
	_, err = orch.Import(bundle, false)
	if err == nil {
		t.Fatal("expected conflict on real import after competing mutation")
	}
	ce, ok := err.(*ErrConflict)
	if !ok {
		t.Fatalf("wrong error type: %T", err)
	}
	if len(ce.Conflicts) == 0 {
		t.Fatal("no conflicts reported")
	}

	// Step 4: original entity unchanged, no imported entity exists under different identity.
	rt, _ := repo.GetRuntime("rt1")
	if rt.Name != "Competing" {
		t.Fatalf("original modified: %q", rt.Name)
	}
}
