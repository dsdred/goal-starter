package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/webui/audit"
)

// ADR 007 entity-CRUD audit extension (accepted design 2026-09-16): 13 new
// success-only events for model/runtime/pipeline CRUD with bounded,
// secret-safe detail. Reuses the auditEnv harness from
// audit_integration_test.go.

// Sentinel values: none of these substrings may ever appear in the audit
// file after exercising the extended taxonomy (secret-safety contract).
const (
	entitySentinelModelName = "SENTINEL-NAME-MODEL-7c2f"
	entitySentinelArg       = "SENTINEL-ARGS-VALUE-9a1d"
	entitySentinelEnvValue  = "SENTINEL-ENV-VALUE-4e8b"
	entitySentinelExec      = "SENTINEL-EXECUTABLE-PATH-3f6a"
	entitySentinelWorkDir   = "SENTINEL-WORKDIR-5b9c"
	entitySentinelPipeName  = "SENTINEL-NAME-PIPELINE-d41e"
	entitySentinelPipeArg   = "SENTINEL-PIPE-ARG-77aa"
	entitySentinelRTName    = "SENTINEL-NAME-RUNTIME-b3e5"
)

func entitySentinels() []string {
	return []string{
		entitySentinelModelName,
		entitySentinelArg,
		entitySentinelEnvValue,
		entitySentinelExec,
		entitySentinelWorkDir,
		entitySentinelPipeName,
		entitySentinelPipeArg,
		entitySentinelRTName,
	}
}

// auditEventsByName returns the audit events of the given name, newest
// first (query order), from the audit file.
func (e *auditEnv) eventsByName(t *testing.T, event string) []audit.AuditEvent {
	t.Helper()
	return e.query(t, 1000, 0, event)
}

// fetchPipelineEntryID reads the stored pipeline and returns the
// server-assigned id of its first entry (ADR 013 D1: client entry ids are
// dropped on create).
func fetchPipelineEntryID(t *testing.T, env *auditEnv, addr, sess, csrf, pipeID string) string {
	t.Helper()
	rec := env.do(t, http.MethodGet, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline get status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Detail shape: {"pipeline": {...entries...}, "models": [live statuses]}.
	var out struct {
		Pipeline struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"pipeline"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal pipeline response: %v", err)
	}
	if len(out.Pipeline.Models) != 1 || out.Pipeline.Models[0].ID == "" {
		t.Fatalf("pipeline response entries = %d, want 1 with a non-empty entry id", len(out.Pipeline.Models))
	}
	return out.Pipeline.Models[0].ID
}

// assertDetailExact verifies the event's detail map equals want exactly
// (no extra keys, no missing keys, exact values).
func assertDetailExact(t *testing.T, got audit.AuditEvent, want map[string]string) {
	t.Helper()
	if len(got.Detail) != len(want) {
		t.Fatalf("detail = %v, want exactly %v", got.Detail, want)
	}
	for k, v := range want {
		if got.Detail[k] != v {
			t.Fatalf("detail[%q] = %q, want %q (full detail: %v)", k, got.Detail[k], v, got.Detail)
		}
	}
}

func TestModelCRUDAuditEvents(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.1:5000")

	// create with sentinel args/environment values
	body := `{"id":"m-audit-1","name":"` + entitySentinelModelName + `","runtime_id":"rt-none","args":["--flag","` + entitySentinelArg + `` + `"],"environment":{"KEY":"` + entitySentinelEnvValue + `"}}`
	rec := env.do(t, http.MethodPost, "/api/v1/models", "10.0.0.1:5000", sess, csrf, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	evs := env.eventsByName(t, audit.EventModelCreate)
	if len(evs) != 1 {
		t.Fatalf("model.create events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": "m-audit-1"})
	if evs[0].User != "admin" {
		t.Errorf("model.create user = %q, want admin", evs[0].User)
	}
	if evs[0].SourceIP == "" {
		t.Error("model.create src_ip is empty")
	}

	// update: args + environment + autostart_delay changed
	upd := `{"args":["--other"],"environment_patch":[{"key":"KEY","action":"set","value":"another-secret-value-1"}],"autostart_delay":5}`
	rec = env.do(t, http.MethodPut, "/api/v1/models/m-audit-1", "10.0.0.1:5000", sess, csrf, upd)
	if rec.Code != http.StatusOK {
		t.Fatalf("model update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventModelUpdate)
	if len(evs) != 1 {
		t.Fatalf("model.update events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{
		"id":              "m-audit-1",
		"args":            "changed",
		"environment":     "changed",
		"autostart_delay": "changed",
	})

	// update with no fields: no event
	rec = env.do(t, http.MethodPut, "/api/v1/models/m-audit-1", "10.0.0.1:5000", sess, csrf, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op model update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if evs = env.eventsByName(t, audit.EventModelUpdate); len(evs) != 1 {
		t.Fatalf("model.update events after no-op = %d, want 1 (no event emitted)", len(evs))
	}

	// delete
	rec = env.do(t, http.MethodDelete, "/api/v1/models/m-audit-1", "10.0.0.1:5000", sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("model delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if evs = env.eventsByName(t, audit.EventModelDelete); len(evs) != 1 {
		t.Fatalf("model.delete events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": "m-audit-1"})

	// rejected mutations: no events
	before := len(env.query(t, 1000, 0, ""))
	env.do(t, http.MethodPost, "/api/v1/models", "10.0.0.1:5000", sess, csrf, `{invalid json`) // 400
	rec = env.do(t, http.MethodDelete, "/api/v1/models/does-not-exist", "10.0.0.1:5000", sess, csrf, "")
	if rec.Code == http.StatusOK {
		t.Fatalf("delete missing returned 200; want a rejection")
	}
	after := len(env.query(t, 1000, 0, ""))
	if after != before {
		t.Fatalf("audit events changed on rejected mutations: before=%d after=%d", before, after)
	}
}

func TestModelActivateDeactivateAudit(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.2:5000")

	body := `{"id":"m-audit-act","name":"autostart-target","runtime_id":"rt-none"}`
	if rec := env.do(t, http.MethodPost, "/api/v1/models", "10.0.0.2:5000", sess, csrf, body); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec := env.do(t, http.MethodPost, "/api/v1/models/m-audit-act/activate", "10.0.0.2:5000", sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs := env.eventsByName(t, audit.EventModelActivate)
	if len(evs) != 1 {
		t.Fatalf("model.activate events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": "m-audit-act", "active": "true"})

	rec = env.do(t, http.MethodPost, "/api/v1/models/m-audit-act/deactivate", "10.0.0.2:5000", sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventModelDeactivate)
	if len(evs) != 1 {
		t.Fatalf("model.deactivate events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": "m-audit-act", "active": "false"})

	// 404: no event
	before := len(env.query(t, 1000, 0, ""))
	env.do(t, http.MethodPost, "/api/v1/models/missing/activate", "10.0.0.2:5000", sess, csrf, "")
	if after := len(env.query(t, 1000, 0, "")); after != before {
		t.Fatalf("audit events changed on 404 activate: before=%d after=%d", before, after)
	}
}

func TestRuntimeCRUDAuditEvents(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.3:5000")
	const addr = "10.0.0.3:5000"

	// create with sentinel executable/working directory/environment
	body := `{"id":"rt-audit-1","name":"` + entitySentinelRTName + `","executable":"` + entitySentinelExec + `","working_directory":"` + entitySentinelWorkDir + `","environment":{"TOKEN":"` + entitySentinelEnvValue + `"}}`
	rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("runtime create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	evs := env.eventsByName(t, audit.EventRuntimeCreate)
	if len(evs) != 1 {
		t.Fatalf("runtime.create events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": "rt-audit-1"})
	if evs[0].User != "admin" {
		t.Errorf("runtime.create user = %q, want admin", evs[0].User)
	}

	// update: executable + environment changed
	upd := `{"executable":"C:/other/llama-server.exe","environment_patch":[{"key":"TOKEN","action":"set","value":"new-secret-9"}]}`
	rec = env.do(t, http.MethodPut, "/api/v1/runtimes/rt-audit-1", addr, sess, csrf, upd)
	if rec.Code != http.StatusOK {
		t.Fatalf("runtime update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventRuntimeUpdate)
	if len(evs) != 1 {
		t.Fatalf("runtime.update events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{
		"id":          "rt-audit-1",
		"executable":  "changed",
		"environment": "changed",
	})

	// delete while in use: 409, no event
	modelBody := `{"id":"m-rt-1","name":"ref-model","runtime_id":"rt-audit-1"}`
	if rec = env.do(t, http.MethodPost, "/api/v1/models", addr, sess, csrf, modelBody); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	rec = env.do(t, http.MethodDelete, "/api/v1/runtimes/rt-audit-1", addr, sess, csrf, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("in-use runtime delete status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if evs = env.eventsByName(t, audit.EventRuntimeDelete); len(evs) != 0 {
		t.Fatalf("runtime.delete events on 409 = %d, want 0", len(evs))
	}

	// cascade-delete: runtime + referencing model
	rec = env.do(t, http.MethodPost, "/api/v1/runtimes/rt-audit-1/cascade-delete", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("cascade-delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventRuntimeCascadeDel)
	if len(evs) != 1 {
		t.Fatalf("runtime.cascade_delete events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": "rt-audit-1", "models_deleted": "1"})

	// delete missing: 404, no event
	before := len(env.query(t, 1000, 0, ""))
	env.do(t, http.MethodDelete, "/api/v1/runtimes/missing", addr, sess, csrf, "")
	if after := len(env.query(t, 1000, 0, "")); after != before {
		t.Fatalf("audit events changed on 404 delete: before=%d after=%d", before, after)
	}
}

func TestRuntimeReplaceAudit(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.4:5000")
	const addr = "10.0.0.4:5000"

	rtBody := `{"id":"rt-src","name":"src-runtime","executable":"C:/llama/llama-server.exe"}`
	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf, rtBody); rec.Code != http.StatusCreated {
		t.Fatalf("runtime create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	modelBody := `{"id":"m-move","name":"moved-model","runtime_id":"rt-src"}`
	if rec := env.do(t, http.MethodPost, "/api/v1/models", addr, sess, csrf, modelBody); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// destination runtime
	rt2Body := `{"id":"rt-dst","name":"dst-runtime","executable":"C:/llama2/llama-server.exe"}`
	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf, rt2Body); rec.Code != http.StatusCreated {
		t.Fatalf("runtime2 create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec := env.do(t, http.MethodPost, "/api/v1/runtimes/rt-src/replace", addr, sess, csrf, `{"new_runtime_id":"rt-dst"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs := env.eventsByName(t, audit.EventRuntimeReplace)
	if len(evs) != 1 {
		t.Fatalf("runtime.replace events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{
		"id":             "rt-src",
		"new_runtime_id": "rt-dst",
		"models_moved":   "1",
	})

	// rejected replace: unknown destination -> 404, no event
	before := len(env.query(t, 1000, 0, ""))
	env.do(t, http.MethodPost, "/api/v1/runtimes/rt-dst/replace", addr, sess, csrf, `{"new_runtime_id":"no-such-rt"}`)
	if after := len(env.query(t, 1000, 0, "")); after != before {
		t.Fatalf("audit events changed on rejected replace: before=%d after=%d", before, after)
	}
}

func TestPipelineCRUDAuditEvents(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.5:5000")
	const addr = "10.0.0.5:5000"

	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf, `{"id":"rt-pipe","name":"pipe-rt","executable":"C:/llama/llama-server.exe"}`); rec.Code != http.StatusCreated {
		t.Fatalf("runtime create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/models", addr, sess, csrf, `{"id":"m-pipe","name":"pipe-model","runtime_id":"rt-pipe"}`); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// Entry ids are server-assigned on create (ADR 013 D1): the client id is
	// dropped, so the update bodies must use the stored entry id.
	pipeBody := `{"name":"` + entitySentinelPipeName + `","active":true,"models":[{"model_id":"m-pipe","args":["--pipe","` + entitySentinelPipeArg + `"],"auto_start":false}]}`
	rec := env.do(t, http.MethodPost, "/api/v1/pipelines", addr, sess, csrf, pipeBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pipeline create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	evs := env.eventsByName(t, audit.EventPipelineCreate)
	if len(evs) != 1 {
		t.Fatalf("pipeline.create events = %d, want 1", len(evs))
	}
	if evs[0].Detail["entries"] != "1" {
		t.Errorf("pipeline.create entries = %q, want \"1\" (detail: %v)", evs[0].Detail["entries"], evs[0].Detail)
	}
	if evs[0].Detail["id"] == "" {
		t.Error("pipeline.create id is empty")
	}
	pipeID := evs[0].Detail["id"]
	entryID := fetchPipelineEntryID(t, env, addr, sess, csrf, pipeID)

	// update: entry args changed -> models:"changed"
	updBody := `{"name":"` + entitySentinelPipeName + `","active":true,"models":[{"id":"` + entryID + `","model_id":"m-pipe","args":["--changed-arg"],"auto_start":false}]}`
	rec = env.do(t, http.MethodPut, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, updBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventPipelineUpdate)
	if len(evs) != 1 {
		t.Fatalf("pipeline.update events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": pipeID, "models": "changed"})

	// update with identical content: no event
	rec = env.do(t, http.MethodPut, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, updBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline no-op update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if evs = env.eventsByName(t, audit.EventPipelineUpdate); len(evs) != 1 {
		t.Fatalf("pipeline.update events after no-op = %d, want 1 (no event emitted)", len(evs))
	}

	// update with only the name changed
	nameOnly := `{"name":"renamed-pipeline","active":true,"models":[{"id":"` + entryID + `","model_id":"m-pipe","args":["--changed-arg"],"auto_start":false}]}`
	rec = env.do(t, http.MethodPut, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, nameOnly)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline name update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventPipelineUpdate)
	if len(evs) != 2 {
		t.Fatalf("pipeline.update events = %d, want 2", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": pipeID, "name": "changed"})

	// delete
	rec = env.do(t, http.MethodDelete, "/api/v1/pipelines/"+pipeID, addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	evs = env.eventsByName(t, audit.EventPipelineDelete)
	if len(evs) != 1 {
		t.Fatalf("pipeline.delete events = %d, want 1", len(evs))
	}
	assertDetailExact(t, evs[0], map[string]string{"id": pipeID})

	// delete missing: 404, no event
	before := len(env.query(t, 1000, 0, ""))
	env.do(t, http.MethodDelete, "/api/v1/pipelines/missing", addr, sess, csrf, "")
	if after := len(env.query(t, 1000, 0, "")); after != before {
		t.Fatalf("audit events changed on 404 pipeline delete: before=%d after=%d", before, after)
	}
}

// TestNoModelRuntimeLifecycleDuplication proves that model-page start/stop/
// restart and runtime action/* receive NO new audit events: the instance.*
// trail is the sole process-lifecycle record (accepted design §10).
func TestNoModelRuntimeLifecycleDuplication(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.6:5000")
	const addr = "10.0.0.6:5000"

	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf, `{"id":"rt-nodup","name":"nodup-rt","executable":"C:/llama/llama-server.exe"}`); rec.Code != http.StatusCreated {
		t.Fatalf("runtime create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/models", addr, sess, csrf, `{"id":"m-nodup","name":"nodup-model","runtime_id":"rt-nodup"}`); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// runtime.create + model.create are the only events so far
	if n := len(env.query(t, 1000, 0, "")); n != 3 { // login + runtime.create + model.create
		t.Fatalf("baseline audit events = %d, want 3", n)
	}

	// model stop/restart with no running instance: 200, zero new events
	if rec := env.do(t, http.MethodPost, "/api/v1/models/m-nodup/stop", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("model stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/models/m-nodup/restart", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("model restart status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// runtime action stop with no running instance: 404
	env.do(t, http.MethodPost, "/api/v1/runtimes/rt-nodup/action/stop", addr, sess, csrf, "")

	if n := len(env.query(t, 1000, 0, "")); n != 3 {
		t.Fatalf("audit events after lifecycle ops = %d, want 3 (no duplication)", n)
	}
	for _, name := range []string{"model.start", "model.stop", "model.restart", "runtime.action"} {
		if evs := env.query(t, 100, 0, name); len(evs) != 0 {
			t.Fatalf("forbidden event %q was emitted (%d)", name, len(evs))
		}
	}
}

// TestEntityAuditSecretSentinel is the mandatory secret-safety proof: after
// exercising the full extended taxonomy with sentinel values in names, args,
// environment values, executable, working directory, and pipeline entry
// args, none of those substrings may appear in the serialized audit file.
func TestEntityAuditSecretSentinel(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.7:5000")
	const addr = "10.0.0.7:5000"

	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf,
		`{"id":"rt-s","name":"`+entitySentinelRTName+`","executable":"`+entitySentinelExec+`","working_directory":"`+entitySentinelWorkDir+`","environment":{"API_TOKEN":"`+entitySentinelEnvValue+`"}}`); rec.Code != http.StatusCreated {
		t.Fatalf("runtime create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/models", addr, sess, csrf,
		`{"id":"m-s","name":"`+entitySentinelModelName+`","runtime_id":"rt-s","args":["--chat-template-kwargs","{\"key\":\"`+entitySentinelArg+`\"}"],"environment":{"ENV_VAR":"`+entitySentinelEnvValue+`"},"active":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/models/m-s/activate", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPut, "/api/v1/models/m-s", addr, sess, csrf,
		`{"args":["--replaced","`+entitySentinelArg+`"],"environment_patch":[{"key":"ENV_VAR","action":"delete"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("model update status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/pipelines", addr, sess, csrf,
		`{"name":"`+entitySentinelPipeName+`","active":false,"models":[{"id":"e-s","model_id":"m-s","args":["--pipe","`+entitySentinelPipeArg+`"],"auto_start":false}]}`); rec.Code != http.StatusCreated {
		t.Fatalf("pipeline create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes/rt-s/cascade-delete", addr, sess, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("cascade-delete status = %d, body=%s", rec.Code, rec.Body.String())
	}

	raw := env.auditRaw(t)
	for _, s := range entitySentinels() {
		if strings.Contains(raw, s) {
			t.Fatalf("SECRET SENTINEL LEAK: %q found in audit file", s)
		}
	}
}

// TestEntityAuditQueryMixedTaxonomy proves the query endpoint returns old
// and new events together, honors the exact event filter, and rejects
// non-exact names.
func TestEntityAuditQueryMixedTaxonomy(t *testing.T) {
	env := newAuditEnv(t, 0)
	sess, csrf := env.loggedIn(t, "10.0.0.8:5000")
	const addr = "10.0.0.8:5000"

	if rec := env.do(t, http.MethodPost, "/api/v1/models", addr, sess, csrf, `{"id":"m-q","name":"q-model","runtime_id":"rt-q"}`); rec.Code != http.StatusCreated {
		t.Fatalf("model create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/runtimes", addr, sess, csrf, `{"id":"rt-q","name":"q-rt","executable":"C:/x.exe"}`); rec.Code != http.StatusCreated {
		t.Fatalf("runtime create status = %d, body=%s", rec.Code, rec.Body.String())
	}

	all := env.query(t, 1000, 0, "")
	names := map[string]bool{}
	for _, e := range all {
		names[e.Event] = true
	}
	// old taxonomy + new taxonomy present in one query
	for _, want := range []string{audit.EventLoginSuccess, audit.EventModelCreate, audit.EventRuntimeCreate} {
		if !names[want] {
			t.Fatalf("mixed query missing %q; present: %v", want, names)
		}
	}
	// newest first
	if all[0].Event != audit.EventRuntimeCreate {
		t.Errorf("newest event = %q, want %q", all[0].Event, audit.EventRuntimeCreate)
	}
	// exact filter
	if evs := env.query(t, 100, 0, audit.EventModelCreate); len(evs) != 1 || evs[0].Event != audit.EventModelCreate {
		t.Fatalf("exact filter model.create = %d events, want 1", len(evs))
	}
	// non-exact name matches nothing
	if evs := env.query(t, 100, 0, "model.crea"); len(evs) != 0 {
		t.Fatalf("non-exact filter matched %d events, want 0", len(evs))
	}
}
