package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/security"
)

// killHandlerTestKiller implements platform.ProcessKiller with scripted
// behavior for handler tests.
type killHandlerTestKiller struct {
	gracefulErr   error
	forceErr      error
	gracefulCount int
	forceCount    int
	onGraceful    func()
	onForce       func()
}

func (f *killHandlerTestKiller) SignalGraceful(pid int) error {
	f.gracefulCount++
	if f.onGraceful != nil {
		f.onGraceful()
	}
	return f.gracefulErr
}

func (f *killHandlerTestKiller) SignalForce(pid int) error {
	f.forceCount++
	if f.onForce != nil {
		f.onForce()
	}
	return f.forceErr
}

// killTestProber is a scripted RecoveryProber for handler tests.
type killTestProber struct {
	alive bool
	ident platform.ProcessIdentity
}

func (p *killTestProber) IsProcessAlive(pid int) (bool, error) { return p.alive, nil }
func (p *killTestProber) GetProcessIdentity(pid int) (platform.ProcessIdentity, error) {
	return p.ident, nil
}

// killFixture wires a supervisor with an in-memory instance store, a
// scripted prober/killer, and an audit logger writing to a temp file.
// fixture.entry always reflects the last persisted entry.
type killFixture struct {
	entry     *domain.LaunchInstanceEntry
	h         *InstancesHandler
	logger    *audit.AuditLogger
	auditPath string
}

func newKillHandler(t *testing.T, entry *domain.LaunchInstanceEntry, prober *killTestProber, killer *killHandlerTestKiller) *killFixture {
	t.Helper()
	repo := newTestRepo(t)
	fx := &killFixture{entry: entry}

	insStore := &mockInstanceStore{}
	insStore.GetFunc = func(id string) (*domain.LaunchInstanceEntry, error) {
		if id == fx.entry.ID {
			return fx.entry, nil
		}
		return nil, errors.New("not found")
	}
	insStore.UpdateFunc = func(e *domain.LaunchInstanceEntry) error {
		fx.entry = e
		return nil
	}

	sup := process.NewSupervisor(insStore)
	sup.SetRecoveryProber(prober)
	if killer != nil {
		sup.SetProcessKiller(killer)
	}
	process.SetKillWindows(200*time.Millisecond, 20*time.Millisecond)
	t.Cleanup(func() { process.SetKillWindows(5*time.Second, 250*time.Millisecond) })

	fx.auditPath = filepath.Join(t.TempDir(), audit.FileName)
	fx.logger = audit.New(fx.auditPath)
	t.Cleanup(func() { _ = fx.logger.Close() })

	insSvc := application.NewInstanceService(sup, repo)
	fx.h = NewInstancesHandler(insSvc, nil).WithAudit(fx.logger)
	return fx
}

func orphanKillEntry(id string, pid int) *domain.LaunchInstanceEntry {
	return &domain.LaunchInstanceEntry{
		ID:         id,
		State:      "orphan",
		PID:        pid,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func matchingKillIdentity() platform.ProcessIdentity {
	return platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      time.Now().Add(-time.Minute),
		HasStartTime:   true,
	}
}

func readAuditEvents(t *testing.T, logger *audit.AuditLogger, path string) []audit.AuditEvent {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	events, _, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	return events
}

func doKill(t *testing.T, h *InstancesHandler, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/"+id+"/kill", nil)
	w := httptest.NewRecorder()
	h.Kill(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// ADR 008 Case E: pid gone at kill time → 200 reconciled + audit.
func TestInstancesHandler_Kill_Reconciled(t *testing.T) {
	entry := orphanKillEntry("inst-kill-1", 4000)
	ident := matchingKillIdentity()
	entry.StartedAt = ident.StartTime
	fx := newKillHandler(t, entry, &killTestProber{alive: false, ident: ident}, &killHandlerTestKiller{})

	w := doKill(t, fx.h, "inst-kill-1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["status"] != "reconciled" || body["reason"] != "pid-gone" {
		t.Errorf("expected reconciled/pid-gone, got %v", body)
	}

	events := readAuditEvents(t, fx.logger, fx.auditPath)
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Event != audit.EventInstanceKill {
		t.Errorf("expected instance.kill, got %s", events[0].Event)
	}
	d := events[0].Detail
	if d["instance_id"] != "inst-kill-1" || d["outcome"] != "reconciled" || d["reason"] != "pid-gone" {
		t.Errorf("unexpected audit detail: %v", d)
	}
}

// ADR 008 Cases A/B: terminated → 200 {"status":"killed","method":...} + audit.
func TestInstancesHandler_Kill_Terminated(t *testing.T) {
	entry := orphanKillEntry("inst-kill-2", 4100)
	ident := matchingKillIdentity()
	entry.StartedAt = ident.StartTime
	prober := &killTestProber{alive: true, ident: ident}
	killer := &killHandlerTestKiller{}
	if runtime.GOOS == "windows" {
		killer.onForce = func() { prober.alive = false }
	} else {
		killer.onGraceful = func() { prober.alive = false }
	}
	fx := newKillHandler(t, entry, prober, killer)

	w := doKill(t, fx.h, "inst-kill-2")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["status"] != "killed" || body["method"] == "" {
		t.Errorf("expected killed with method, got %v", body)
	}
	if fx.entry.State != "stale" || fx.entry.RecoveryReason != "killed-by-user" || fx.entry.ExitClass != "killed" {
		t.Errorf("expected stale/killed-by-user/killed, got %s/%q/%q", fx.entry.State, fx.entry.RecoveryReason, fx.entry.ExitClass)
	}

	events := readAuditEvents(t, fx.logger, fx.auditPath)
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	d := events[0].Detail
	if d["outcome"] != "terminated" || d["reason"] != body["method"] {
		t.Errorf("audit must mirror the API method, got %v method=%q", d, body["method"])
	}
}

// ADR 008 Case F: identity unconfirmed → 409 conflict, orphan preserved, audited.
func TestInstancesHandler_Kill_IdentityUnconfirmed(t *testing.T) {
	entry := orphanKillEntry("inst-kill-3", 4200)
	killer := &killHandlerTestKiller{}
	fx := newKillHandler(t, entry, &killTestProber{alive: true, ident: platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/some-other-app",
		StartTime:      time.Now().Add(-time.Minute),
		HasStartTime:   true,
	}}, killer)

	w := doKill(t, fx.h, "inst-kill-3")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"] != "conflict" || body["reason"] != "identity-unconfirmed" {
		t.Errorf("expected conflict/identity-unconfirmed, got %v", body)
	}
	if killer.gracefulCount != 0 || killer.forceCount != 0 {
		t.Errorf("no signal may be sent, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
	}
	if fx.entry.State != "orphan" || fx.entry.LastError != "identity-unconfirmed" {
		t.Errorf("orphan must be preserved with persisted diagnostic, got %s/%q", fx.entry.State, fx.entry.LastError)
	}
	events := readAuditEvents(t, fx.logger, fx.auditPath)
	if len(events) != 1 || events[0].Detail["outcome"] != "refused" || events[0].Detail["reason"] != "identity-unconfirmed" {
		t.Errorf("expected refused/identity-unconfirmed audit, got %v", events)
	}
}

// ADR 008 Case D: privilege denied → 403 forbidden, orphan preserved, audited.
func TestInstancesHandler_Kill_InsufficientPrivilege(t *testing.T) {
	entry := orphanKillEntry("inst-kill-4", 4300)
	ident := matchingKillIdentity()
	entry.StartedAt = ident.StartTime
	fx := newKillHandler(t, entry, &killTestProber{alive: true, ident: ident}, &killHandlerTestKiller{
		gracefulErr: platform.ErrKillAccessDenied,
		forceErr:    platform.ErrKillAccessDenied,
	})

	w := doKill(t, fx.h, "inst-kill-4")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"] != "forbidden" || body["reason"] != "insufficient-privilege" {
		t.Errorf("expected forbidden/insufficient-privilege, got %v", body)
	}
	events := readAuditEvents(t, fx.logger, fx.auditPath)
	if len(events) != 1 || events[0].Detail["outcome"] != "refused" || events[0].Detail["reason"] != "insufficient-privilege" {
		t.Errorf("expected refused/insufficient-privilege audit, got %v", events)
	}
}

// ADR 008 Case C: unconfirmed termination → 500, orphan preserved, audited.
func TestInstancesHandler_Kill_Unconfirmed(t *testing.T) {
	entry := orphanKillEntry("inst-kill-5", 4400)
	ident := matchingKillIdentity()
	entry.StartedAt = ident.StartTime
	fx := newKillHandler(t, entry, &killTestProber{alive: true, ident: ident}, &killHandlerTestKiller{})

	w := doKill(t, fx.h, "inst-kill-5")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["reason"] != "unconfirmed" {
		t.Errorf("expected reason unconfirmed, got %v", body)
	}
	if fx.entry.State != "orphan" || fx.entry.LastError != "unconfirmed" {
		t.Errorf("orphan must be preserved with persisted diagnostic, got %s/%q", fx.entry.State, fx.entry.LastError)
	}
	events := readAuditEvents(t, fx.logger, fx.auditPath)
	if len(events) != 1 || events[0].Detail["outcome"] != "refused" || events[0].Detail["reason"] != "unconfirmed" {
		t.Errorf("expected refused/unconfirmed audit, got %v", events)
	}
}

// ADR 008 Case G: not orphan → 409 and not found → 404, no audit events.
func TestInstancesHandler_Kill_CaseG_NoAudit(t *testing.T) {
	entry := &domain.LaunchInstanceEntry{
		ID:        "inst-running",
		State:     "running",
		PID:       4500,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	fx := newKillHandler(t, entry, &killTestProber{alive: true, ident: matchingKillIdentity()}, &killHandlerTestKiller{})

	w := doKill(t, fx.h, "inst-running")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for non-orphan, got %d", w.Code)
	}
	w2 := doKill(t, fx.h, "missing")
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing, got %d", w2.Code)
	}
	if events := readAuditEvents(t, fx.logger, fx.auditPath); len(events) != 0 {
		t.Errorf("case G must emit no audit events, got %d", len(events))
	}
}

func TestInstancesHandler_Kill_MissingID(t *testing.T) {
	repo := newTestRepo(t)
	sup := process.NewSupervisor(&mockInstanceStore{})
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	w := doKill(t, h, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ADR 008 acceptance 7: the kill route is behind auth + CSRF (like dismiss):
// unauthenticated → 401, missing CSRF → 403, valid session+CSRF reaches the
// handler (404 for a missing instance).
func TestKillRoute_AuthCSRF(t *testing.T) {
	repo := newTestRepo(t)
	supervisor := process.NewSupervisor(repo)
	passwords := security.NewPasswordStore()
	if err := passwords.SetPassword("admin", "secret"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>GoAl</title>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
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
		passwords,
		WithAuthEnabled(true),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
	).Build()

	killURL := "/api/v1/instances/inst-x/kill"

	// Unauthenticated → 401 (the route exists behind auth).
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, killURL, nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated kill must be 401, got %d", w.Code)
	}

	// Login.
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	lw := httptest.NewRecorder()
	router.ServeHTTP(lw, loginReq)
	if lw.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", lw.Code)
	}
	var loginBody map[string]string
	if err := json.Unmarshal(lw.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	csrfToken := loginBody["csrf_token"]
	cookies := lw.Result().Cookies()

	withSession := func(modify func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, killURL, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		if modify != nil {
			modify(req)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Authenticated, no CSRF header → 403.
	w2 := withSession(nil)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("kill without CSRF must be 403, got %d", w2.Code)
	}

	// Authenticated + CSRF → reaches the handler (404: no such instance).
	w3 := withSession(func(req *http.Request) { req.Header.Set("X-CSRF-Token", csrfToken) })
	if w3.Code != http.StatusNotFound {
		t.Fatalf("kill with session+CSRF must reach the handler (404), got %d", w3.Code)
	}
}
