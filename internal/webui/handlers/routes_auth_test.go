package handlers

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

func newAuthenticatedTestRouter(t *testing.T) http.Handler {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	supervisor := process.NewSupervisor(repo)
	passwords := security.NewPasswordStore()
	if err := passwords.SetPassword("admin", "secret"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>GoAl</title>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
	}
	return NewRouteRegistry(
		application.NewInstanceService(supervisor, repo),
		application.NewRuntimeService(repo),
		application.NewModelService(repo),
		application.NewPipelineService(supervisor, repo),
		supervisor,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		passwords,
		WithAuthEnabled(true),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
	).Build()
}

func TestAuthenticatedBrowserWorkflow(t *testing.T) {
	router := newAuthenticatedTestRouter(t)

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexResp := httptest.NewRecorder()
	router.ServeHTTP(indexResp, indexReq)
	if indexResp.Code != http.StatusOK {
		t.Fatalf("login page status = %d, want 200", indexResp.Code)
	}

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
	csrfToken := loginBody["csrf_token"]
	if csrfToken == "" {
		t.Fatal("login response did not include csrf_token")
	}
	cookies := loginResp.Result().Cookies()
	if len(cookies) < 2 {
		t.Fatalf("login set %d cookies, want session and CSRF", len(cookies))
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	for _, cookie := range cookies {
		sessionReq.AddCookie(cookie)
	}
	sessionResp := httptest.NewRecorder()
	router.ServeHTTP(sessionResp, sessionReq)
	if sessionResp.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200", sessionResp.Code)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewBufferString(`{"name":"runtime","executable":"runtime.exe"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-CSRF-Token", csrfToken)
	for _, cookie := range cookies {
		createReq.AddCookie(cookie)
	}
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("authenticated create status = %d, want 201; body=%s", createResp.Code, createResp.Body.String())
	}

	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewBufferString(`{"name":"blocked","executable":"runtime.exe"}`))
	for _, cookie := range cookies {
		noCSRFReq.AddCookie(cookie)
	}
	noCSRFResp := httptest.NewRecorder()
	router.ServeHTTP(noCSRFResp, noCSRFReq)
	if noCSRFResp.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d, want 403", noCSRFResp.Code)
	}
}
