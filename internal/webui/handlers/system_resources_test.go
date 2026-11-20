package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dsdred/goal/internal/platform"
)

// fakeSystemProvider is a test-only SystemProvider (ADR 026 RES-03B) returning
// a fixed report, so handler/state logic is testable without a real 250 ms CPU
// measurement window or OS counters.
type fakeSystemProvider struct {
	report platform.SystemReport
	calls  int32
}

func (f *fakeSystemProvider) Sample(ctx context.Context) platform.SystemReport {
	atomic.AddInt32(&f.calls, 1)
	return f.report
}

func okSystemReport() platform.SystemReport {
	return platform.SystemReport{
		State: platform.SystemStateOK,
		RAMTotal: platform.SystemMetric{
			Available: true, OK: true, Value: 16 * 1024 * 1024 * 1024, Source: "fake total",
		},
		RAMAvailable: platform.SystemMetric{
			Available: true, OK: true, Value: 8 * 1024 * 1024 * 1024, Source: "fake available",
		},
		CPUCores: platform.SystemMetric{
			Available: true, OK: true, Value: 8, Source: "fake cores",
		},
		CPUUtil: platform.SystemMetric{
			Available: true, OK: true, Value: 25, Source: "fake util",
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}
}

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
	gpuOK := platform.GpuReport{
		State: platform.GpuStateOK,
		GPUs: []platform.GpuDevice{
			{UUID: "GPU-x", Index: 0, Name: "NVIDIA Test", Driver: "555.99", TotalBytes: 100, UsedBytes: 40, FreeBytes: 60},
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}
	gpuUnavailable := platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}
	gpuFailed := platform.GpuReport{State: platform.GpuStateFailed, GPUs: []platform.GpuDevice{}, Error: "measurement_timeout"}
	sysUnavailable := platform.SystemReport{State: platform.SystemStateUnavailable, Error: "platform_not_supported"}
	sysFailed := platform.SystemReport{State: platform.SystemStateFailed, Error: "meminfo_read_error"}

	cases := []struct {
		name        string
		gpu         platform.GpuReport
		sys         platform.SystemReport
		wantState   string
		wantDevices int
		wantSampled bool
		wantSystem  bool
	}{
		{"gpu ok + system ok", gpuOK, okSystemReport(), "ok", 1, true, true},
		{"cpu-only healthy (D-8.3)", gpuUnavailable, okSystemReport(), "ok", 0, true, true},
		{"gpu failed + system ok => partial", gpuFailed, okSystemReport(), "partial", 0, true, true},
		{"gpu ok + system unavailable", gpuOK, sysUnavailable, "ok", 1, true, false},
		{"gpu ok + system failed => partial", gpuOK, sysFailed, "partial", 1, true, false},
		{"both unavailable", gpuUnavailable, sysUnavailable, "unavailable", 0, false, false},
		{"both failed", gpuFailed, sysFailed, "measurement_failed", 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gpu := &fakeGpuProvider{report: tc.gpu}
			sys := &fakeSystemProvider{report: tc.sys}
			router := newResourceTestRouter(t, gpu, sys, false)
			resp := getResources(t, router, nil)

			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
			if atomic.LoadInt32(&gpu.calls) != 1 || atomic.LoadInt32(&sys.calls) != 1 {
				t.Fatalf("provider calls gpu=%d sys=%d, want 1 each per universal request", gpu.calls, sys.calls)
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
				t.Fatalf("processes must be [] (no process monitoring in this slice): %v", body["processes"])
			}
			if (body["system"] != nil) != tc.wantSystem {
				t.Fatalf("system present = %v, want %v: %v", body["system"] != nil, tc.wantSystem, body["system"])
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

// TestResourcesSystemShape pins the RES-03B system wire shape (ADR 026 D-11):
// ram.total/available measured bytes, ram.used derived estimate, cpu cores
// count and utilization percent — and the per-provider capabilities block.
func TestResourcesSystemShape(t *testing.T) {
	gpu := &fakeGpuProvider{report: platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}}
	sys := &fakeSystemProvider{report: okSystemReport()}
	router := newResourceTestRouter(t, gpu, sys, false)
	resp := getResources(t, router, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	system, ok := body["system"].(map[string]any)
	if !ok {
		t.Fatalf("system must be an object: %v", body["system"])
	}
	ram := system["ram"].(map[string]any)
	cpu := system["cpu"].(map[string]any)

	total := ram["total"].(map[string]any)
	if total["value"] != float64(16*1024*1024*1024) || total["unit"] != "bytes" || total["provenance"] != "measured" {
		t.Fatalf("ram.total = %v", total)
	}
	available := ram["available"].(map[string]any)
	if available["value"] != float64(8*1024*1024*1024) || available["provenance"] != "measured" {
		t.Fatalf("ram.available = %v", available)
	}
	used := ram["used"].(map[string]any)
	if used["value"] != float64(8*1024*1024*1024) || used["unit"] != "bytes" || used["provenance"] != "estimated" {
		t.Fatalf("ram.used = %v (must be the derived total-available estimate)", used)
	}
	if used["source"] != "system_ram_total - system_ram_available" {
		t.Fatalf("ram.used source = %v, want the named formula", used["source"])
	}
	cores := cpu["logical_cores"].(map[string]any)
	if cores["value"] != float64(8) || cores["unit"] != "count" || cores["provenance"] != "measured" {
		t.Fatalf("cpu.logical_cores = %v", cores)
	}
	util := cpu["utilization"].(map[string]any)
	if util["value"] != float64(25) || util["unit"] != "percent" || util["provenance"] != "measured" {
		t.Fatalf("cpu.utilization = %v", util)
	}

	caps := body["capabilities"].(map[string]any)
	sysCaps, ok := caps["system"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities must carry the system provider: %v", caps)
	}
	metrics := sysCaps["metrics"].(map[string]any)
	for _, key := range []string{"system_ram_total", "system_ram_available", "system_ram_used", "cpu_logical_cores", "cpu_util"} {
		m, ok := metrics[key].(map[string]any)
		if !ok {
			t.Fatalf("missing capability metric %q: %v", key, metrics)
		}
		if m["supported"] != true || m["available"] != true || m["sampled"] != true {
			t.Fatalf("metric %q levels = %v, want all true on a full ok sample", key, m)
		}
	}
}

// TestResourcesSystemOptionalAbsence: an optional metric the source cannot
// supply is a null Reading and never downgrades the report (D-6/D-7); an
// estimated available propagates provenance, and used stays derived.
func TestResourcesSystemOptionalAbsence(t *testing.T) {
	sr := okSystemReport()
	sr.RAMAvailable = platform.SystemMetric{
		Available: true, OK: true, Value: 4 * 1024 * 1024 * 1024, Estimated: true,
		Source: "vm_stat (free+inactive+speculative)*page_size", Reason: "estimated: (free+inactive+speculative)*page_size",
	}
	sr.CPUUtil = platform.SystemMetric{Available: true, OK: false, Source: "GetSystemTimes", Reason: "counter_delta_invalid"}
	sr.CPUCores = platform.SystemMetric{Available: false, OK: false, Reason: "cpu_cores_source_not_found"}
	gpu := &fakeGpuProvider{report: platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}}
	router := newResourceTestRouter(t, gpu, &fakeSystemProvider{report: sr}, false)
	resp := getResources(t, router, nil)
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["state"] != "ok" {
		t.Fatalf("state = %v, want ok (optional absence must not downgrade)", body["state"])
	}
	system := body["system"].(map[string]any)
	ram := system["ram"].(map[string]any)
	cpu := system["cpu"].(map[string]any)

	available := ram["available"].(map[string]any)
	if available["provenance"] != "estimated" || available["value"] != float64(4*1024*1024*1024) {
		t.Fatalf("estimated ram.available = %v", available)
	}
	used := ram["used"].(map[string]any)
	if used["value"] != float64(12*1024*1024*1024) || used["provenance"] != "estimated" {
		t.Fatalf("ram.used must derive from the estimated available: %v", used)
	}
	util := cpu["utilization"].(map[string]any)
	if util["value"] != nil || util["provenance"] != "unavailable" || util["reason"] != "counter_delta_invalid" {
		t.Fatalf("absent utilization must be null/unavailable with bounded reason: %v", util)
	}
	cores := cpu["logical_cores"].(map[string]any)
	if cores["value"] != nil || cores["provenance"] != "unavailable" {
		t.Fatalf("absent cores must be null/unavailable: %v", cores)
	}
}

// TestResourcesRAMUsedDependency: when available could not be sampled, used is
// unavailable too — never computed against a fabricated zero (D-6).
func TestResourcesRAMUsedDependency(t *testing.T) {
	sr := okSystemReport()
	sr.RAMAvailable = platform.SystemMetric{Available: true, OK: false, Source: "/proc/meminfo MemAvailable", Reason: "memavailable_not_present"}
	gpu := &fakeGpuProvider{report: platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}}
	router := newResourceTestRouter(t, gpu, &fakeSystemProvider{report: sr}, false)
	resp := getResources(t, router, nil)
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["state"] != "ok" {
		t.Fatalf("state = %v, want ok", body["state"])
	}
	ram := body["system"].(map[string]any)["ram"].(map[string]any)
	used := ram["used"].(map[string]any)
	if used["value"] != nil || used["provenance"] != "unavailable" || used["reason"] != "ram_dependency_unavailable" {
		t.Fatalf("used with missing dependency = %v", used)
	}
	available := ram["available"].(map[string]any)
	if available["value"] != nil || available["reason"] != "memavailable_not_present" {
		t.Fatalf("absent available = %v", available)
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
// (unchanged GpuReport contract, D-12), never samples the system provider, and
// the two endpoints sample independently — no shared snapshot or cache.
func TestLegacyGPUUnchangedAndIndependent(t *testing.T) {
	report := platform.GpuReport{
		State: platform.GpuStateOK,
		GPUs: []platform.GpuDevice{
			{UUID: "GPU-x", Index: 0, Name: "NVIDIA Test", Driver: "555.99", TotalBytes: 100, UsedBytes: 40, FreeBytes: 60},
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}
	gpu := &fakeGpuProvider{report: report}
	sys := &fakeSystemProvider{report: okSystemReport()}
	router := newResourceTestRouter(t, gpu, sys, false)

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
	if atomic.LoadInt32(&sys.calls) != 0 {
		t.Fatalf("legacy /gpu sampled the system provider %d times, want 0 (D-12 path unchanged)", sys.calls)
	}

	// Universal request is a separate sample (both providers called, no cache).
	resResp := getResources(t, router, nil)
	if resResp.Code != http.StatusOK {
		t.Fatalf("resources status = %d, want 200", resResp.Code)
	}
	if atomic.LoadInt32(&gpu.calls) != 2 || atomic.LoadInt32(&sys.calls) != 1 {
		t.Fatalf("calls gpu=%d sys=%d, want 2/1 (legacy + universal sample independently)", gpu.calls, sys.calls)
	}

	// A second universal request re-samples again (no last-good cache).
	getResources(t, router, nil)
	if atomic.LoadInt32(&gpu.calls) != 3 || atomic.LoadInt32(&sys.calls) != 2 {
		t.Fatalf("calls gpu=%d sys=%d, want 3/2 (no cache across universal requests)", gpu.calls, sys.calls)
	}
}

// D-8.3: a machine without NVIDIA is healthy — the report is ok with system
// data and devices [], never an error.
func TestResourcesCPUOnlyHealthy(t *testing.T) {
	gpu := &fakeGpuProvider{report: platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}}
	sys := &fakeSystemProvider{report: okSystemReport()}
	router := newResourceTestRouter(t, gpu, sys, false)
	resp := getResources(t, router, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("cpu-only status = %d, want 200 (absence is not an error)", resp.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["state"] != "ok" {
		t.Fatalf("state = %v, want ok (CPU-only healthy, D-8.3)", body["state"])
	}
	if arr, ok := body["devices"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("cpu-only devices must be [] never null: %v", body["devices"])
	}
	system, ok := body["system"].(map[string]any)
	if !ok {
		t.Fatalf("cpu-only report must carry system data: %v", body["system"])
	}
	total := system["ram"].(map[string]any)["total"].(map[string]any)
	if total["value"] != float64(16*1024*1024*1024) {
		t.Fatalf("cpu-only ram.total = %v", total)
	}
}
