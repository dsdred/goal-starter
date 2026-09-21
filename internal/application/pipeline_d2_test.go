package application

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

// D2 (BF-02): best-effort EXECUTION and best-effort SUCCESS ATTRIBUTION are
// separate concerns. The application layer reports WHICH instance of WHICH
// entry failed and WHY (typed metadata); turning that into a status code and a
// response body belongs to the handler (BF-03a). These tests pin the
// attribution contract and the boundary between the two layers.

// entryID returns the generated entry id of the given pipeline position.
func entryID(t *testing.T, repo storage.Repository, pipelineID string, index int) string {
	t.Helper()
	p, err := repo.GetPipeline(pipelineID)
	if err != nil {
		t.Fatalf("GetPipeline %s: %v", pipelineID, err)
	}
	if index >= len(p.Models) {
		t.Fatalf("pipeline %s has %d entries, want index %d", pipelineID, len(p.Models), index)
	}
	return p.Models[index].ID
}

func seedOwnedRecord(t *testing.T, repo storage.Repository, id, modelID, pipelineID, entryID string) {
	t.Helper()
	now := time.Now()
	if err := repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: id, ModelID: modelID, State: "running", PID: 99999,
		PipelineID: pipelineID, PipelineEntryID: entryID,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed owned record %s: %v", id, err)
	}
}

// failuresOf returns the aggregate rows of one phase, in aggregate order.
func failuresOf(agg *PipelineStopError, phase string) []PipelineFailure {
	out := []PipelineFailure{}
	for _, f := range agg.Failures {
		if f.Phase == phase {
			out = append(out, f)
		}
	}
	return out
}

// T-P2: one entry whose real instance stops and whose stale record cannot stop
// keeps BOTH outcomes: the success in the stopped lists, the failure in the
// aggregate with its own reason. Before BF-02 the failure overwrote the entry
// (and InstanceID claimed an instance that never stopped).
func TestPipelineStop_PartialFailureKeepsEveryResult(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "stop-partial", storage.PipelineModel{ModelID: m1})
	eid := entryID(t, e.repo, pipe, 0)

	started, err := e.svc.Start(ctx, pipe)
	if err != nil || len(started.Results) != 1 || started.Results[0].Status != OutcomeStarted {
		t.Fatalf("Start = %+v err=%v, want one started entry", started, err)
	}
	realID := started.Results[0].InstanceID
	seedOwnedRecord(t, e.repo, "stale-owned", m1, pipe, eid)

	res, err := e.svc.Stop(ctx, pipe)
	var agg *PipelineStopError
	if !errors.As(err, &agg) {
		t.Fatalf("Stop error = %v, want *PipelineStopError", err)
	}
	if res == nil || len(res.Results) != 1 {
		t.Fatalf("Stop body = %+v", res)
	}
	got := res.Results[0]
	if got.Status != OutcomeFailed {
		t.Fatalf("entry status = %q, want %q", got.Status, OutcomeFailed)
	}
	if len(got.StoppedInstanceIDs) != 1 || got.StoppedInstanceIDs[0] != realID {
		t.Fatalf("stopped_instance_ids = %v, want [%s]", got.StoppedInstanceIDs, realID)
	}
	if got.InstanceID != realID {
		t.Fatalf("instance_id = %q, want the last STOPPED id %q", got.InstanceID, realID)
	}
	if got.Error != string(ReasonInstanceGone) {
		t.Fatalf("entry error = %q, want %q", got.Error, ReasonInstanceGone)
	}
	rows := failuresOf(agg, PhaseStop)
	if len(rows) != 1 {
		t.Fatalf("aggregate failures = %+v, want one stop row", agg.Failures)
	}
	f := rows[0]
	if f.EntryID != eid || f.Index != 0 || f.ModelID != m1 || f.InstanceID != "stale-owned" ||
		f.Reason != ReasonInstanceGone {
		t.Fatalf("aggregate failure = %+v, want stale-owned/%s on entry %s", f, ReasonInstanceGone, eid)
	}
}

// T-P2b: every individual failure survives — two unstopable records of the
// same entry produce two attributed rows in execution order, not one.
func TestPipelineStop_MultipleFailuresAllAttributed(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "stop-two", storage.PipelineModel{ModelID: m1})
	eid := entryID(t, e.repo, pipe, 0)
	seedOwnedRecord(t, e.repo, "ghost-1", m1, pipe, eid)
	seedOwnedRecord(t, e.repo, "ghost-2", m1, pipe, eid)

	res, err := e.svc.Stop(ctx, pipe)
	var agg *PipelineStopError
	if !errors.As(err, &agg) {
		t.Fatalf("Stop error = %v, want *PipelineStopError", err)
	}
	if res == nil {
		t.Fatal("Stop must return the per-entry body alongside the aggregate")
	}
	got := res.Results[0]
	if len(got.StoppedInstanceIDs) != 0 || got.InstanceID != "" {
		t.Fatalf("entry must claim nothing stopped: %+v", got)
	}
	rows := failuresOf(agg, PhaseStop)
	if len(rows) != 2 || rows[0].InstanceID != "ghost-1" || rows[1].InstanceID != "ghost-2" {
		t.Fatalf("aggregate failures = %+v, want ghost-1 then ghost-2", rows)
	}
	for _, f := range rows {
		if f.EntryID != eid || f.Reason != ReasonInstanceGone {
			t.Fatalf("failure attribution = %+v", f)
		}
	}
	// Both underlying causes stay reachable by identity.
	if !errors.Is(err, process.ErrInstanceNotFound) {
		t.Fatalf("aggregate must unwrap process.ErrInstanceNotFound: %v", err)
	}
}

// T-P3: a fully successful stop is not an error at all — the caller gets the
// plain per-entry body and nothing to attribute (ADR 010 acceptance 8).
func TestPipelineStop_CleanStopHasNoAggregate(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "stop-clean", storage.PipelineModel{ModelID: m1})
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}

	res, err := e.svc.Stop(ctx, pipe)
	if err != nil {
		t.Fatalf("clean Stop error = %v, want nil", err)
	}
	if res == nil || len(res.Results) != 1 || res.Results[0].Status != OutcomeStopped {
		t.Fatalf("clean Stop body = %+v", res)
	}
}

// T-P4: restart reports the stop-phase failures and STILL runs the start phase
// for every entry (ADR 010 acceptance 9 is unchanged by BF-02).
func TestPipelineRestart_StopFailureIsReportedAndStartStillRuns(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	m2 := e.addModel(t, "m2", "b", "graceful")
	pipe := e.addPipeline(t, "restart-partial",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m2},
	)
	eid1 := entryID(t, e.repo, pipe, 0)
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// An unrecoverable extra record of entry 1: its stop always fails.
	seedOwnedRecord(t, e.repo, "ghost-m1", m1, pipe, eid1)

	res, err := e.svc.Restart(ctx, pipe)
	var agg *PipelineStopError
	if !errors.As(err, &agg) {
		t.Fatalf("Restart error = %v, want *PipelineStopError", err)
	}
	if res == nil {
		t.Fatal("Restart must return the full body alongside the aggregate")
	}
	if agg.Phase != PhaseRestart || agg.PipelineID != pipe {
		t.Fatalf("aggregate = phase %q pipeline %q", agg.Phase, agg.PipelineID)
	}
	if len(res.StopResults) != 2 || len(res.StartResults) != 2 {
		t.Fatalf("phases = stop %+v start %+v, want 2 and 2", res.StopResults, res.StartResults)
	}
	rows := failuresOf(agg, PhaseStop)
	if len(rows) != 1 || rows[0].InstanceID != "ghost-m1" || rows[0].Reason != ReasonInstanceGone ||
		rows[0].EntryID != eid1 || rows[0].Index != 0 {
		t.Fatalf("stop-phase failures = %+v, want ghost-m1/instance-gone on entry 0", rows)
	}
	for _, sr := range res.StartResults {
		if sr.Status != OutcomeStarted {
			t.Fatalf("start phase must run for every entry: %+v", res.StartResults)
		}
	}
}

// T-P5 (BF-02e): a start refused by system shutdown is a failed entry of the
// shutting-down class AND a non-successful request — never "already-running".
func TestPipelineStart_ShutdownRejectionIsNotAlreadyRunning(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "start-shutdown", storage.PipelineModel{ModelID: m1})

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := e.sup.ShutdownWithPersistence(shutdownCtx); err != nil {
		t.Fatalf("ShutdownWithPersistence: %v", err)
	}

	res, err := e.svc.Start(ctx, pipe)
	if res == nil || len(res.Results) != 1 {
		t.Fatalf("Start body = %+v", res)
	}
	if got := res.Results[0]; got.Status != OutcomeFailed || got.Error != string(ReasonShuttingDown) {
		t.Fatalf("entry = %+v, want failed/%s", got, ReasonShuttingDown)
	}
	var agg *PipelineStartError
	if !errors.As(err, &agg) {
		t.Fatalf("Start error = %v, want *PipelineStartError", err)
	}
	if agg.Phase != PhaseStart || agg.PipelineID != pipe {
		t.Fatalf("aggregate = phase %q pipeline %q", agg.Phase, agg.PipelineID)
	}
	if len(agg.Failures) != 1 || agg.Failures[0].Reason != ReasonShuttingDown ||
		agg.Failures[0].Phase != PhaseStart {
		t.Fatalf("aggregate failures = %+v", agg.Failures)
	}
	// The handler classifies by identity through Unwrap, never by text.
	if !process.HasRejectionReason(err, process.RejShuttingDown) {
		t.Fatal("aggregate must expose the shutdown rejection by identity")
	}
}

// T-P5b: the other best-effort start outcomes stay reportable business
// results: per-entry statuses and NO request-level error (ADR 010
// acceptance 5 — that is what keeps a normal group start a 200).
func TestPipelineStart_OrdinaryBestEffortOutcomesProduceNoAggregate(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	if err := e.repo.CreateModel(&storage.ModelEntry{
		ID: "orphan-model", Name: "no runtime", RuntimeID: "no-such-runtime",
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	pipe := e.addPipeline(t, "start-no-runtime", storage.PipelineModel{ModelID: "orphan-model"})

	res, err := e.svc.Start(ctx, pipe)
	if err != nil {
		t.Fatalf("ordinary best-effort start outcome must not become an error: %v", err)
	}
	if res == nil || len(res.Results) != 1 || res.Results[0].Status != OutcomeNoRuntime {
		t.Fatalf("Start body = %+v, want one no-runtime entry", res)
	}
}

// T-P6: the group failure contract lives in the handler, not in the
// application layer. The aggregates carry failure metadata ONLY: no response
// payload, no status, no API error, no callback — and the result types have no
// fields a service could stamp an error into.
func TestGroupFailureAggregatesAreMetadataOnly(t *testing.T) {
	exported := func(v any) []string {
		st := reflect.TypeOf(v).Elem()
		out := []string{}
		for i := 0; i < st.NumField(); i++ {
			if f := st.Field(i); f.PkgPath == "" {
				out = append(out, f.Name)
			}
		}
		return out
	}
	for _, tc := range []struct {
		name string
		v    any
		want []string
	}{
		{"stop", &PipelineStopError{}, []string{"PipelineID", "Phase", "Failures"}},
		{"start", &PipelineStartError{}, []string{"PipelineID", "Phase", "Failures"}},
		{"failure", &PipelineFailure{},
			[]string{"Phase", "EntryID", "Index", "ModelID", "InstanceID", "Reason"}},
		{"stop result", &PipelineStopResult{}, []string{"PipelineID", "Results"}},
		{"start result", &PipelineStartResult{}, []string{"PipelineID", "Results"}},
		{"restart result", &PipelineRestartResult{},
			[]string{"PipelineID", "StopResults", "StartResults"}},
	} {
		if got := exported(tc.v); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s exported fields = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Nothing in an aggregate can point at a response or a handler: every
	// exported field is a string, an int, or a slice of value metadata.
	st := reflect.TypeOf(PipelineStopError{})
	for i := 0; i < st.NumField(); i++ {
		typ := st.Field(i).Type
		if typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Func ||
			typ.Kind() == reflect.Interface {
			t.Fatalf("aggregate field %s has transport-capable type %s", st.Field(i).Name, typ)
		}
	}
}

// T-P6b: building the aggregate never mutates the result. The per-entry body
// is the authoritative outcome returned alongside the error, so a 200 body and
// an incomplete body are the same shape (BF-02a must not stamp it).
func TestStopAggregateDoesNotMutateResult(t *testing.T) {
	res := &PipelineStopResult{
		PipelineID: "p-1",
		Results: []PipelineEntryStop{
			{ModelID: "m1", EntryID: "e-1", Index: 0, Status: OutcomeFailed, InstanceID: "i-ok"},
		},
	}
	before, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	col := &lifecycleCollector{}
	col.add(PipelineFailure{Phase: PhaseStop, Index: 0, InstanceID: "i-1"}, process.ErrInstanceNotFound)
	col.add(PipelineFailure{Phase: PhaseStop, Index: 0, InstanceID: "i-2"}, process.ErrLaunchInFlight)
	agg := col.stopAggregate(PhaseStop, "p-1")

	after, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("aggregate mutated the body:\n before=%s\n after=%s", before, after)
	}
	var stopErr *PipelineStopError
	if !errors.As(agg, &stopErr) {
		t.Fatalf("aggregate = %v, want *PipelineStopError", agg)
	}
	if len(stopErr.Failures) != 2 {
		t.Fatalf("failures = %+v, want both causes attributed", stopErr.Failures)
	}
	if !errors.Is(agg, process.ErrInstanceNotFound) || !errors.Is(agg, process.ErrLaunchInFlight) {
		t.Fatalf("aggregate must unwrap both causes: %v", agg)
	}
	if agg.Error() != "pipeline stop incomplete: 2 failure(s) for pipeline p-1" {
		t.Fatalf("message = %q", agg.Error())
	}
	if got := (&lifecycleCollector{}).stopAggregate(PhaseStop, "p-1"); got != nil {
		t.Fatalf("empty aggregate = %v, want nil", got)
	}
	if got := (&lifecycleCollector{}).startAggregate("p-1"); got != nil {
		t.Fatalf("empty start aggregate = %v, want nil", got)
	}
}

// T-P7: the frozen reason vocabulary is selected by sentinel identity only.
func TestClassifyStopFailureUsesSentinelsNotText(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want PipelineFailureReason
	}{
		{"gone", process.ErrInstanceNotFound, ReasonInstanceGone},
		{"in-flight", process.ErrLaunchInFlight, ReasonLaunchInFlight},
		{"persist", process.ErrPersistenceFailure, ReasonPersistenceFailed},
		{"unconfirmed", process.ErrTerminationUnconfirmed, ReasonTerminationUnconfirmed},
		{"abort", process.ErrLaunchAbortedByShutdown, ReasonShuttingDown},
		{"other", errors.New("manager refused"), ReasonStopFailed},
		// Deliberately look like a class the identity does not carry: a
		// message-based classifier would get these wrong.
		{"text-only-not-found", errors.New("instance not found: lookalike"), ReasonStopFailed},
		{"joined-nested", errors.Join(errors.New("wrap"), process.ErrInstanceNotFound), ReasonInstanceGone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyStopFailure(tc.err); got != tc.want {
				t.Fatalf("classifyStopFailure(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// T-P7b: a rejection nested inside a joined error is still recognized, and an
// unrelated rejection never makes the class look like shutdown.
func TestIsShutdownClassReachesNestedRejections(t *testing.T) {
	shutdownRej := &process.AdmissionRejection{Reason: process.RejShuttingDown}
	inFlightRej := &process.AdmissionRejection{Reason: process.RejInFlight, ModelID: "m1"}

	if !isShutdownClass(errors.Join(errors.New("other"), shutdownRej)) {
		t.Fatal("nested shutdown rejection must be shutdown class")
	}
	if isShutdownClass(errors.Join(inFlightRej, errors.New("noise"))) {
		t.Fatal("an in-flight rejection alone must not be shutdown class")
	}
	if isShutdownClass(inFlightRej) {
		t.Fatal("RejInFlight must not be shutdown class")
	}
	if isShutdownClass(nil) {
		t.Fatal("nil must not be shutdown class")
	}
}

// T-P2c: a pipeline may repeat one Model across entries (ADR 013 D1), so a
// failure must be attributed by ENTRY/INDEX, never by model. Entry 2 carries
// the unstopable record: entry 1 keeps its own stopped outcome untouched.
func TestPipelineStop_DuplicateModelAttributesByEntry(t *testing.T) {
	e := newPipelineEnv(t)
	ctx := context.Background()

	m1 := e.addModel(t, "m1", "a", "graceful")
	pipe := e.addPipeline(t, "stop-dup",
		storage.PipelineModel{ModelID: m1},
		storage.PipelineModel{ModelID: m1},
	)
	eid1 := entryID(t, e.repo, pipe, 0)
	eid2 := entryID(t, e.repo, pipe, 1)
	if eid1 == eid2 {
		t.Fatal("duplicate entries must have distinct ids")
	}

	started, err := e.svc.Start(ctx, pipe)
	if err != nil || len(started.Results) != 2 {
		t.Fatalf("Start = %+v err=%v, want two started entries", started, err)
	}
	// The stale record belongs to the SECOND entry only.
	seedOwnedRecord(t, e.repo, "ghost-of-e2", m1, pipe, eid2)

	res, err := e.svc.Stop(ctx, pipe)
	var agg *PipelineStopError
	if !errors.As(err, &agg) {
		t.Fatalf("Stop error = %v, want *PipelineStopError", err)
	}
	rows := failuresOf(agg, PhaseStop)
	if len(rows) != 1 {
		t.Fatalf("aggregate failures = %+v, want exactly one", rows)
	}
	if f := rows[0]; f.EntryID != eid2 || f.Index != 1 || f.ModelID != m1 ||
		f.InstanceID != "ghost-of-e2" || f.Reason != ReasonInstanceGone {
		t.Fatalf("failure attribution = %+v, want entry %s at index 1", f, eid2)
	}
	// Reverse order: e2 (index 1) first, then e1 (index 0).
	if len(res.Results) != 2 || res.Results[0].EntryID != eid2 || res.Results[1].EntryID != eid1 {
		t.Fatalf("entry rows = %+v", res.Results)
	}
	if res.Results[0].Status != OutcomeFailed || res.Results[1].Status != OutcomeStopped {
		t.Fatalf("only the failing entry may be marked failed: %+v", res.Results)
	}
	if len(res.Results[1].StoppedInstanceIDs) != 1 || res.Results[1].InstanceID == "" {
		t.Fatalf("entry 1 lost its own successful stop: %+v", res.Results[1])
	}
	for _, id := range res.Results[0].StoppedInstanceIDs {
		if id == "ghost-of-e2" {
			t.Fatalf("the failed instance was claimed stopped by entry 2: %+v", res.Results[0])
		}
	}
}
