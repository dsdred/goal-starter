package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := fakeruntime.Cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, "fake runtime cleanup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// pipelineEnv is the ADR 010 test fixture: real repository + real supervisor
// + real fake-runtime processes.
type pipelineEnv struct {
	repo storage.Repository
	sup  *process.Supervisor
	svc  *PipelineService
}

func newPipelineEnv(t *testing.T) *pipelineEnv {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "goal_repo.json"))
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	sup := process.NewSupervisor(repo)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.ShutdownWithPersistence(ctx)
	})
	return &pipelineEnv{repo: repo, sup: sup, svc: NewPipelineService(sup, repo)}
}

// addModel creates a fake-runtime-backed model ("graceful" keeps it
// stoppable on both platforms) and returns the model ID.
func (e *pipelineEnv) addModel(t *testing.T, id, name string, args ...string) string {
	t.Helper()
	rt := &storage.RuntimeEntry{ID: id + "-rt", Name: id + "-runtime", Executable: fakeruntime.Path(t)}
	if err := e.repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime %s: %v", id, err)
	}
	m := &storage.ModelEntry{ID: id, Name: name, RuntimeID: rt.ID, Args: args}
	if err := e.repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel %s: %v", id, err)
	}
	return id
}

func (e *pipelineEnv) addPipeline(t *testing.T, name string, entries ...storage.PipelineModel) string {
	t.Helper()
	entry := &storage.PipelineEntry{Name: name, Models: entries}
	if err := e.svc.CreatePipeline(context.Background(), entry); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	return entry.ID
}

func (e *pipelineEnv) stopPipeline(t *testing.T, id string) *PipelineStopResult {
	t.Helper()
	res, err := e.svc.Stop(context.Background(), id)
	if err != nil {
		t.Fatalf("Stop pipeline %s: %v", id, err)
	}
	return res
}

func (e *pipelineEnv) instancesFor(t *testing.T, modelID string) []*storage.LaunchInstanceEntry {
	t.Helper()
	insts, err := e.repo.ListByModelID(modelID)
	if err != nil {
		t.Fatalf("ListByModelID %s: %v", modelID, err)
	}
	return insts
}

func ownedActive(e *pipelineEnv, t *testing.T, pipelineID string) []*storage.LaunchInstanceEntry {
	t.Helper()
	out := []*storage.LaunchInstanceEntry{}
	insts, err := e.repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	for _, inst := range insts {
		if inst.PipelineID == pipelineID && isActiveInstanceState(inst.State) {
			out = append(out, inst)
		}
	}
	return out
}

// ADR 010 acceptance 3+4: ordered launch, pipeline_id attribution, snapshot
// args, all-or-nothing override, persisted Model.Args byte-identical.
func TestPipelineStart_OrderedAttributedArgsOverride(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "one", "graceful")
	m2 := e.addModel(t, "m2", "two", "delayed", "5")
	pipe := e.addPipeline(t, "ordered",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2, Args: []string{"echo", "OV1", "OV2"}},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	modelsBefore, err := e.repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2: %+v", len(res.Results), res.Results)
	}
	if res.Results[0].ModelID != m1 || res.Results[0].Status != OutcomeStarted {
		t.Fatalf("result[0] = %+v, want m1 started", res.Results[0])
	}
	if res.Results[1].ModelID != m2 || res.Results[1].Status != OutcomeStarted {
		t.Fatalf("result[1] = %+v, want m2 started", res.Results[1])
	}

	inst1, err := e.repo.GetInstance(res.Results[0].InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst1.PipelineID != pipe {
		t.Fatalf("inst1 pipeline_id = %q, want %q", inst1.PipelineID, pipe)
	}
	if !reflect.DeepEqual(inst1.Args, []string{"graceful"}) {
		t.Fatalf("inst1 args = %v, want model args [graceful]", inst1.Args)
	}

	inst2, err := e.repo.GetInstance(res.Results[1].InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst2.PipelineID != pipe {
		t.Fatalf("inst2 pipeline_id = %q, want %q", inst2.PipelineID, pipe)
	}
	if !reflect.DeepEqual(inst2.Args, []string{"echo", "OV1", "OV2"}) {
		t.Fatalf("inst2 args = %v, want the full override (no merge/append)", inst2.Args)
	}

	modelsAfter, err := e.repo.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	for i, m := range modelsAfter {
		if !reflect.DeepEqual(m.Args, modelsBefore[i].Args) {
			t.Fatalf("model %s args changed by pipeline start: %v -> %v", m.ID, modelsBefore[i].Args, m.Args)
		}
	}
}

// ADR 010 acceptance 5+6: best-effort start; a failed entry leaves a
// terminal failed record with a bounded reason and processing continues.
func TestPipelineStart_BestEffortFailure(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "one", "graceful")
	// m2 references an existing directory as the executable: resolution
	// passes (the path exists) but the spawn fails → standard Supervisor
	// launch-failure semantics (terminal failed record, supervisor.go:795-804).
	badDir := t.TempDir()
	rtBad := &storage.RuntimeEntry{ID: "m2-rt", Name: "bad", Executable: badDir}
	if err := e.repo.CreateRuntime(rtBad); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	m2 := "m2"
	if err := e.repo.CreateModel(&storage.ModelEntry{ID: m2, Name: "bad-model", RuntimeID: "m2-rt"}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	m3 := e.addModel(t, "m3", "three", "graceful")
	m4 := e.addModel(t, "m4", "four", "graceful")
	pipe := e.addPipeline(t, "best-effort",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
		storage.PipelineModel{ModelID: m3},
		storage.PipelineModel{ModelID: m4},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(res.Results) != 4 {
		t.Fatalf("results = %d, want 4: %+v", len(res.Results), res.Results)
	}
	want := []string{OutcomeStarted, OutcomeFailed, OutcomeStarted, OutcomeStarted}
	for i, w := range want {
		if res.Results[i].Status != w {
			t.Fatalf("result[%d] = %+v, want status %q", i, res.Results[i], w)
		}
	}
	if got := res.Results[1].Error; got != ReasonStartFailed {
		t.Fatalf("failed reason = %q, want %q (bounded class, no raw error text)", got, ReasonStartFailed)
	}
	if res.Results[1].InstanceID != "" {
		t.Fatalf("failed entry must not carry an instance_id: %+v", res.Results[1])
	}

	// Entry 1 stays running; entries 3 and 4 were still processed.
	m1Insts := e.instancesFor(t, m1)
	if len(m1Insts) != 1 || !isActiveInstanceState(m1Insts[0].State) {
		t.Fatalf("m1 instance not running: %+v", m1Insts)
	}
	if len(e.instancesFor(t, m3)) != 1 || len(e.instancesFor(t, m4)) != 1 {
		t.Fatal("entries 3 and 4 must still be processed after entry 2 failed")
	}

	// The failed entry leaves a terminal failed instance record.
	failed := e.instancesFor(t, m2)
	if len(failed) != 1 {
		t.Fatalf("failed entry must persist exactly one instance record, got %d: %+v", len(failed), failed)
	}
	if failed[0].State != "failed" {
		t.Fatalf("failed record state = %q, want failed", failed[0].State)
	}
	if failed[0].PipelineID != pipe {
		t.Fatalf("failed record pipeline_id = %q, want %q", failed[0].PipelineID, pipe)
	}
}

// A missing executable fails at resolve — the same standard Supervisor
// semantics as a manual model start: a bounded resolve-failed reason and no
// instance record (the pending record is only persisted after resolve).
func TestPipelineStart_MissingExecutableResolveFailed(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "one", "graceful")
	m2 := e.addModel(t, "m2", "two", "graceful")
	if err := e.repo.UpdateRuntime(&storage.RuntimeEntry{
		ID: "m2-rt", Name: "gone", Executable: filepath.Join(t.TempDir(), "missing-exe"),
	}); err != nil {
		t.Fatalf("UpdateRuntime: %v", err)
	}
	pipe := e.addPipeline(t, "resolve-fail",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Results[0].Status != OutcomeStarted {
		t.Fatalf("m1 = %+v, want started (failure must not block)", res.Results[0])
	}
	if res.Results[1].Status != OutcomeFailed || res.Results[1].Error != ReasonResolveFailed {
		t.Fatalf("m2 = %+v, want failed/resolve-failed", res.Results[1])
	}
	if got := e.instancesFor(t, "m2"); len(got) != 0 {
		t.Fatalf("resolve failure persists no instance record (same as manual start): %+v", got)
	}
}

// ADR 010 acceptance 7: manual active instance → already-running, no new
// instance, no adoption.
func TestPipelineStart_AlreadyRunningManual(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "manual", "graceful")
	me, err := e.repo.GetModel(m1)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	rte, err := e.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	if _, err := e.sup.Start(ctx, domain.ModelEntryToDomain(me),
		process.RuntimeToDomain(rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory, rte.Environment), nil, nil); err != nil {
		t.Fatalf("manual Start: %v", err)
	}

	pipe := e.addPipeline(t, "shared", storage.PipelineModel{ModelID: m1})
	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Results[0].Status != OutcomeAlreadyRunning {
		t.Fatalf("status = %q, want already-running: %+v", res.Results[0].Status, res.Results[0])
	}
	if res.Results[0].InstanceID != "" {
		t.Fatalf("already-running must not carry an instance_id: %+v", res.Results[0])
	}
	insts := e.instancesFor(t, m1)
	if len(insts) != 1 {
		t.Fatalf("no second copy allowed: %d instances: %+v", len(insts), insts)
	}
	if insts[0].PipelineID != "" {
		t.Fatalf("manual instance must keep an empty pipeline_id (no adoption), got %q", insts[0].PipelineID)
	}
}

// ADR 010 acceptance 7: orphan latest instance → orphan-skipped, no launch.
func TestPipelineStart_OrphanSkipped(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "orphaned", "graceful")
	now := time.Now()
	orphan := &storage.LaunchInstanceEntry{
		ID: "orphan-m1", ModelID: m1, State: "orphan", PID: 99999,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := e.repo.CreateLaunchInstance(orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	pipe := e.addPipeline(t, "orphaned-pipe", storage.PipelineModel{ModelID: m1})
	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Results[0].Status != OutcomeOrphanSkipped {
		t.Fatalf("status = %q, want orphan-skipped: %+v", res.Results[0].Status, res.Results[0])
	}
	if len(e.instancesFor(t, m1)) != 1 {
		t.Fatal("orphan-skipped must create no instance record")
	}
}

// Defensive skip outcomes (no-runtime / model-missing) create no records.
func TestPipelineStart_DefensiveMissing(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "no-rt-model", "graceful")
	// A model whose runtime does not exist (bypassing service validation).
	if err := e.repo.CreateModel(&storage.ModelEntry{ID: "m2", Name: "ghost-rt", RuntimeID: "ghost-rt"}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	// A pipeline referencing a missing model (bypassing service validation).
	entry := &storage.PipelineEntry{
		ID: "pipe-def", Name: "defensive",
		Models: []storage.PipelineModel{{ModelID: "m2"}, {ModelID: m1 + "-ghost"}},
	}
	if err := e.repo.CreatePipeline(entry); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	res, err := e.svc.Start(ctx, "pipe-def")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Results[0].Status != OutcomeNoRuntime {
		t.Fatalf("result[0] = %+v, want no-runtime", res.Results[0])
	}
	if res.Results[1].Status != OutcomeModelMissing {
		t.Fatalf("result[1] = %+v, want model-missing", res.Results[1])
	}
	if len(e.instancesFor(t, "m2")) != 0 {
		t.Fatal("no-runtime must create no instance record")
	}
	_ = m1
}

// ADR 010 acceptance 8: stop exactly the active owned instances in reverse
// order; manual/orphan/stale instances are untouched.
func TestPipelineStop_ReverseOwnedOnly(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	m3 := e.addModel(t, "m3", "c", "graceful")
	pipe := e.addPipeline(t, "stop-order",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
		storage.PipelineModel{ModelID: m3},
	)
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A manually started second instance of m2 (no pipeline_id).
	me, err := e.repo.GetModel(m2)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	rte, err := e.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	manual, err := e.sup.Start(ctx, domain.ModelEntryToDomain(me),
		process.RuntimeToDomain(rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory, rte.Environment), nil, nil)
	if err != nil {
		t.Fatalf("manual Start m2: %v", err)
	}

	now := time.Now()
	if err := e.repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: "orphan-m1", ModelID: m1, State: "orphan", PID: 99999, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := e.repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: "stale-m1", ModelID: m1, State: "stale", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	res := e.stopPipeline(t, pipe)
	if len(res.Results) != 3 {
		t.Fatalf("results = %d, want 3: %+v", len(res.Results), res.Results)
	}
	// Reverse pipeline order.
	if res.Results[0].ModelID != m3 || res.Results[1].ModelID != m2 || res.Results[2].ModelID != m1 {
		t.Fatalf("stop order not reverse: %+v", res.Results)
	}
	for _, r := range res.Results {
		if r.Status != OutcomeStopped {
			t.Fatalf("entry %s status = %q, want stopped: %+v", r.ModelID, r.Status, r)
		}
	}

	// Owned instances are stopped; the manual instance is untouched.
	for _, modelID := range []string{m1, m2, m3} {
		for _, inst := range e.instancesFor(t, modelID) {
			if inst.PipelineID == pipe && isActiveInstanceState(inst.State) {
				t.Fatalf("owned instance %s of %s still active after stop", inst.ID, modelID)
			}
		}
	}
	manualEntry, err := e.repo.GetInstance(string(manual.ID))
	if err != nil {
		t.Fatalf("GetInstance manual: %v", err)
	}
	if !isActiveInstanceState(manualEntry.State) {
		t.Fatalf("manual instance %s must survive pipeline stop, state=%q", manualEntry.ID, manualEntry.State)
	}
	if manualEntry.PipelineID != "" {
		t.Fatalf("manual instance must keep an empty pipeline_id, got %q", manualEntry.PipelineID)
	}

	// Orphan and stale are untouched.
	orphan, err := e.repo.GetInstance("orphan-m1")
	if err != nil || orphan.State != "orphan" {
		t.Fatalf("orphan instance touched: %+v err=%v", orphan, err)
	}
	stale, err := e.repo.GetInstance("stale-m1")
	if err != nil || stale.State != "stale" {
		t.Fatalf("stale instance touched: %+v err=%v", stale, err)
	}

	// Clean up the manual instance.
	if err := e.sup.Stop(ctx, manual.ID); err != nil {
		t.Fatalf("cleanup manual stop: %v", err)
	}
}

// ADR 010 acceptance 8: a stop failure on one entry does not block the rest.
func TestPipelineStop_FailureDoesNotBlock(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	pipe := e.addPipeline(t, "stop-fail",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
	)

	// m1: an owned active instance that the supervisor does not know about
	// (e.g. after a restart without recovery in this process) → stop fails.
	now := time.Now()
	if err := e.repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: "fake-owned-m1", ModelID: m1, State: "running", PID: 99999, PipelineID: pipe,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed fake owned: %v", err)
	}

	// m2: a real owned instance (start of m1 yields already-running).
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}

	res := e.stopPipeline(t, pipe)
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2: %+v", len(res.Results), res.Results)
	}
	// Reverse order: m2 first (stopped), then m1 (failed).
	if res.Results[0].ModelID != m2 || res.Results[0].Status != OutcomeStopped {
		t.Fatalf("m2 result = %+v, want stopped", res.Results[0])
	}
	if res.Results[1].ModelID != m1 || res.Results[1].Status != OutcomeFailed {
		t.Fatalf("m1 result = %+v, want failed", res.Results[1])
	}
	if res.Results[1].Error != ReasonStopFailed {
		t.Fatalf("m1 reason = %q, want %q", res.Results[1].Error, ReasonStopFailed)
	}
	if res.Results[1].InstanceID != "fake-owned-m1" {
		t.Fatalf("m1 instance_id = %q, want fake-owned-m1", res.Results[1].InstanceID)
	}
}

// ADR 010 acceptance 9: restart response contract — stop_results in reverse
// order, start_results in forward order for all entries, new instances.
func TestPipelineRestart_Contract(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	pipe := e.addPipeline(t, "restart",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
	)
	first, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	old1, old2 := first.Results[0].InstanceID, first.Results[1].InstanceID

	res, err := e.svc.Restart(ctx, pipe)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if res.PipelineID != pipe {
		t.Fatalf("pipeline_id = %q, want %q", res.PipelineID, pipe)
	}
	if len(res.StopResults) != 2 || res.StopResults[0].ModelID != m2 || res.StopResults[1].ModelID != m1 {
		t.Fatalf("stop_results order wrong: %+v", res.StopResults)
	}
	for _, r := range res.StopResults {
		if r.Status != OutcomeStopped {
			t.Fatalf("stop result %s = %q, want stopped: %+v", r.ModelID, r.Status, r)
		}
	}
	if len(res.StartResults) != 2 || res.StartResults[0].ModelID != m1 || res.StartResults[1].ModelID != m2 {
		t.Fatalf("start_results order wrong: %+v", res.StartResults)
	}
	if res.StartResults[0].Status != OutcomeStarted || res.StartResults[1].Status != OutcomeStarted {
		t.Fatalf("start_results: %+v", res.StartResults)
	}
	if res.StartResults[0].InstanceID == old1 || res.StartResults[1].InstanceID == old2 {
		t.Fatal("restart must launch new instances, not reuse the old ones")
	}

	// Old instances are stopped; exactly one active owned instance per model.
	for _, old := range []string{old1, old2} {
		oe, err := e.repo.GetInstance(old)
		if err != nil {
			t.Fatalf("GetInstance %s: %v", old, err)
		}
		if isActiveInstanceState(oe.State) {
			t.Fatalf("old instance %s still active after restart", old)
		}
	}
	if got := ownedActive(e, t, pipe); len(got) != 2 {
		t.Fatalf("active owned instances after restart = %d, want 2: %+v", len(got), got)
	}

	t.Cleanup(func() { e.stopPipeline(t, pipe) })
}

// ADR 010 acceptance 9: an entry still active after a stop failure yields
// already-running in start_results (no second launch); start_results are
// present for ALL entries.
func TestPipelineRestart_StopFailureStillStartsAll(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	pipe := e.addPipeline(t, "restart-fail",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
	)
	now := time.Now()
	if err := e.repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: "fake-owned-m1", ModelID: m1, State: "running", PID: 99999, PipelineID: pipe,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed fake owned: %v", err)
	}
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}

	res, err := e.svc.Restart(ctx, pipe)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(res.StopResults) != 2 {
		t.Fatalf("stop_results = %+v, want 2", res.StopResults)
	}
	if res.StopResults[1].ModelID != m1 || res.StopResults[1].Status != OutcomeFailed {
		t.Fatalf("m1 stop result = %+v, want failed", res.StopResults[1])
	}
	if len(res.StartResults) != 2 {
		t.Fatalf("start_results must be present for all entries: %+v", res.StartResults)
	}
	// m1 is still active (stop failed) → already-running, no second launch.
	if res.StartResults[0].ModelID != m1 || res.StartResults[0].Status != OutcomeAlreadyRunning {
		t.Fatalf("m1 start result = %+v, want already-running", res.StartResults[0])
	}
	if res.StartResults[1].ModelID != m2 || res.StartResults[1].Status != OutcomeStarted {
		t.Fatalf("m2 start result = %+v, want started", res.StartResults[1])
	}
	// m1 still has exactly one instance record (the fake one, untouched).
	if got := e.instancesFor(t, m1); len(got) != 1 || got[0].ID != "fake-owned-m1" {
		t.Fatalf("no second launch allowed for m1: %+v", got)
	}

	t.Cleanup(func() { e.stopPipeline(t, pipe) })
}

// ADR 010 acceptance 11: non-structural updates succeed with active owned
// instances; structural changes (add/remove/reorder) → 409 conflict.
func TestPipelineUpdate_Integrity(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	pipe := e.addPipeline(t, "integrity", storage.PipelineModel{ModelID: m1})
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	current, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}

	// Name / args / Active / AutoStart changes are allowed.
	current.Name = "renamed"
	current.Active = true
	current.Models[0].Args = []string{"graceful", "-extra"}
	current.Models[0].AutoStart = true
	if err := e.svc.UpdatePipeline(ctx, current); err != nil {
		t.Fatalf("non-structural update must succeed: %v", err)
	}
	got, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if got.Name != "renamed" || !got.Active || !got.Models[0].AutoStart || !reflect.DeepEqual(got.Models[0].Args, []string{"graceful", "-extra"}) {
		t.Fatalf("update not applied: %+v", got)
	}

	// Structural: add a model → conflict.
	adding := &storage.PipelineEntry{
		ID: pipe, Name: got.Name, Active: got.Active,
		Models: []storage.PipelineModel{
			{ModelID: m1, Args: got.Models[0].Args, AutoStart: true},
			{ModelID: m2},
		},
	}
	if err := e.svc.UpdatePipeline(ctx, adding); !isConflict(err) {
		t.Fatalf("add model with active instances: err = %v, want 409 conflict", err)
	}
	// Structural: reorder → conflict.
	reordered := &storage.PipelineEntry{
		ID: pipe, Name: got.Name, Active: got.Active,
		Models: []storage.PipelineModel{{ModelID: m2}, {ModelID: m1}},
	}
	if err := e.svc.UpdatePipeline(ctx, reordered); !isConflict(err) {
		t.Fatalf("reorder with active instances: err = %v, want 409 conflict", err)
	}
	// Structural: remove → conflict.
	removing := &storage.PipelineEntry{
		ID: pipe, Name: got.Name, Active: got.Active,
		Models: []storage.PipelineModel{{ModelID: m2}},
	}
	if err := e.svc.UpdatePipeline(ctx, removing); !isConflict(err) {
		t.Fatalf("remove model with active instances: err = %v, want 409 conflict", err)
	}
	// The live pipeline is unperturbed: still the original single entry.
	after, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if len(after.Models) != 1 || after.Models[0].ModelID != m1 {
		t.Fatalf("pipeline mutated by rejected structural updates: %+v", after)
	}

	// After stopping, structural updates succeed.
	e.stopPipeline(t, pipe)
	if err := e.svc.UpdatePipeline(ctx, adding); err != nil {
		t.Fatalf("structural update after stop must succeed: %v", err)
	}
}

// ADR 010 acceptance 10: delete with active owned instances → 409; after
// stopping, delete succeeds and terminal instances keep the pipeline_id.
func TestPipelineDelete_Integrity(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "deletable", storage.PipelineModel{ModelID: m1})
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := e.svc.DeletePipeline(ctx, pipe); !isConflict(err) {
		t.Fatalf("delete with active owned instances: err = %v, want 409 conflict", err)
	}

	stopRes := e.stopPipeline(t, pipe)
	if stopRes.Results[0].Status != OutcomeStopped {
		t.Fatalf("stop = %+v, want stopped", stopRes.Results)
	}
	if err := e.svc.DeletePipeline(ctx, pipe); err != nil {
		t.Fatalf("delete after stop: %v", err)
	}
	if _, err := e.repo.GetPipeline(pipe); err == nil {
		t.Fatal("pipeline must be gone")
	}
	// Terminal instances retain the historical pipeline_id.
	insts := e.instancesFor(t, m1)
	if len(insts) != 1 {
		t.Fatalf("instances = %+v, want exactly 1", insts)
	}
	if insts[0].PipelineID != pipe {
		t.Fatalf("terminal instance lost its historical pipeline_id: %q", insts[0].PipelineID)
	}

	// Unknown delete → not_found.
	var apiErr *apierrors.APIError
	if err := e.svc.DeletePipeline(ctx, pipe); errors.As(err, &apiErr) {
		if apiErr.Code != apierrors.CodeNotFound {
			t.Fatalf("second delete code = %q, want not_found", apiErr.Code)
		}
	} else {
		t.Fatalf("second delete: err = %v, want not_found APIError", err)
	}
}

// ADR 010 acceptance 10: model delete with a pipeline reference → 409,
// model survives; after the pipeline is gone, delete succeeds.
func TestModelDelete_PipelineReference(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "referenced", "graceful")
	pipe := e.addPipeline(t, "refs-model", storage.PipelineModel{ModelID: m1})

	modelSvc := NewModelService(e.repo).WithPipelineIntegrity(e.svc.ListPipelinesReferencingModel)
	if err := modelSvc.DeleteModel(ctx, m1); !isConflict(err) {
		t.Fatalf("DeleteModel with pipeline reference: err = %v, want 409 conflict", err)
	}
	if _, err := e.repo.GetModel(m1); err != nil {
		t.Fatalf("model must survive a refused delete: %v", err)
	}

	if err := e.svc.DeletePipeline(ctx, pipe); err != nil {
		t.Fatalf("DeletePipeline: %v", err)
	}
	if err := modelSvc.DeleteModel(ctx, m1); err != nil {
		t.Fatalf("DeleteModel after pipeline removal: %v", err)
	}
}

// ADR 010 D4: autostart processes only AutoStart=true entries; manual start
// processes all entries.
func TestPipelineAutostart_OnlyAutoStartEntries(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	pipe := e.addPipeline(t, "autostart",
		storage.PipelineModel{ModelID: m1, AutoStart: true},
		storage.PipelineModel{ModelID: m2},
	)
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	res, err := e.svc.Autostart(ctx, pipe)
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ModelID != m1 || res.Results[0].Status != OutcomeStarted {
		t.Fatalf("autostart results = %+v, want only m1 started", res.Results)
	}
	if len(e.instancesFor(t, m2)) != 0 {
		t.Fatal("AutoStart=false entry must not be launched by autostart")
	}

	// Manual start still processes all entries.
	res, err = e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("manual start must process all entries: %+v", res.Results)
	}
	if res.Results[0].Status != OutcomeAlreadyRunning {
		t.Fatalf("m1 = %q, want already-running (autostarted)", res.Results[0].Status)
	}
	if res.Results[1].Status != OutcomeStarted {
		t.Fatalf("m2 = %q, want started", res.Results[1].Status)
	}
}

// ADR 010 D1.3 + D5: create validation (400 bad_request).
func TestPipelineCreate_Validation(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")

	cases := []struct {
		name  string
		entry *storage.PipelineEntry
	}{
		{"nil entry", nil},
		{"empty name", &storage.PipelineEntry{Name: "  ", Models: []storage.PipelineModel{{ModelID: m1}}}},
		{"empty model list", &storage.PipelineEntry{Name: "p", Models: nil}},
		{"duplicate model", &storage.PipelineEntry{Name: "p", Models: []storage.PipelineModel{{ModelID: m1}, {ModelID: m1}}}},
		{"unknown model", &storage.PipelineEntry{Name: "p", Models: []storage.PipelineModel{{ModelID: "ghost"}}}},
		{"empty model id", &storage.PipelineEntry{Name: "p", Models: []storage.PipelineModel{{ModelID: ""}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := e.svc.CreatePipeline(ctx, tc.entry)
			var apiErr *apierrors.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != apierrors.CodeBadRequest {
				t.Fatalf("err = %v, want bad_request APIError", err)
			}
		})
	}
	if got, _ := e.repo.ListPipelines(); len(got) != 0 {
		t.Fatalf("rejected creates must not persist: %+v", got)
	}
}

// ADR 010 D3: concurrent starts of the same pipeline are serialized; the
// second request sees the first outcome (no double launch).
func TestPipelineStart_ConcurrentNoDoubleLaunch(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "concurrent", storage.PipelineModel{ModelID: m1})
	t.Cleanup(func() { e.stopPipeline(t, pipe) })

	var wg sync.WaitGroup
	results := make([]*PipelineStartResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := e.svc.Start(ctx, pipe)
			if err != nil {
				t.Errorf("concurrent Start: %v", err)
				return
			}
			results[i] = res
		}(i)
	}
	wg.Wait()

	started := 0
	already := 0
	for _, res := range results {
		switch res.Results[0].Status {
		case OutcomeStarted:
			started++
		case OutcomeAlreadyRunning:
			already++
		default:
			t.Fatalf("unexpected status: %+v", res.Results[0])
		}
	}
	if started != 1 || already != 1 {
		t.Fatalf("want exactly one started and one already-running, got %d/%d: %+v", started, already, results)
	}
	if got := e.instancesFor(t, m1); len(got) != 1 {
		t.Fatalf("double launch: %d instances: %+v", len(got), got)
	}
}

func isConflict(err error) bool {
	var apiErr *apierrors.APIError
	return errors.As(err, &apiErr) && apiErr.Code == apierrors.CodeConflict
}
