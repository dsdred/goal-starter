package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

// ─── ADR 013 D1: entry identity and repeatable models ───

// D10 item 2: a model may enter one pipeline multiple times; create and update
// are accepted (no 400) and each entry gets a distinct, server-generated id.
func TestPipelineRepeat_DuplicateModelAccepted(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "dup", "graceful")
	entry := &storage.PipelineEntry{Name: "dup", Models: []storage.PipelineModel{{ModelID: m1}, {ModelID: m1}}}
	if err := e.svc.CreatePipeline(ctx, entry); err != nil {
		t.Fatalf("duplicate model create must be accepted: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("server must assign a pipeline id")
	}
	if entry.Models[0].ID == "" || entry.Models[1].ID == "" {
		t.Fatalf("server must assign distinct entry ids: %+v", entry.Models)
	}
	if entry.Models[0].ID == entry.Models[1].ID {
		t.Fatalf("entry ids must be distinct: %+v", entry.Models)
	}
	e1, e2 := entry.Models[0].ID, entry.Models[1].ID
	t.Cleanup(func() { e.stopPipeline(t, entry.ID) })

	// Update round-trips the ids (non-structural) — accepted while nothing active.
	if err := e.svc.UpdatePipeline(ctx, &storage.PipelineEntry{
		ID: entry.ID, Name: "dup-2",
		Models: []storage.PipelineModel{{ID: e1, ModelID: m1}, {ID: e2, ModelID: m1}},
	}); err != nil {
		t.Fatalf("non-structural update (ids round-tripped) must be accepted: %v", err)
	}
}

// D1: create drops client-supplied entry ids (server is the only source of
// entry identity); update rejects an unknown entry id with 400.
func TestPipelineRepeat_EntryIDOwnership(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "own", "graceful")
	entry := &storage.PipelineEntry{Name: "own", Models: []storage.PipelineModel{{ID: "client-1", ModelID: m1}}}
	if err := e.svc.CreatePipeline(ctx, entry); err != nil {
		t.Fatalf("create: %v", err)
	}
	if entry.Models[0].ID == "client-1" {
		t.Fatal("client-supplied entry id must be dropped on create (server-generated)")
	}

	if err := e.svc.UpdatePipeline(ctx, &storage.PipelineEntry{
		ID: entry.ID, Name: "own", Models: []storage.PipelineModel{{ID: "ghost-id", ModelID: m1}},
	}); err == nil {
		t.Fatal("update with an unknown entry id must be rejected")
	} else {
		var apiErr *apierrors.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != apierrors.CodeBadRequest {
			t.Fatalf("unknown entry id error = %v, want bad_request", err)
		}
	}
}

// ─── ADR 013 D2/D3: within-pipeline independence + per-entry Args ───

// D10 item 3: the same model twice with different overrides launches two
// independent instances with different resolved Args; the persisted Model.Args
// is byte-identical afterward.
func TestPipelineRepeat_TwoEntriesDifferentArgs(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "port", "graceful")
	before, err := e.repo.GetModel(m1)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	pipe := e.addPipeline(t, "ports",
		storage.PipelineModel{ModelID: m1, Args: []string{"echo", "port-8081"}},
		storage.PipelineModel{ModelID: m1, Args: []string{"echo", "port-8082"}},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Results[0].Status != OutcomeStarted || res.Results[1].Status != OutcomeStarted {
		t.Fatalf("both duplicate entries must start independently: %+v", res.Results)
	}
	if res.Results[0].EntryID == res.Results[1].EntryID || res.Results[0].EntryID == "" {
		t.Fatalf("results must carry distinct entry ids: %+v", res.Results)
	}
	if res.Results[0].InstanceID == res.Results[1].InstanceID {
		t.Fatalf("the two entries must launch distinct instances: %+v", res.Results)
	}

	insts := e.instancesFor(t, m1)
	if len(insts) != 2 {
		t.Fatalf("want 2 instances of the model, got %d: %+v", len(insts), insts)
	}
	argsByEntry := map[string][]string{}
	for _, inst := range insts {
		if inst.PipelineID != pipe {
			t.Fatalf("instance %s not attributed to the pipeline: %+v", inst.ID, inst)
		}
		if inst.PipelineEntryID == "" {
			t.Fatalf("instance %s must carry a pipeline_entry_id: %+v", inst.ID, inst)
		}
		argsByEntry[inst.PipelineEntryID] = inst.Args
	}
	a := argsByEntry[res.Results[0].EntryID]
	b := argsByEntry[res.Results[1].EntryID]
	if !reflect.DeepEqual(a, []string{"echo", "port-8081"}) || !reflect.DeepEqual(b, []string{"echo", "port-8082"}) {
		t.Fatalf("per-entry resolved args wrong: e1=%v e2=%v", a, b)
	}

	after, err := e.repo.GetModel(m1)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if !reflect.DeepEqual(after.Args, before.Args) {
		t.Fatalf("persisted Model.Args must be byte-identical: before=%v after=%v", before.Args, after.Args)
	}
}

// D10 item 5 (part): per-entry idempotency — a second start of the same entry
// yields already-running and creates no second copy for that entry.
func TestPipelineRepeat_PerEntryIdempotency(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "idem", "graceful")
	pipe := e.addPipeline(t, "idem",
		storage.PipelineModel{ModelID: m1, Args: []string{"graceful"}},
		storage.PipelineModel{ModelID: m1, Args: []string{"graceful"}},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(e.instancesFor(t, m1)) != 2 {
		t.Fatalf("want 2 instances after first start: %+v", e.instancesFor(t, m1))
	}
	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start (idempotent): %v", err)
	}
	for i, r := range res.Results {
		if r.Status != OutcomeAlreadyRunning {
			t.Fatalf("second start entry %d = %q, want already-running: %+v", i, r.Status, r)
		}
	}
	if len(e.instancesFor(t, m1)) != 2 {
		t.Fatalf("idempotent start must not create copies: %+v", e.instancesFor(t, m1))
	}
}

// D10 item 5 (part): the model-owner rule. An active instance owned by ANOTHER
// pipeline blocks this pipeline's entry (no adoption); a manual active instance
// likewise blocks (already-running).
func TestPipelineRepeat_ModelOwnerRule(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "owner", "graceful")
	pipeOwner := e.addPipeline(t, "owner", storage.PipelineModel{ModelID: m1})
	pipeOther := e.addPipeline(t, "other", storage.PipelineModel{ModelID: m1})
	t.Cleanup(func() { e.stopPipeline(t, pipeOwner) })
	t.Cleanup(func() { e.stopPipeline(t, pipeOther) })

	// The earlier pipeline owns the model.
	if _, err := e.svc.Start(ctx, pipeOwner); err != nil {
		t.Fatalf("owner Start: %v", err)
	}
	// The later pipeline's entry yields already-running, never adopted.
	res, err := e.svc.Start(ctx, pipeOther)
	if err != nil {
		t.Fatalf("other Start: %v", err)
	}
	if res.Results[0].Status != OutcomeAlreadyRunning {
		t.Fatalf("later pipeline entry = %q, want already-running: %+v", res.Results[0].Status, res.Results[0])
	}
	// The single instance still belongs to the owner pipeline.
	insts := e.instancesFor(t, m1)
	if len(insts) != 1 || insts[0].PipelineID != pipeOwner {
		t.Fatalf("owner must keep its single instance: %+v", insts)
	}

	// Manual active instance → already-running for a pipeline entry.
	e.stopPipeline(t, pipeOwner)
	me, err := e.repo.GetModel(m1)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	rte, err := e.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	if _, err := e.sup.AdmitAndStart(ctx, domain.ModelEntryToDomain(me),
		process.RuntimeToDomain(rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory, rte.Environment), domain.ManualOwner, nil, nil); err != nil {
		t.Fatalf("manual Start: %v", err)
	}
	res, err = e.svc.Start(ctx, pipeOther)
	if err != nil {
		t.Fatalf("Start (manual active): %v", err)
	}
	if res.Results[0].Status != OutcomeAlreadyRunning {
		t.Fatalf("manual-active entry = %q, want already-running: %+v", res.Results[0].Status, res.Results[0])
	}
}

// ─── ADR 013 D4: per-entry stop attribution + legacy fallback ───

// D10 item 6: stopEntry stops exactly the entry's attributed instances. Two
// duplicate entries stop independently (each instance stops with its own entry).
func TestPipelineRepeat_StopPerEntryAttribution(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "stopper", "graceful")
	pipe := e.addPipeline(t, "stopper",
		storage.PipelineModel{ModelID: m1, Args: []string{"graceful"}},
		storage.PipelineModel{ModelID: m1, Args: []string{"graceful"}},
	)
	start, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	instA := start.Results[0].InstanceID
	instB := start.Results[1].InstanceID
	if instA == instB {
		t.Fatalf("expected distinct instances: %+v", start.Results)
	}

	stop, err := e.svc.Stop(ctx, pipe)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Reverse order in the response: entry 2 first, entry 1 second.
	if len(stop.Results) != 2 {
		t.Fatalf("want 2 stop rows, got %d: %+v", len(stop.Results), stop.Results)
	}
	if !contains(stop.Results[0].StoppedInstanceIDs, instB) || !contains(stop.Results[1].StoppedInstanceIDs, instA) {
		t.Fatalf("per-entry stop attribution wrong: %+v", stop.Results)
	}
	if len(ownedActive(e, t, pipe)) != 0 {
		t.Fatalf("all owned instances must stop: %+v", ownedActive(e, t, pipe))
	}
}

// D10 item 6 (legacy fallback): a pre-ADR 013 instance owned by the pipeline
// without an entry attribution (pipeline_entry_id == "") is stopped via the
// first entry of its model — no owned active instance is left unassigned.
func TestPipelineRepeat_StopLegacyFallback(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "legacy", "graceful")
	me, err := e.repo.GetModel(m1)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	rte, err := e.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	legacy, err := e.sup.AdmitAndStart(ctx, domain.ModelEntryToDomain(me),
		process.RuntimeToDomain(rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory, rte.Environment), domain.ManualOwner, nil, nil)
	if err != nil {
		t.Fatalf("Start legacy: %v", err)
	}
	// Rewrite the instance as a legacy pipeline-owned instance (no entry id).
	inst, err := e.repo.GetLaunchInstance(string(legacy.ID))
	if err != nil {
		t.Fatalf("GetLaunchInstance: %v", err)
	}
	inst.PipelineID = "legacy-pipe"
	inst.PipelineEntryID = ""
	if err := e.repo.UpdateLaunchInstance(inst); err != nil {
		t.Fatalf("UpdateLaunchInstance: %v", err)
	}
	// A pipeline (now with an entry id) referencing the model.
	pipe := e.addPipeline(t, "legacy-pipe", storage.PipelineModel{ModelID: m1})
	// The pipeline id must match the instance's pipeline_id for ownership.
	if pipe != "legacy-pipe" {
		// addPipeline returns the server-generated id; re-attribute the instance.
		inst.PipelineID = pipe
		if err := e.repo.UpdateLaunchInstance(inst); err != nil {
			t.Fatalf("re-attribute: %v", err)
		}
	}

	stop, err := e.svc.Stop(ctx, pipe)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(stop.Results) != 1 {
		t.Fatalf("want 1 stop row: %+v", stop.Results)
	}
	if !contains(stop.Results[0].StoppedInstanceIDs, string(legacy.ID)) {
		t.Fatalf("legacy instance must stop via the first entry fallback: %+v", stop.Results[0])
	}
}

// ─── ADR 013 D8: reorder is structural (409 while active) ───

// D10 item 4 (part): reordering the entry list (different entry-id sequence) is
// a structural change → 409 while the pipeline has active owned instances.
func TestPipelineRepeat_ReorderStructural409(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "ord1", "graceful")
	m2 := e.addModel(t, "m2", "ord2", "graceful")
	pipe := e.addPipeline(t, "order",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	cur, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	// Start so the pipeline owns active instances.
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Reordered sequence (m2 entry first) → structural → 409 while active.
	reordered := &storage.PipelineEntry{ID: pipe, Name: "order", Models: []storage.PipelineModel{
		{ID: cur.Models[1].ID, ModelID: m2},
		{ID: cur.Models[0].ID, ModelID: m1},
	}}
	if err := e.svc.UpdatePipeline(ctx, reordered); err == nil {
		t.Fatal("reorder with active owned instances must be rejected (409)")
	} else {
		var apiErr *apierrors.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != apierrors.CodeConflict {
			t.Fatalf("reorder error = %v, want conflict", err)
		}
	}

	// After stopping, the same reorder is accepted.
	e.stopPipeline(t, pipe)
	if err := e.svc.UpdatePipeline(ctx, reordered); err != nil {
		t.Fatalf("reorder after stop must be accepted: %v", err)
	}
	after, _ := e.repo.GetPipeline(pipe)
	if after.Models[0].ModelID != m2 || after.Models[1].ModelID != m1 {
		t.Fatalf("reorder not persisted: %+v", after.Models)
	}
	// Entry ids travel with the entries (not their position).
	if after.Models[0].ID != cur.Models[1].ID || after.Models[1].ID != cur.Models[0].ID {
		t.Fatalf("entry ids must follow the entries, not the position: %+v", after.Models)
	}
}

// ─── ADR 013 D2/D4: per-entry failed record for repeatable models ───

// D10 item 10: a failed start of a duplicate entry persists a terminal failed
// record carrying that entry's pipeline_entry_id; the record is per-entry.
func TestPipelineRepeat_FailedStartPerEntryRecord(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "fail", "graceful")
	pipe := e.addPipeline(t, "fail",
		storage.PipelineModel{ModelID: m1, Args: []string{"graceful"}},
		storage.PipelineModel{ModelID: m1, Args: []string{"graceful"}},
	)
	// Break the runtime so the start fails (missing executable).
	me, err := e.repo.GetModel(m1)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if err := e.repo.UpdateRuntime(&storage.RuntimeEntry{
		ID: me.RuntimeID, Name: "gone", Executable: "missing-exe-definitely-absent",
	}); err != nil {
		t.Fatalf("UpdateRuntime: %v", err)
	}

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i, r := range res.Results {
		if r.Status != OutcomeFailed {
			t.Fatalf("entry %d = %q, want failed: %+v", i, r.Status, r)
		}
	}
	insts := e.instancesFor(t, m1)
	if len(insts) != 2 {
		t.Fatalf("want 2 terminal failed records (one per entry), got %d: %+v", len(insts), insts)
	}
	seen := map[string]bool{}
	for _, inst := range insts {
		if inst.State != "failed" {
			t.Fatalf("failed record state = %q", inst.State)
		}
		if inst.PipelineID != pipe || inst.PipelineEntryID == "" {
			t.Fatalf("failed record must carry pipeline + entry id: %+v", inst)
		}
		seen[inst.PipelineEntryID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("failed records must be attributed to distinct entries: %+v", insts)
	}
}

// ─── ADR 013 D5: update round-trips entry ids for a simple rename ───

// A non-structural rename that round-trips the (now mandatory) entry ids is
// accepted even while the pipeline owns active instances.
func TestPipelineRepeat_RenameRoundTripsEntryIDs(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "rename", "graceful")
	pipe := e.addPipeline(t, "rename", storage.PipelineModel{ModelID: m1})
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cur, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if err := e.svc.UpdatePipeline(ctx, &storage.PipelineEntry{
		ID: pipe, Name: "renamed",
		Models: []storage.PipelineModel{{ID: cur.Models[0].ID, ModelID: m1}},
	}); err != nil {
		t.Fatalf("rename round-tripping entry id must be accepted (non-structural): %v", err)
	}
	after, _ := e.repo.GetPipeline(pipe)
	if after.Name != "renamed" || after.Models[0].ID != cur.Models[0].ID {
		t.Fatalf("rename must persist with the same entry id: %+v", after)
	}
}

// contains reports whether ids contains v.
func contains(ids []string, v string) bool {
	for _, x := range ids {
		if x == v {
			return true
		}
	}
	return false
}

// TestPipelineRepeat_NoOpSave_StoppedAndActive is the exact Owner acceptance
// scenario: a pipeline with 4 entries all referencing the same ModelID (mix of
// FromModel and Custom args). A no-op save (same IDs, same order, same args)
// must succeed both while stopped and while active (referenced by running
// instances). Entry IDs must remain unchanged.
func TestPipelineRepeat_NoOpSave_StoppedAndActive(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "gemma", "graceful")
	pipe := e.addPipeline(t, "four-dup",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m1, Args: []string{"--port", "8083"}},
		storage.PipelineModel{ModelID: m1, Args: []string{"--port", "8084"}},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	cur, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if len(cur.Models) != 4 {
		t.Fatalf("want 4 entries, got %d", len(cur.Models))
	}
	ids := make([]string, 4)
	for i := range cur.Models {
		ids[i] = cur.Models[i].ID
		if ids[i] == "" {
			t.Fatalf("entry %d has empty ID after create", i)
		}
	}
	for i := 1; i < 4; i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("entry IDs must be distinct: %v", ids)
		}
	}

	// STOPPED: no-op save must succeed.
	if err := e.svc.UpdatePipeline(ctx, &storage.PipelineEntry{
		ID:   pipe,
		Name: "four-dup",
		Models: []storage.PipelineModel{
			{ID: ids[0], ModelID: m1},
			{ID: ids[1], ModelID: m1},
			{ID: ids[2], ModelID: m1, Args: []string{"--port", "8083"}},
			{ID: ids[3], ModelID: m1, Args: []string{"--port", "8084"}},
		},
	}); err != nil {
		t.Fatalf("STOPPED no-op save must succeed: %v", err)
	}
	after, _ := e.repo.GetPipeline(pipe)
	for i := range after.Models {
		if after.Models[i].ID != ids[i] {
			t.Fatalf("STOPPED: entry %d ID changed: was %s, now %s", i, ids[i], after.Models[i].ID)
		}
	}

	// ACTIVE: start the pipeline, then no-op save must still succeed.
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}
	active := ownedActive(e, t, pipe)
	if len(active) < 1 {
		t.Fatalf("want at least 1 active instance for structural protection, got %d", len(active))
	}

	if err := e.svc.UpdatePipeline(ctx, &storage.PipelineEntry{
		ID:   pipe,
		Name: "four-dup",
		Models: []storage.PipelineModel{
			{ID: ids[0], ModelID: m1},
			{ID: ids[1], ModelID: m1},
			{ID: ids[2], ModelID: m1, Args: []string{"--port", "8083"}},
			{ID: ids[3], ModelID: m1, Args: []string{"--port", "8084"}},
		},
	}); err != nil {
		t.Fatalf("ACTIVE no-op save must succeed (non-structural): %v", err)
	}
	after2, _ := e.repo.GetPipeline(pipe)
	for i := range after2.Models {
		if after2.Models[i].ID != ids[i] {
			t.Fatalf("ACTIVE: entry %d ID changed: was %s, now %s", i, ids[i], after2.Models[i].ID)
		}
	}
}

// Owner acceptance scenario (2026-09-11): a pipeline of FOUR entries of the
// SAME model must produce four independent instances - unique instance ids,
// unique PIDs, and one-to-one per-entry attribution. The "four identical IDs
// on screen" observation is a presentation truncation, not an identity
// collision; this test pins the backend identity contract.
func TestPipelineRepeat_FourEntriesIdentityUniqueness(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "four", "graceful")
	pipe := e.addPipeline(t, "four",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m1},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i, r := range res.Results {
		if r.Status != OutcomeStarted {
			t.Fatalf("entry %d must start, got %q (%s)", i, r.Status, r.Error)
		}
	}

	insts := e.instancesFor(t, m1)
	if len(insts) != 4 {
		t.Fatalf("want 4 instances of the model, got %d", len(insts))
	}
	ids := make(map[string]bool, 4)
	pids := make(map[int]bool, 4)
	entries := make(map[string]bool, 4)
	for _, inst := range insts {
		if ids[inst.ID] {
			t.Fatalf("duplicate instance id: %s", inst.ID)
		}
		ids[inst.ID] = true
		if inst.PID == 0 {
			t.Fatalf("instance %s must carry a PID", inst.ID)
		}
		if pids[inst.PID] {
			t.Fatalf("duplicate PID: %d", inst.PID)
		}
		pids[inst.PID] = true
		if inst.PipelineID != pipe {
			t.Fatalf("instance %s not attributed to the pipeline", inst.ID)
		}
		if inst.PipelineEntryID == "" {
			t.Fatalf("instance %s must carry a pipeline_entry_id", inst.ID)
		}
		if entries[inst.PipelineEntryID] {
			t.Fatalf("two instances attributed to entry %s", inst.PipelineEntryID)
		}
		entries[inst.PipelineEntryID] = true
	}
	if len(ids) != 4 || len(pids) != 4 || len(entries) != 4 {
		t.Fatalf("want 4 unique instance ids, 4 unique PIDs, 4 entry attributions; got %d/%d/%d", len(ids), len(pids), len(entries))
	}
}
