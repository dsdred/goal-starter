package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func classificationIndex(plan *ImportGraphPlan) map[string]string {
	out := map[string]string{}
	for _, c := range plan.Classifications {
		out[c.Type+":"+c.ID] = c.Status
	}
	return out
}

func reasonIndex(plan *ImportGraphPlan) map[string]string {
	out := map[string]string{}
	for _, c := range plan.Classifications {
		out[c.Type+":"+c.ID] = c.Reason
	}
	return out
}

// Identity is proven per entity type: ID identifies every type, a runtime name
// is additionally unique, and model/pipeline names are not identity at all.
func TestPlanGraphImport_IdentityByEntityType(t *testing.T) {
	state := ImportGraphState{
		Runtimes:  []*RuntimeEntry{{ID: "rt-existing", Name: "Taken"}},
		Models:    []*ModelEntry{{ID: "m-existing", Name: "Model One"}},
		Pipelines: []*PipelineEntry{{ID: "p-existing", Name: "Pipe One"}},
	}

	plan := PlanGraphImport(state,
		[]*RuntimeEntry{
			{ID: "rt-existing", Name: "Renamed"},
			{ID: "rt-new", Name: "taken"},
			{ID: "rt-fresh", Name: "Fresh"},
		},
		[]*ModelEntry{
			{ID: "m-existing", Name: "Model One", RuntimeID: "rt-existing"},
			{ID: "m-samename", Name: "model one", RuntimeID: "rt-existing"},
			{ID: "m-orphan", Name: "Orphan", RuntimeID: "rt-missing"},
		},
		[]*PipelineEntry{
			{ID: "p-existing", Name: "Pipe One"},
			{ID: "p-samename", Name: "pipe one", Models: []PipelineModel{{ID: "e1", ModelID: "m-existing"}}},
			{ID: "p-dangling", Name: "Dangling", Models: []PipelineModel{{ID: "e2", ModelID: "m-orphan"}}},
			{ID: "p-missing", Name: "Missing", Models: []PipelineModel{{ID: "e3", ModelID: "m-none"}}},
		},
	)

	statuses := classificationIndex(plan)
	reasons := reasonIndex(plan)

	// rt-existing carries a different name and no local owner holds "Renamed",
	// so it is a restore onto the same identity (ADR 018 D3), not a conflict.
	want := map[string]string{
		"runtime:rt-existing": ImportStatusUpdate,
		"runtime:rt-new":      ImportStatusBlocked,
		"runtime:rt-fresh":    ImportStatusNew,
		// m-existing differs from its local record by RuntimeID, p-existing is
		// equivalent to its local record (ADR 018 D11 projection).
		"model:m-existing":    ImportStatusUpdate,
		"model:m-samename":    ImportStatusNew,
		"model:m-orphan":      ImportStatusBlocked,
		"pipeline:p-existing": ImportStatusUnchanged,
		"pipeline:p-samename": ImportStatusNew,
		"pipeline:p-dangling": ImportStatusBlocked,
		"pipeline:p-missing":  ImportStatusBlocked,
	}
	for key, wantStatus := range want {
		if statuses[key] != wantStatus {
			t.Fatalf("%s = %q, want %q", key, statuses[key], wantStatus)
		}
	}
	if reasons["runtime:rt-new"] != ImportBlockedRuntimeNameTaken {
		t.Fatalf("runtime name reason = %q", reasons["runtime:rt-new"])
	}
	if reasons["model:m-orphan"] != ImportBlockedRuntimeRef {
		t.Fatalf("model runtime reason = %q", reasons["model:m-orphan"])
	}
	if reasons["pipeline:p-dangling"] != ImportBlockedDependency {
		t.Fatalf("pipeline dependency reason = %q", reasons["pipeline:p-dangling"])
	}
	if reasons["pipeline:p-missing"] != ImportBlockedModelRef {
		t.Fatalf("pipeline model reason = %q", reasons["pipeline:p-missing"])
	}

	if len(plan.CreateRuntimes) != 1 || plan.CreateRuntimes[0].ID != "rt-fresh" {
		t.Fatalf("create runtimes = %+v", plan.CreateRuntimes)
	}
	if len(plan.CreateModels) != 1 || plan.CreateModels[0].ID != "m-samename" {
		t.Fatalf("create models = %+v", plan.CreateModels)
	}
	if len(plan.CreatePipelines) != 1 || plan.CreatePipelines[0].ID != "p-samename" {
		t.Fatalf("create pipelines = %+v", plan.CreatePipelines)
	}
	if plan.Created.Runtimes != 1 || plan.Skipped.Models != 1 || len(plan.Blocked) != 4 {
		t.Fatalf("counts = %+v blocked=%d", plan, len(plan.Blocked))
	}
	if plan.Total.Runtimes != 3 || plan.Total.Models != 3 || plan.Total.Pipelines != 4 {
		t.Fatalf("totals = %+v", plan.Total)
	}
}

// The plan must not mutate the state it was given.
func TestPlanGraphImport_DoesNotMutateState(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt1", Name: "One"}},
	}
	before := len(state.Runtimes)
	PlanGraphImport(state, []*RuntimeEntry{{ID: "rt2", Name: "Two"}}, nil, nil)
	if len(state.Runtimes) != before {
		t.Fatalf("state slice changed: %d -> %d", before, len(state.Runtimes))
	}
	if state.Runtimes[0].Name != "One" {
		t.Fatalf("state entity changed: %+v", state.Runtimes[0])
	}
}

// A new entity may depend on an existing one or on another new entity of the
// same import; a blocked dependency blocks its dependents instead of creating
// a dangling object.
func TestPlanGraphImport_DependencyResolution(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt-live", Name: "Live"}},
	}
	plan := PlanGraphImport(state,
		[]*RuntimeEntry{
			{ID: "rt-live", Name: "Live", Executable: "/bin/live"},
			{ID: "rt-new", Name: "New", Executable: "/bin/new"},
		},
		[]*ModelEntry{
			{ID: "m-on-existing", Name: "A", RuntimeID: "rt-live"},
			{ID: "m-on-new", Name: "B", RuntimeID: "rt-new"},
		},
		[]*PipelineEntry{
			{ID: "p-chain", Name: "Chain", Models: []PipelineModel{
				{ID: "e1", ModelID: "m-on-existing"},
				{ID: "e2", ModelID: "m-on-new"},
			}},
		},
	)
	if plan.HasBlocked() {
		t.Fatalf("nothing should block: %+v", plan.Blocked)
	}
	if plan.Created.Runtimes != 1 || plan.Created.Models != 2 || plan.Created.Pipelines != 1 {
		t.Fatalf("created = %+v", plan.Created)
	}
	if plan.Skipped.Runtimes != 1 {
		t.Fatalf("skipped = %+v", plan.Skipped)
	}
	if !plan.WillCreate() {
		t.Fatal("plan must have entities to create")
	}
}

// ImportGraph must not overwrite a local record yet: ADR 018 Slice 1 changes
// classification only, so a same-ID entity with different restorable content
// classifies UPDATE while the apply step still writes only what is created.
// Slice 2' turns this into the restore itself.
func TestImportGraph_UpdateClassifiedButNotApplied(t *testing.T) {
	repo, err := NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "Original", Executable: "/bin/original", WorkingDirectory: "/orig"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&ModelEntry{ID: "m1", Name: "Original Model", RuntimeID: "rt1", Args: []string{"--keep"}, Active: true}); err != nil {
		t.Fatal(err)
	}

	plan, err := r.ImportGraph(
		[]*RuntimeEntry{{ID: "rt1", Name: "Replaced", Executable: "/bin/replaced", WorkingDirectory: "/replaced"}},
		[]*ModelEntry{{ID: "m1", Name: "Replaced Model", RuntimeID: "rt1", Args: []string{"--replaced"}, Active: false}},
		nil,
	)
	if err != nil {
		t.Fatalf("same-ID entities must be skipped, not rejected: %v", err)
	}
	if plan.Created.Total() != 0 || plan.Skipped.Runtimes != 1 || plan.Skipped.Models != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	statuses := classificationIndex(plan)
	if statuses["runtime:rt1"] != ImportStatusUpdate || statuses["model:m1"] != ImportStatusUpdate {
		t.Fatalf("same-ID entities with different restorable content must classify as update: %+v", statuses)
	}

	rt, _ := repo.GetRuntime("rt1")
	if rt.Name != "Original" || rt.Executable != "/bin/original" || rt.WorkingDirectory != "/orig" {
		t.Fatalf("runtime overwritten: %+v", rt)
	}
	m, _ := repo.GetModel("m1")
	if m.Name != "Original Model" || len(m.Args) != 1 || m.Args[0] != "--keep" || !m.Active {
		t.Fatalf("model overwritten: %+v", m)
	}
}

// A blocked plan writes nothing at all: not the blocked entity, and not the
// otherwise-new entities beside it.
func TestImportGraph_BlockedPlanWritesNothing(t *testing.T) {
	repo, err := NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt-owner", Name: "Taken", Executable: "/bin/o"}); err != nil {
		t.Fatal(err)
	}

	_, err = r.ImportGraph(
		[]*RuntimeEntry{
			{ID: "rt-clash", Name: "taken", Executable: "/bin/clash"},
			{ID: "rt-fine", Name: "Fine", Executable: "/bin/fine"},
		},
		nil, nil,
	)
	var conflictErr *ErrImportConflict
	if !errorsAsImportConflict(err, &conflictErr) {
		t.Fatalf("expected ErrImportConflict, got %v", err)
	}
	if len(conflictErr.Conflicts) != 1 || conflictErr.Conflicts[0].ID != "rt-clash" {
		t.Fatalf("conflicts = %+v", conflictErr.Conflicts)
	}
	if conflictErr.Conflicts[0].RelatedID != "rt-owner" {
		t.Fatalf("conflict must name the colliding entity: %+v", conflictErr.Conflicts[0])
	}
	if _, err := repo.GetRuntime("rt-fine"); err == nil {
		t.Fatal("blocked plan created an unrelated new entity")
	}
	if _, err := repo.GetRuntime("rt-clash"); err == nil {
		t.Fatal("blocked plan created the blocked entity")
	}
	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 1 {
		t.Fatalf("runtime count changed: %d", len(runtimes))
	}
}

// A plan with nothing to create must not touch the durable file either. Here
// every entity is UNCHANGED, so the file stays byte-identical.
func TestImportGraph_NothingToCreate_LeavesFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "One", Executable: "/bin/one"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := r.ImportGraph([]*RuntimeEntry{{ID: "rt1", Name: "One", Executable: "/bin/one"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Created.Total() != 0 || plan.Skipped.Runtimes != 1 {
		t.Fatalf("plan = %+v", plan)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("nothing-to-create import rewrote the durable file")
	}
}

func errorsAsImportConflict(err error, target **ErrImportConflict) bool {
	for err != nil {
		if c, ok := err.(*ErrImportConflict); ok {
			*target = c
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ADR 018 D3/D4/D11: for a same-ID entity the restorable projection decides
// UPDATE versus UNCHANGED, and neither outcome is a conflict.
func TestPlanGraphImport_RestoreClassificationPerEntityType(t *testing.T) {
	when := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{
			{ID: "rt-keep", Name: "RT", Executable: "/bin/rt", Environment: map[string]string{"KEY": "value"}, CreatedAt: when, UpdatedAt: when},
			{ID: "rt-fix", Name: "Other", Executable: "/bin/other", CreatedAt: when},
		},
		Models: []*ModelEntry{
			{ID: "m-keep", Name: "M", RuntimeID: "rt-keep", Args: []string{"--port", "8080"}, Environment: map[string]string{"KEY": "value"}, Active: true, CreatedAt: when},
			{ID: "m-fix", Name: "Two", RuntimeID: "rt-keep", Args: []string{"--a"}, Active: true, CreatedAt: when},
		},
		Pipelines: []*PipelineEntry{
			{ID: "p-keep", Name: "P", Active: true, CreatedAt: when, Models: []PipelineModel{
				{ID: "e1", ModelID: "m-keep", Args: []string{"--x"}, AutoStart: true},
			}},
			{ID: "p-fix", Name: "Q", Active: true, CreatedAt: when, Models: []PipelineModel{
				{ID: "e2", ModelID: "m-keep"},
			}},
		},
	}

	plan := PlanGraphImport(state,
		// rt-keep restates the stored projection without environment values, which
		// bundle v1 never carries (D7); rt-fix restores a new executable.
		[]*RuntimeEntry{
			{ID: "rt-keep", Name: "RT", Executable: "/bin/rt"},
			{ID: "rt-fix", Name: "Other", Executable: "/bin/new"},
		},
		// m-keep restates args as an equal sequence; m-fix restores Active=false.
		[]*ModelEntry{
			{ID: "m-keep", Name: "M", RuntimeID: "rt-keep", Args: []string{"--port", "8080"}, Active: true},
			{ID: "m-fix", Name: "Two", RuntimeID: "rt-keep", Args: []string{"--a"}, Active: false},
		},
		// p-keep carries the same entry content under different entry ids, which
		// are excluded from equality (D10/D11); p-fix restores another reference.
		[]*PipelineEntry{
			{ID: "p-keep", Name: "P", Active: true, Models: []PipelineModel{
				{ID: "p-keep-e99", ModelID: "m-keep", Args: []string{"--x"}, AutoStart: true},
			}},
			{ID: "p-fix", Name: "Q", Active: true, Models: []PipelineModel{
				{ID: "e2", ModelID: "m-fix"},
			}},
		},
	)

	statuses := classificationIndex(plan)
	want := map[string]string{
		"runtime:rt-keep": ImportStatusUnchanged,
		"runtime:rt-fix":  ImportStatusUpdate,
		"model:m-keep":    ImportStatusUnchanged,
		"model:m-fix":     ImportStatusUpdate,
		"pipeline:p-keep": ImportStatusUnchanged,
		"pipeline:p-fix":  ImportStatusUpdate,
	}
	for key, wantStatus := range want {
		if statuses[key] != wantStatus {
			t.Fatalf("%s = %q, want %q", key, statuses[key], wantStatus)
		}
	}
	if len(plan.Blocked) != 0 {
		t.Fatalf("restore classifications are not conflicts: %+v", plan.Blocked)
	}
	if len(plan.CreateRuntimes) != 0 || len(plan.CreateModels) != 0 || len(plan.CreatePipelines) != 0 {
		t.Fatalf("nothing may be created for existing ids: %+v", plan)
	}
	// Inertness of Slice 1: an update classification does not make the plan
	// actionable, because the apply step still writes only what is created.
	if plan.WillCreate() || plan.Created.Total() != 0 {
		t.Fatalf("update/unchanged must not enable the shipped apply path: %+v", plan.Created)
	}
	if plan.Skipped.Runtimes != 2 || plan.Skipped.Models != 2 || plan.Skipped.Pipelines != 2 {
		t.Fatalf("transitional skipped counts = %+v", plan.Skipped)
	}
}

// ADR 018 D7: environment values and environment_keys are never compared, so a
// local secret map is never a difference and never produces a pending restore.
func TestPlanGraphImport_EnvironmentIsNeverADifference(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt", Environment: map[string]string{
			"SECRET_TOKEN": "keep-me", "PATH_EXTRA": "/opt/bin",
		}}},
		Models: []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--a"}, Active: true, Environment: map[string]string{
			"MODEL_SECRET": "keep-me-too",
		}}},
	}
	// convertBundle materializes Environment: nil for every entity, so this is the
	// exact shape a real v1 bundle presents against a secret-carrying repository.
	plan := PlanGraphImport(state,
		[]*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt"}},
		[]*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--a"}, Active: true}},
		nil,
	)
	statuses := classificationIndex(plan)
	if statuses["runtime:rt1"] != ImportStatusUnchanged || statuses["model:m1"] != ImportStatusUnchanged {
		t.Fatalf("missing environment values must not classify as a restore: %+v", statuses)
	}
	if len(plan.Blocked) != 0 || plan.WillCreate() {
		t.Fatalf("plan = %+v", plan)
	}
	rt, m := state.Runtimes[0], state.Models[0]
	if rt.Environment["SECRET_TOKEN"] != "keep-me" || m.Environment["MODEL_SECRET"] != "keep-me-too" {
		t.Fatal("planner mutated the local environment maps")
	}
}

// ADR 018 D5 with the ratified Slice 1 contract: restoring a rename onto a name
// owned by a different Runtime ID is a conflict, whatever the bundle order, and
// a name nobody holds stays a plain restore.
func TestPlanGraphImport_UpdateRenameCollision(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{
			{ID: "rt-a", Name: "Alpha", Executable: "/bin/a"},
			{ID: "rt-b", Name: "Beta", Executable: "/bin/b"},
		},
	}
	forward := []*RuntimeEntry{
		{ID: "rt-a", Name: "Beta", Executable: "/bin/a"},
		{ID: "rt-b", Name: "Alpha", Executable: "/bin/b"},
	}
	reversed := []*RuntimeEntry{forward[1], forward[0]}

	for _, bundle := range [][]*RuntimeEntry{forward, reversed} {
		plan := PlanGraphImport(state, bundle, nil, nil)
		statuses, reasons := classificationIndex(plan), reasonIndex(plan)
		if statuses["runtime:rt-a"] != ImportStatusBlocked || statuses["runtime:rt-b"] != ImportStatusBlocked {
			t.Fatalf("a name swap must block in both orders, got %+v", statuses)
		}
		if reasons["runtime:rt-a"] != ImportBlockedRuntimeNameTaken || reasons["runtime:rt-b"] != ImportBlockedRuntimeNameTaken {
			t.Fatalf("reason = %q / %q", reasons["runtime:rt-a"], reasons["runtime:rt-b"])
		}
		if len(plan.Blocked) != 2 || plan.Skipped.Total() != 0 || plan.Created.Total() != 0 {
			t.Fatalf("plan = %+v", plan)
		}
	}

	// A rename onto an unheld name is an ordinary restore.
	plan := PlanGraphImport(state,
		[]*RuntimeEntry{{ID: "rt-a", Name: "Gamma", Executable: "/bin/a"}}, nil, nil)
	if classificationIndex(plan)["runtime:rt-a"] != ImportStatusUpdate {
		t.Fatalf("rename onto an unheld name must be update: %+v", plan.Classifications)
	}
	// Display case alone is restorable, because only uniqueness folds case.
	plan = PlanGraphImport(state,
		[]*RuntimeEntry{{ID: "rt-a", Name: "alpha", Executable: "/bin/a"}}, nil, nil)
	if got := classificationIndex(plan)["runtime:rt-a"]; got != ImportStatusUpdate {
		t.Fatalf("display-case rename of the same identity must be update, got %q", got)
	}
	if len(plan.Blocked) != 0 {
		t.Fatalf("display-case rename must not block: %+v", plan.Blocked)
	}
}

// ADR 018 D5 keys `runtime_name_taken_other_id` on the name being owned by a
// different LOCAL Runtime ID, so a name a pending UPDATE merely intends to write
// is not held against the rest of the bundle. Slice 1 applies no update, so
// nothing here can put two colliding names in the repository: the create is
// checked against the repository and against earlier creates only. Deterministic
// precedence between a pending UPDATE target and a NEW name is a Slice 2'
// obligation (ADR 018 test obligation 8); over HTTP the shape is a bundle
// validation 400 first (`portable.go:273-281`, pinned by
// TestImport_ClashingUpdateAndNewRuntimeNames_RejectedBeforePlanning).
func TestPlanGraphImport_PendingUpdateDoesNotReserveItsTargetName(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt-a", Name: "Alpha", Executable: "/bin/a"}},
	}
	restore := &RuntimeEntry{ID: "rt-a", Name: "Gamma", Executable: "/bin/a"}
	fresh := &RuntimeEntry{ID: "rt-new", Name: "gamma", Executable: "/bin/new"}

	for _, bundle := range [][]*RuntimeEntry{{restore, fresh}, {fresh, restore}} {
		plan := PlanGraphImport(state, bundle, nil, nil)
		statuses := classificationIndex(plan)
		if statuses["runtime:rt-a"] != ImportStatusUpdate || statuses["runtime:rt-new"] != ImportStatusNew {
			t.Fatalf("classification must not depend on bundle order, got %+v", statuses)
		}
		if len(plan.Blocked) != 0 || plan.Created.Runtimes != 1 || plan.Skipped.Runtimes != 1 || plan.Total.Runtimes != 2 {
			t.Fatalf("plan = %+v", plan)
		}
		if len(plan.CreateRuntimes) != 1 || plan.CreateRuntimes[0].ID != "rt-new" {
			t.Fatalf("only the new runtime may be queued for writing: %+v", plan.CreateRuntimes)
		}
	}
}

// ADR 018 D5 with the ratified Slice 1 contract: an empty entry list blocks a
// Pipeline only when a write would leave it empty.
func TestPlanGraphImport_EmptyPipelineEntries(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt"}},
		Models:   []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Active: true}},
		Pipelines: []*PipelineEntry{
			{ID: "p-emptied", Name: "Full", Active: true, Models: []PipelineModel{{ID: "e1", ModelID: "m1"}}},
			{ID: "p-already-empty", Name: "Empty", Active: false},
		},
	}

	// NEW with no entries: cannot be created.
	plan := PlanGraphImport(state, nil, nil, []*PipelineEntry{{ID: "p-new", Name: "Fresh"}})
	if classificationIndex(plan)["pipeline:p-new"] != ImportStatusBlocked ||
		reasonIndex(plan)["pipeline:p-new"] != ImportBlockedPipelineEntriesEmpty {
		t.Fatalf("a new empty pipeline must be blocked: %+v", plan.Classifications)
	}
	if len(plan.CreatePipelines) != 0 || plan.WillCreate() {
		t.Fatalf("blocked pipeline must not be created: %+v", plan)
	}

	// UPDATE that would leave the stored pipeline empty: cannot be restored.
	plan = PlanGraphImport(state, nil, nil, []*PipelineEntry{{ID: "p-emptied", Name: "Renamed", Active: true}})
	if classificationIndex(plan)["pipeline:p-emptied"] != ImportStatusBlocked ||
		reasonIndex(plan)["pipeline:p-emptied"] != ImportBlockedPipelineEntriesEmpty {
		t.Fatalf("an update to an empty entry list must be blocked: %+v", plan.Classifications)
	}

	// UNCHANGED with no entries: nothing is written, so nothing is blocked. This
	// is the shape a repository can already hold from before this rule existed.
	plan = PlanGraphImport(state, nil, nil, []*PipelineEntry{{ID: "p-already-empty", Name: "Empty", Active: false}})
	if got := classificationIndex(plan)["pipeline:p-already-empty"]; got != ImportStatusUnchanged {
		t.Fatalf("an unchanged empty pipeline must stay unchanged, got %q", got)
	}
	if len(plan.Blocked) != 0 {
		t.Fatalf("unchanged pipeline must not block the import: %+v", plan.Blocked)
	}

	// A pipeline with entries still blocks on an unresolvable reference.
	plan = PlanGraphImport(state, nil, nil, []*PipelineEntry{
		{ID: "p-dangling", Name: "Dangling", Models: []PipelineModel{{ID: "e9", ModelID: "m-none"}}},
	})
	if reasonIndex(plan)["pipeline:p-dangling"] != ImportBlockedModelRef {
		t.Fatalf("reference blocking must stay intact: %+v", plan.Classifications)
	}
}

// ADR 018 D12: a pending UPDATE writes its entity too, so its references must
// resolve; an UNCHANGED pipeline writes nothing and is therefore not checked.
func TestPlanGraphImport_PendingUpdatePipelineResolvesDependencies(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt"}},
		Models:   []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Active: true}},
		Pipelines: []*PipelineEntry{
			{ID: "p-rename", Name: "P", Active: true, Models: []PipelineModel{{ID: "e1", ModelID: "m-new"}}},
			{ID: "p-keep", Name: "Q", Active: true, Models: []PipelineModel{{ID: "e2", ModelID: "m-new"}}},
		},
	}
	// m-new exists neither locally nor as a creatable entry: its own runtime
	// reference is unresolvable, so it is blocked.
	bundleModels := []*ModelEntry{{ID: "m-new", Name: "Fresh", RuntimeID: "rt-missing", Active: true}}

	// p-rename restores a name, so it is a pending write and inherits the block.
	plan := PlanGraphImport(state, nil, bundleModels, []*PipelineEntry{
		{ID: "p-rename", Name: "Renamed", Active: true, Models: []PipelineModel{{ID: "e1", ModelID: "m-new"}}},
	})
	if classificationIndex(plan)["pipeline:p-rename"] != ImportStatusBlocked {
		t.Fatalf("a pending update on a blocked dependency must block: %+v", plan.Classifications)
	}
	if reasonIndex(plan)["pipeline:p-rename"] != ImportBlockedDependency {
		t.Fatalf("reason = %q", reasonIndex(plan)["pipeline:p-rename"])
	}

	// p-keep is equivalent to the stored pipeline: no write, so no dependency
	// verdict. The import still writes nothing at all, because m-new blocks it.
	plan = PlanGraphImport(state, nil, bundleModels, []*PipelineEntry{
		{ID: "p-keep", Name: "Q", Active: true, Models: []PipelineModel{{ID: "e2", ModelID: "m-new"}}},
	})
	if got := classificationIndex(plan)["pipeline:p-keep"]; got != ImportStatusUnchanged {
		t.Fatalf("an unchanged pipeline is never blocked, got %q", got)
	}
	if len(plan.Blocked) != 1 || plan.Blocked[0].ID != "m-new" {
		t.Fatalf("only the unresolvable model may block: %+v", plan.Blocked)
	}
}

// The transitional counts must stay coherent for the shipped wire contract:
// every classified entity is counted exactly once per type.
func TestPlanGraphImport_TransitionalCountsStayCoherent(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{
			{ID: "rt-keep", Name: "RT", Executable: "/bin/rt"},
			{ID: "rt-fix", Name: "Fix", Executable: "/bin/fix"},
			{ID: "rt-taken", Name: "Taken", Executable: "/bin/taken"},
		},
	}
	plan := PlanGraphImport(state,
		[]*RuntimeEntry{
			{ID: "rt-keep", Name: "RT", Executable: "/bin/rt"},
			{ID: "rt-fix", Name: "Fix", Executable: "/bin/new"},
			{ID: "rt-other", Name: "TAKEN", Executable: "/bin/other"},
			{ID: "rt-fresh", Name: "Fresh", Executable: "/bin/fresh"},
		},
		nil, nil)

	blockedByType := map[string]int{}
	for _, c := range plan.Blocked {
		blockedByType[c.Type]++
	}
	if plan.Total.Runtimes != plan.Created.Runtimes+plan.Skipped.Runtimes+blockedByType["runtime"] {
		t.Fatalf("counts incoherent: total=%d created=%d skipped=%d blocked=%d",
			plan.Total.Runtimes, plan.Created.Runtimes, plan.Skipped.Runtimes, blockedByType["runtime"])
	}
	if plan.Total.Runtimes != 4 || plan.Created.Runtimes != 1 || plan.Skipped.Runtimes != 2 || blockedByType["runtime"] != 1 {
		t.Fatalf("plan = %+v blocked=%d", plan, blockedByType["runtime"])
	}
	if plan.ClassificationsByStatus(ImportStatusUpdate)[0].ID != "rt-fix" {
		t.Fatal("update classification must be readable by status")
	}
	if len(plan.ClassificationsByStatus(ImportStatusUnchanged)) != 1 {
		t.Fatalf("unchanged classification = %+v", plan.Classifications)
	}
}

// The comparison must not write into either side of the plan.
func TestPlanGraphImport_DoesNotMutateComparedRecords(t *testing.T) {
	state := ImportGraphState{
		Runtimes:  []*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt", Environment: map[string]string{"K": "V"}}},
		Models:    []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--keep"}, Active: true}},
		Pipelines: []*PipelineEntry{{ID: "p1", Name: "P", Active: true, Models: []PipelineModel{{ID: "e1", ModelID: "m1", Args: []string{"--x"}}}}},
	}
	bundleRuntimes := []*RuntimeEntry{{ID: "rt1", Name: "Renamed", Executable: "/bin/new"}}
	bundleModels := []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--keep", "--extra"}}}
	bundlePipelines := []*PipelineEntry{{ID: "p1", Name: "P", Active: true, Models: []PipelineModel{{ID: "e1", ModelID: "m1"}}}}

	PlanGraphImport(state, bundleRuntimes, bundleModels, bundlePipelines)

	if rt := state.Runtimes[0]; rt.Name != "RT" || rt.Executable != "/bin/rt" || len(rt.Environment) != 1 {
		t.Fatalf("stored runtime changed: %+v", rt)
	}
	if m := state.Models[0]; len(m.Args) != 1 || m.Args[0] != "--keep" || !m.Active {
		t.Fatalf("stored model changed: %+v", m)
	}
	if p := state.Pipelines[0]; len(p.Models) != 1 || len(p.Models[0].Args) != 1 || p.Models[0].Args[0] != "--x" {
		t.Fatalf("stored pipeline entries changed: %+v", p)
	}
	if rt := bundleRuntimes[0]; rt.Name != "Renamed" || rt.Executable != "/bin/new" {
		t.Fatalf("bundle runtime changed: %+v", rt)
	}
	if m := bundleModels[0]; len(m.Args) != 2 || m.Args[1] != "--extra" {
		t.Fatalf("bundle model changed: %+v", m)
	}
	if p := bundlePipelines[0]; len(p.Models) != 1 || p.Models[0].ID != "e1" || len(p.Models[0].Args) != 0 {
		t.Fatalf("bundle pipeline changed: %+v", p)
	}
}
