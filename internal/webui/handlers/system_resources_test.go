package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dsdred/goal/internal/platform"
)

func getResources(t *testing.T, router http.Handler, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/resources", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func TestResourcesEndpointStates(t *testing.T) {
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
		wantDevices int
		wantSampled bool
	}{
		{"ok", okReport, "ok", 1, true},
		{"unavailable", platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}, "unavailable", 0, false},
		{"failed", platform.GpuReport{State: platform.GpuStateFailed, GPUs: []platform.GpuDevice{}, Error: "measurement_timeout"}, "measurement_failed", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeGpuProvider{report: tc.report}
			router := newGpuTestRouter(t, fake, false)
			resp := getResources(t, router, nil)

			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
			if atomic.LoadInt32(&fake.calls) != 1 {
				t.Fatalf("provider called %d times, want 1 per universal request", fake.calls)
			}
			var body map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["state"] != tc.wantState {
				t.Fatalf("state = %v, want %q", body["state"], tc.wantState)
			}
			devices, ok := body["devices"].([]any)
			if !ok {
				t.Fatalf("devices must be an array never null: %v", body["devices"])
			}
			if len(devices) != tc.wantDevices {
				t.Fatalf("len(devices) = %d, want %d", len(devices), tc.wantDevices)
			}
			if procs, ok := body["processes"].([]any); !ok || len(procs) != 0 {
				t.Fatalf("processes must be [] in slice 1: %v", body["processes"])
			}
			if body["system"] != nil {
				t.Fatalf("system must be null in slice 1: %v", body["system"])
			}
			if _, ok := body["capabilities"].(map[string]any); !ok {
				t.Fatalf("capabilities must be an object: %v", body["capabilities"])
			}
			if _, present := body["sampled_at"]; present != tc.wantSampled {
				t.Fatalf("sampled_at present = %v, want %v", present, tc.wantSampled)
			}
			if tc.wantDevices > 0 {
				d0 := devices[0].(map[string]any)
				if d0["device_id"] != "GPU-x" || d0["vendor"] != "nvidia" || d0["memory_class"] != "dedicated" {
					t.Fatalf("device0 = %v", d0)
				}
				res := d0["resources"].(map[string]any)
				total := res["vram_total"].(map[string]any)
				if total["value"] != float64(100) || total["provenance"] != "measured" || total["unit"] != "bytes" {
					t.Fatalf("vram_total reading = %v", total)
				}
			}
		})
	}
}

func TestResourcesEndpointAuthAndNoCSRF(t *testing.T) {
	fake := &fakeGpuProvider{report: platform.GpuReport{
		State:     platform.GpuStateOK,
		GPUs:      []platform.GpuDevice{{UUID: "GPU-x", Index: 0, Name: "N", Driver: "555.99", TotalBytes: 1, UsedBytes: 0, FreeBytes: 1}},
		SampledAt: "2026-11-19T00:00:00Z",
	}}
	router := newGpuTestRouter(t, fake, true)

	unauth := getResources(t, router, nil)
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

	authed := getResources(t, router, cookies)
	if authed.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200 (no CSRF required); body=%s", authed.Code, authed.Body.String())
	}
}

// C-5: the legacy GPU endpoint JSON is byte-identical to the provider report
// (unchanged GpuReport contract), and the two endpoints sample independently —
// no shared snapshot or cache between requests.
func TestLegacyGPUUnchangedAndIndependent(t *testing.T) {
	report := platform.GpuReport{
		State: platform.GpuStateOK,
		GPUs: []platform.GpuDevice{
			{UUID: "GPU-x", Index: 0, Name: "NVIDIA Test", Driver: "555.99", TotalBytes: 100, UsedBytes: 40, FreeBytes: 60},
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}
	fake := &fakeGpuProvider{report: report}
	router := newGpuTestRouter(t, fake, false)

	// Legacy body equals the marshaled GpuReport plus the encoder's trailing newline.
	gpuResp := getGPU(t, router, nil)
	want, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	want = append(want, '\n')
	if !bytes.Equal(gpuResp.Body.Bytes(), want) {
		t.Fatalf("legacy GPU body changed:\n got=%s\nwant=%s", gpuResp.Body.Bytes(), want)
	}

	// Universal request is a separate sample (provider called again, no cache).
	resResp := getResources(t, router, nil)
	if resResp.Code != http.StatusOK {
		t.Fatalf("resources status = %d, want 200", resResp.Code)
	}
	if atomic.LoadInt32(&fake.calls) != 2 {
		t.Fatalf("provider calls = %d, want 2 (legacy + universal sample independently)", fake.calls)
	}

	// A second universal request re-samples again (no last-good cache).
	getResources(t, router, nil)
	if atomic.LoadInt32(&fake.calls) != 3 {
		t.Fatalf("provider calls = %d, want 3 (no cache across universal requests)", fake.calls)
	}
}

func TestResourcesCPUOnlyHealthy(t *testing.T) {
	fake := &fakeGpuProvider{report: platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}}
	router := newGpuTestRouter(t, fake, false)
	resp := getResources(t, router, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("cpu-only status = %d, want 200 (absence is not an error)", resp.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["state"] != "unavailable" {
		t.Fatalf("state = %v, want unavailable", body["state"])
	}
	if arr, ok := body["devices"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("cpu-only devices must be [] never null: %v", body["devices"])
	}
}
