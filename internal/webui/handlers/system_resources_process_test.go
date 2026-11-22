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
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/resource"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// ADR 026 Amendment A-026-1 handler-side tests: the processes[] contract on
// GET /api/v1/system/resources (C-11 wire shape, C-14 no lifecycle side
// effects and no legacy-path change, C-15 no new endpoint, auth-only).

type fakeProcessProvider struct {
	report      platform.ProcessMemReport
	calls       int32
	lastTracked []platform.TrackedProcess
}

func (f *fakeProcessProvider) Sample(_ context.Context, tracked []platform.TrackedProcess) platform.ProcessMemReport {
	atomic.AddInt32(&f.calls, 1)
	f.lastTracked = tracked
	return f.report
}

func (f *fakeProcessProvider) sampleCount() int32 { return atomic.LoadInt32(&f.calls) }

type fakeAttributor struct {
	snapshots []resource.TrackedProcessSnapshot
	calls     int32
}

func (f *fakeAttributor) TrackedLiveInstances(context.Context) []resource.TrackedProcessSnapshot {
	atomic.AddInt32(&f.calls, 1)
	return f.snapshots
}

func newProcessResourceTestRouter(t *testing.T, gpu platform.GpuProvider, sys platform.SystemProvider, proc platform.ProcessMemProvider, attr resource.InstanceAttributor, authEnabled bool) http.Handler {
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
	opts := []RouteRegistryOption{
		WithAuthEnabled(authEnabled),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
		WithGpuProvider(gpu),
		WithSystemProvider(sys),
	}
	if proc != nil {
		opts = append(opts, WithProcessMemProvider(proc))
	}
	if attr != nil {
		opts = append(opts, WithInstanceAttributor(attr))
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
		opts...,
	).Build()
}

func gpuUnavailableReport() platform.GpuReport {
	return platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}
}

func procEntry(instID string, pid int, ok bool, value int64, reason string) platform.ProcessMemEntry {
	e := platform.ProcessMemEntry{
		TrackedProcess: platform.TrackedProcess{InstanceID: instID, ModelID: "m-" + instID, RuntimeID: "r-" + instID, PipelineID: "p-" + instID, PipelineEntryID: "pe-" + instID, PID: pid},
		OK:             ok,
		Reason:         reason,
	}
	if ok {
		e.Value = value
		e.Source = "fake-counter"
	}
	return e
}

func TestResourcesProcessesWireShape(t *testing.T) {
	gpu := &fakeGpuProvider{report: gpuUnavailableReport()}
	sys := &fakeSystemProvider{report: okSystemReport()}
	proc := &fakeProcessProvider{report: platform.ProcessMemReport{
		State: platform.ProcessMemStateOK, SourceAvailable: true,
		Entries: []platform.ProcessMemEntry{
			procEntry("i1", 111, true, 1234, ""),
			procEntry("i2", 222, false, 0, platform.ProcessMemReasonIdentityMismatch),
		},
		SampledAt: "2026-11-22T00:00:00Z",
	}}
	attr := &fakeAttributor{snapshots: []resource.TrackedProcessSnapshot{
		{InstanceID: "i1", ModelID: "m-i1", RuntimeID: "r-i1", PipelineID: "p-i1", PipelineEntryID: "pe-i1", PID: 111},
		{InstanceID: "i2", ModelID: "m-i2", RuntimeID: "r-i2", PipelineID: "p-i2", PipelineEntryID: "pe-i2", PID: 222},
	}}
	router := newProcessResourceTestRouter(t, gpu, sys, proc, attr, false)

	resp := getResources(t, router, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	procs, ok := body["processes"].([]any)
	if !ok || len(procs) != 2 {
		t.Fatalf("processes = %v", body["processes"])
	}
	p0 := procs[0].(map[string]any)
	if p0["instance_id"] != "i1" || p0["model_id"] != "m-i1" || p0["runtime_id"] != "r-i1" || p0["pid"] != float64(111) {
		t.Fatalf("process0 identity = %v", p0)
	}
	if p0["pipeline_id"] != "p-i1" || p0["pipeline_entry_id"] != "pe-i1" {
		t.Fatalf("process0 pipeline joins = %v", p0)
	}
	rss0 := p0["resources"].(map[string]any)["process_rss"].(map[string]any)
	if rss0["value"] != float64(1234) || rss0["provenance"] != "measured" || rss0["unit"] != "bytes" {
		t.Fatalf("process0 rss = %v", rss0)
	}
	p1 := procs[1].(map[string]any)
	rss1 := p1["resources"].(map[string]any)["process_rss"].(map[string]any)
	if rss1["value"] != nil || rss1["provenance"] != "unavailable" || rss1["reason"] != platform.ProcessMemReasonIdentityMismatch {
		t.Fatalf("process1 rss = %v, want null measured-never-zero with bounded reason", rss1)
	}
	// A-6: the third capabilities provider block.
	caps := body["capabilities"].(map[string]any)
	pc, ok := caps["process"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.process missing: %v", caps)
	}
	if pc["vendor"] != "system" {
		t.Fatalf("capabilities.process vendor = %v", pc["vendor"])
	}
	lvl := pc["metrics"].(map[string]any)["process_rss"].(map[string]any)
	if lvl["supported"] != true || lvl["available"] != true || lvl["sampled"] != true {
		t.Fatalf("process_rss levels = %v, want supported/available/sampled true", lvl)
	}
	// C-11: exactly one attributor snapshot + one provider pass per request.
	if atomic.LoadInt32(&attr.calls) != 1 || proc.sampleCount() != 1 {
		t.Fatalf("calls attr=%d proc=%d, want 1/1", attr.calls, proc.sampleCount())
	}
	// The tracked set crossed the seam.
	if len(proc.lastTracked) != 2 || proc.lastTracked[0].InstanceID != "i1" {
		t.Fatalf("tracked = %+v", proc.lastTracked)
	}

	// No cache: a second request re-samples both layers.
	getResources(t, router, nil)
	if atomic.LoadInt32(&attr.calls) != 2 || proc.sampleCount() != 2 {
		t.Fatalf("second request calls attr=%d proc=%d, want 2/2 (A-4 no caching)", attr.calls, proc.sampleCount())
	}
}

// C-14 (shape half): the published processes=[] contract of the shipped slice
// stays byte-compatible when no attributor is wired at all.
func TestResourcesProcessesAbsentAttributorKeepsEmptyArray(t *testing.T) {
	gpu := &fakeGpuProvider{report: gpuUnavailableReport()}
	sys := &fakeSystemProvider{report: okSystemReport()}
	router := newProcessResourceTestRouter(t, gpu, sys, &fakeProcessProvider{}, nil, false)
	resp := getResources(t, router, nil)
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if procs, ok := body["processes"].([]any); !ok || len(procs) != 0 {
		t.Fatalf("processes = %v, want []", body["processes"])
	}
	caps := body["capabilities"].(map[string]any)
	if _, present := caps["process"]; present {
		t.Fatalf("capabilities.process must be absent without the attributor seam: %v", caps)
	}
}

func TestResourcesProcessesEmptyTrackedIsOK(t *testing.T) {
	gpu := &fakeGpuProvider{report: gpuUnavailableReport()}
	sys := &fakeSystemProvider{report: okSystemReport()}
	proc := &fakeProcessProvider{report: platform.ProcessMemReport{
		State: platform.ProcessMemStateOK, SourceAvailable: true, SampledAt: "2026-11-22T00:00:00Z",
	}}
	attr := &fakeAttributor{}
	router := newProcessResourceTestRouter(t, gpu, sys, proc, attr, false)
	resp := getResources(t, router, nil)
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["state"] != "ok" {
		t.Fatalf("state = %v, want ok (empty tracked set is a value, D-6)", body["state"])
	}
	if procs, ok := body["processes"].([]any); !ok || len(procs) != 0 {
		t.Fatalf("processes = %v, want []", body["processes"])
	}
	lvl := body["capabilities"].(map[string]any)["process"].(map[string]any)["metrics"].(map[string]any)["process_rss"].(map[string]any)
	if lvl["supported"] != true || lvl["available"] != true || lvl["sampled"] != false {
		t.Fatalf("empty-pass levels = %v, want supported+available, not sampled", lvl)
	}
}

// C-15: the universal endpoint is auth-only, never CSRF-gated, and 401s
// unauthenticated requests like the rest of the API surface.
func TestResourcesProcessesAuthOnly(t *testing.T) {
	gpu := &fakeGpuProvider{report: gpuUnavailableReport()}
	sys := &fakeSystemProvider{report: okSystemReport()}
	proc := &fakeProcessProvider{report: platform.ProcessMemReport{State: platform.ProcessMemStateOK, SourceAvailable: true}}
	attr := &fakeAttributor{}
	router := newProcessResourceTestRouter(t, gpu, sys, proc, attr, true)

	unauth := getResources(t, router, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauth.Code)
	}
	if proc.sampleCount() != 0 {
		t.Fatalf("unauthenticated request sampled the process provider %d times, want 0", proc.sampleCount())
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp := httptest.NewRecorder()
	router.ServeHTTP(loginResp, loginReq)
	cookies := loginResp.Result().Cookies()

	authed := getResources(t, router, cookies)
	if authed.Code != http.StatusOK {
		t.Fatalf("authenticated GET (no CSRF token) = %d, want 200 (read-only endpoint)", authed.Code)
	}
}

// C-14 (behavior half): attribution is observation-only. A real fake-runtime
// instance transitions Running -> (stop) -> Exited exactly as without this
// slice, appears in processes[] while live with its launch PID, and
// disappears after the terminal transition. No control surface is touched.
func TestResourcesProcessesLifecycleSideEffectFree(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	// The recorded launch executable must equal what the OS reports for the
	// live process: /proc/<pid>/exe resolves symlinks on Linux, so the seed
	// path is normalized exactly as in the application attribution test.
	fakePath, err := filepath.EvalSymlinks(fakeruntime.Path(t))
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-proc", Name: "proc-runtime", Executable: fakePath}); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{
		ID: "m-proc", Name: "proc-model", RuntimeID: "rt-proc", Args: []string{"infinite"},
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	supervisor := process.NewSupervisor(repo)
	svc := application.NewInstanceService(supervisor, repo)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = supervisor.Shutdown(ctx)
	})

	passwords := security.NewPasswordStore()
	if err := passwords.SetPassword("admin", "secret"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>GoAl</title>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
	}
	router := NewRouteRegistry(
		svc,
		application.NewRuntimeService(repo),
		application.NewModelService(repo),
		application.NewPipelineService(supervisor, repo),
		supervisor,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		passwords,
		WithAuthEnabled(false),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
		WithGpuProvider(&fakeGpuProvider{report: gpuUnavailableReport()}),
		WithSystemProvider(&fakeSystemProvider{report: okSystemReport()}),
		WithProcessMemProvider(platform.NewProcessMemProvider()),
		WithInstanceAttributor(svc),
	).Build()

	ctx := context.Background()
	inst, err := svc.StartModel(ctx, "m-proc")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	if err := waitForSupervisorState(t, supervisor, inst.ID, domain.InstanceStateRunning, 20*time.Second); err != nil {
		t.Fatalf("fake runtime never reached running: %v", err)
	}

	resp := getResources(t, router, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found map[string]any
	for _, p := range body["processes"].([]any) {
		m := p.(map[string]any)
		if m["instance_id"] == string(inst.ID) {
			found = m
			break
		}
	}
	if platform.ProcessMemSupported() {
		// A-7 C-13 on Windows/Linux: a real GoAl launch must be attributed as
		// measured with a positive RSS and identity verified. An unavailable
		// outcome is a contract-valid refusal elsewhere, but here it would
		// mean the check silently never ran — it is not accepted as a pass.
		if found == nil {
			t.Fatalf("running instance missing from processes: %v | state=%v caps=%v", body["processes"], body["state"], body["capabilities"])
		}
		if found["pid"] != float64(inst.PID) {
			t.Fatalf("attributed pid = %v, want the launch-record pid %d", found["pid"], inst.PID)
		}
		rss := found["resources"].(map[string]any)["process_rss"].(map[string]any)
		if rss["provenance"] != "measured" {
			t.Fatalf("process_rss provenance = %v reason = %v on a supported platform: want measured (unavailable is never a pass here)", rss["provenance"], rss["reason"])
		}
		if v, ok := rss["value"].(float64); !ok || v <= 0 {
			t.Fatalf("measured rss value = %v, want a positive byte count", rss["value"])
		}
	} else {
		// Unsupported platform: no identity source exists, so the entry is
		// never attributed at all (A-3/A-4) — processes carries no rows.
		if found != nil {
			t.Fatalf("unsupported platform attributed a process entry: %v", found)
		}
	}

	// Observation left the instance alive and running (no side effect).
	status, err := svc.GetInstanceStatus(ctx, inst.ID)
	if err != nil || status.State != domain.InstanceStateRunning {
		t.Fatalf("post-sample state = %v err=%v, want running untouched", status, err)
	}

	// The unconfirmed orphan of a dead PID-like store row would still be
	// emitted with its launch PID — covered in platform; here: after the
	// real stop the entry disappears with the emitted state.
	if err := svc.StopInstance(ctx, inst.ID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	resp = getResources(t, router, nil)
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, p := range body["processes"].([]any) {
		if m := p.(map[string]any); m["instance_id"] == string(inst.ID) {
			t.Fatalf("terminal instance still attributed: %v", m)
		}
	}
	// And the lifecycle itself is unchanged by this slice: exited with a PID
	// fact preserved, exactly the pre-slice behavior.
	after, err := svc.GetInstanceStatus(ctx, inst.ID)
	if err != nil {
		t.Fatalf("GetInstanceStatus after stop: %v", err)
	}
	if after.State != domain.InstanceStateExited {
		t.Fatalf("state after stop = %q, want exited", after.State)
	}
}

// C-14 (legacy paths): the GPU legacy endpoint still never touches the
// process provider (its D-12 path is unchanged).
func TestLegacyGPUUntouchedByProcessProvider(t *testing.T) {
	gpu := &fakeGpuProvider{report: platform.GpuReport{
		State: platform.GpuStateOK,
		GPUs:  []platform.GpuDevice{{UUID: "GPU-x", Index: 0, Name: "N", Driver: "555.99", TotalBytes: 1, UsedBytes: 0, FreeBytes: 1}},
	}}
	sys := &fakeSystemProvider{report: okSystemReport()}
	proc := &fakeProcessProvider{report: platform.ProcessMemReport{State: platform.ProcessMemStateOK, SourceAvailable: true}}
	attr := &fakeAttributor{}
	router := newProcessResourceTestRouter(t, gpu, sys, proc, attr, false)

	resp := getGPU(t, router, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("legacy gpu status = %d", resp.Code)
	}
	if proc.sampleCount() != 0 || atomic.LoadInt32(&attr.calls) != 0 {
		t.Fatalf("legacy /gpu path sampled process provider=%d attributor=%d, want 0/0", proc.sampleCount(), attr.calls)
	}
}
