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
	hCommands := NewCommandsHandler(insSvc, repo)
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/execute", commandBody("model.stop", "mC"))
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

	return NewCommandsHandler(application.NewInstanceService(sup, repo), repo)
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
