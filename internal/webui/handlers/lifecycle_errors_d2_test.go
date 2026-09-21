package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

// D2 (BF-03a): the lifecycle endpoints classify process errors by sentinel
// identity into the flat error/code contract. These tests pin the whole
// matrix, the "no raw internal text on a classified class" rule, the group
// stop payload attribution, and the boundaries D2 must not move (RB-004 410,
// the ADR 016 500-class sentinels).

func writeLifecycle(t *testing.T, err error) (int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	writeLifecycleError(rec, err)
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatalf("response is not the flat error contract: %s (%v)", rec.Body.String(), e)
	}
	return rec.Code, body.Code, body.Error
}

func TestLifecycleErrorMappingMatrix(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantToken  string
	}{
		{
			name:       "aborted after admission (RB-015b)",
			err:        process.ErrLaunchAbortedByShutdown,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "service_unavailable",
			wantToken:  "launch_aborted",
		},
		{
			name:       "rejected before admission",
			err:        &process.AdmissionRejection{Reason: process.RejShuttingDown},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "service_unavailable",
			wantToken:  "shutting_down",
		},
		{
			name:       "in-flight rejection",
			err:        &process.AdmissionRejection{Reason: process.RejInFlight, ModelID: "m1"},
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
			wantToken:  "launch_in_flight",
		},
		{
			name:       "orphan rejection",
			err:        &process.AdmissionRejection{Reason: process.RejOrphan, ModelID: "m1"},
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
			wantToken:  "orphan",
		},
		{
			name:       "launch in flight",
			err:        process.ErrLaunchInFlight,
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
			wantToken:  "launch_in_flight",
		},
		{
			name:       "not restartable",
			err:        process.ErrNotRestartable,
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
			wantToken:  "not_restartable",
		},
		{
			name:       "instance gone",
			err:        fmt.Errorf("supervisor stop: %w: i-1", process.ErrInstanceNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
			wantToken:  "instance_not_found",
		},
		{
			// Classification is by sentinel identity only. Look-alike text
			// never produces a 404; it keeps the endpoint's plain 500.
			name:       "text-only not-found look-alike stays unclassified",
			err:        errors.New("supervisor stop: instance not found: i-1"),
			wantStatus: http.StatusInternalServerError,
			wantToken:  "supervisor stop: instance not found: i-1",
		},
		{
			name:       "persist failed stays 500",
			err:        process.ErrPersistenceFailure,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_server_error",
			wantToken:  "launch_persist_failed",
		},
		{
			name:       "termination unconfirmed stays 500",
			err:        fmt.Errorf("stop instance i-1: %w", process.ErrTerminationUnconfirmed),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_server_error",
			wantToken:  "termination_unconfirmed",
		},
		{
			name:       "rollback failed stays 500",
			err:        fmt.Errorf("stop instance i-1: %w", process.ErrRollbackFailed),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_server_error",
			wantToken:  "rollback_failed",
		},
		{
			name: "rejection nested in a joined restart error",
			err: errors.Join(errors.New("restart instance i-1"),
				&process.AdmissionRejection{Reason: process.RejShuttingDown}),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "service_unavailable",
			wantToken:  "shutting_down",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, code, msg := writeLifecycle(t, tc.err)
			if status != tc.wantStatus || code != tc.wantCode || msg != tc.wantToken {
				t.Fatalf("got %d code=%q error=%q, want %d code=%q error=%q",
					status, code, msg, tc.wantStatus, tc.wantCode, tc.wantToken)
			}
			if code != "" && strings.Contains(msg, "instance i-1") {
				t.Fatalf("raw internal text leaked into a classified response: %q", msg)
			}
		})
	}
}

// TestLifecycleErrorOrdering pins the precedence rules that keep ADR 016 and
// RB-015b intact inside a joined multi-cause error: the ADR 016 server class
// outranks the caller-visible classes, shutdown outranks conflict, and within
// the 500 class persistence failure outranks the termination causes.
func TestLifecycleErrorOrdering(t *testing.T) {
	notFoundAndPersist := errors.Join(process.ErrInstanceNotFound, process.ErrPersistenceFailure)
	if status, _, _ := writeLifecycle(t, notFoundAndPersist); status != http.StatusInternalServerError {
		t.Fatalf("joined persistence status = %d, want 500 (the platform failure wins)", status)
	}
	inFlightAndShutdown := errors.Join(process.ErrLaunchInFlight, process.ErrLaunchAbortedByShutdown)
	if status, _, _ := writeLifecycle(t, inFlightAndShutdown); status != http.StatusServiceUnavailable {
		t.Fatalf("joined shutdown status = %d, want 503 (retry-later wins over conflict)", status)
	}
	// Within the 500 class the order is fixed too: the platform failing to
	// persist the launch is the cause the operator acts on first.
	persistAndTermination := errors.Join(process.ErrPersistenceFailure, process.ErrTerminationUnconfirmed)
	if _, _, msg := writeLifecycle(t, persistAndTermination); msg != "launch_persist_failed" {
		t.Fatalf("joined 500-class token = %q, want launch_persist_failed", msg)
	}
}

// An unrecognized error keeps the endpoint's previous behavior: a plain 500
// carrying the error text. D2 does not silently reclassify unknown failures.
func TestLifecycleErrorUnrecognizedKeepsPlain500(t *testing.T) {
	status, code, msg := writeLifecycle(t, errors.New("model not found: m1"))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if code != "" {
		t.Fatalf("classified code %q on an unclassified error", code)
	}
	if msg != "model not found: m1" {
		t.Fatalf("message = %q", msg)
	}
}

// lifecycleAuditToken reports exactly what the response carries, so the audit
// record and the client see the same bounded class (ADR 007).
func TestLifecycleAuditTokenMatchesResponse(t *testing.T) {
	for _, err := range []error{
		process.ErrLaunchInFlight,
		&process.AdmissionRejection{Reason: process.RejOrphan},
		errors.Join(process.ErrPersistenceFailure, process.ErrTerminationUnconfirmed),
	} {
		_, _, msg := writeLifecycle(t, err)
		if tok := lifecycleAuditToken(err); tok != msg {
			t.Fatalf("audit token %q != response token %q", tok, msg)
		}
	}
	if tok := lifecycleAuditToken(errors.New("something odd")); tok != "something odd" {
		t.Fatalf("unclassified audit token = %q", tok)
	}
}

// T-H5: a group stop that stopped SOME instances is no longer a bare 200. The
// response keeps the full per-entry payload (what stopped, what did not) plus
// the flat error/code keys.
func TestPipelineStopAPI_PartialStopReturnsPayloadWithConflict(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "d2-model")

	pipeID, entryID := createD2Pipeline(t, e, addr, sess, csrf, "d2-partial", "d2-model")

	startRec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/start", addr, sess, csrf, "")
	if startRec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", startRec.Code, startRec.Body.String())
	}
	// An owned record the supervisor has no controller for: its stop fails.
	now := time.Now()
	if err := e.repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: "d2-ghost", ModelID: "d2-model", State: "running", PID: 99999,
		PipelineID: pipeID, PipelineEntryID: entryID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}

	rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("partial stop status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Error   string `json:"error"`
		Results []struct {
			EntryID            string   `json:"entry_id"`
			Status             string   `json:"status"`
			Error              string   `json:"error"`
			InstanceID         string   `json:"instance_id"`
			StoppedInstanceIDs []string `json:"stopped_instance_ids"`
			Failures           []struct {
				InstanceID string `json:"instance_id"`
				Reason     string `json:"reason"`
			} `json:"failures"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if body.Code != "conflict" || body.Error != "pipeline_stop_incomplete" {
		t.Fatalf("flat keys = code %q error %q", body.Code, body.Error)
	}
	if len(body.Results) != 1 {
		t.Fatalf("per-entry results must survive the non-200: %s", rec.Body.String())
	}
	entry := body.Results[0]
	if entry.EntryID != entryID || entry.Status != "failed" || entry.Error != "instance-gone" {
		t.Fatalf("entry row = %+v", entry)
	}
	if len(entry.StoppedInstanceIDs) != 1 || entry.InstanceID != entry.StoppedInstanceIDs[0] {
		t.Fatalf("the instance that DID stop must be reported: %+v", entry)
	}
	if len(entry.Failures) != 1 || entry.Failures[0].InstanceID != "d2-ghost" ||
		entry.Failures[0].Reason != "instance-gone" {
		t.Fatalf("failure rows = %+v", entry.Failures)
	}

	// ADR 010 D6: one pipeline.stop audit event per request, carrying the
	// bounded class alongside the counters.
	events := e.eventsByName(t, "pipeline.stop")
	if len(events) != 1 {
		t.Fatalf("pipeline.stop events = %d, want 1", len(events))
	}
	if events[0].Detail["error"] != "pipeline_stop_incomplete" {
		t.Fatalf("audit detail = %+v", events[0].Detail)
	}
}

// T-H5b: a fully successful group stop stays 200 with no error keys, and the
// unknown-pipeline case keeps its 404 shape.
func TestPipelineStopAPI_CleanStopAndUnknownPipeline(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "d2-clean")

	pipeID, _ := createD2Pipeline(t, e, addr, sess, csrf, "d2-clean", "d2-clean")
	if rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/start", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}

	rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clean stop = %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["code"]; ok {
		t.Fatalf("200 body must not carry error keys: %s", rec.Body.String())
	}
	if _, ok := body["error"]; ok {
		t.Fatalf("200 body must not carry error keys: %s", rec.Body.String())
	}

	missing := e.do(t, http.MethodPost, "/api/v1/pipelines/no-such-pipeline/stop", addr, sess, csrf, "")
	if missing.Code != http.StatusNotFound || flatErrorCode(t, missing) != "not_found" {
		t.Fatalf("unknown pipeline = %d %s", missing.Code, missing.Body.String())
	}
}

// T-H6: the boundaries D2 must not move. RB-004 stays a deterministic 410
// pointing at the canonical endpoint, and an instance the supervisor does not
// know becomes a bounded 404 instead of a 500 carrying raw internal text.
func TestLifecycleBoundariesRB004AndNotFound(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	rec := e.do(t, http.MethodPost, "/api/v1/runtimes/rt-1/action/start", addr, sess, csrf, "{}")
	if rec.Code != http.StatusGone || flatErrorCode(t, rec) != "gone" {
		t.Fatalf("RB-004 runtime start = %d %s, want 410/gone", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/api/v1/models/{id}/start") {
		t.Fatalf("RB-004 must point at the canonical endpoint: %s", rec.Body.String())
	}

	stopRec := e.do(t, http.MethodPost, "/api/v1/instances/ghost-instance/stop", addr, sess, csrf, "")
	if stopRec.Code != http.StatusNotFound || flatErrorCode(t, stopRec) != "not_found" {
		t.Fatalf("stop unknown instance = %d %s, want 404/not_found", stopRec.Code, stopRec.Body.String())
	}
	if !strings.Contains(stopRec.Body.String(), "instance_not_found") {
		t.Fatalf("stop token = %s", stopRec.Body.String())
	}
	if strings.Contains(stopRec.Body.String(), "not found:") {
		t.Fatalf("raw internal text leaked: %s", stopRec.Body.String())
	}

	restartRec := e.do(t, http.MethodPost, "/api/v1/instances/ghost-instance/restart", addr, sess, csrf, "")
	if restartRec.Code != http.StatusNotFound || flatErrorCode(t, restartRec) != "not_found" {
		t.Fatalf("restart unknown instance = %d %s", restartRec.Code, restartRec.Body.String())
	}
}

// createD2Pipeline creates one single-entry pipeline through the API and
// returns its id and the server-generated entry id.
func createD2Pipeline(t *testing.T, e *auditEnv, addr, sess, csrf, name, modelID string) (pipelineID, entryID string) {
	t.Helper()
	status, body := createPipelineAPI(t, e, addr, sess, csrf,
		`{"name":"`+name+`","models":[{"model_id":"`+modelID+`"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", status, body)
	}
	var created struct {
		ID     string `json:"id"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create: %v; body=%s", err, body)
	}
	if created.ID == "" || len(created.Models) != 1 || created.Models[0].ID == "" {
		t.Fatalf("create response = %s", body)
	}
	return created.ID, created.Models[0].ID
}

func flatErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v; raw=%s", err, rec.Body.String())
	}
	return body.Code
}

// T-H5c: a restart whose stop phase could not finish reports BOTH phases: the
// stop rows carry the failure attribution, the start rows stay the
// authoritative outcome of the phase that did run, and the flat keys describe
// the request-level class.
func TestPipelineRestartAPI_PartialStopReportsBothPhases(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "d2-restart")

	pipeID, entryID := createD2Pipeline(t, e, addr, sess, csrf, "d2-restart", "d2-restart")
	if rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/start", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	now := time.Now()
	if err := e.repo.CreateLaunchInstance(&storage.LaunchInstanceEntry{
		ID: "d2-restart-ghost", ModelID: "d2-restart", State: "running", PID: 99999,
		PipelineID: pipeID, PipelineEntryID: entryID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}

	rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/restart", addr, sess, csrf, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("partial restart = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		PipelineID  string `json:"pipeline_id"`
		Code        string `json:"code"`
		Error       string `json:"error"`
		StopResults []struct {
			EntryID            string   `json:"entry_id"`
			Status             string   `json:"status"`
			StoppedInstanceIDs []string `json:"stopped_instance_ids"`
			Failures           []struct {
				InstanceID string `json:"instance_id"`
				Reason     string `json:"reason"`
			} `json:"failures"`
		} `json:"stop_results"`
		StartResults []struct {
			EntryID string `json:"entry_id"`
			Status  string `json:"status"`
		} `json:"start_results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; raw=%s", err, rec.Body.String())
	}
	if body.PipelineID != pipeID || body.Code != "conflict" ||
		body.Error != "pipeline_restart_incomplete" {
		t.Fatalf("flat keys = %+v; raw=%s", body, rec.Body.String())
	}
	if len(body.StopResults) != 1 || len(body.StartResults) != 1 {
		t.Fatalf("both phases must be reported: %s", rec.Body.String())
	}
	stop := body.StopResults[0]
	if stop.EntryID != entryID || stop.Status != "failed" {
		t.Fatalf("stop row = %+v", stop)
	}
	if len(stop.Failures) != 1 || stop.Failures[0].InstanceID != "d2-restart-ghost" ||
		stop.Failures[0].Reason != "instance-gone" {
		t.Fatalf("stop failures = %+v", stop.Failures)
	}
	for _, id := range stop.StoppedInstanceIDs {
		if id == "d2-restart-ghost" {
			t.Fatalf("the failed instance was claimed as stopped: %+v", stop)
		}
	}
	if started := body.StartResults[0]; started.EntryID != entryID || started.Status != "started" {
		t.Fatalf("start row = %+v, want the start phase to run despite the stop failure", started)
	}
	events := e.eventsByName(t, "pipeline.restart")
	if len(events) != 1 || events[0].Detail["error"] != "pipeline_restart_incomplete" {
		t.Fatalf("pipeline.restart audit = %+v", events)
	}
}

// The handler owns the rendering of the failure metadata: rows attach to their
// own entry, a start-phase failure never invents a stop row, and no entry row
// is reordered or dropped.
func TestStopFailureRowsKeepsEveryEntryRow(t *testing.T) {
	entries := []application.PipelineEntryStop{
		{ModelID: "m2", EntryID: "e-2", Index: 1, Status: application.OutcomeStopped},
		{ModelID: "m1", EntryID: "e-1", Index: 0, Status: application.OutcomeFailed},
	}
	failures := []application.PipelineFailure{
		{Phase: application.PhaseStop, Index: 0, EntryID: "e-1",
			InstanceID: "i-ghost", Reason: application.ReasonInstanceGone},
		{Phase: application.PhaseStart, Index: 1, EntryID: "e-2",
			Reason: application.ReasonShuttingDown},
	}
	rows := stopFailureRows(entries, failures)
	if len(rows) != 2 || rows[0].EntryID != "e-2" || rows[1].EntryID != "e-1" {
		t.Fatalf("rows must keep the body order: %+v", rows)
	}
	if rows[0].Failures != nil {
		t.Fatalf("a start-phase failure must not render on a stop row: %+v", rows[0].Failures)
	}
	if len(rows[1].Failures) != 1 || rows[1].Failures[0].InstanceID != "i-ghost" ||
		rows[1].Failures[0].Reason != string(application.ReasonInstanceGone) {
		t.Fatalf("stop row failures = %+v", rows[1].Failures)
	}
	if got := stopFailureRows(entries, nil); len(got) != 2 ||
		got[0].Status != application.OutcomeStopped {
		t.Fatalf("no failures must still render every row: %+v", got)
	}
}

// A group aggregate has its own precedence, parallel to the single-target
// mapping: the platform's own failure outranks a caller-visible conflict even
// when both are present in one request, and the retry-later shutdown class
// outranks a conflict. The token is always the PHASE's incomplete token, never
// one instance's class.
func TestGroupFailureClassPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		phase      string
		err        error
		wantStatus int
		wantCode   string
		wantToken  string
	}{
		{"stop conflict", application.PhaseStop,
			process.ErrInstanceNotFound, http.StatusConflict, "conflict", "pipeline_stop_incomplete"},
		{"restart conflict", application.PhaseRestart,
			process.ErrLaunchInFlight, http.StatusConflict, "conflict", "pipeline_restart_incomplete"},
		{"persistence outranks a gone instance", application.PhaseStop,
			errors.Join(process.ErrInstanceNotFound, process.ErrPersistenceFailure),
			http.StatusInternalServerError, "internal_server_error", "pipeline_stop_incomplete"},
		{"termination-unconfirmed outranks in-flight", application.PhaseRestart,
			errors.Join(process.ErrLaunchInFlight, process.ErrTerminationUnconfirmed),
			http.StatusInternalServerError, "internal_server_error", "pipeline_restart_incomplete"},
		{"shutdown outranks a conflict", application.PhaseStop,
			errors.Join(process.ErrLaunchInFlight, &process.AdmissionRejection{Reason: process.RejShuttingDown}),
			http.StatusServiceUnavailable, "service_unavailable", "shutting_down"},
		{"aborted launch is the shutdown class", application.PhaseStart,
			process.ErrLaunchAbortedByShutdown,
			http.StatusServiceUnavailable, "service_unavailable", "shutting_down"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := groupFailureClass(tc.phase, tc.err)
			gotStatus := statusForAPICode(apiErr.Code)
			if gotStatus != tc.wantStatus || string(apiErr.Code) != tc.wantCode || apiErr.Message != tc.wantToken {
				t.Fatalf("group class = %d %s/%s, want %d %s/%s",
					gotStatus, apiErr.Code, apiErr.Message, tc.wantStatus, tc.wantCode, tc.wantToken)
			}
		})
	}
}
