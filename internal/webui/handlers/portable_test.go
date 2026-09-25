package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/application/portable"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

func newPortableTestRouter(t *testing.T) (http.Handler, storage.Repository) {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	router := NewRouteRegistry(
		nil, nil, nil, nil, nil,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(false),
	).Build()
	return router, repo
}

func seedExportData(t *testing.T, repo storage.Repository) {
	t.Helper()
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT1", Executable: "/bin/a", Environment: map[string]string{"TOKEN": "runtime-secret"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt2", Name: "RT2", Executable: "/bin/b"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "M1", RuntimeID: "rt1", Args: []string{"--api-key=visible-by-contract"}, Environment: map[string]string{"API_KEY": "model-secret"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePipeline(&storage.PipelineEntry{ID: "p1", Name: "P1", Active: true, Models: []domain.PipelineModel{{ID: "e1", ModelID: "m1", AutoStart: true}}}); err != nil {
		t.Fatal(err)
	}
}

// --- EXPORT TESTS ---

func TestExport_All(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := resp.Header().Get("Content-Disposition"); cd != `attachment; filename="goal-portable-config.json"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	var bundle portable.Bundle
	if err := json.Unmarshal(resp.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Format != "goal-portable-config" || bundle.Version != 1 {
		t.Fatalf("format=%q version=%d", bundle.Format, bundle.Version)
	}
	if len(bundle.Runtimes) != 2 || len(bundle.Models) != 1 || len(bundle.Pipelines) != 1 {
		t.Fatalf("counts: rt=%d m=%d p=%d", len(bundle.Runtimes), len(bundle.Models), len(bundle.Pipelines))
	}
}

func TestExport_RuntimeRoot(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?runtime_id=rt1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var bundle portable.Bundle
	json.Unmarshal(resp.Body.Bytes(), &bundle)
	if len(bundle.Runtimes) != 1 || bundle.Runtimes[0].ID != "rt1" {
		t.Fatalf("expected only rt1, got %+v", bundle.Runtimes)
	}
	// Runtime root does not pull in dependent models (closure = dependencies, not dependents).
	if len(bundle.Models) != 0 {
		t.Fatalf("runtime root should not include models, got %+v", bundle.Models)
	}
}

func TestExport_ModelRoot(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?model_id=m1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var bundle portable.Bundle
	json.Unmarshal(resp.Body.Bytes(), &bundle)
	if len(bundle.Runtimes) != 1 || bundle.Runtimes[0].ID != "rt1" {
		t.Fatalf("closure: expected rt1, got %+v", bundle.Runtimes)
	}
	if len(bundle.Models) != 1 || bundle.Models[0].ID != "m1" {
		t.Fatalf("expected m1, got %+v", bundle.Models)
	}
}

func TestExport_PipelineRoot(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?pipeline_id=p1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var bundle portable.Bundle
	json.Unmarshal(resp.Body.Bytes(), &bundle)
	if len(bundle.Pipelines) != 1 || bundle.Pipelines[0].ID != "p1" {
		t.Fatalf("expected p1, got %+v", bundle.Pipelines)
	}
}

func TestExport_UnknownRoot(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?runtime_id=nonexistent", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
}

func TestExport_TwoRoots(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?runtime_id=rt1&model_id=m1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestExport_EmptySelector(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	for _, query := range []string{
		"?runtime_id=",
		"?model_id=",
		"?pipeline_id=",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/export"+query, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", query, resp.Code)
		}
	}
}

func TestExport_RepeatedSelector(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?runtime_id=rt1&runtime_id=rt2", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for repeated param", resp.Code)
	}
}

// --- EXPORT SECURITY TESTS ---

func TestExport_EnvironmentValuesNotLeaked(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	body := resp.Body.String()

	if strings.Contains(body, "runtime-secret") {
		t.Fatal("runtime environment value leaked in export")
	}
	if strings.Contains(body, "model-secret") {
		t.Fatal("model environment value leaked in export")
	}
	if !strings.Contains(body, "TOKEN") {
		t.Fatal("environment_keys missing TOKEN")
	}
	if !strings.Contains(body, "API_KEY") {
		t.Fatal("environment_keys missing API_KEY")
	}
}

func TestExport_ArgsExportedUnchanged(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	seedExportData(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	body := resp.Body.String()
	if !strings.Contains(body, "--api-key=visible-by-contract") {
		t.Fatal("Args not exported unchanged")
	}
}

// --- IMPORT TESTS ---

func validBundle() []byte {
	return []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt-new","name":"NewRT","executable":"/bin/new"}],"models":[],"pipelines":[]}`)
}

func TestImport_ValidReal(t *testing.T) {
	router, repo := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var result map[string]any
	json.Unmarshal(resp.Body.Bytes(), &result)
	if result["dry_run"] != false {
		t.Fatalf("dry_run = %v", result["dry_run"])
	}
	if result["runtimes"].(float64) != 1 {
		t.Fatalf("runtimes = %v", result["runtimes"])
	}

	if _, err := repo.GetRuntime("rt-new"); err != nil {
		t.Fatal("imported runtime not found")
	}
}

func TestImport_DryRun(t *testing.T) {
	router, repo := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var result map[string]any
	json.Unmarshal(resp.Body.Bytes(), &result)
	if result["dry_run"] != true {
		t.Fatalf("dry_run = %v", result["dry_run"])
	}
	if result["runtimes"].(float64) != 1 {
		t.Fatalf("runtimes = %v", result["runtimes"])
	}

	// Zero mutation.
	if _, err := repo.GetRuntime("rt-new"); err == nil {
		t.Fatal("dry-run should not create entities")
	}
}

func TestImport_MalformedJSON(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", strings.NewReader("{invalid"))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_WrongFormat(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := []byte(`{"format":"wrong-format","version":1,"runtimes":[],"models":[],"pipelines":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_UnsupportedVersion(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := []byte(`{"format":"goal-portable-config","version":99,"runtimes":[],"models":[],"pipelines":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_MissingDependency(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[],"models":[{"id":"m1","name":"M","runtime_id":"nonexistent"}],"pipelines":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_ExistingIDSkipped_NoOverwrite(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-new", Name: "Existing", Executable: "/bin/existing"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	body := decodeImportBody(t, resp)
	if body.Created.Runtimes != 0 || body.Skipped.Runtimes != 1 {
		t.Fatalf("created/skipped = %+v / %+v", body.Created, body.Skipped)
	}
	if body.CanImport {
		t.Fatal("nothing-to-import must not report can_import")
	}
	if body.Runtimes != 0 {
		t.Fatalf("flat runtimes must be the created count, got %v", body.Runtimes)
	}

	rt, _ := repo.GetRuntime("rt-new")
	if rt.Name != "Existing" || rt.Executable != "/bin/existing" {
		t.Fatalf("existing runtime overwritten: %+v", rt)
	}
}

func TestImport_EmptyBody(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_TrailingGarbage(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := validBundle()
	body = append(body, []byte(`{"extra":true}`)...)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for trailing data", resp.Code)
	}
}

func TestImport_UnknownTopLevelField(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[],"models":[],"pipelines":[],"unknown_field":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown field: %s", resp.Code, resp.Body.String())
	}
}

func TestImport_UnknownNestedField(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt1","name":"R","executable":"/bin/x","bogus_field":1}],"models":[],"pipelines":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown nested field: %s", resp.Code, resp.Body.String())
	}
}

func TestImport_MalformedVariable(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	body := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt1","name":"R","executable":"/bin/${1BAD}"}],"models":[],"pipelines":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed variable: %s", resp.Code, resp.Body.String())
	}
}

func TestImport_UndefinedVariable_Accepted(t *testing.T) {
	router, repo := newPortableTestRouter(t)

	body := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt1","name":"R","executable":"/bin/${GOAL_PORTABLE_UNDEFINED_VAR}"}],"models":[],"pipelines":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (undefined variable accepted): %s", resp.Code, resp.Body.String())
	}

	// Raw ${VAR} remains stored unchanged.
	rt, err := repo.GetRuntime("rt1")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Executable != "/bin/${GOAL_PORTABLE_UNDEFINED_VAR}" {
		t.Fatalf("executable = %q, want raw reference preserved", rt.Executable)
	}
}

func TestImport_Oversize(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	// 10 MiB + 1 byte of 'a' (invalid JSON but size check must fire first).
	body := make([]byte, importMaxBodyBytes+1)
	for i := range body {
		body[i] = 'a'
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.Code)
	}
}

func TestImport_ExactBoundary(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	// Exactly 10 MiB: not rejected by size. Will fail JSON validation with 400.
	body := make([]byte, importMaxBodyBytes)
	for i := range body {
		body[i] = 'a'
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code == http.StatusRequestEntityTooLarge {
		t.Fatal("exact 10 MiB body rejected as oversize")
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid JSON at boundary)", resp.Code)
	}
}

func TestImport_DryRunMalformed(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=yes", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_DryRunDuplicate(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true&dry_run=false", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestImport_DryRunEmpty(t *testing.T) {
	router, _ := newPortableTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

// --- ATOMICITY THROUGH HTTP ---

type failingImportRepo struct {
	storage.Repository
}

func (f *failingImportRepo) ImportGraph(runtimes []*storage.RuntimeEntry, models []*storage.ModelEntry, pipelines []*storage.PipelineEntry) (*storage.ImportGraphPlan, error) {
	return nil, fmt.Errorf("injected persistence failure")
}

func TestImport_PersistenceFailure_HTTP500(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Seed state A.
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "Original", Executable: "/bin/orig"}); err != nil {
		t.Fatal(err)
	}

	failRepo := &failingImportRepo{Repository: repo}
	router := NewRouteRegistry(
		nil, nil, nil, nil, nil,
		failRepo,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(false),
	).Build()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", resp.Code, resp.Body.String())
	}

	// Imported entity absent (real repo unaffected).
	if _, err := repo.GetRuntime("rt-new"); err == nil {
		t.Fatal("imported entity visible after persistence failure")
	}
	// Original state intact.
	rt, _ := repo.GetRuntime("rt1")
	if rt.Name != "Original" {
		t.Fatalf("original modified: %q", rt.Name)
	}
}

// --- AUTH/CSRF TESTS ---

func TestExport_AuthRequired(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouteRegistry(
		nil, nil, nil, nil, nil,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(true),
	).Build()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}

func TestImport_AuthRequired(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouteRegistry(
		nil, nil, nil, nil, nil,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(true),
	).Build()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}

func TestImport_CSRFRequired(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	passwords := security.NewPasswordStore()
	if err := passwords.SetPassword("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	sessionStore := security.NewSessionStore()
	csrf := security.NewCSRF()
	router := NewRouteRegistry(
		nil, nil, nil, nil, nil,
		repo,
		csrf,
		sessionStore,
		passwords,
		WithAuthEnabled(true),
	).Build()

	// Login first.
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp := httptest.NewRecorder()
	router.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login failed: %d", loginResp.Code)
	}
	var loginBody map[string]string
	json.Unmarshal(loginResp.Body.Bytes(), &loginBody)
	csrfToken := loginBody["csrf_token"]
	cookies := loginResp.Result().Cookies()

	// Import without CSRF header.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF): %s", resp.Code, resp.Body.String())
	}

	// Import with CSRF header.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	req2.Header.Set("X-CSRF-Token", csrfToken)
	resp2 := httptest.NewRecorder()
	router.ServeHTTP(resp2, req2)

	if resp2.Code != http.StatusOK {
		t.Fatalf("with CSRF: status = %d, want 200: %s", resp2.Code, resp2.Body.String())
	}
}
