package handlers

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/security"
)

const (
	reloadTestOldPassword = "reload-old-pw-7f3a"
	reloadTestNewPassword = "reload-new-pw-9x2k"
)

type reloadEnv struct {
	router    http.Handler
	logger    *audit.AuditLogger
	cfgPath   string
	dataDir   string
	liveCfg   *config.Config
	repo      storage.Repository
	passStore *security.PasswordStore
}

func newReloadEnv(t *testing.T) *reloadEnv {
	t.Helper()
	dataDir := t.TempDir()
	logger := audit.New(filepath.Join(dataDir, audit.FileName))
	t.Cleanup(func() { _ = logger.Close() })

	repo, err := storage.NewJSONRepository(filepath.Join(dataDir, "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	sup := process.NewSupervisor(repo)

	passStore := security.NewPasswordStore()
	if err := passStore.SetPassword("admin", reloadTestOldPassword); err != nil {
		t.Fatalf("set password: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.AuthEnabled = true
	cfg.AdminUser = "admin"
	hash, err := config.HashPassword(reloadTestOldPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	cfg.AdminPasswordHash = hash

	cfgPath := filepath.Join(dataDir, "goal.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	live := cfg

	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>GoAl</title>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
	}
	reg := NewRouteRegistry(
		application.NewInstanceService(sup, repo),
		application.NewRuntimeService(repo),
		application.NewModelService(repo),
		application.NewPipelineService(sup, repo),
		sup,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		passStore,
		WithAuthEnabled(true),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
		WithConfigPath(cfgPath),
		WithLiveConfig(&live),
		WithAuditLogger(logger),
	)
	return &reloadEnv{
		router:    reg.Build(),
		logger:    logger,
		cfgPath:   cfgPath,
		dataDir:   dataDir,
		liveCfg:   &live,
		repo:      repo,
		passStore: passStore,
	}
}

func (e *reloadEnv) loggedIn(t *testing.T) (sessionCookie, csrf string) {
	t.Helper()
	body := `{"username":"admin","password":"` + reloadTestOldPassword + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "goal_session" {
			sessionCookie = c.Value
		}
	}
	return sessionCookie, out.CSRF
}

func (e *reloadEnv) reload(t *testing.T, sessionCookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reload", nil)
	if sessionCookie != "" {
		req.AddCookie(&http.Cookie{Name: "goal_session", Value: sessionCookie})
	}
	if csrf != "" {
		req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return doRequest(t, e.router, req)
}

func (e *reloadEnv) setFile(t *testing.T, mutate func(*config.Config)) {
	t.Helper()
	cfg, err := config.LoadReadOnly(e.cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	mutate(&cfg)
	if err := config.Save(e.cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestReload_AppliesLogLevelHot(t *testing.T) {
	t.Cleanup(func() { SetApplicationLogLevel(slog.LevelInfo) })
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)

	e.setFile(t, func(c *config.Config) { c.LogLevel = "debug" })

	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status          string   `json:"status"`
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "reloaded" {
		t.Errorf("status = %q, want reloaded", body.Status)
	}
	if len(body.Applied) != 1 || body.Applied[0] != "logLevel" {
		t.Errorf("applied = %v, want [logLevel]", body.Applied)
	}
	if len(body.RestartRequired) != 0 {
		t.Errorf("restart_required = %v, want empty", body.RestartRequired)
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("application log level did not switch to debug")
	}
}

func TestReload_ReportsRestartRequiredFields(t *testing.T) {
	t.Cleanup(func() { SetApplicationLogLevel(slog.LevelInfo) })
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)

	e.setFile(t, func(c *config.Config) {
		c.WebPort = 9999
		c.DataDir = "/other/data"
	})

	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status          string   `json:"status"`
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Applied) != 0 {
		t.Errorf("applied = %v, want empty", body.Applied)
	}
	if len(body.RestartRequired) != 2 || body.RestartRequired[0] != "webPort" || body.RestartRequired[1] != "dataDir" {
		t.Errorf("restart_required = %v, want [webPort dataDir]", body.RestartRequired)
	}
}

func TestReload_RejectedBrokenJSON_FileAndLiveUnchanged(t *testing.T) {
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)
	corrupted := []byte("{broken")
	if err := os.WriteFile(e.cfgPath, corrupted, 0o600); err != nil {
		t.Fatalf("corrupt config: %v", err)
	}
	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "rejected" || body.Code != "bad_request" {
		t.Errorf("body = %+v, want status=rejected code=bad_request", body)
	}
	after, err := os.ReadFile(e.cfgPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(corrupted) != string(after) {
		t.Error("config file was modified by a rejected reload")
	}
	events := e.queryEvents(t, audit.EventConfigReload)
	if len(events) != 1 {
		t.Fatalf("expected 1 config.reload event, got %d", len(events))
	}
	if events[0].Detail["status"] != "rejected" || events[0].Detail["error"] != "invalid_config" {
		t.Errorf("detail = %v, want status=rejected error=invalid_config", events[0].Detail)
	}
}

func TestReload_RejectedInvalidLogLevel(t *testing.T) {
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)
	if err := os.WriteFile(e.cfgPath, []byte(`{"version":2,"listenAddress":"127.0.0.1","webPort":8088,"dataDir":"`+e.dataDir+`","adminUser":"admin","authEnabled":true,"logLevel":"trace"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "rejected" {
		t.Errorf("status = %q, want rejected", body.Status)
	}
}

func TestReload_SeedSectionsNotReseeded(t *testing.T) {
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)

	e.setFile(t, func(c *config.Config) {
		c.Runtimes = []config.Runtime{{ID: "r1", Name: "r1", Executable: "/bin/true"}}
		c.Models = []config.Model{{ID: "m1", Name: "m1", RuntimeID: "r1"}}
	})
	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}
	runtimes, err := e.repo.ListRuntimes()
	if err != nil {
		t.Fatalf("list runtimes: %v", err)
	}
	if len(runtimes) != 0 {
		t.Errorf("reload re-seeded runtimes into the repository: %d entries", len(runtimes))
	}
	models, err := e.repo.ListModels()
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("reload re-seeded models into the repository: %d entries", len(models))
	}
}

func TestReload_DoesNotApplyCredentialMaterial(t *testing.T) {
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)

	newHash, err := config.HashPassword(reloadTestNewPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	e.setFile(t, func(c *config.Config) { c.AdminPasswordHash = newHash })

	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	if e.passStore.ValidateCredentials("admin", reloadTestNewPassword) {
		t.Error("hand-edited password hash was applied by reload (ADR 009 D1: reload never applies credentials)")
	}
	if !e.passStore.ValidateCredentials("admin", reloadTestOldPassword) {
		t.Error("live password store no longer accepts the pre-reload password")
	}
}

func TestReload_Security_NoSession(t *testing.T) {
	e := newReloadEnv(t)
	rec := e.reload(t, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestReload_Security_MissingCSRF(t *testing.T) {
	e := newReloadEnv(t)
	sess, _ := e.loggedIn(t)
	rec := e.reload(t, sess, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (missing CSRF), got %d", rec.Code)
	}
}

func TestReload_AuditEventFieldNamesOnly(t *testing.T) {
	e := newReloadEnv(t)
	sess, csrf := e.loggedIn(t)

	e.setFile(t, func(c *config.Config) {
		c.LogLevel = "warn"
		c.WebPort = 9999
	})
	rec := e.reload(t, sess, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}
	events := e.queryEvents(t, audit.EventConfigReload)
	if len(events) != 1 {
		t.Fatalf("expected 1 config.reload event, got %d", len(events))
	}
	d := events[0].Detail
	if d["status"] != "reloaded" {
		t.Errorf("status = %q, want reloaded", d["status"])
	}
	if d["applied"] != "logLevel" {
		t.Errorf("applied = %q, want logLevel", d["applied"])
	}
	if d["restart_required"] != "webPort" {
		t.Errorf("restart_required = %q, want webPort", d["restart_required"])
	}
}

func (e *reloadEnv) queryEvents(t *testing.T, event string) []audit.AuditEvent {
	t.Helper()
	events, _, err := e.logger.Query(100, 0, event)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	return events
}
