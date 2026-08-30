package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/security"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := fakeruntime.Cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, "fake runtime cleanup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// ADR 007 acceptance scenarios 1-13 and 16-17 (14 rotation and 15 concurrency
// are covered by internal/webui/audit unit tests).
const (
	auditTestOldPassword = "old-secret-pw-7f3a"
	auditTestNewPassword = "brand-new-secret-9x2k"
	auditTestEnvSecret   = "env-secret-value-xyz-42"
)

type auditEnv struct {
	router    http.Handler
	logger    *audit.AuditLogger
	auditPath string
	cfgPath   string
	dataDir   string
	repo      storage.Repository
	sup       *process.Supervisor
}

func newAuditEnv(t *testing.T, loginLimit int) *auditEnv {
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
	if err := passStore.SetPassword("admin", auditTestOldPassword); err != nil {
		t.Fatalf("set password: %v", err)
	}

	cfgPath := filepath.Join(dataDir, "goal.json")
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.AuthEnabled = true
	cfg.AdminUser = "admin"
	hash, err := config.HashPassword(auditTestOldPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	cfg.AdminPasswordHash = hash
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

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
		WithAuditLogger(logger),
	)
	if loginLimit > 0 {
		reg.loginLimiter = security.NewRateLimiter(loginLimit, time.Minute)
	}
	return &auditEnv{
		router:    reg.Build(),
		logger:    logger,
		auditPath: filepath.Join(dataDir, audit.FileName),
		cfgPath:   cfgPath,
		dataDir:   dataDir,
		repo:      repo,
		sup:       sup,
	}
}

func (e *auditEnv) login(t *testing.T, addr, user, password string) (rec *httptest.ResponseRecorder, sessionCookie, csrf string) {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, user, password)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = addr
	rec = doRequest(t, e.router, req)
	var out struct {
		CSRF string `json:"csrf"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	csrf = out.CSRF
	for _, c := range rec.Result().Cookies() {
		if c.Name == "goal_session" {
			sessionCookie = c.Value
		}
	}
	return rec, sessionCookie, csrf
}

func (e *auditEnv) do(t *testing.T, method, path, addr, sessionCookie, csrf, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = addr
	if sessionCookie != "" {
		req.AddCookie(&http.Cookie{Name: "goal_session", Value: sessionCookie})
	}
	if csrf != "" {
		req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return doRequest(t, e.router, req)
}

func (e *auditEnv) loggedIn(t *testing.T, addr string) (sessionCookie, csrf string) {
	t.Helper()
	rec, sess, c := e.login(t, addr, "admin", auditTestOldPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if sess == "" {
		t.Fatal("no session cookie in login response")
	}
	return sess, c
}

func (e *auditEnv) query(t *testing.T, limit, offset int, event string) []audit.AuditEvent {
	t.Helper()
	events, _, err := e.logger.Query(limit, offset, event)
	if err != nil {
		t.Fatalf("audit Query: %v", err)
	}
	return events
}

func (e *auditEnv) auditRaw(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(e.auditPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read audit file: %v", err)
	}
	return string(data)
}

func (e *auditEnv) seedGracefulModel(t *testing.T, id string) {
	t.Helper()
	if err := e.repo.CreateRuntime(&storage.RuntimeEntry{
		ID:         id + "-rt",
		Name:       id + "-runtime",
		Executable: fakeruntime.Path(t),
	}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := e.repo.CreateModel(&storage.ModelEntry{
		ID:        id,
		Name:      id + "-model",
		RuntimeID: id + "-rt",
		Args:      []string{"graceful"},
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}
}

// stopInstance stops an instance through the API (used for cleanup).
func (e *auditEnv) stopInstance(t *testing.T, addr, sessionCookie, csrf, id string) {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/v1/instances/"+id+"/stop", addr, sessionCookie, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop %s status = %d; body=%s", id, rec.Code, rec.Body.String())
	}
}

// Scenario 1: successful login → login.success with user + src_ip.
func TestAuditLoginSuccessEmitsEvent(t *testing.T) {
	e := newAuditEnv(t, 0)
	rec, _, _ := e.login(t, "10.9.8.7:4444", "admin", auditTestOldPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", rec.Code)
	}

	events := e.query(t, 100, 0, audit.EventLoginSuccess)
	if len(events) != 1 {
		t.Fatalf("want 1 login.success, got %d", len(events))
	}
	if events[0].User != "admin" {
		t.Fatalf("user = %q, want admin", events[0].User)
	}
	if events[0].SourceIP != "10.9.8.7" {
		t.Fatalf("src_ip = %q, want 10.9.8.7 (TCP peer, not header)", events[0].SourceIP)
	}
}

// Scenario 2: wrong password → login.failure with attempted user.
func TestAuditLoginFailureEmitsAttemptedUser(t *testing.T) {
	e := newAuditEnv(t, 0)
	rec, _, _ := e.login(t, "10.9.8.7:4444", "admin", "definitely-wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", rec.Code)
	}

	events := e.query(t, 100, 0, audit.EventLoginFailure)
	if len(events) != 1 {
		t.Fatalf("want 1 login.failure, got %d", len(events))
	}
	if events[0].User != "admin" {
		t.Fatalf("user = %q, want attempted user admin", events[0].User)
	}
}

// Scenario 3: rate limit exhausted → login.rate_limited, no user field.
func TestAuditRateLimitedEmitsEventWithoutUser(t *testing.T) {
	e := newAuditEnv(t, 1)
	rec, _, _ := e.login(t, "10.9.8.7:4444", "admin", "definitely-wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("first login status = %d, want 401", rec.Code)
	}
	rec, _, _ = e.login(t, "10.9.8.7:4444", "admin", auditTestOldPassword)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second login status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}

	events := e.query(t, 100, 0, audit.EventLoginRateLimited)
	if len(events) != 1 {
		t.Fatalf("want 1 login.rate_limited, got %d", len(events))
	}
	if events[0].User != "" {
		t.Fatalf("rate_limited event must not carry a user, got %q", events[0].User)
	}
	if events[0].SourceIP != "10.9.8.7" {
		t.Fatalf("src_ip = %q, want 10.9.8.7", events[0].SourceIP)
	}
}

// Scenario 4: logout → session.logout.
func TestAuditLogoutEmitsEvent(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	rec := e.do(t, http.MethodPost, "/api/v1/auth/logout", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	events := e.query(t, 100, 0, audit.EventSessionLogout)
	if len(events) != 1 {
		t.Fatalf("want 1 session.logout, got %d", len(events))
	}
	if events[0].User != "admin" {
		t.Fatalf("user = %q, want admin", events[0].User)
	}
}

// Scenario 5: settings save with password → settings.saved with
// password_changed; neither old nor new password appears in the audit file.
func TestAuditSettingsSavedWithPassword(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	body := fmt.Sprintf(`{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_password":%q}`, auditTestNewPassword)
	rec := e.do(t, http.MethodPut, "/api/v1/settings", addr, sess, csrf, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	events := e.query(t, 100, 0, audit.EventSettingsSaved)
	if len(events) != 1 {
		t.Fatalf("want 1 settings.saved, got %d", len(events))
	}
	if events[0].Detail["password_changed"] != "true" {
		t.Fatalf("password_changed = %q, want true; detail=%v", events[0].Detail["password_changed"], events[0].Detail)
	}
	raw := e.auditRaw(t)
	for _, secret := range []string{auditTestOldPassword, auditTestNewPassword} {
		if strings.Contains(raw, secret) {
			t.Fatalf("audit file contains password %q", secret)
		}
	}
}

// Scenario 6: settings save changing only the port → settings.saved carries
// the field name, never the value.
func TestAuditSettingsSavedPortOnly(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	rec := e.do(t, http.MethodPut, "/api/v1/settings", addr, sess, csrf, `{"listen_address":"127.0.0.1","web_port":9099,"auth_enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	events := e.query(t, 100, 0, audit.EventSettingsSaved)
	if len(events) != 1 {
		t.Fatalf("want 1 settings.saved, got %d", len(events))
	}
	if _, ok := events[0].Detail["web_port"]; !ok {
		t.Fatalf("web_port name missing from detail: %v", events[0].Detail)
	}
	line, err := json.Marshal(events[0])
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if bytes.Contains(line, []byte("9099")) {
		t.Fatalf("event carries the port value: %s", line)
	}
}

// Scenario 7: instance start (success and failure) → instance.start with
// instance_id / sanitized error.
func TestAuditInstanceStartSuccessAndFailure(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	if err := e.repo.CreateRuntime(&storage.RuntimeEntry{
		ID:         "rt-bad",
		Name:       "bad-runtime",
		Executable: filepath.Join(t.TempDir(), "no-such-executable"),
	}); err != nil {
		t.Fatalf("create bad runtime: %v", err)
	}
	if err := e.repo.CreateModel(&storage.ModelEntry{ID: "model-bad", Name: "bad-model", RuntimeID: "rt-bad"}); err != nil {
		t.Fatalf("create bad model: %v", err)
	}
	e.seedGracefulModel(t, "model-good")

	rec := e.do(t, http.MethodPost, "/api/v1/instances/start", addr, sess, csrf, `{"model_id":"model-bad"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("bad start status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}

	var started struct {
		ID string `json:"id"`
	}
	rec = e.do(t, http.MethodPost, "/api/v1/instances/start", addr, sess, csrf, `{"model_id":"model-good"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("good start status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	t.Cleanup(func() { e.stopInstance(t, addr, sess, csrf, started.ID) })

	failed := e.query(t, 100, 0, audit.EventInstanceStart)
	if len(failed) != 2 {
		t.Fatalf("want 2 instance.start events, got %d", len(failed))
	}
	// Newest first: the successful start is last.
	ok := failed[0]
	if ok.Detail["instance_id"] != started.ID {
		t.Fatalf("instance_id = %q, want %q", ok.Detail["instance_id"], started.ID)
	}
	if _, hasErr := ok.Detail["error"]; hasErr {
		t.Fatalf("success event must not carry an error: %v", ok.Detail)
	}
	fail := failed[1]
	if fail.Detail["model_id"] != "model-bad" {
		t.Fatalf("model_id = %q, want model-bad", fail.Detail["model_id"])
	}
	if errText := fail.Detail["error"]; errText == "" || len(errText) > 200 {
		t.Fatalf("sanitized error missing or too long: %q", errText)
	}
}

// Scenario 8: stop / restart / dismiss → corresponding events with instance_id.
func TestAuditStopRestartDismiss(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)
	e.seedGracefulModel(t, "model-lifecycle")

	rec := e.do(t, http.MethodPost, "/api/v1/instances/start", addr, sess, csrf, `{"model_id":"model-lifecycle"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	t.Cleanup(func() { e.stopInstance(t, addr, sess, csrf, started.ID) })

	rec = e.do(t, http.MethodPost, "/api/v1/instances/"+started.ID+"/stop", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/instances/"+started.ID+"/restart", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restart status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	orphan := &domain.LaunchInstanceEntry{
		ID:        "inst-orphan-audit",
		State:     "orphan",
		PID:       99999,
		StartedAt: time.Now().Add(-time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := e.repo.CreateLaunchInstance(orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	rec = e.do(t, http.MethodPost, "/api/v1/instances/inst-orphan-audit/dismiss", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if evs := e.query(t, 100, 0, audit.EventInstanceStop); len(evs) != 1 || evs[0].Detail["instance_id"] != started.ID {
		t.Fatalf("instance.stop events: %+v", evs)
	}
	if evs := e.query(t, 100, 0, audit.EventInstanceRestart); len(evs) != 1 || evs[0].Detail["instance_id"] != started.ID {
		t.Fatalf("instance.restart events: %+v", evs)
	}
	if evs := e.query(t, 100, 0, audit.EventInstanceDismiss); len(evs) != 1 || evs[0].Detail["instance_id"] != "inst-orphan-audit" {
		t.Fatalf("instance.dismiss events: %+v", evs)
	}
}

// Scenario 9: cleanup with matches → instance.cleanup with mode + deleted.
func TestAuditCleanupEmitsEvent(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr)

	now := time.Now()
	for _, id := range []string{"inst-term-1", "inst-term-2"} {
		if err := e.repo.CreateLaunchInstance(&domain.LaunchInstanceEntry{
			ID:        id,
			State:     "exited",
			StoppedAt: now,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed terminal %s: %v", id, err)
		}
	}

	rec := e.do(t, http.MethodPost, "/api/v1/instances/cleanup", addr, sess, csrf, `{"mode":"all_terminal"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	events := e.query(t, 100, 0, audit.EventInstanceCleanup)
	if len(events) != 1 {
		t.Fatalf("want 1 instance.cleanup, got %d", len(events))
	}
	if events[0].Detail["mode"] != "all_terminal" || events[0].Detail["deleted"] != "2" {
		t.Fatalf("detail = %v, want mode=all_terminal deleted=2", events[0].Detail)
	}
}

// Scenario 10: secret scan — the audit file contains none of the injected
// secrets (passwords, session token, CSRF token, environment values).
func TestAuditFileContainsNoSecrets(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"

	if err := e.repo.CreateRuntime(&storage.RuntimeEntry{
		ID:          "rt-secrets",
		Name:        "secrets-runtime",
		Executable:  filepath.Join(t.TempDir(), "no-such-executable"),
		Environment: map[string]string{"INJECTED_ENV": auditTestEnvSecret},
	}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := e.repo.CreateModel(&storage.ModelEntry{
		ID:          "model-secrets",
		Name:        "secrets-model",
		RuntimeID:   "rt-secrets",
		Environment: map[string]string{"MODEL_ENV": auditTestEnvSecret},
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	rec, sess, csrf := e.login(t, addr, "admin", auditTestOldPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", rec.Code)
	}

	// Exercise an audited failure path that mentions the model.
	e.do(t, http.MethodPost, "/api/v1/instances/start", addr, sess, csrf, `{"model_id":"model-secrets"}`)
	body := fmt.Sprintf(`{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_password":%q}`, auditTestNewPassword)
	e.do(t, http.MethodPut, "/api/v1/settings", addr, sess, csrf, body)
	e.do(t, http.MethodPost, "/api/v1/auth/logout", addr, sess, csrf, "")

	raw := e.auditRaw(t)
	if raw == "" {
		t.Fatal("audit file is empty; expected events")
	}
	secrets := []string{
		auditTestOldPassword,
		auditTestNewPassword,
		sess,
		csrf,
		auditTestEnvSecret,
	}
	for _, s := range secrets {
		if s == "" {
			t.Fatal("test setup produced an empty secret value")
		}
		if strings.Contains(raw, s) {
			t.Fatalf("audit file contains secret %q", s)
		}
	}
}

// Scenario 11: query API without a session → 401.
func TestAuditAPIRequiresAuth(t *testing.T) {
	e := newAuditEnv(t, 0)
	rec := e.do(t, http.MethodGet, "/api/v1/admin/audit", "10.9.8.7:4444", "", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// Scenario 12: query API with a session → newest first, limit/offset honored,
// exact event filter.
func TestAuditAPIQuery(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	sess, csrf := e.loggedIn(t, addr) // 1 login.success

	// Two login failures → 3 events total.
	for i := 0; i < 2; i++ {
		e.do(t, http.MethodPost, "/api/v1/auth/login", addr, "", "", `{"username":"admin","password":"wrong"}`)
	}

	rec := e.do(t, http.MethodGet, "/api/v1/admin/audit?limit=2", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Events []audit.AuditEvent `json:"events"`
		Total  int                `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != 3 || len(out.Events) != 2 {
		t.Fatalf("total=%d len=%d, want 3/2", out.Total, len(out.Events))
	}
	if out.Events[0].Event != audit.EventLoginFailure || out.Events[1].Event != audit.EventLoginFailure {
		t.Fatalf("newest-first violated: %+v", out.Events)
	}

	// Offset beyond the first page reaches the oldest (login.success).
	rec = e.do(t, http.MethodGet, "/api/v1/admin/audit?limit=2&offset=2", addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Events) != 1 || out.Events[0].Event != audit.EventLoginSuccess {
		t.Fatalf("offset window: %+v", out.Events)
	}

	// Exact event filter.
	rec = e.do(t, http.MethodGet, "/api/v1/admin/audit?event="+audit.EventLoginSuccess, addr, sess, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != 1 || len(out.Events) != 1 || out.Events[0].Event != audit.EventLoginSuccess {
		t.Fatalf("filter: total=%d events=%+v", out.Total, out.Events)
	}

	// Invalid parameters → 400.
	for _, qs := range []string{"limit=0", "limit=abc", "offset=-1"} {
		rec = e.do(t, http.MethodGet, "/api/v1/admin/audit?"+qs, addr, sess, csrf, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", qs, rec.Code)
		}
	}
}

// Scenario 13: fresh install (no audit file) → 200 + empty list.
func TestAuditAPIFreshInstall(t *testing.T) {
	e := newAuditEnv(t, 0)
	addr := "10.9.8.7:4444"
	// The fresh-install path is exercised through a handler bound to a
	// logger whose file does not exist yet (the API reads the file on every
	// request).
	freshHandler := NewAuditHandler(audit.New(filepath.Join(t.TempDir(), audit.FileName)))
	w := httptest.NewRecorder()
	freshHandler.Query(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Events []audit.AuditEvent `json:"events"`
		Total  int                `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != 0 || len(out.Events) != 0 {
		t.Fatalf("fresh install must be empty: total=%d events=%+v", out.Total, out.Events)
	}
	// The wired route requires auth even before any event exists.
	rec := e.do(t, http.MethodGet, "/api/v1/admin/audit", addr, "", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
}

// Scenario 16: audit write failure → the business operation still succeeds,
// a structured diagnostic is emitted, and it carries no event payload.
func TestAuditWriteFailureIsFailOpen(t *testing.T) {
	var diagBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&diagBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dataDir := t.TempDir()
	// Block the audit path with a regular file at the parent position.
	blocker := filepath.Join(dataDir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	logger := audit.New(filepath.Join(blocker, audit.FileName))
	t.Cleanup(func() { _ = logger.Close() })

	repo, err := storage.NewJSONRepository(filepath.Join(dataDir, "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	sup := process.NewSupervisor(repo)
	passStore := security.NewPasswordStore()
	if err := passStore.SetPassword("admin", auditTestOldPassword); err != nil {
		t.Fatalf("set password: %v", err)
	}
	cfgPath := filepath.Join(dataDir, "goal.json")
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.AuthEnabled = true
	cfg.AdminUser = "admin"
	hash, _ := config.HashPassword(auditTestOldPassword)
	cfg.AdminPasswordHash = hash
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
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
		WithAuditLogger(logger),
	)
	router := reg.Build()

	addr := "10.9.8.7:4444"
	rec, sess, csrf := func() (*httptest.ResponseRecorder, string, string) {
		body := fmt.Sprintf(`{"username":"admin","password":%q}`, auditTestOldPassword)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		r := doRequest(t, router, req)
		var out struct {
			CSRF string `json:"csrf"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		var token string
		for _, c := range r.Result().Cookies() {
			if c.Name == "goal_session" {
				token = c.Value
			}
		}
		return r, token, out.CSRF
	}()
	if rec.Code != http.StatusOK {
		t.Fatalf("login must succeed despite audit failure: %d; body=%s", rec.Code, rec.Body.String())
	}

	// An audited settings save must also succeed.
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		req.AddCookie(&http.Cookie{Name: "goal_session", Value: sess})
		req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)
		return doRequest(t, router, req)
	}
	rec = put(fmt.Sprintf(`{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_password":%q}`, auditTestNewPassword))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings must succeed despite audit failure: %d; body=%s", rec.Code, rec.Body.String())
	}

	diag := diagBuf.String()
	if !strings.Contains(diag, "level=ERROR") {
		t.Fatalf("want structured ERROR diagnostic, got: %q", diag)
	}
	if !strings.Contains(diag, "event=login.success") || !strings.Contains(diag, "event=settings.saved") {
		t.Fatalf("diagnostic must name the failed events: %q", diag)
	}
	for _, secret := range []string{auditTestOldPassword, auditTestNewPassword, sess, csrf} {
		if strings.Contains(diag, secret) {
			t.Fatalf("diagnostic must not contain %q: %q", secret, diag)
		}
	}
}

// Scenario 17: after a failed write the logger is not latched; once the I/O
// fault is cleared, the next event on the same logger is persisted.
func TestAuditRecoveryAfterWriteFailure(t *testing.T) {
	dataDir := t.TempDir()
	// Fault: the audit file path sits below a regular file, so open fails.
	blocker := filepath.Join(dataDir, "blocker")
	auditPath := filepath.Join(blocker, audit.FileName)
	logger := audit.New(auditPath)
	t.Cleanup(func() { _ = logger.Close() })
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if err := logger.Log(audit.AuditEvent{Event: audit.EventLoginFailure, User: "u", SourceIP: "1.2.3.4"}); err == nil {
		t.Fatal("want the first write to fail")
	}

	// Clear the fault at the same path: replace the file with a directory.
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}
	if err := logger.Log(audit.AuditEvent{Event: audit.EventLoginSuccess, User: "admin", SourceIP: "1.2.3.4"}); err != nil {
		t.Fatalf("post-fault write must succeed (no latch): %v", err)
	}
	events, total, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || events[0].Event != audit.EventLoginSuccess {
		t.Fatalf("want exactly the recovered event: total=%d %+v", total, events)
	}
}
