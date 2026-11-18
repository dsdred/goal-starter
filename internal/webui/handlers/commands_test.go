package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/security"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// newCommandsTestEnv builds a CommandsHandler wired to a real supervisor and
// repository, mirroring the models lifecycle tests (ADR 024). A runtime and a
// model are created so model.start can be exercised.
func newCommandsTestEnv(t *testing.T) (*CommandsHandler, *ModelsHandler, *process.Supervisor, storage.Repository) {
	t.Helper()
	repo := newTestRepo(t)
	fakePath := fakeruntime.Path(t)
	insStore := newMockInstanceStoreForTest()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mC", Name: "C", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
	})

	insSvc := application.NewInstanceService(sup, repo)
	csrf := security.NewCSRF()
	hCommands := NewCommandsHandler(insSvc, application.NewPipelineService(sup, repo), repo)
	hModels := NewModelsHandler(application.NewModelService(repo), insSvc, sup, repo, csrf)
	return hCommands, hModels, sup, repo
}

func commandBody(command, targetID string) *bytes.Buffer {
	b, _ := json.Marshal(map[string]string{"command": command, "targetId": targetID})
	return bytes.NewBuffer(b)
}

func TestCommandsHandler_ModelStart_Success(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.start", "mC"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// The same instance-entry projection the model route returns (ADR 024 D-5).
	if body["model_id"] != "mC" {
		t.Errorf("model_id = %v, want mC", body["model_id"])
	}
}

func TestCommandsHandler_UnknownCommand_Returns400(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	// A value outside the closed Stage 1 vocabulary (all six model.* / pipeline.*
	// commands are now known) is a bounded 400 unknown_command, never a 404.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.frobnicate", "mC"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "unknown_command" {
		t.Errorf("code = %q, want unknown_command", body["code"])
	}
}

func TestCommandsHandler_EmptyCommand_Returns400(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", bytes.NewBufferString(`{"targetId":"mC"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCommandsHandler_EmptyTargetID_Returns400(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", bytes.NewBufferString(`{"command":"model.start"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCommandsHandler_InvalidJSON_Returns400(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", bytes.NewBufferString(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCommandsHandler_UnknownTarget_Returns404(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.start", "does-not-exist"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %q, want not_found", body["code"])
	}
}

// newPendingTargetFixture brings up a slot-limited supervisor whose target model
// (mB) is deterministically in-flight — pending, blocked behind a sleeping instance
// mA that holds the only slot — and returns a CommandsHandler wired against it.
func newPendingTargetFixture(t *testing.T) *CommandsHandler {
	t.Helper()
	repo := newTestRepo(t)
	insStore := newMockInstanceStoreForTest()
	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64})

	fakePath := fakeruntime.Path(t)
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}
	modelA := &domain.Model{ID: "mA", Name: "A", RuntimeID: "rt"}
	modelB := &domain.Model{ID: "mB", Name: "B", RuntimeID: "rt"}
	ctx := context.Background()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mA", Name: "A", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model A: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mB", Name: "B", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model B: %v", err)
	}

	instA, err := sup.AdmitAndStart(ctx, modelA, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := waitForSupervisorState(t, sup, instA.ID, domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}

	// mB blocks on slot acquisition → pending. Cancel before Shutdown so the
	// goroutine cannot spawn after cleanup begins.
	pendingCtx, cancelPending := context.WithCancel(ctx)
	pendingDone := make(chan struct{})
	go func() {
		defer close(pendingDone)
		_, _ = sup.AdmitAndStart(pendingCtx, modelB, rt, domain.ManualOwner, []string{"-sleep", "60"}, nil)
	}()
	t.Cleanup(func() {
		cancelPending()
		select {
		case <-pendingDone:
		case <-time.After(5 * time.Second):
			t.Errorf("timeout waiting for pending start goroutine to finish")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(shutdownCtx)
	})

	deadline := time.Now().Add(5 * time.Second)
	pending := false
	for time.Now().Before(deadline) {
		insts, _ := sup.List()
		for _, inst := range insts {
			if inst.ModelID == "mB" && inst.State == domain.InstanceStatePending {
				pending = true
				break
			}
		}
		if pending {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !pending {
		t.Fatal("timeout waiting for mB to reach pending")
	}

	return NewCommandsHandler(application.NewInstanceService(sup, repo), application.NewPipelineService(sup, repo), repo)
}

func TestCommandsHandler_InFlight_Returns409(t *testing.T) {
	h := newPendingTargetFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.start", "mB"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}
}

func TestCommandsHandler_AuthAndCSRF(t *testing.T) {
	router := newAuthenticatedTestRouter(t)

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.start", "m"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401; body=%s", resp.Code, resp.Body.String())
	}

	// Log in to obtain a session + CSRF cookie.
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp := httptest.NewRecorder()
	router.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginResp.Code, loginResp.Body.String())
	}
	var loginBody map[string]string
	if err := json.Unmarshal(loginResp.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginBody["csrf_token"] == "" {
		t.Fatal("login response did not include csrf_token")
	}
	cookies := loginResp.Result().Cookies()

	// Authenticated but missing the CSRF token → 403.
	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.start", "m"))
	noCSRFReq.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		noCSRFReq.AddCookie(c)
	}
	noCSRFResp := httptest.NewRecorder()
	router.ServeHTTP(noCSRFResp, noCSRFReq)
	if noCSRFResp.Code != http.StatusForbidden {
		t.Fatalf("no-CSRF status = %d, want 403; body=%s", noCSRFResp.Code, noCSRFResp.Body.String())
	}
}

// TestExistingModelRoute_StartRegression pins ADR 024 C-6: the existing
// POST /api/v1/models/{id}/start route is unchanged. A valid start still returns
// 200, and a non-existent model still returns the pre-existing 500 (NOT the new
// 404 the command surface adds) — proving the command slice did not touch the
// model route's semantics.
func TestExistingModelRoute_StartRegression(t *testing.T) {
	_, hModels, _, _ := newCommandsTestEnv(t)

	// Existing route, valid model → 200.
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/models/mC/start", nil)
	okW := httptest.NewRecorder()
	hModels.Start(okW, okReq)
	if okW.Code != http.StatusOK {
		t.Fatalf("existing route start status = %d, want 200; body=%s", okW.Code, okW.Body.String())
	}

	// Existing route, non-existent model → 500 (unchanged pre-existing behavior).
	ghostReq := httptest.NewRequest(http.MethodPost, "/api/v1/models/ghost/start", nil)
	ghostW := httptest.NewRecorder()
	hModels.Start(ghostW, ghostReq)
	if ghostW.Code != http.StatusInternalServerError {
		t.Fatalf("existing route unknown-model status = %d, want 500 (unchanged); body=%s", ghostW.Code, ghostW.Body.String())
	}
}

// TestCommandsHandler_ModelStop_Success exercises the model.stop command against a
// model whose instance is deterministically running (mA in the slot-limited
// fixture), so the stop path actually reaches InstanceService.StopInstance → the
// single ADR 017 Supervisor.Stop boundary. The command returns the same 200 body the
// per-object route returns (ADR 024 D-10).
func TestCommandsHandler_ModelStop_Success(t *testing.T) {
	h := newPendingTargetFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.stop", "mA"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("model.stop status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "stopped" {
		t.Errorf("status = %q, want stopped", body["status"])
	}
}

// TestCommandsHandler_ModelStop_PendingReturns409 mirrors the model route's
// launch_in_flight guard: a model with a pending (not-yet-spawned) launch is a
// bounded 409, never a stop attempt on an instance that has no process yet.
func TestCommandsHandler_ModelStop_PendingReturns409(t *testing.T) {
	h := newPendingTargetFixture(t)

	// mB is pending in the fixture (blocked behind mA on the only slot).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.stop", "mB"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
}

// TestCommandsHandler_ModelRestart_Success exercises the model.restart command
// against a running model (mA), covering the D1 pre-flight-then-restart order: the
// whole in-flight target set is preflighted before any instance is mutated, then
// each is restarted through the single ADR 017 Supervisor.RestartWithLaunch
// boundary. The command returns the same 200 body as the per-object route.
func TestCommandsHandler_ModelRestart_Success(t *testing.T) {
	h := newPendingTargetFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.restart", "mA"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("model.restart status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "restarted" {
		t.Errorf("status = %q, want restarted", body["status"])
	}
}

// TestCommandsHandler_PipelineUnknownTarget_Returns404 pins ADR 024 D-6 on the
// pipeline half of the command surface: a pipeline command whose targetId is not a
// known pipeline is a bounded 404 (the same deliberate improvement over the model
// routes the D-6 404 gives model.* commands).
func TestCommandsHandler_PipelineUnknownTarget_Returns404(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("pipeline.start", "no-such-pipeline"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %q, want not_found", body["code"])
	}
}

// TestCommandsHandler_PipelineLifecycleAndAudit exercises the three pipeline.*
// commands through the full router (auth + CSRF + audit wired, ADR 024 D-2/D-7) and
// asserts: (1) each command returns the same body its per-object route returns (D-10);
// (2) a second start of an already-running owned entry is `already_running`, not an
// error or 409 (D1.4 ownership is preserved, not duplicated); and (3) each command
// emits exactly one control-action audit event with bounded counters and the
// authenticated user — the same events as the per-object routes (D-7 parity).
func TestCommandsHandler_PipelineLifecycleAndAudit(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcm-lifecycle")

	if status, body := createPipelineAPI(t, e, addr, sess, csrf, `{"name":"cmdpipe","models":[{"model_id":"pcm-lifecycle"}]}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	pipelines, err := e.repo.ListPipelines()
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	pipeID := pipelines[0].ID
	cmd := func(command string) *httptest.ResponseRecorder {
		return e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
			`{"command":"`+command+`","targetId":"`+pipeID+`"}`)
	}

	// pipeline.start → 200, entry started.
	var startOut struct {
		PipelineID string `json:"pipeline_id"`
		Results    []struct {
			Status     string `json:"status"`
			InstanceID string `json:"instance_id"`
		} `json:"results"`
	}
	rec := cmd("pipeline.start")
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.start status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startOut); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if startOut.PipelineID != pipeID || len(startOut.Results) != 1 || startOut.Results[0].Status != "started" || startOut.Results[0].InstanceID == "" {
		t.Fatalf("start = %+v", startOut)
	}

	// A second pipeline.start of the already-running owned entry reports the
	// `already-running` outcome (ADR 010 D3 wire vocabulary), not a conflict and not
	// an error (D1.4 ownership is preserved through the command, never duplicated).
	var again struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	rec = cmd("pipeline.start")
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.start (again) status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil || len(again.Results) != 1 || again.Results[0].Status != "already-running" {
		t.Fatalf("expected already-running outcome, got %s", rec.Body.String())
	}

	// pipeline.stop → 200, entry stopped.
	var stopOut struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	rec = cmd("pipeline.stop")
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.stop status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stopOut); err != nil || len(stopOut.Results) != 1 || stopOut.Results[0].Status != "stopped" {
		t.Fatalf("stop = %s", rec.Body.String())
	}

	// pipeline.restart → 200, one stop row + one start row, start succeeded.
	var restartOut struct {
		PipelineID   string `json:"pipeline_id"`
		StopResults  []any  `json:"stop_results"`
		StartResults []struct {
			Status string `json:"status"`
		} `json:"start_results"`
	}
	rec = cmd("pipeline.restart")
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.restart status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &restartOut); err != nil {
		t.Fatalf("decode restart: %v", err)
	}
	if restartOut.PipelineID != pipeID || len(restartOut.StopResults) != 1 || len(restartOut.StartResults) != 1 || restartOut.StartResults[0].Status != "started" {
		t.Fatalf("restart = %+v", restartOut)
	}
	t.Cleanup(func() {
		e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	})

	// Audit parity (D-7): exactly one event per distinct command, bounded counters,
	// authenticated user. pipeline.start was sent twice (the already-running re-start
	// emits an event too) → two pipeline.start events. Query is newest-first, so
	// starts[0] is the re-start (already_running) and starts[1] is the first (started).
	starts := e.query(t, 100, 0, audit.EventPipelineStart)
	if len(starts) != 2 {
		t.Fatalf("pipeline.start events = %d, want 2: %+v", len(starts), starts)
	}
	if starts[1].Detail["pipeline_id"] != pipeID || starts[1].Detail["started"] != "1" || starts[1].User != "admin" {
		t.Fatalf("first start audit detail = %v user=%q", starts[1].Detail, starts[1].User)
	}
	if starts[0].Detail["pipeline_id"] != pipeID || starts[0].Detail["already_running"] != "1" {
		t.Fatalf("re-start audit detail = %v, want already_running=1", starts[0].Detail)
	}

	stops := e.query(t, 100, 0, audit.EventPipelineStop)
	if len(stops) != 1 {
		t.Fatalf("pipeline.stop events = %d, want 1: %+v", len(stops), stops)
	}
	if stops[0].Detail["stopped"] != "1" || stops[0].Detail["pipeline_id"] != pipeID {
		t.Fatalf("stop audit detail = %v", stops[0].Detail)
	}

	restarts := e.query(t, 100, 0, audit.EventPipelineRestart)
	if len(restarts) != 1 {
		t.Fatalf("pipeline.restart events = %d, want 1: %+v", len(restarts), restarts)
	}
	if restarts[0].Detail["pipeline_id"] != pipeID || restarts[0].Detail["stopped"] != "1" || restarts[0].Detail["started"] != "1" {
		t.Fatalf("restart audit detail = %v, want combined counters", restarts[0].Detail)
	}

	// No instance/model identity leaks into the counters-only detail (ADR 007).
	for _, ev := range append(append(starts, stops...), restarts...) {
		for _, v := range ev.Detail {
			if v == startOut.Results[0].InstanceID || v == "pcm-lifecycle-model" {
				t.Fatalf("audit detail leaked instance/model identity: %v", ev.Detail)
			}
		}
	}
}

// startPipelineComponent brings up a single-entry pipeline over modelID and starts
// it through the command surface, waiting for the component to reach running. It
// returns the component's instance ID. This is the deterministic source of a live,
// owned instance for the instance.* command legs (ADR 024 D-11).
func startPipelineComponent(t *testing.T, e *auditEnv, addr, sess, csrf, modelID string) string {
	t.Helper()
	body := `{"name":"cmdcomp","models":[{"model_id":"` + modelID + `"}]}`
	if status, raw := createPipelineAPI(t, e, addr, sess, csrf, body); status != http.StatusCreated {
		t.Fatalf("create pipeline: %d %s", status, raw)
	}
	pipelines, err := e.repo.ListPipelines()
	if err != nil || len(pipelines) == 0 {
		t.Fatalf("ListPipelines: %v (n=%d)", err, len(pipelines))
	}
	pipeID := pipelines[0].ID
	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"pipeline.start","targetId":"`+pipeID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.start: %d %s", rec.Code, rec.Body.String())
	}
	var startOut struct {
		Results []struct {
			Status     string `json:"status"`
			InstanceID string `json:"instance_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startOut); err != nil || len(startOut.Results) != 1 {
		t.Fatalf("pipeline.start results = %s", rec.Body.String())
	}
	if startOut.Results[0].Status != "started" || startOut.Results[0].InstanceID == "" {
		t.Fatalf("pipeline.start result = %+v, want started with instance_id", startOut.Results[0])
	}
	id := startOut.Results[0].InstanceID
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(id), domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait component %s running: %v", id, err)
	}
	t.Cleanup(func() {
		e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	})
	return id
}

// TestCommandsHandler_InstanceStop_Success pins ADR 024 D-11 on the success leg:
// instance.stop over the command surface stops exactly the targeted instance and
// returns the same 200 body the per-object /instances/{id}/stop route returns
// (D-10 parity).
func TestCommandsHandler_InstanceStop_Success(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-stop")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-stop")

	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.stop","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "stopped" {
		t.Errorf("status = %q, want stopped", body["status"])
	}
}

// TestCommandsHandler_InstanceRestart_Success pins ADR 024 D-11 on the restart leg:
// instance.restart restarts exactly the targeted instance and returns the same 200
// instance-entry body (environment nulled) the per-object /instances/{id}/restart
// route returns (D-10 parity). The restarted instance keeps its identity.
func TestCommandsHandler_InstanceRestart_Success(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-restart")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-restart")

	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.restart","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.restart status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["id"] != id {
		t.Errorf("restarted id = %v, want %s (identity preserved)", body["id"], id)
	}
	if body["model_id"] != "pcomp-restart" {
		t.Errorf("model_id = %v, want pcomp-restart", body["model_id"])
	}
}

// TestCommandsHandler_InstanceUnknownTarget_Returns404 pins ADR 024 D-6 on the
// instance half of the command surface: an instance command whose targetId is not a
// resolvable instance (live or a retained terminal one) is a bounded 404 not_found,
// the deliberate command-surface improvement over the per-object route.
func TestCommandsHandler_InstanceUnknownTarget_Returns404(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("instance.stop", "no-such-instance"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %q, want not_found", body["code"])
	}
}

// TestCommandsHandler_InstanceStop_IsSingleInstanceNotModelFanout is the D-11
// justification leg: a model MAY be present in a pipeline as two entries (ADR 013
// repeatable models), so two live instances of the SAME model coexist as two
// components. instance.stop names ONE component's InstanceID and must stop exactly
// that instance — never fan out over the model's other instance the way model.stop
// does. It proves the command is instance-scoped, not model-scoped.
func TestCommandsHandler_InstanceStop_IsSingleInstanceNotModelFanout(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "fanout-model")

	// One pipeline, the same model in two entries → two live components of one model.
	if status, raw := createPipelineAPI(t, e, addr, sess, csrf,
		`{"name":"fanout","models":[{"model_id":"fanout-model"},{"model_id":"fanout-model"}]}`); status != http.StatusCreated {
		t.Fatalf("create pipeline: %d %s", status, raw)
	}
	pipelines, err := e.repo.ListPipelines()
	if err != nil || len(pipelines) == 0 {
		t.Fatalf("ListPipelines: %v (n=%d)", err, len(pipelines))
	}
	pipeID := pipelines[0].ID

	var startOut struct {
		Results []struct {
			Status     string `json:"status"`
			InstanceID string `json:"instance_id"`
		} `json:"results"`
	}
	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"pipeline.start","targetId":"`+pipeID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.start: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startOut); err != nil || len(startOut.Results) != 2 {
		t.Fatalf("pipeline.start results = %s, want 2 components", rec.Body.String())
	}
	idA, idB := startOut.Results[0].InstanceID, startOut.Results[1].InstanceID
	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("want two distinct component ids, got %q and %q", idA, idB)
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(idA), domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(idB), domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait B running: %v", err)
	}
	t.Cleanup(func() {
		e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	})

	// Stop exactly component A by its InstanceID.
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.stop","targetId":"`+idA+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var stopBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &stopBody); err != nil || stopBody["status"] != "stopped" {
		t.Fatalf("instance.stop body = %s, want status=stopped", rec.Body.String())
	}

	// Component A is now terminal (no longer live/in-flight).
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, err := e.sup.Status(domain.InstanceID(idA))
		if err == nil && a != nil && !a.State.IsInFlight() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("component A did not reach a terminal state after instance.stop: %+v", a)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Component B — the OTHER instance of the SAME model — is untouched and still
	// running. A model-scoped fan-out (model.stop) would have stopped B too.
	b, err := e.sup.Status(domain.InstanceID(idB))
	if err != nil || b == nil {
		t.Fatalf("sup.Status(B): %v", err)
	}
	if !b.State.IsRunningOrStarting() {
		t.Fatalf("component B state = %q, want still running — instance.stop must not fan out to the model's other instance", b.State)
	}

	// Audit parity (D-7/D-10): exactly one instance.stop event, naming only the
	// targeted component, never the sibling.
	stops := e.query(t, 100, 0, audit.EventInstanceStop)
	if len(stops) != 1 {
		t.Fatalf("instance.stop events = %d, want 1: %+v", len(stops), stops)
	}
	if stops[0].Detail["instance_id"] != idA {
		t.Fatalf("instance.stop audit detail = %v, want instance_id=%s", stops[0].Detail, idA)
	}
}

// TestCommandsHandler_InstanceLifecycleAndAudit pins ADR 024 D-11 audit parity on
// the command surface: instance.stop and instance.restart each emit exactly one
// control-action audit event (instance.stop / instance.restart) carrying the
// targeted instance_id — the same events and detail the per-object routes emit
// (D-7/D-10 parity). Unlike the pipeline commands, the instance events carry the
// instance_id (the command's target IS a single instance, so there is no group
// counter to protect); no model-name or sibling identity is added.
func TestCommandsHandler_InstanceLifecycleAndAudit(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-audit")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-audit")

	// instance.restart → 200 (restarts the running component in place).
	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.restart","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.restart status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(id), domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait component running after restart: %v", err)
	}

	// instance.stop → 200 (stops the restarted component).
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.stop","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	restarts := e.query(t, 100, 0, audit.EventInstanceRestart)
	if len(restarts) != 1 {
		t.Fatalf("instance.restart events = %d, want 1: %+v", len(restarts), restarts)
	}
	if restarts[0].Detail["instance_id"] != id || restarts[0].User != "admin" {
		t.Fatalf("instance.restart audit detail = %v user=%q", restarts[0].Detail, restarts[0].User)
	}

	stops := e.query(t, 100, 0, audit.EventInstanceStop)
	if len(stops) != 1 {
		t.Fatalf("instance.stop events = %d, want 1: %+v", len(stops), stops)
	}
	if stops[0].Detail["instance_id"] != id {
		t.Fatalf("instance.stop audit detail = %v, want instance_id=%s", stops[0].Detail, id)
	}

	// No model name leaks into the detail.
	for _, ev := range append(stops, restarts...) {
		for _, v := range ev.Detail {
			if v == "pcomp-audit-model" {
				t.Fatalf("audit detail leaked model identity: %v", ev.Detail)
			}
		}
	}
}

// waitComponentTerminal polls the supervisor until the component is no longer
// live/in-flight (terminal: exited or failed).
func waitComponentTerminal(t *testing.T, e *auditEnv, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		inst, err := e.sup.Status(domain.InstanceID(id))
		if err == nil && inst != nil && !inst.State.IsInFlight() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("component %s did not reach a terminal state", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCommandsHandler_InstanceStart_Success pins ADR 024 D-12 on the success
// leg: instance.start over the command surface starts exactly the targeted
// TERMINAL component on its SAME InstanceID and returns the same 200
// instance-entry body the per-object routes return.
func TestCommandsHandler_InstanceStart_Success(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-start")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-start")

	// To terminal via the command surface.
	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.stop","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	waitComponentTerminal(t, e, id)

	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.start","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.start status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["id"] != id {
		t.Errorf("started id = %v, want %s (identity preserved)", body["id"], id)
	}
	if body["model_id"] != "pcomp-start" {
		t.Errorf("model_id = %v, want pcomp-start", body["model_id"])
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(id), domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait component running after instance.start: %v", err)
	}
}

// TestCommandsHandler_InstanceStart_Running_Returns409 pins the D-12 refusal
// leg with the side-effect absence asserted: a start of a RUNNING component is
// 409 already_running, and the live generation is NOT stopped, restarted or
// mutated (same PID, still running).
func TestCommandsHandler_InstanceStart_Running_Returns409(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-start409")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-start409")

	live, err := e.sup.Status(domain.InstanceID(id))
	if err != nil || live == nil {
		t.Fatalf("status: %v", err)
	}
	firstPID := live.PID

	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.start","targetId":"`+id+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("instance.start status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "already_running" {
		t.Errorf("error = %q, want already_running", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}

	// No unwanted lifecycle side effect on the live generation.
	after, err := e.sup.Status(domain.InstanceID(id))
	if err != nil || after == nil {
		t.Fatalf("status after refused start: %v", err)
	}
	if after.PID != firstPID {
		t.Fatalf("PID changed by a refused start: %d -> %d", firstPID, after.PID)
	}
	if after.State != domain.InstanceStateRunning {
		t.Fatalf("state = %q, want still running", after.State)
	}
}

// TestCommandsHandler_InstanceStart_VsPipelineStart pins the D-12 coexistence
// with the group command: on a running component, instance.start is refused
// 409 already_running and pipeline.start reports the `already-running`
// outcome — pipeline.start semantics are unchanged by the new command, and
// the component's generation is untouched by both.
func TestCommandsHandler_InstanceStart_VsPipelineStart(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-start-pipe")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-start-pipe")
	pipelines, err := e.repo.ListPipelines()
	if err != nil || len(pipelines) == 0 {
		t.Fatalf("ListPipelines: %v", err)
	}
	pipeID := pipelines[0].ID
	live, err := e.sup.Status(domain.InstanceID(id))
	if err != nil || live == nil {
		t.Fatalf("status: %v", err)
	}
	firstPID := live.PID

	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.start","targetId":"`+id+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("instance.start status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"pipeline.start","targetId":"`+pipeID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.start status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var pipeBody struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pipeBody); err != nil || len(pipeBody.Results) != 1 || pipeBody.Results[0].Status != "already-running" {
		t.Fatalf("pipeline.start = %s, want already-running", rec.Body.String())
	}

	after, err := e.sup.Status(domain.InstanceID(id))
	if err != nil || after == nil {
		t.Fatalf("status after both commands: %v", err)
	}
	if after.PID != firstPID || after.State != domain.InstanceStateRunning {
		t.Fatalf("generation mutated: pid=%d state=%q (want %d running)", after.PID, after.State, firstPID)
	}
}

// TestCommandsHandler_InstanceStart_DuplicateModelIsolation pins the
// repeatable-model leg on the command surface: two live components of ONE
// model; instance.start names ONE InstanceID. It starts exactly the targeted
// terminal component; the sibling of the SAME model is untouched (same PID,
// still running).
func TestCommandsHandler_InstanceStart_DuplicateModelIsolation(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-start-fanout")

	if status, raw := createPipelineAPI(t, e, addr, sess, csrf,
		`{"name":"startfanout","models":[{"model_id":"pcomp-start-fanout"},{"model_id":"pcomp-start-fanout"}]}`); status != http.StatusCreated {
		t.Fatalf("create pipeline: %d %s", status, raw)
	}
	pipelines, err := e.repo.ListPipelines()
	if err != nil || len(pipelines) == 0 {
		t.Fatalf("ListPipelines: %v (n=%d)", err, len(pipelines))
	}
	pipeID := pipelines[0].ID
	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"pipeline.start","targetId":"`+pipeID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline.start: %d %s", rec.Code, rec.Body.String())
	}
	var startOut struct {
		Results []struct {
			Status     string `json:"status"`
			InstanceID string `json:"instance_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startOut); err != nil || len(startOut.Results) != 2 {
		t.Fatalf("pipeline.start results = %s, want 2 components", rec.Body.String())
	}
	idA, idB := startOut.Results[0].InstanceID, startOut.Results[1].InstanceID
	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("want two distinct component ids, got %q and %q", idA, idB)
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(idA), domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait A running: %v", err)
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(idB), domain.InstanceStateRunning, 5*time.Second); err != nil {
		t.Fatalf("wait B running: %v", err)
	}
	bLive, err := e.sup.Status(domain.InstanceID(idB))
	if err != nil || bLive == nil {
		t.Fatalf("status B: %v", err)
	}
	bPID := bLive.PID
	t.Cleanup(func() {
		e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	})

	// Stop A, then instance.start A by its InstanceID.
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.stop","targetId":"`+idA+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	waitComponentTerminal(t, e, idA)
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.start","targetId":"`+idA+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.start status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["id"] != idA {
		t.Fatalf("instance.start body = %s, want id=%s", rec.Body.String(), idA)
	}
	if err := waitForSupervisorState(t, e.sup, domain.InstanceID(idA), domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait A running after instance.start: %v", err)
	}

	// Sibling B of the SAME model is untouched.
	bAfter, err := e.sup.Status(domain.InstanceID(idB))
	if err != nil || bAfter == nil {
		t.Fatalf("status B after: %v", err)
	}
	if bAfter.PID != bPID {
		t.Fatalf("sibling B PID changed by instance.start of A: %d -> %d", bPID, bAfter.PID)
	}
	if bAfter.State != domain.InstanceStateRunning {
		t.Fatalf("sibling B state = %q, want still running", bAfter.State)
	}
}

// TestCommandsHandler_InstanceStart_UnknownTarget_Returns404 pins D-6 on the
// instance.start half of the command surface: an unresolvable InstanceID is a
// bounded 404 not_found.
func TestCommandsHandler_InstanceStart_UnknownTarget_Returns404(t *testing.T) {
	h, _, _, _ := newCommandsTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("instance.start", "no-such-instance"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %q, want not_found", body["code"])
	}
}

// TestCommandsHandler_InstanceStart_Audit pins the D-12 audit leg: instance.start
// emits exactly one control-action audit event per attempt, carrying the
// instance_id (identifier-only) plus, on refusal, the bounded error token —
// never the model name or any sibling identity.
func TestCommandsHandler_InstanceStart_Audit(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pcomp-start-audit")
	id := startPipelineComponent(t, e, addr, sess, csrf, "pcomp-start-audit")

	// Refused leg (running) → one audit with the bounded error token.
	rec := e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.start","targetId":"`+id+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("refused leg status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Success leg → one audit without the error token.
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.stop","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	waitComponentTerminal(t, e, id)
	rec = e.do(t, http.MethodPost, "/api/v1/commands/execute", addr, sess, csrf,
		`{"command":"instance.start","targetId":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance.start status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	events := e.query(t, 100, 0, audit.EventInstanceStart)
	if len(events) != 2 {
		t.Fatalf("instance.start events = %d, want 2: %+v", len(events), events)
	}
	// Newest-first: events[0] is the success, events[1] the refusal.
	if events[0].Detail["instance_id"] != id || events[0].Detail["error"] != "" {
		t.Fatalf("success audit detail = %v, want instance_id only", events[0].Detail)
	}
	if events[1].Detail["instance_id"] != id || events[1].Detail["error"] != "already_running" {
		t.Fatalf("refusal audit detail = %v, want error=already_running", events[1].Detail)
	}
	if events[0].User != "admin" {
		t.Fatalf("audit user = %q, want admin", events[0].User)
	}
	// No model identity leak.
	for _, ev := range events {
		for _, v := range ev.Detail {
			if v == "pcomp-start-audit-model" {
				t.Fatalf("audit detail leaked model identity: %v", ev.Detail)
			}
		}
	}
}
