package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

// fakeGpuProvider is a test-only GpuProvider (ADR 025 D-10) returning a fixed
// report after an optional delay, so the handler/state logic is testable without
// GPU hardware or nvidia-smi.
type fakeGpuProvider struct {
	report platform.GpuReport
	delay  time.Duration
	calls  int32
}

func (f *fakeGpuProvider) Sample(ctx context.Context) platform.GpuReport {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
		}
	}
	return f.report
}

func newGpuTestRouter(t *testing.T, p platform.GpuProvider, authEnabled bool) http.Handler {
	t.Helper()
	return newResourceTestRouter(t, p, &fakeSystemProvider{report: okSystemReport()}, authEnabled)
}

func newResourceTestRouter(t *testing.T, p platform.GpuProvider, sys platform.SystemProvider, authEnabled bool) http.Handler {
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
		WithAuthEnabled(authEnabled),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
		WithGpuProvider(p),
		WithSystemProvider(sys),
	).Build()
}

func getGPU(t *testing.T, router http.Handler, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/gpu", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func TestGpuEndpointStates(t *testing.T) {
	okReport := platform.GpuReport{
		State: platform.GpuStateOK,
		GPUs: []platform.GpuDevice{
			{UUID: "GPU-x", Index: 0, Name: "NVIDIA Test", Driver: "555.99", TotalBytes: 100, UsedBytes: 40, FreeBytes: 60},
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}
	cases := []struct {
		name        string
		report      platform.GpuReport
		wantState   string
		wantGPUs    int
		wantError   string
		wantSampled bool
	}{
		{"ok", okReport, "ok", 1, "", true},
		{"unavailable", platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}, "gpu_unavailable", 0, "nvidia_smi_not_found", false},
		{"failed", platform.GpuReport{State: platform.GpuStateFailed, GPUs: []platform.GpuDevice{}, Error: "measurement_timeout"}, "measurement_failed", 0, "measurement_timeout", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeGpuProvider{report: tc.report}
			router := newGpuTestRouter(t, fake, false)
			resp := getGPU(t, router, nil)

			// Always HTTP 200, state in the body (D-4/D-6).
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
			if atomic.LoadInt32(&fake.calls) != 1 {
				t.Fatalf("provider called %d times, want 1", fake.calls)
			}
			var body map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["state"] != tc.wantState {
				t.Fatalf("state = %v, want %q", body["state"], tc.wantState)
			}
			gpus, ok := body["gpus"].([]any)
			if !ok {
				t.Fatalf("gpus is not an array: %v", body["gpus"])
			}
			if len(gpus) != tc.wantGPUs {
				t.Fatalf("len(gpus) = %d, want %d", len(gpus), tc.wantGPUs)
			}
			if tc.wantError == "" {
				if _, present := body["error"]; present {
					t.Fatalf("ok body must not carry error: %v", body["error"])
				}
			} else if body["error"] != tc.wantError {
				t.Fatalf("error = %v, want %q", body["error"], tc.wantError)
			}
			_, sampledPresent := body["sampled_at"]
			if sampledPresent != tc.wantSampled {
				t.Fatalf("sampled_at present = %v, want %v", sampledPresent, tc.wantSampled)
			}
		})
	}
}

func TestGpuEndpointAuthAndNoCSRF(t *testing.T) {
	fake := &fakeGpuProvider{report: platform.GpuReport{
		State:     platform.GpuStateOK,
		GPUs:      []platform.GpuDevice{{UUID: "GPU-x", Index: 0, Name: "N", Driver: "555.99", TotalBytes: 1, UsedBytes: 0, FreeBytes: 1}},
		SampledAt: "2026-11-19T00:00:00Z",
	}}
	router := newGpuTestRouter(t, fake, true)

	// Unauthenticated ⇒ 401 (existing requireAuth behavior).
	unauth := getGPU(t, router, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauth.Code)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp := httptest.NewRecorder()
	router.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginResp.Code, loginResp.Body.String())
	}
	cookies := loginResp.Result().Cookies()

	// Authenticated GET with NO CSRF token ⇒ 200 (read-only, no CSRF required).
	authed := getGPU(t, router, cookies)
	if authed.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200; body=%s", authed.Code, authed.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(authed.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["state"] != "ok" {
		t.Fatalf("state = %v, want ok", body["state"])
	}
}
