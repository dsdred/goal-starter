package handlers

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
	"testing/fstest"
)

func newCacheTestRouter(t *testing.T) http.Handler {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	supervisor := process.NewSupervisor(repo)
	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>GoAl</title>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
		"static/style.css":     &fstest.MapFile{Data: []byte("body{}")},
		"static/i18n/en.json":  &fstest.MapFile{Data: []byte(`{"hello":"Hello"}`)},
		"static/i18n/ru.json":  &fstest.MapFile{Data: []byte(`{"hello":"Привет"}`)},
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
		security.NewPasswordStore(),
		WithAuthEnabled(false),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
	).Build()
}

func TestCachePolicy_IndexPage(t *testing.T) {
	router := newCacheTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	got := rec.Header().Get("Cache-Control")
	if got != "no-cache" {
		t.Errorf("GET / Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestCachePolicy_StaticAppJS(t *testing.T) {
	router := newCacheTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.js status = %d, want 200", rec.Code)
	}
	got := rec.Header().Get("Cache-Control")
	if got != "no-store" {
		t.Errorf("GET /static/app.js Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestCachePolicy_StaticCSS(t *testing.T) {
	router := newCacheTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css status = %d, want 200", rec.Code)
	}
	got := rec.Header().Get("Cache-Control")
	if got != "no-store" {
		t.Errorf("GET /static/style.css Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestCachePolicy_StaticI18nEN(t *testing.T) {
	router := newCacheTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/static/i18n/en.json", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/i18n/en.json status = %d, want 200", rec.Code)
	}
	got := rec.Header().Get("Cache-Control")
	if got != "no-store" {
		t.Errorf("GET /static/i18n/en.json Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestCachePolicy_StaticI18nRU(t *testing.T) {
	router := newCacheTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/static/i18n/ru.json", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/i18n/ru.json status = %d, want 200", rec.Code)
	}
	got := rec.Header().Get("Cache-Control")
	if got != "no-store" {
		t.Errorf("GET /static/i18n/ru.json Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestCachePolicy_APIEndpoints_NoStore(t *testing.T) {
	router := newCacheTestRouter(t)
	for _, path := range []string{
		"/api/v1/health",
		"/api/v1/version",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		got := rec.Header().Get("Cache-Control")
		if got != "no-store, private" {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, got, "no-store, private")
		}
	}
}

func TestCachePolicy_APIListEndpoints_NoStore(t *testing.T) {
	router := newCacheTestRouter(t)
	for _, path := range []string{
		"/api/v1/models",
		"/api/v1/runtimes",
		"/api/v1/instances",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		got := rec.Header().Get("Cache-Control")
		if got != "no-store, private" {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, got, "no-store, private")
		}
	}
}

func TestCachePolicy_UpgradeSimulation(t *testing.T) {
	// Simulates: browser loaded assets from Version A (no cache headers,
	// simulating old GoAl binary), then server is replaced with Version B
	// (with Cache-Control: no-store on static assets). A normal reload
	// must receive Version B content because no-store prevents the browser
	// from using any previously stored copy.

	// Version A: no Cache-Control headers (old binary behavior).
	assetsA := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><script src='/static/app.js'></script>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("console.log('vA');")},
	}
	repoA, _ := storage.NewJSONRepository(filepath.Join(t.TempDir(), "a.json"))
	supA := process.NewSupervisor(repoA)
	routerA := NewRouteRegistry(
		application.NewInstanceService(supA, repoA),
		application.NewRuntimeService(repoA),
		application.NewModelService(repoA),
		application.NewPipelineService(supA, repoA),
		supA,
		repoA,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(false),
		WithWebAssets(fs.FS(assetsA), fs.FS(assetsA)),
	).Build()

	// Browser loads Version A.
	reqA := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	recA := httptest.NewRecorder()
	routerA.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("Version A GET /static/app.js: status = %d", recA.Code)
	}
	bodyA := recA.Body.String()
	if bodyA != "console.log('vA');" {
		t.Fatalf("Version A body = %q", bodyA)
	}

	// Version B: different content, same URL.
	assetsB := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><script src='/static/app.js'></script>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("console.log('vB');")},
	}
	repoB, _ := storage.NewJSONRepository(filepath.Join(t.TempDir(), "b.json"))
	supB := process.NewSupervisor(repoB)
	routerB := NewRouteRegistry(
		application.NewInstanceService(supB, repoB),
		application.NewRuntimeService(repoB),
		application.NewModelService(repoB),
		application.NewPipelineService(supB, repoB),
		supB,
		repoB,
		security.NewCSRF(),
		security.NewSessionStore(),
		security.NewPasswordStore(),
		WithAuthEnabled(false),
		WithWebAssets(fs.FS(assetsB), fs.FS(assetsB)),
	).Build()

	// Browser reloads after upgrade. With no-store, the browser is
	// forbidden from using any stored copy and MUST download fresh.
	reqB := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	recB := httptest.NewRecorder()
	routerB.ServeHTTP(recB, reqB)

	if recB.Code != http.StatusOK {
		t.Fatalf("Version B GET /static/app.js: status = %d, want 200", recB.Code)
	}
	bodyB := recB.Body.String()
	if bodyB != "console.log('vB');" {
		t.Fatalf("Version B body = %q, want %q", bodyB, "console.log('vB');")
	}
	ccB := recB.Header().Get("Cache-Control")
	if ccB != "no-store" {
		t.Fatalf("Version B Cache-Control = %q, want no-store", ccB)
	}
}
