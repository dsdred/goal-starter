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
	if res.Created.Runtimes != 1 || res.Created.Models != 1 || res.Created.Pipelines != 0 {
		t.Fatalf("unexpected created counts: %+v", res)
	}
	if res.Updated.Total() != 0 || res.Unchanged.Total() != 0 || len(res.Blocked) != 0 {
		t.Fatalf("unexpected update/block counts: %+v", res)
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

// §13.7 — same ID is repository identity, so it is never a conflict: the exported
// configuration is restored onto that identity (ADR 018 D3/D4). The restore is
// non-destructive: restorable fields are written, and what bundle v1 cannot carry
// (environment values) stays exactly as stored.
func TestImport_IDCollision_UpdateIsApplied(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{
		ID: "rt1", Name: "Existing", Executable: "/bin/existing", WorkingDirectory: "/existing",
		Environment: map[string]string{"TOKEN": "keep-me"},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.GetRuntime("rt1")
	if err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "New", Executable: "/bin/new"}},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatalf("same-ID entity must not be rejected as a conflict: %v", err)
	}
	if res.Created.Runtimes != 0 || res.Updated.Runtimes != 1 || res.Unchanged.Total() != 0 {
		t.Fatalf("expected 0 created / 1 restored, got %+v", res)
	}
	if !res.CanImport {
		t.Fatal("a pending restore must enable import")
	}
	rt, _ := repo.GetRuntime("rt1")
	if rt.Name != "New" || rt.Executable != "/bin/new" {
		t.Fatalf("restore not applied: %+v", rt)
	}
	// WorkingDirectory is restorable, and the bundle carries none, so the exported
	// value (absent) wins over the local one (D11 normalization).
	if rt.WorkingDirectory != "" {
		t.Fatalf("restore kept a field the bundle cleared: %+v", rt)
	}
	if rt.Environment["TOKEN"] != "keep-me" {
		t.Fatalf("restore destroyed a local secret: %+v", rt.Environment)
	}
	if !rt.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("restore rewrote CreatedAt: %+v vs %+v", rt, before)
	}
	if rt.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("restore did not stamp UpdatedAt: %+v vs %+v", rt, before)
	}
}

// A dry run reports the pending restore without writing it, and re-importing the
// exact same bundle after the restore completes classifies as UNCHANGED with
// nothing to import (ADR 018 D4/D16).
func TestImport_RestoreThenReimport_IsUnchanged(t *testing.T) {
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
	dry, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Updated.Runtimes != 1 || dry.Created.Total() != 0 || !dry.CanImport {
		t.Fatalf("dry run must report the pending restore: %+v", dry)
	}
	if rt, _ := repo.GetRuntime("rt1"); rt.Name != "Existing" {
		t.Fatalf("dry run wrote: %+v", rt)
	}

	if _, err := orch.Import(bundle, false); err != nil {
		t.Fatal(err)
	}
	again, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Unchanged.Runtimes != 1 || again.Updated.Total() != 0 || again.Created.Total() != 0 {
		t.Fatalf("re-import of the applied bundle must be unchanged: %+v", again)
	}
	if again.CanImport {
		t.Fatal("nothing to write must not enable import")
	}
}

// §13.8 — runtime name is a proven unique identity (RuntimeService.nameExists),
// so same-name/different-ID can neither be created nor proven identical: blocked.
func TestImport_NameCollision_Blocked(t *testing.T) {
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
		t.Fatal("expected name collision to block the import")
	}
	ce, ok := err.(*ErrConflict)
	if !ok {
		t.Fatalf("wrong error type: %T", err)
	}
	if len(ce.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", ce.Conflicts)
	}
	c := ce.Conflicts[0]
	if c.Type != "runtime" || c.ID != "rt2" || c.Reason != storage.ImportBlockedRuntimeNameTaken || c.RelatedID != "rt1" {
		t.Fatalf("unexpected conflict: %+v", c)
	}
	// Nothing was written: no second runtime exists.
	all, _ := repo.ListRuntimes()
	if len(all) != 1 {
		t.Fatalf("blocked import wrote state: %d runtimes", len(all))
	}
}

// ADR 018 D-1 scope boundary: a pending UPDATE and a NEW Runtime that want the
// same name are never a planning question, because the bundle carrying both is
// invalid on its own (`portable.go:273-281`). Validation therefore has to reject
// it before the planner runs, in both element orders, for a dry run as well as
// for a real import, and with no mutation of the repository.
func TestImport_ClashingUpdateAndNewRuntimeNames_RejectedBeforePlanning(t *testing.T) {
	restore := PortableRuntime{ID: "rt1", Name: "Gamma", Executable: "/bin/rt1"}
	fresh := PortableRuntime{ID: "rt2", Name: "gamma", Executable: "/bin/rt2"}

	for _, dryRun := range []bool{false, true} {
		for _, order := range [][2]PortableRuntime{{restore, fresh}, {fresh, restore}} {
			repo := newTestRepo(t)
			if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "Alpha", Executable: "/bin/rt1"}); err != nil {
				t.Fatal(err)
			}
			orch := NewImportOrchestrator(repo)

			bundle := &Bundle{
				Format:   FormatIdentity,
				Version:  1,
				Runtimes: []PortableRuntime{order[0], order[1]},
			}
			_, err := orch.Import(bundle, dryRun)
			ver, ok := err.(*ErrValidation)
			if !ok {
				t.Fatalf("dryRun=%v order=%v: want *ErrValidation, got %T (%v)", dryRun, order, err, err)
			}
			if !strings.Contains(ver.Reason, "duplicate runtime name") {
				t.Fatalf("dryRun=%v order=%v: unexpected reason %q", dryRun, order, ver.Reason)
			}
			// Planning was never reached, so the repository holds exactly what it
			// did before: the pre-existing runtime, still under its own name.
			all, lerr := repo.ListRuntimes()
			if lerr != nil || len(all) != 1 || all[0].ID != "rt1" || all[0].Name != "Alpha" {
				t.Fatalf("dryRun=%v order=%v: validation mutated state: %+v (%v)", dryRun, order, all, lerr)
			}
		}
	}
}

// §13.8 — model and pipeline names are NOT identity: a same-name/different-ID
// model is a distinct entity and imports as new.
func TestImport_ModelNameNotIdentity(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "Same Name", RuntimeID: "rt1"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m2", Name: "same name", RuntimeID: "rt1"}},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created.Models != 1 || res.Unchanged.Models != 0 || res.Unchanged.Runtimes != 1 {
		t.Fatalf("model with a duplicate name must import as new: %+v", res)
	}
	if _, err := repo.GetModel("m2"); err != nil {
		t.Fatal("second same-name model not created")
	}
	// The original model is untouched.
	m, _ := repo.GetModel("m1")
	if m.ID != "m1" || m.Name != "Same Name" {
		t.Fatalf("original model modified: %+v", m)
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
	if res.Created.Runtimes != 1 {
		t.Fatalf("dry-run result: %+v", res)
	}
	if !res.CanImport {
		t.Fatal("new entity must make the import available")
	}
	// Zero writes.
	if _, err := repo.GetRuntime("rt1"); err == nil {
		t.Fatal("dry-run should not write")
	}
}

// §13.1 — a bundle that only re-describes stored state is a valid file with
// nothing to write: not a conflict, and not importable (ADR 018 D4/D-9).
func TestImport_DryRun_AllUnchanged_NothingToImport(t *testing.T) {
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
	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatalf("unchanged entities must not be reported as conflicts: %v", err)
	}
	if res.Created.Total() != 0 || res.Updated.Total() != 0 {
		t.Fatalf("expected zero new and zero restore, got %+v", res)
	}
	if len(res.Blocked) != 0 {
		t.Fatalf("expected zero blocked, got %+v", res.Blocked)
	}
	if res.CanImport {
		t.Fatal("nothing-to-import must not enable import")
	}
	if res.Unchanged.Runtimes != 1 {
		t.Fatalf("expected 1 unchanged runtime, got %+v", res)
	}
}

// A valid file that mixes unchanged and new entities is importable.
func TestImport_DryRun_MixedEnablesImport(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
	}
	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.CanImport || res.Created.Models != 1 || res.Unchanged.Runtimes != 1 {
		t.Fatalf("unexpected plan: %+v", res)
	}
	if _, err := repo.GetModel("m1"); err == nil {
		t.Fatal("dry-run should not write")
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

	// The bundle wants to create a runtime named "RT".
	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
	}

	// Step 1: actual application dry-run on empty repo — succeeds as new.
	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatalf("dry-run should succeed on empty repo: %v", err)
	}
	if res.Created.Runtimes != 1 || !res.CanImport {
		t.Fatalf("dry-run result: %+v", res)
	}

	// Step 2: competing repository mutation between dry-run and real import.
	// A different ID claiming the same runtime name makes the plan unsafe.
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-competing", Name: "rt", Executable: "/bin/competing"}); err != nil {
		t.Fatal(err)
	}

	// Step 3: real Import — the plan rebuilt under the write lock must reject.
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
	if ce.Result != nil && ce.Result.CanImport {
		t.Fatal("rejected plan must not report can_import")
	}

	// Step 4: original entity unchanged, and the stale plan wrote nothing.
	rt, _ := repo.GetRuntime("rt-competing")
	if rt.Name != "rt" || rt.Executable != "/bin/competing" {
		t.Fatalf("original modified: %+v", rt)
	}
	if _, err := repo.GetRuntime("rt1"); err == nil {
		t.Fatal("stale plan created an entity")
	}
}

// §13.1 / test obligation 5 — the same re-planning must also catch a competing
// mutation that only makes a pending RESTORE unsafe. The dry run reports the
// restore; another ID claims the name the restore intends to write; the real
// import re-plans under the lock and writes nothing at all, updates included.
func TestImport_DryRunTOCTOU_PendingRestoreReplans(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "Own", Executable: "/bin/1"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--keep"}}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "Claimed", Executable: "/bin/1"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--restored"}}},
	}
	dry, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Updated.Runtimes != 1 || dry.Updated.Models != 1 || dry.Created.Total() != 0 || !dry.CanImport {
		t.Fatalf("dry run must report the pending restore: %+v", dry)
	}

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-rival", Name: "claimed", Executable: "/bin/r"}); err != nil {
		t.Fatal(err)
	}

	_, err = orch.Import(bundle, false)
	ce, ok := err.(*ErrConflict)
	if !ok {
		t.Fatalf("expected blocking conflict, got %T (%v)", err, err)
	}
	if len(ce.Conflicts) != 1 || ce.Conflicts[0].ID != "rt1" {
		t.Fatalf("conflicts = %+v", ce.Conflicts)
	}
	if ce.Conflicts[0].Reason != storage.ImportBlockedRuntimeNameTaken || ce.Conflicts[0].RelatedID != "rt-rival" {
		t.Fatalf("conflict = %+v", ce.Conflicts[0])
	}
	if ce.Result == nil || ce.Result.CanImport {
		t.Fatalf("rejected plan must not report can_import: %+v", ce.Result)
	}
	// The blocked restore leaves no write, and its still-valid dependent restore
	// is abandoned with it: the whole graph operation is atomic.
	rt1, _ := repo.GetRuntime("rt1")
	if rt1.Name != "Own" || rt1.Executable != "/bin/1" {
		t.Fatalf("blocked restore was applied: %+v", rt1)
	}
	m1, _ := repo.GetModel("m1")
	if len(m1.Args) != 1 || m1.Args[0] != "--keep" {
		t.Fatalf("abandoned plan applied its dependent update: %+v", m1)
	}
}

// §13.9 — the same competing mutation must also be caught when it only makes a
// dependency unresolvable, so no dependent entity is written as a dangling object.
func TestImport_RevalidationPreventsDanglingWrite(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/s"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
	}

	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created.Runtimes != 1 || res.Created.Models != 1 {
		t.Fatalf("expected the whole graph to be new: %+v", res)
	}

	// The runtime the model depends on becomes unsolvable: the name is taken by
	// another ID, so rt1 is blocked and m1 must not be created on its own.
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-taken", Name: "rt", Executable: "/bin/taken"}); err != nil {
		t.Fatal(err)
	}

	_, err = orch.Import(bundle, false)
	ce, ok := err.(*ErrConflict)
	if !ok {
		t.Fatalf("expected blocking conflict, got %v", err)
	}
	if len(ce.Conflicts) != 2 {
		t.Fatalf("expected the runtime and its dependent model to block, got %+v", ce.Conflicts)
	}
	byID := map[string]string{}
	for _, c := range ce.Conflicts {
		byID[c.ID] = c.Reason
	}
	if byID["rt1"] != storage.ImportBlockedRuntimeNameTaken || byID["m1"] != storage.ImportBlockedDependency {
		t.Fatalf("unexpected blocking reasons: %+v", ce.Conflicts)
	}
	if _, err := repo.GetModel("m1"); err == nil {
		t.Fatal("dependent model was created while its runtime is blocked")
	}
	if _, err := repo.GetRuntime("rt1"); err == nil {
		t.Fatal("blocked runtime was created")
	}
}

// §13.1 — the Owner's reproduction: export the current configuration and
// validate that exact file against the unchanged repository.
func TestImport_ExportThenValidate_SameRepo(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)
	orch := NewImportOrchestrator(repo)

	data, err := ExportBundle(repo, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ParseBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Runtimes) == 0 || len(bundle.Models) == 0 {
		t.Fatalf("export produced nothing to validate: %+v", bundle)
	}

	res, err := orch.Import(bundle, true)
	if err != nil {
		t.Fatalf("self-reimport must validate cleanly: %v", err)
	}
	if res.Created.Total() != 0 || res.Updated.Total() != 0 {
		t.Fatalf("expected zero new and zero restore, got %+v %+v", res.Created, res.Updated)
	}
	if len(res.Blocked) != 0 {
		t.Fatalf("expected zero blocked, got %+v", res.Blocked)
	}
	if res.CanImport {
		t.Fatal("nothing to import must not enable import")
	}
	if res.Unchanged.Total() != res.Total.Total() {
		t.Fatalf("every entity must classify as up to date: %+v %+v", res.Unchanged, res.Total)
	}

	// The same file against the same repository is also a no-op when applied: the
	// exported projection is exactly what is stored, so nothing is written.
	applied, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatalf("self-reimport must apply cleanly: %v", err)
	}
	if applied.Created.Total() != 0 || applied.Updated.Total() != 0 {
		t.Fatalf("self-reimport wrote state: %+v", applied)
	}
	reexported, err := ExportBundle(repo, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	if string(reexported) != string(data) {
		t.Fatal("a no-op self-reimport changed the exported configuration")
	}
}

// §13.2 — a file of only new independent entities imports all of them.
func TestImport_AllNew_CreatesAll(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:  FormatIdentity,
		Version: 1,
		Runtimes: []PortableRuntime{
			{ID: "rtA", Name: "RT A", Executable: "/bin/a"},
			{ID: "rtB", Name: "RT B", Executable: "/bin/b"},
		},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created.Runtimes != 2 || res.Unchanged.Total() != 0 || res.Updated.Total() != 0 {
		t.Fatalf("unexpected plan: %+v", res)
	}
	if _, err := repo.GetRuntime("rtA"); err != nil {
		t.Fatal("rtA not created")
	}
	if _, err := repo.GetRuntime("rtB"); err != nil {
		t.Fatal("rtB not created")
	}
}

// §13.3 — a new model may depend on a runtime that already exists.
func TestImport_NewModelOnExistingRuntime(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged.Runtimes != 1 || res.Created.Models != 1 {
		t.Fatalf("unexpected plan: %+v", res)
	}
	m, err := repo.GetModel("m1")
	if err != nil {
		t.Fatal("model referencing an existing runtime was not imported")
	}
	if m.RuntimeID != "rt1" {
		t.Fatalf("reference not preserved: %q", m.RuntimeID)
	}
}

// §13.4 — a new pipeline may depend on a model the plan classifies as UNCHANGED,
// because the stored record already satisfies the reference (ADR 018 D5).
func TestImport_NewPipelineOnExistingModel(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
		Pipelines: []PortablePipeline{{
			ID: "p1", Name: "P",
			Models: []PortableEntry{{ID: "e1", ModelID: "m1", AutoStart: true}},
		}},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged.Runtimes != 1 || res.Unchanged.Models != 1 || res.Created.Pipelines != 1 {
		t.Fatalf("unexpected plan: %+v", res)
	}
	p, err := repo.GetPipeline("p1")
	if err != nil {
		t.Fatal("pipeline referencing an unchanged model was not imported")
	}
	if len(p.Models) != 1 || p.Models[0].ModelID != "m1" || !p.Models[0].AutoStart {
		t.Fatalf("entry not preserved: %+v", p.Models)
	}
}

// §13.5 — a complete new graph (runtime → model → pipeline) imports together.
func TestImport_NewGraphChain(t *testing.T) {
	repo := newTestRepo(t)
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "RT", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
		Pipelines: []PortablePipeline{{
			ID: "p1", Name: "P", Active: true,
			Models: []PortableEntry{{ID: "e1", ModelID: "m1", AutoStart: true}},
		}},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created.Runtimes != 1 || res.Created.Models != 1 || res.Created.Pipelines != 1 {
		t.Fatalf("unexpected plan: %+v", res)
	}
	if _, err := repo.GetPipeline("p1"); err != nil {
		t.Fatal("pipeline not created")
	}
}

// §13.6 — a blocked dependency never yields a dangling dependent.
func TestImport_BlockedDependency_NoDangling(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-owner", Name: "Taken Name", Executable: "/bin/o"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "taken name", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
		Pipelines: []PortablePipeline{{
			ID: "p1", Name: "P",
			Models: []PortableEntry{{ID: "e1", ModelID: "m1"}},
		}},
	}
	_, err := orch.Import(bundle, false)
	ce, ok := err.(*ErrConflict)
	if !ok {
		t.Fatalf("expected blocking conflict, got %v", err)
	}
	if len(ce.Conflicts) != 3 {
		t.Fatalf("expected runtime, model and pipeline all blocked, got %+v", ce.Conflicts)
	}
	wantReasons := map[string]string{
		"rt1": storage.ImportBlockedRuntimeNameTaken,
		"m1":  storage.ImportBlockedDependency,
		"p1":  storage.ImportBlockedDependency,
	}
	for _, c := range ce.Conflicts {
		if wantReasons[c.ID] != c.Reason {
			t.Fatalf("unexpected reason for %s: %+v", c.ID, c)
		}
	}
	// Nothing was written anywhere.
	if _, err := repo.GetRuntime("rt1"); err == nil {
		t.Fatal("blocked runtime created")
	}
	if _, err := repo.GetModel("m1"); err == nil {
		t.Fatal("model with unresolved runtime created")
	}
	if _, err := repo.GetPipeline("p1"); err == nil {
		t.Fatal("pipeline with unresolved model created")
	}
	all, _ := repo.ListRuntimes()
	if len(all) != 1 {
		t.Fatalf("runtime count changed: %d", len(all))
	}
}

// §13.10 — NEW, UPDATE and UNCHANGED stay distinct classes in one mixed plan, and
// the applied result describes exactly what each of them did (ADR 018 D4/D16).
func TestImport_ResultCounts_DistinguishCreatedUpdatedUnchanged(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt2", Name: "RT Old", Executable: "/bin/old"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "M1", RuntimeID: "rt1"}); err != nil {
		t.Fatal(err)
	}
	orch := NewImportOrchestrator(repo)

	bundle := &Bundle{
		Format:  FormatIdentity,
		Version: 1,
		Runtimes: []PortableRuntime{
			{ID: "rt1", Name: "RT", Executable: "/bin/a"},
			{ID: "rt2", Name: "RT Restored", Executable: "/bin/old"},
			{ID: "rt3", Name: "RT Three", Executable: "/bin/three"},
		},
		Models: []PortableModel{
			{ID: "m1", Name: "M1", RuntimeID: "rt1"},
			{ID: "m2", Name: "M2", RuntimeID: "rt2"},
		},
	}
	res, err := orch.Import(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created.Runtimes != 1 || res.Updated.Runtimes != 1 || res.Unchanged.Runtimes != 1 {
		t.Fatalf("runtime statuses not distinguished: %+v", res)
	}
	if res.Created.Models != 1 || res.Updated.Models != 0 || res.Unchanged.Models != 1 {
		t.Fatalf("model statuses not distinguished: %+v", res)
	}
	if res.Total.Runtimes != 3 || res.Total.Models != 2 {
		t.Fatalf("totals wrong: %+v", res.Total)
	}
	if !res.CanImport {
		t.Fatal("a plan with writes and no conflicts must enable import")
	}

	rt2, _ := repo.GetRuntime("rt2")
	if rt2.Name != "RT Restored" || rt2.Executable != "/bin/old" {
		t.Fatalf("restore not applied: %+v", rt2)
	}
	if _, err := repo.GetRuntime("rt3"); err != nil {
		t.Fatal("new runtime not created")
	}
	rt1, _ := repo.GetRuntime("rt1")
	if rt1.Name != "RT" || rt1.Executable != "/bin/a" {
		t.Fatalf("unchanged runtime rewritten: %+v", rt1)
	}
	m1, _ := repo.GetModel("m1")
	if m1.Name != "M1" || m1.RuntimeID != "rt1" {
		t.Fatalf("unchanged model rewritten: %+v", m1)
	}
	if _, err := repo.GetModel("m2"); err != nil {
		t.Fatal("new model not created")
	}
}
