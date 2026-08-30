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
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

func newLoginRateLimitedRouter(t *testing.T, limit int) http.Handler {
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
	reg := NewRouteRegistry(
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
	)
	reg.loginLimiter = security.NewRateLimiter(limit, time.Minute)
	return reg.Build()
}

func loginRequest(t *testing.T, router http.Handler, remoteAddr, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"`+username+`","password":"`+password+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	return doRequest(t, router, req)
}

func doRequest(t *testing.T, router http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestLoginRateLimitReturns429WhenExhausted(t *testing.T) {
	router := newLoginRateLimitedRouter(t, 3)
	addr := "10.1.2.3:5555"

	for i := 0; i < 3; i++ {
		resp := loginRequest(t, router, addr, "admin", "wrong")
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401; body=%s", i+1, resp.Code, resp.Body.String())
		}
	}

	resp := loginRequest(t, router, addr, "admin", "secret")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body.Code != "rate_limited" {
		t.Fatalf("code = %q, want rate_limited", body.Code)
	}
}

func TestLoginRateLimitIsPerClientAddress(t *testing.T) {
	router := newLoginRateLimitedRouter(t, 1)

	resp := loginRequest(t, router, "10.0.0.1:5555", "admin", "wrong")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}

	resp = loginRequest(t, router, "10.0.0.2:5555", "admin", "secret")
	if resp.Code != http.StatusOK {
		t.Fatalf("different client status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestLoginRateLimitDoesNotTrustForwardedHeaders(t *testing.T) {
	router := newLoginRateLimitedRouter(t, 1)
	addr := "10.0.0.1:5555"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	resp := loginRequest(t, router, addr, "admin", "secret")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: spoofed X-Forwarded-For must not bypass the limiter", resp.Code)
	}
}

func TestLoginRateLimitDoesNotAffectOtherEndpoints(t *testing.T) {
	router := newLoginRateLimitedRouter(t, 1)
	addr := "10.0.0.1:5555"

	loginRequest(t, router, addr, "admin", "wrong")
	loginRequest(t, router, addr, "admin", "wrong")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = addr
	rec := doRequest(t, router, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
