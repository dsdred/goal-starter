package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
)

// ADR 010 acceptance 12: unauthenticated pipeline request → 401.
func TestPipelineAPI_RequiresAuth(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/pipelines"},
		{http.MethodGet, "/api/v1/pipelines/p-1"},
		{http.MethodPost, "/api/v1/pipelines"},
		{http.MethodPost, "/api/v1/pipelines/p-1/start"},
	} {
		rec := e.do(t, tc.method, tc.path, addr, "", "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// ADR 010 acceptance 12: authenticated mutation without CSRF → 403.
func TestPipelineAPI_CSRFRequired(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, _ := e.loggedIn(t, addr)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/pipelines"},
		{http.MethodPut, "/api/v1/pipelines/p-1"},
		{http.MethodDelete, "/api/v1/pipelines/p-1"},
		{http.MethodPost, "/api/v1/pipelines/p-1/start"},
		{http.MethodPost, "/api/v1/pipelines/p-1/stop"},
		{http.MethodPost, "/api/v1/pipelines/p-1/restart"},
	} {
		rec := e.do(t, tc.method, tc.path, addr, sess, "", "{}")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: status = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	// Reads are authenticated without CSRF.
	rec := e.do(t, http.MethodGet, "/api/v1/pipelines", addr, sess, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list: status = %d, want 200", rec.Code)
	}
}

func createPipelineAPI(t *testing.T, e *auditEnv, addr, sess, csrf, body string) (int, string) {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/v1/pipelines", addr, sess, csrf, body)
	return rec.Code, rec.Body.String()
}

// ADR 010 acceptance 2: create validation → 400 bad_request; valid create →
// 201 with defaults active=false / auto_start=false.
func TestPipelineAPI_CreateValidationAndDefaults(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pm-model")

	// Invalid JSON follows the codebase-wide convention: 400 + flat error.
	if status, body := createPipelineAPI(t, e, addr, sess, csrf, `{`); status != http.StatusBadRequest {
		t.Fatalf("invalid json status = %d, want 400; body=%s", status, body)
	}

	cases := []struct {
		name string
		body string
	}{
		{"empty name", `{"name":"  ","models":[{"model_id":"pm-model"}]}`},
		{"empty model list", `{"name":"p","models":[]}`},
		{"duplicate model", `{"name":"p","models":[{"model_id":"pm-model"},{"model_id":"pm-model"}]}`},
		{"unknown model", `{"name":"p","models":[{"model_id":"ghost"}]}`},
		{"empty model id", `{"name":"p","models":[{"model_id":""}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := createPipelineAPI(t, e, addr, sess, csrf, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", status, body)
			}
			var errResp struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(body), &errResp); err != nil || errResp.Code != "bad_request" {
				t.Fatalf("error contract violated: %s", body)
			}
		})
	}

	status, body := createPipelineAPI(t, e, addr, sess, csrf, `{"name":"defaults","models":[{"model_id":"pm-model"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}
	var created struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
		Models []struct {
			ModelID   string `json:"model_id"`
			ModelName string `json:"model_name"`
			AutoStart bool   `json:"auto_start"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" || created.Name != "defaults" || created.Active {
		t.Fatalf("created = %+v, want id set, active=false by default", created)
	}
	if len(created.Models) != 1 || created.Models[0].AutoStart || created.Models[0].ModelName != "pm-model-model" {
		t.Fatalf("models = %+v, want auto_start=false and the model name", created.Models)
	}
}

// ADR 010 D5: list shape and detail per-model live status.
func TestPipelineAPI_ListAndDetail(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pm-a")
	e.seedGracefulModel(t, "pm-b")

	if status, body := createPipelineAPI(t, e, addr, sess, csrf,
		`{"name":"one","active":true,"models":[{"model_id":"pm-a","auto_start":true},{"model_id":"pm-b","args":["graceful","-x"]}]}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}

	rec := e.do(t, http.MethodGet, "/api/v1/pipelines", addr, sess, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
		Models []struct {
			ModelID   string   `json:"model_id"`
			ModelName string   `json:"model_name"`
			Args      []string `json:"args"`
			AutoStart bool     `json:"auto_start"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "one" || !list[0].Active {
		t.Fatalf("list = %+v", list)
	}
	if len(list[0].Models) != 2 || !list[0].Models[0].AutoStart || list[0].Models[0].ModelName == "" {
		t.Fatalf("list models = %+v", list[0].Models)
	}
	if !reflect.DeepEqual(list[0].Models[1].Args, []string{"graceful", "-x"}) {
		t.Fatalf("override args not in list: %+v", list[0].Models[1])
	}

	rec = e.do(t, http.MethodGet, "/api/v1/pipelines/"+list[0].ID, addr, sess, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Pipeline struct {
			ID string `json:"id"`
		} `json:"pipeline"`
		Models []struct {
			ModelID     string `json:"model_id"`
			State       string `json:"state"`
			AutoStart   bool   `json:"auto_start"`
			HasOverride bool   `json:"has_args_override"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Models) != 2 {
		t.Fatalf("detail models = %+v", detail.Models)
	}
	for _, m := range detail.Models {
		if m.State != "stopped" {
			t.Fatalf("idle model state = %q, want stopped: %+v", m.State, m)
		}
	}
	if !detail.Models[0].AutoStart || !detail.Models[1].HasOverride {
		t.Fatalf("detail flags: %+v", detail.Models)
	}

	// Unknown ID → 404.
	if rec = e.do(t, http.MethodGet, "/api/v1/pipelines/missing", addr, sess, "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing detail status = %d, want 404", rec.Code)
	}
}

// ADR 010 acceptance 13: exactly one pipeline.start / pipeline.stop /
// pipeline.restart audit event per lifecycle request, with pipeline_id and
// bounded counters only.
func TestPipelineAPI_LifecycleAndAudit(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pm-lifecycle")

	if status, body := createPipelineAPI(t, e, addr, sess, csrf, `{"name":"lifecycle","models":[{"model_id":"pm-lifecycle"}]}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	pipelines, err := e.repo.ListPipelines()
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	pipeID := pipelines[0].ID

	var startOut struct {
		PipelineID string `json:"pipeline_id"`
		Results    []struct {
			ModelID    string `json:"model_id"`
			Status     string `json:"status"`
			InstanceID string `json:"instance_id"`
		} `json:"results"`
	}
	rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/start", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startOut); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if startOut.PipelineID != pipeID || len(startOut.Results) != 1 || startOut.Results[0].Status != "started" || startOut.Results[0].InstanceID == "" {
		t.Fatalf("start = %+v", startOut)
	}

	rec = e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var stopOut struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stopOut); err != nil || len(stopOut.Results) != 1 || stopOut.Results[0].Status != "stopped" {
		t.Fatalf("stop = %s", rec.Body.String())
	}

	rec = e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/restart", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restart status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var restartOut struct {
		PipelineID   string `json:"pipeline_id"`
		StopResults  []any  `json:"stop_results"`
		StartResults []struct {
			Status string `json:"status"`
		} `json:"start_results"`
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

	// Exactly one event per request, counters bounded.
	starts := e.query(t, 100, 0, audit.EventPipelineStart)
	if len(starts) != 1 {
		t.Fatalf("pipeline.start events = %d, want 1: %+v", len(starts), starts)
	}
	if starts[0].Detail["pipeline_id"] != pipeID || starts[0].Detail["started"] != "1" {
		t.Fatalf("start audit detail = %v", starts[0].Detail)
	}
	if starts[0].User != "admin" {
		t.Fatalf("start audit user = %q, want admin", starts[0].User)
	}

	stops := e.query(t, 100, 0, audit.EventPipelineStop)
	if len(stops) != 1 {
		t.Fatalf("pipeline.stop events = %d, want 1 (restart emits only pipeline.restart): %+v", len(stops), stops)
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

	// No raw instance IDs or model names leak into the counters-only detail.
	for _, ev := range append(append(starts, stops...), restarts...) {
		for _, v := range ev.Detail {
			if v == startOut.Results[0].InstanceID || v == "pm-lifecycle-model" {
				t.Fatalf("audit detail leaked instance/model identity: %v", ev.Detail)
			}
		}
	}
}

// ADR 010 D6 (regression for the restart combined `failed` counter): a
// restart whose STOP phase and START phase each produce one failure must emit
// a single pipeline.restart event whose `failed` counter is the SUM (2), not
// one phase overwriting the other. The other bounded counters stay intact.
func TestPipelineAPI_RestartAuditCombinedFailedCounter(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	// Model A: a real graceful model. A fake owned "running" instance the
	// supervisor does not know about makes the stop phase fail for A; the
	// start phase then sees A as already-running (no second launch).
	e.seedGracefulModel(t, "pm-raf-a")
	// Model B: a real model whose executable does not exist → resolve fails →
	// the start phase fails for B (bounded resolve-failed reason).
	if err := e.repo.CreateRuntime(&storage.RuntimeEntry{
		ID:         "pm-raf-b-rt",
		Name:       "bad-runtime",
		Executable: filepath.Join(t.TempDir(), "no-such-executable"),
	}); err != nil {
		t.Fatalf("create bad runtime: %v", err)
	}
	if err := e.repo.CreateModel(&storage.ModelEntry{ID: "pm-raf-b", Name: "bad-model", RuntimeID: "pm-raf-b-rt"}); err != nil {
		t.Fatalf("create bad model: %v", err)
	}

	if status, body := createPipelineAPI(t, e, addr, sess, csrf,
		`{"name":"raf","models":[{"model_id":"pm-raf-a"},{"model_id":"pm-raf-b"}]}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	pipelines, err := e.repo.ListPipelines()
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	pipeID := pipelines[0].ID

	// Fake owned running instance for A: the stop phase cannot find it in the
	// supervisor → a genuine stop failure for the A entry.
	now := time.Now()
	if err := e.repo.CreateLaunchInstance(&domain.LaunchInstanceEntry{
		ID: "fake-owned-a", ModelID: "pm-raf-a", State: "running", PID: 99999,
		PipelineID: pipeID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed fake owned: %v", err)
	}

	rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/restart", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restart status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Exactly one pipeline.restart event, and no separate start/stop events.
	restarts := e.query(t, 100, 0, audit.EventPipelineRestart)
	if len(restarts) != 1 {
		t.Fatalf("pipeline.restart events = %d, want 1: %+v", len(restarts), restarts)
	}
	if n := len(e.query(t, 100, 0, audit.EventPipelineStart)); n != 0 {
		t.Fatalf("restart must not emit a separate pipeline.start event, got %d", n)
	}
	if n := len(e.query(t, 100, 0, audit.EventPipelineStop)); n != 0 {
		t.Fatalf("restart must not emit a separate pipeline.stop event, got %d", n)
	}

	d := restarts[0].Detail
	if d[auditPipelineIDKey] != pipeID {
		t.Fatalf("restart audit pipeline_id = %q, want %q: %v", d[auditPipelineIDKey], pipeID, d)
	}
	// The combined `failed` counter is the SUM of both phases (2): one
	// stop-failure (A) + one start-failure (B).
	if d["failed"] != "2" {
		t.Fatalf("restart audit failed = %q, want \"2\": %v", d["failed"], d)
	}
	// The other bounded counters keep their existing semantics.
	if d["already_running"] != "1" {
		t.Fatalf("restart audit already_running = %q, want \"1\" (A): %v", d["already_running"], d)
	}
	if d["stopped"] != "1" {
		t.Fatalf("restart audit stopped = %q, want \"1\" (B stop no-op): %v", d["stopped"], d)
	}
	if _, ok := d["started"]; ok {
		t.Fatalf("restart audit must not carry a started counter (no entry started): %v", d)
	}
	// No instance/model identity leaks into the counters-only detail.
	for _, v := range d {
		if v == "fake-owned-a" || v == "pm-raf-a-model" || v == "bad-model" {
			t.Fatalf("restart audit detail leaked identity: %v", d)
		}
	}
}

// ADR 010 acceptance 10: model delete with a pipeline reference → 409,
// model survives; after the pipeline is deleted the model delete succeeds.
func TestPipelineAPI_ModelDeleteConflict(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pm-ref")

	if status, body := createPipelineAPI(t, e, addr, sess, csrf, `{"name":"refs","models":[{"model_id":"pm-ref"}]}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	pipelines, _ := e.repo.ListPipelines()
	pipeID := pipelines[0].ID

	rec := e.do(t, http.MethodDelete, "/api/v1/models/pm-ref", addr, sess, csrf, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("model delete status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.Code != "conflict" {
		t.Fatalf("error contract: %s", rec.Body.String())
	}
	if _, err := e.repo.GetModel("pm-ref"); err != nil {
		t.Fatalf("model must survive a refused delete: %v", err)
	}

	if rec = e.do(t, http.MethodDelete, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("pipeline delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodDelete, "/api/v1/models/pm-ref", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("model delete after pipeline removal = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ADR 010 acceptance 10+11: delete/structural-update with active owned
// instances → 409; non-structural update succeeds; after stop, delete.
func TestPipelineAPI_ActiveIntegrity(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pm-i1")
	e.seedGracefulModel(t, "pm-i2")

	status, body := createPipelineAPI(t, e, addr, sess, csrf, `{"name":"active","models":[{"model_id":"pm-i1"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	pipelines, _ := e.repo.ListPipelines()
	pipeID := pipelines[0].ID

	rec := e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/start", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d; body=%s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	})

	// Delete with active owned instances → 409.
	rec = e.do(t, http.MethodDelete, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Non-structural update (name) → 200.
	rec = e.do(t, http.MethodPut, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, `{"name":"renamed","models":[{"model_id":"pm-i1"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-structural update = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Structural update (add a model) → 409.
	rec = e.do(t, http.MethodPut, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, `{"name":"renamed","models":[{"model_id":"pm-i1"},{"model_id":"pm-i2"}]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("structural update status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// After stopping, structural update and delete succeed.
	rec = e.do(t, http.MethodPost, "/api/v1/pipelines/"+pipeID+"/stop", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop = %d", rec.Code)
	}
	rec = e.do(t, http.MethodPut, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, `{"name":"renamed","models":[{"model_id":"pm-i1"},{"model_id":"pm-i2"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("structural update after stop = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodDelete, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete after stop = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// Unknown pipeline IDs → 404 on all lifecycle/mutation endpoints.
func TestPipelineAPI_NotFound(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/pipelines/missing"},
		{http.MethodPut, "/api/v1/pipelines/missing"},
		{http.MethodDelete, "/api/v1/pipelines/missing"},
		{http.MethodPost, "/api/v1/pipelines/missing/start"},
		{http.MethodPost, "/api/v1/pipelines/missing/stop"},
		{http.MethodPost, "/api/v1/pipelines/missing/restart"},
	} {
		rec := e.do(t, tc.method, tc.path, addr, sess, csrf, `{}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

// ADR 010 acceptance 2: a valid create is persisted durably (the repository
// file carries the pipeline and a .bak backup exists after the write).
func TestPipelineAPI_CreateDurable(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "pm-dur")

	if status, body := createPipelineAPI(t, e, addr, sess, csrf, `{"name":"durable","models":[{"model_id":"pm-dur"}]}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}

	data, err := os.ReadFile(filepath.Join(e.dataDir, "repo.json"))
	if err != nil {
		t.Fatalf("read repository file: %v", err)
	}
	if !strings.Contains(string(data), "durable") {
		t.Fatal("repository file does not carry the created pipeline")
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "repo.json.bak")); err != nil {
		t.Fatalf("backup file missing after pipeline write: %v", err)
	}
}
