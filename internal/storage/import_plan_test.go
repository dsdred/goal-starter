package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// relatedIndex reports the counterpart each blocked classification names, so the
// D-14 determinism proof can compare plans built from permuted inputs.
func relatedIndex(plan *ImportGraphPlan) map[string]string {
	out := map[string]string{}
	for _, c := range plan.Classifications {
		if c.Status == ImportStatusBlocked {
			out[c.Type+":"+c.ID] = c.RelatedID
		}
	}
	return out
}

func countPlan(plan *ImportGraphPlan) string {
	return fmt.Sprintf("total=%+v created=%+v updated=%+v unchanged=%+v blocked=%d",
		plan.Total, plan.Created, plan.Updated, plan.Unchanged, len(plan.Blocked))
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

	// rt-existing renames to a name nobody holds, so it is a restore onto the same
	// identity (ADR 018 D3). rt-new wants "taken", which the repository still owns
	// under its own ID: an UPDATE never releases the name it vacates (D-1).
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
	if len(plan.UpdateRuntimes) != 1 || plan.UpdateRuntimes[0].ID != "rt-existing" {
		t.Fatalf("update runtimes = %+v", plan.UpdateRuntimes)
	}
	if len(plan.UpdateModels) != 1 || plan.UpdateModels[0].ID != "m-existing" {
		t.Fatalf("update models = %+v", plan.UpdateModels)
	}
	if len(plan.UpdatePipelines) != 0 {
		t.Fatalf("no pipeline here is a restore: %+v", plan.UpdatePipelines)
	}
	if plan.Created.Runtimes != 1 || plan.Created.Models != 1 || plan.Created.Pipelines != 1 {
		t.Fatalf("created = %+v (%s)", plan.Created, countPlan(plan))
	}
	if plan.Updated.Runtimes != 1 || plan.Updated.Models != 1 || plan.Updated.Pipelines != 0 {
		t.Fatalf("updated = %+v (%s)", plan.Updated, countPlan(plan))
	}
	if plan.Unchanged.Pipelines != 1 {
		t.Fatalf("unchanged = %+v (%s)", plan.Unchanged, countPlan(plan))
	}
	if len(plan.Blocked) != 4 {
		t.Fatalf("blocked = %d: %+v", len(plan.Blocked), plan.Blocked)
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
	if plan.Unchanged.Total() != 0 {
		t.Fatalf("nothing is equivalent here: %+v", plan.Unchanged)
	}
	// rt-live is restated with its executable, which the bare state record does not
	// carry, so it is a pending restore rather than an unchanged record.
	if plan.Updated.Runtimes != 1 || len(plan.UpdateRuntimes) != 1 {
		t.Fatalf("updated = %+v / %+v", plan.Updated, plan.UpdateRuntimes)
	}
	if !plan.WillMutate() {
		t.Fatal("plan must have entities to write")
	}
}

// ADR 018 D3/D4/D11: for a same-ID entity the restorable projection decides
// UPDATE versus UNCHANGED, neither outcome is a conflict, and an UPDATE is an
// actionable write (ADR 018 D-9).
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
	if len(plan.UpdateRuntimes) != 1 || plan.UpdateRuntimes[0].ID != "rt-fix" {
		t.Fatalf("update runtimes = %+v", plan.UpdateRuntimes)
	}
	if len(plan.UpdateModels) != 1 || plan.UpdateModels[0].ID != "m-fix" {
		t.Fatalf("update models = %+v", plan.UpdateModels)
	}
	if len(plan.UpdatePipelines) != 1 || plan.UpdatePipelines[0].ID != "p-fix" {
		t.Fatalf("update pipelines = %+v", plan.UpdatePipelines)
	}
	// An update makes the plan actionable: D-9 counts NEW and UPDATE together.
	if !plan.WillMutate() {
		t.Fatal("a pending restore must make the plan actionable")
	}
	if plan.Created.Total() != 0 || plan.Updated.Total() != 3 || plan.Unchanged.Total() != 3 {
		t.Fatalf("counts = %s", countPlan(plan))
	}
	if plan.Total.Total() != 6 {
		t.Fatalf("totals = %+v", plan.Total)
	}

	// The restore records carry the bundle projection over the local identity.
	localFix := state.Runtimes[1]
	rt := plan.UpdateRuntimes[0]
	if rt.Executable != "/bin/new" || rt.Name != "Other" || rt.CreatedAt != when {
		t.Fatalf("restored runtime = %+v", rt)
	}
	// The planner stamps no time; the write path owns UpdatedAt.
	if rt.UpdatedAt != localFix.UpdatedAt {
		t.Fatalf("the planner must not stamp time: %+v vs %+v", rt.UpdatedAt, localFix.UpdatedAt)
	}
	if plan.UpdateModels[0].Active {
		t.Fatal("model restore must carry the exported Active=false")
	}
	if len(plan.UpdatePipelines[0].Models) != 1 || plan.UpdatePipelines[0].Models[0].ModelID != "m-fix" {
		t.Fatalf("pipeline restore = %+v", plan.UpdatePipelines[0].Models)
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
	if len(plan.Blocked) != 0 || plan.WillMutate() {
		t.Fatalf("plan = %s", countPlan(plan))
	}
	rt, m := state.Runtimes[0], state.Models[0]
	if rt.Environment["SECRET_TOKEN"] != "keep-me" || m.Environment["MODEL_SECRET"] != "keep-me-too" {
		t.Fatal("planner mutated the local environment maps")
	}
}

// A restore record is a copy: it never aliases the bundle entry (whose
// Environment is nil and would clear a local secret) and never aliases the local
// record (whose pointer the apply step must be able to roll back).
func TestPlanGraphImport_RestoreRecordsAreCopies(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt", Environment: map[string]string{"SECRET": "local"}}},
		Models:   []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--local"}, Environment: map[string]string{"SECRET": "local"}}},
		Pipelines: []*PipelineEntry{{ID: "p1", Name: "P", Active: true, Models: []PipelineModel{
			{ID: "local-e1", ModelID: "m1", Args: []string{"--local"}},
		}}},
	}
	bundleRuntime := &RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/new"}
	bundleModel := &ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--bundle"}}
	bundlePipeline := &PipelineEntry{ID: "p1", Name: "P", Active: true, Models: []PipelineModel{
		{ID: "bundle-e1", ModelID: "m1", Args: []string{"--bundle"}},
	}}

	plan := PlanGraphImport(state, []*RuntimeEntry{bundleRuntime}, []*ModelEntry{bundleModel}, []*PipelineEntry{bundlePipeline})

	if plan.UpdateRuntimes[0] == state.Runtimes[0] || plan.UpdateRuntimes[0] == bundleRuntime {
		t.Fatal("runtime restore record aliases a plan input")
	}
	if plan.UpdateModels[0] == state.Models[0] || plan.UpdateModels[0] == bundleModel {
		t.Fatal("model restore record aliases a plan input")
	}
	if plan.UpdatePipelines[0] == state.Pipelines[0] || plan.UpdatePipelines[0] == bundlePipeline {
		t.Fatal("pipeline restore record aliases a plan input")
	}
	if plan.UpdateRuntimes[0].Environment["SECRET"] != "local" {
		t.Fatalf("restore lost the local environment: %+v", plan.UpdateRuntimes[0].Environment)
	}
	if plan.UpdateModels[0].Environment["SECRET"] != "local" {
		t.Fatalf("restore lost the local model environment: %+v", plan.UpdateModels[0].Environment)
	}
	// The args slice is copied, not shared with the bundle entry.
	plan.UpdateModels[0].Args[0] = "mutated by test"
	if bundleModel.Args[0] != "--bundle" || state.Models[0].Args[0] != "--local" {
		t.Fatalf("args aliased: bundle=%v local=%v", bundleModel.Args, state.Models[0].Args)
	}
	// D10: the restored entry list is the bundle list, entry IDs included.
	entries := plan.UpdatePipelines[0].Models
	if len(entries) != 1 || entries[0].ID != "bundle-e1" || entries[0].Args[0] != "--bundle" {
		t.Fatalf("restored entries = %+v", entries)
	}
}

// ADR 018 D5/D-1: restoring a rename onto a name owned by a different Runtime ID
// is a conflict, whatever the bundle order, and a name nobody holds stays a
// plain restore.
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
		if len(plan.Blocked) != 2 || plan.Unchanged.Total() != 0 || plan.Created.Total() != 0 || plan.Updated.Total() != 0 {
			t.Fatalf("plan = %s", countPlan(plan))
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

// ─── ADR 018 obligation 8: the complete runtime-name contest ───

// assertContest proves one collision case of the ratified matrix: the named
// entities are BLOCKED with the name-taken reason, nothing is queued for a
// write, and every permutation of the same bundle inputs classifies identically.
func assertContest(t *testing.T, state ImportGraphState, permutations [][]*RuntimeEntry, wantBlocked []string) {
	t.Helper()

	var first map[string]string
	var firstRelated map[string]string

	for i, bundle := range permutations {
		plan := PlanGraphImport(state, bundle, nil, nil)
		statuses, related := classificationIndex(plan), relatedIndex(plan)
		if first != nil {
			if len(statuses) != len(first) {
				t.Fatalf("permutation %d classifies a different entity set: %v vs %v", i, statuses, first)
			}
			for k, v := range first {
				if statuses[k] != v {
					t.Fatalf("permutation %d: %s = %q, want %q (order-dependent classification)", i, k, statuses[k], v)
				}
				if related[k] != firstRelated[k] {
					t.Fatalf("permutation %d: %s related_id = %q, want %q", i, k, related[k], firstRelated[k])
				}
			}
		} else {
			first, firstRelated = statuses, related
		}
		for _, id := range wantBlocked {
			key := "runtime:" + id
			if statuses[key] != ImportStatusBlocked {
				t.Fatalf("permutation %d: %s = %q, want blocked: %+v", i, key, statuses[key], statuses)
			}
			if reasonIndex(plan)[key] != ImportBlockedRuntimeNameTaken {
				t.Fatalf("permutation %d: %s reason = %q", i, key, reasonIndex(plan)[key])
			}
		}
		if plan.WillMutate() {
			t.Fatalf("permutation %d: a blocked contest must queue no write: %s", i, countPlan(plan))
		}
		if len(plan.CreateRuntimes) != 0 || len(plan.UpdateRuntimes) != 0 {
			t.Fatalf("permutation %d: write lists must be empty: %+v / %+v", i, plan.CreateRuntimes, plan.UpdateRuntimes)
		}
	}
}

// Cases 1 and 2: a NEW runtime and a pending UPDATE of a different runtime that
// claim one case-insensitive final name are BOTH blocked, with no precedence for
// either class, in both bundle orders (ADR 018 D-8).
func TestPlanGraphImport_NewVsPendingUpdateBothBlocked(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt-a", Name: "x", Executable: "/bin/a"}},
	}
	updateA := &RuntimeEntry{ID: "rt-a", Name: "y", Executable: "/bin/a"}
	newB := &RuntimeEntry{ID: "rt-b", Name: "Y", Executable: "/bin/b"}

	assertContest(t, state, [][]*RuntimeEntry{{updateA, newB}, {newB, updateA}}, []string{"rt-a", "rt-b"})
}

// Case 3: a pending UPDATE that keeps its own name still collides with a NEW
// runtime of that name — the two different final runtimes contend, and the
// locally-held name is reserved exactly as D-1 states.
func TestPlanGraphImport_UpdateKeepingItsOwnNameCollidesWithNew(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt-a", Name: "x", Executable: "/bin/a"}},
	}
	// Same name, different executable: still a pending write of "x".
	updateA := &RuntimeEntry{ID: "rt-a", Name: "x", Executable: "/bin/new"}
	newB := &RuntimeEntry{ID: "rt-b", Name: "X", Executable: "/bin/b"}

	assertContest(t, state, [][]*RuntimeEntry{{updateA, newB}, {newB, updateA}}, []string{"rt-a", "rt-b"})
}

// Case 4: self-ownership is never a conflict — an UPDATE that keeps its own name
// and claims no other name restores normally.
func TestPlanGraphImport_SelfOwnershipIsNotAConflict(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt-a", Name: "x", Executable: "/bin/a"}},
	}
	plan := PlanGraphImport(state,
		[]*RuntimeEntry{{ID: "rt-a", Name: "X", Executable: "/bin/new"}}, nil, nil)
	if classificationIndex(plan)["runtime:rt-a"] != ImportStatusUpdate {
		t.Fatalf("keeping one's own name must not block: %+v", plan.Classifications)
	}
	if len(plan.Blocked) != 0 {
		t.Fatalf("blocked = %+v", plan.Blocked)
	}
	if len(plan.UpdateRuntimes) != 1 || !plan.WillMutate() {
		t.Fatalf("plan = %s", countPlan(plan))
	}
}

// Case 5: an UPDATE/UPDATE rename swap stays blocked under D-1 — each target name
// is held by the other runtime, and neither write can be proven safe.
func TestPlanGraphImport_UpdateSwapStaysBlocked(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{
			{ID: "rt-a", Name: "x", Executable: "/bin/a"},
			{ID: "rt-b", Name: "y", Executable: "/bin/b"},
		},
	}
	swap := []*RuntimeEntry{
		{ID: "rt-a", Name: "y", Executable: "/bin/a"},
		{ID: "rt-b", Name: "x", Executable: "/bin/b"},
	}
	assertContest(t, state, [][]*RuntimeEntry{swap, {swap[1], swap[0]}}, []string{"rt-a", "rt-b"})
}

// Case 7 (D-12): two different NEW runtimes claiming one case-insensitive name
// are BOTH blocked, with no first-wins, in both orders. This shape is unreachable
// over HTTP (bundle validation answers 400), so it is proven at the planner and
// at the repository.
func TestPlanGraphImport_TwoNewRuntimesBothBlocked(t *testing.T) {
	newA := &RuntimeEntry{ID: "rt-a", Name: "y", Executable: "/bin/a"}
	newB := &RuntimeEntry{ID: "rt-b", Name: "Y", Executable: "/bin/b"}
	assertContest(t, ImportGraphState{}, [][]*RuntimeEntry{{newA, newB}, {newB, newA}}, []string{"rt-a", "rt-b"})
}

// Case 8 (D-13): an entity that cannot execute contributes no intended-final-name
// claim, so blocking one claimant never manufactures a second collision for
// entities whose verdict does not depend on it.
func TestPlanGraphImport_BlockedClaimantManufacturesNoSecondCollision(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{
			{ID: "rt-a", Name: "x", Executable: "/bin/a"},
			{ID: "rt-c", Name: "z", Executable: "/bin/c"},
		},
	}
	// rt-a renames onto the locally-owned "z" and blocks; rt-b is an unrelated new
	// runtime, and rt-c keeps its own name as an UNCHANGED record.
	plan := PlanGraphImport(state,
		[]*RuntimeEntry{
			{ID: "rt-a", Name: "Z", Executable: "/bin/a"},
			{ID: "rt-b", Name: "Fresh", Executable: "/bin/b"},
			{ID: "rt-c", Name: "z", Executable: "/bin/c"},
		},
		nil, nil)

	statuses := classificationIndex(plan)
	if statuses["runtime:rt-a"] != ImportStatusBlocked || statuses["runtime:rt-b"] != ImportStatusNew || statuses["runtime:rt-c"] != ImportStatusUnchanged {
		t.Fatalf("only the claimant of a locally-owned name may block: %+v", statuses)
	}
	if relatedIndex(plan)["runtime:rt-a"] != "rt-c" {
		t.Fatalf("related_id = %q, want rt-c", relatedIndex(plan)["runtime:rt-a"])
	}
	if plan.Created.Runtimes != 1 || plan.Unchanged.Runtimes != 1 || plan.Updated.Runtimes != 0 {
		t.Fatalf("plan = %s", countPlan(plan))
	}
	// A bundle whose runtime is blocked still writes nothing at all, because the
	// whole plan is rejected — no dependent cascade is manufactured here.
	if len(plan.Blocked) != 1 {
		t.Fatalf("blocked = %+v", plan.Blocked)
	}
}

// D-14: for a contested set larger than two, every claimant reports the
// lexicographically smallest OTHER claimant, and neither the classification nor
// the reported ID depends on bundle order.
func TestPlanGraphImport_ContestedSetReportsSmallestOtherClaimant(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{
			{ID: "rt-keep", Name: "Unrelated", Executable: "/bin/keep"},
			{ID: "rt-m", Name: "Shared", Executable: "/bin/m"},
		},
	}
	a := &RuntimeEntry{ID: "rt-a", Name: "shared", Executable: "/bin/a"}
	b := &RuntimeEntry{ID: "rt-b", Name: "SHARED", Executable: "/bin/b"}
	m := &RuntimeEntry{ID: "rt-m", Name: "Shared", Executable: "/bin/new"}

	orders := [][]*RuntimeEntry{
		{a, b, m, {ID: "rt-keep", Name: "Unrelated", Executable: "/bin/keep"}},
		{m, a, b, {ID: "rt-keep", Name: "Unrelated", Executable: "/bin/keep"}},
		{b, m, {ID: "rt-keep", Name: "Unrelated", Executable: "/bin/keep"}, a},
	}
	assertContest(t, state, orders, []string{"rt-a", "rt-b", "rt-m"})

	// The three-way set reports, for each claimant, the lexicographically smallest
	// OTHER claimant: rt-a and rt-b name each other, rt-m names rt-a. Never the
	// entity's own ID, and never the one that happened to come first in the bundle.
	plan := PlanGraphImport(state, orders[0], nil, nil)
	related := relatedIndex(plan)
	if related["runtime:rt-a"] != "rt-b" || related["runtime:rt-b"] != "rt-a" || related["runtime:rt-m"] != "rt-a" {
		t.Fatalf("related_id map = %+v", related)
	}
	// The uncontested runtime of the same bundle is unaffected.
	if classificationIndex(plan)["runtime:rt-keep"] != ImportStatusUnchanged {
		t.Fatalf("unrelated runtime = %+v", plan.Classifications)
	}
}

// The per-type counts must stay coherent for the wire contract: every classified
// entity is counted exactly once per type.
func TestPlanGraphImport_CountsStayCoherent(t *testing.T) {
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
	if plan.Total.Runtimes != plan.Created.Runtimes+plan.Updated.Runtimes+plan.Unchanged.Runtimes+blockedByType["runtime"] {
		t.Fatalf("counts incoherent: %s blocked=%d", countPlan(plan), blockedByType["runtime"])
	}
	if plan.Total.Runtimes != 4 || plan.Created.Runtimes != 1 || plan.Updated.Runtimes != 1 ||
		plan.Unchanged.Runtimes != 1 || blockedByType["runtime"] != 1 {
		t.Fatalf("plan = %s blocked=%d", countPlan(plan), blockedByType["runtime"])
	}
	if plan.ClassificationsByStatus(ImportStatusUpdate)[0].ID != "rt-fix" {
		t.Fatal("update classification must be readable by status")
	}
	if len(plan.ClassificationsByStatus(ImportStatusUnchanged)) != 1 {
		t.Fatalf("unchanged classification = %+v", plan.Classifications)
	}
}

// ADR 018 D5: an empty entry list blocks a Pipeline only when a write would
// leave it empty.
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
	if len(plan.CreatePipelines) != 0 || plan.WillMutate() {
		t.Fatalf("blocked pipeline must not be created: %s", countPlan(plan))
	}

	// UPDATE that would leave the stored pipeline empty: cannot be restored.
	plan = PlanGraphImport(state, nil, nil, []*PipelineEntry{{ID: "p-emptied", Name: "Renamed", Active: true}})
	if classificationIndex(plan)["pipeline:p-emptied"] != ImportStatusBlocked ||
		reasonIndex(plan)["pipeline:p-emptied"] != ImportBlockedPipelineEntriesEmpty {
		t.Fatalf("an update to an empty entry list must be blocked: %+v", plan.Classifications)
	}
	if len(plan.UpdatePipelines) != 0 || plan.WillMutate() {
		t.Fatalf("a blocked restore must not be queued: %+v", plan.UpdatePipelines)
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

// ADR 018 D12 closes the pending-UPDATE reference hole for Models too: a model
// that exists locally but would be restored onto an unresolvable runtime is
// blocked instead of being written dangling.
func TestPlanGraphImport_PendingUpdateModelResolvesRuntime(t *testing.T) {
	state := ImportGraphState{
		Runtimes: []*RuntimeEntry{{ID: "rt1", Name: "RT", Executable: "/bin/rt"}},
		Models:   []*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt1", Active: true}},
	}
	plan := PlanGraphImport(state, nil,
		[]*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt-gone", Active: true}}, nil)
	if classificationIndex(plan)["model:m1"] != ImportStatusBlocked ||
		reasonIndex(plan)["model:m1"] != ImportBlockedRuntimeRef {
		t.Fatalf("a restore onto an unresolvable runtime must block: %+v", plan.Classifications)
	}
	if len(plan.UpdateModels) != 0 || plan.WillMutate() {
		t.Fatalf("blocked restore must queue nothing: %s", countPlan(plan))
	}

	// The same model restored onto a runtime the plan itself creates resolves.
	plan = PlanGraphImport(state,
		[]*RuntimeEntry{{ID: "rt2", Name: "RT2", Executable: "/bin/rt2"}},
		[]*ModelEntry{{ID: "m1", Name: "M", RuntimeID: "rt2", Active: true}}, nil)
	if classificationIndex(plan)["model:m1"] != ImportStatusUpdate {
		t.Fatalf("a reference to a runtime created by this plan resolves: %+v", plan.Classifications)
	}
	if len(plan.UpdateModels) != 1 || plan.UpdateModels[0].RuntimeID != "rt2" {
		t.Fatalf("update models = %+v", plan.UpdateModels)
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

// ─── ImportGraph apply (ADR 018 D3/D4/D13) ───

// An UPDATE is applied, not merely classified: the restored projection replaces
// the local record, the identity and the fields restore must not touch are
// preserved, and NEW + UPDATE land in one write.
func TestImportGraph_UpdateIsAppliedWithCreateInOneWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "Original", Executable: "/bin/original", WorkingDirectory: "/orig", Environment: map[string]string{"SECRET": "keep-me"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&ModelEntry{ID: "m1", Name: "Original Model", RuntimeID: "rt1", Args: []string{"--keep"}, Active: true, Environment: map[string]string{"SECRET": "keep-me-too"}}); err != nil {
		t.Fatal(err)
	}
	before := r.snapshotForTest()

	plan, err := r.ImportGraph(
		[]*RuntimeEntry{
			{ID: "rt1", Name: "Restored", Executable: "/bin/restored", WorkingDirectory: "/restored"},
			{ID: "rt2", Name: "Fresh", Executable: "/bin/fresh"},
		},
		[]*ModelEntry{{ID: "m1", Name: "Restored Model", RuntimeID: "rt1", Args: []string{"--restored"}, Active: false}},
		nil,
	)
	if err != nil {
		t.Fatalf("update must be applied, not rejected: %v", err)
	}
	if plan.Created.Runtimes != 1 || plan.Updated.Runtimes != 1 || plan.Updated.Models != 1 {
		t.Fatalf("plan = %s", countPlan(plan))
	}
	if !plan.WillMutate() {
		t.Fatal("a plan that writes must report WillMutate")
	}

	rt, err := repo.GetRuntime("rt1")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Name != "Restored" || rt.Executable != "/bin/restored" || rt.WorkingDirectory != "/restored" {
		t.Fatalf("runtime not restored: %+v", rt)
	}
	if rt.Environment["SECRET"] != "keep-me" {
		t.Fatalf("restore destroyed the local environment: %+v", rt.Environment)
	}
	if rt.CreatedAt != before.runtimes[0].CreatedAt {
		t.Fatalf("restore changed CreatedAt: %v -> %v", before.runtimes[0].CreatedAt, rt.CreatedAt)
	}
	if !rt.UpdatedAt.After(before.runtimes[0].UpdatedAt) && rt.UpdatedAt != before.runtimes[0].UpdatedAt {
		t.Fatalf("UpdatedAt must be restamped by the write path: %v", rt.UpdatedAt)
	}
	fresh, err := repo.GetRuntime("rt2")
	if err != nil || fresh.Name != "Fresh" {
		t.Fatalf("new runtime not created: %+v %v", fresh, err)
	}
	m, err := repo.GetModel("m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Restored Model" || len(m.Args) != 1 || m.Args[0] != "--restored" || m.Active {
		t.Fatalf("model not restored: %+v", m)
	}
	if m.Environment["SECRET"] != "keep-me-too" {
		t.Fatalf("model restore destroyed the local environment: %+v", m.Environment)
	}

	// One durable write for the whole graph: the file carries both changes.
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Restored", "Fresh", "--restored"} {
		if !strings.Contains(string(disk), want) {
			t.Fatalf("durable file missing %q: %s", want, disk)
		}
	}
}

// D10: a restored Pipeline takes the bundle entry list — bundle entry IDs and
// order are preserved, local entries the bundle does not carry are dropped, and a
// new entry ID the repository has never seen is kept.
func TestImportGraph_PipelineEntryIdentityRestored(t *testing.T) {
	repo, err := NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/rt"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m-keep", "m-drop"} {
		if err := repo.CreateModel(&ModelEntry{ID: id, Name: id, RuntimeID: "rt1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreatePipeline(&PipelineEntry{ID: "p1", Name: "P", Active: true, Models: []PipelineModel{
		{ID: "local-e1", ModelID: "m-keep", Args: []string{"--local"}},
		{ID: "local-e2", ModelID: "m-drop"},
	}}); err != nil {
		t.Fatal(err)
	}

	plan, err := r.ImportGraph(nil, nil, []*PipelineEntry{{
		ID: "p1", Name: "P", Active: true, Models: []PipelineModel{
			{ID: "bundle-e9", ModelID: "m-keep", Args: []string{"--bundle"}, AutoStart: true},
			{ID: "", ModelID: "m-keep"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Updated.Pipelines != 1 {
		t.Fatalf("plan = %s", countPlan(plan))
	}

	p, err := repo.GetPipeline("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Models) != 2 {
		t.Fatalf("entries = %+v", p.Models)
	}
	if p.Models[0].ID != "bundle-e9" || p.Models[0].ModelID != "m-keep" ||
		len(p.Models[0].Args) != 1 || p.Models[0].Args[0] != "--bundle" || !p.Models[0].AutoStart {
		t.Fatalf("bundle entry not preserved: %+v", p.Models[0])
	}
	// An empty entry ID is allocated by the repository, never left empty (D10).
	if p.Models[1].ID == "" {
		t.Fatalf("empty entry ID must be allocated: %+v", p.Models[1])
	}
	if p.Models[1].ID == "bundle-e9" {
		t.Fatalf("allocated ID collided with a bundle ID: %+v", p.Models[1])
	}
	// local-e2 is gone: the restored membership is the bundle's.
	for _, e := range p.Models {
		if e.ID == "local-e2" || e.ModelID == "m-drop" {
			t.Fatalf("unconsumed local entry survived: %+v", p.Models)
		}
	}
}

// A NEW pipeline keeps the entry IDs the bundle carries.
func TestImportGraph_NewPipelinePreservesBundleEntryIDs(t *testing.T) {
	repo, err := NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/rt"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := r.ImportGraph(nil, nil, []*PipelineEntry{{
		ID: "p-new", Name: "Fresh", Active: true,
		Models: []PipelineModel{{ID: "bundle-e1", ModelID: "m1"}},
	}}); err != nil {
		t.Fatal(err)
	}
	p, err := repo.GetPipeline("p-new")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Models) != 1 || p.Models[0].ID != "bundle-e1" {
		t.Fatalf("created pipeline entries = %+v", p.Models)
	}
}

// A blocked plan writes nothing at all: not the blocked entity, not the
// otherwise-new entities beside it, and not a pending restore.
func TestImportGraph_BlockedPlanWritesNothing(t *testing.T) {
	repo, err := NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt-owner", Name: "Taken", Executable: "/bin/o"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt-stable", Name: "Stable", Executable: "/bin/stable", WorkingDirectory: "/keep"}); err != nil {
		t.Fatal(err)
	}

	_, err = r.ImportGraph(
		[]*RuntimeEntry{
			{ID: "rt-clash", Name: "taken", Executable: "/bin/clash"},
			{ID: "rt-fine", Name: "Fine", Executable: "/bin/fine"},
			{ID: "rt-stable", Name: "Renamed", Executable: "/bin/stable"},
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
	stable, _ := repo.GetRuntime("rt-stable")
	if stable.Name != "Stable" || stable.WorkingDirectory != "/keep" {
		t.Fatalf("blocked plan applied a pending restore: %+v", stable)
	}
	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 2 {
		t.Fatalf("runtime count changed: %d", len(runtimes))
	}
}

// A plan with nothing to write must not touch the durable file either. Here every
// entity is UNCHANGED, so the file stays byte-identical.
func TestImportGraph_NothingToMutate_LeavesFileUntouched(t *testing.T) {
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
	if plan.Created.Total() != 0 || plan.Updated.Total() != 0 || plan.Unchanged.Runtimes != 1 {
		t.Fatalf("plan = %s", countPlan(plan))
	}
	if plan.WillMutate() {
		t.Fatal("an all-unchanged plan must not report a mutation")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("nothing-to-write import rewrote the durable file")
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

// snapshotForTest copies the in-memory graph so a test can compare identity and
// timestamps against what the write changed.
func (r *JSONRepository) snapshotForTest() testSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := testSnapshot{}
	for _, e := range r.runtimes {
		cp := *e
		s.runtimes = append(s.runtimes, &cp)
	}
	for _, e := range r.models {
		cp := *e
		s.models = append(s.models, &cp)
	}
	for _, e := range r.pipelines {
		cp := *e
		s.pipelines = append(s.pipelines, &cp)
	}
	return s
}

type testSnapshot struct {
	runtimes  []*RuntimeEntry
	models    []*ModelEntry
	pipelines []*PipelineEntry
}
