package handlers

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

func newResolveTestRouter(t *testing.T, dataDir string) (http.Handler, storage.Repository) {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	supervisor := process.NewSupervisor(repo)
	supervisor.SetDataDir(dataDir)
	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
		"static/style.css":     &fstest.MapFile{Data: []byte("body{}")},
		"static/i18n/en.json":  &fstest.MapFile{Data: []byte(`{}`)},
		"static/i18n/ru.json":  &fstest.MapFile{Data: []byte(`{}`)},
	}
	router := NewRouteRegistry(
		application.NewInstanceService(supervisor, repo),
		application.NewRuntimeService(repo),
		application.NewModelService(repo),
		application.NewPipelineService(supervisor, repo),
		supervisor,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(false),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
	).Build()
	return router, repo
}

func TestResolve_UndefinedVariable_Returns400(t *testing.T) {
	dataDir := t.TempDir()
	router, repo := newResolveTestRouter(t, dataDir)

	rte := &storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/usr/bin/server"}
	if err := repo.CreateRuntime(rte); err != nil {
		t.Fatal(err)
	}
	me := &storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"${DEFINITELY_MISSING_VAR_ABC123}"}}
	if err := repo.CreateModel(me); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/v1/models/m1/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errResp.Error, "undefined variable") {
		t.Fatalf("error does not mention undefined variable: %q", errResp.Error)
	}
	if !strings.Contains(errResp.Error, "DEFINITELY_MISSING_VAR_ABC123") {
		t.Fatalf("error does not name the variable: %q", errResp.Error)
	}
}

func TestResolve_MalformedVariable_Returns400(t *testing.T) {
	dataDir := t.TempDir()
	router, repo := newResolveTestRouter(t, dataDir)

	rte := &storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/usr/bin/server"}
	if err := repo.CreateRuntime(rte); err != nil {
		t.Fatal(err)
	}
	me := &storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"${123BAD}"}}
	if err := repo.CreateModel(me); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/v1/models/m1/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errResp.Error, "invalid variable reference") {
		t.Fatalf("error does not mention invalid variable reference: %q", errResp.Error)
	}
}

func TestResolve_Success_WithVariables(t *testing.T) {
	dataDir := t.TempDir()
	router, repo := newResolveTestRouter(t, dataDir)

	rte := &storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "${GOAL_DATA}/bin/server"}
	if err := repo.CreateRuntime(rte); err != nil {
		t.Fatal(err)
	}
	me := &storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "${GOAL_DATA}/model.gguf"}}
	if err := repo.CreateModel(me); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/v1/models/m1/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var result struct {
		Executable string   `json:"executable"`
		Args       []string `json:"args"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Executable != dataDir+"/bin/server" {
		t.Fatalf("executable = %q, want %q", result.Executable, dataDir+"/bin/server")
	}
	if len(result.Args) < 2 || result.Args[1] != dataDir+"/model.gguf" {
		t.Fatalf("args = %v, want args[1]=%q", result.Args, dataDir+"/model.gguf")
	}
}

func TestResolve_NotFound_Still404(t *testing.T) {
	dataDir := t.TempDir()
	router, _ := newResolveTestRouter(t, dataDir)

	req := httptest.NewRequest("POST", "/api/v1/models/nonexistent/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}
