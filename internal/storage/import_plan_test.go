package storage

import (
	"os"
	"path/filepath"
	"testing"
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

	want := map[string]string{
		"runtime:rt-existing": ImportStatusExisting,
		"runtime:rt-new":      ImportStatusBlocked,
		"runtime:rt-fresh":    ImportStatusNew,
		"model:m-existing":    ImportStatusExisting,
		"model:m-samename":    ImportStatusNew,
		"model:m-orphan":      ImportStatusBlocked,
		"pipeline:p-existing": ImportStatusExisting,
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

// ImportGraph must not overwrite an existing entity, even when the incoming
// entry carries the same ID but different field values.
func TestImportGraph_SkipsExistingWithoutOverwrite(t *testing.T) {
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

// A plan with nothing to create must not touch the durable file either.
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
