package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/platform"
)

// fakeGpu is a test-only platform.GpuProvider returning a fixed report and
// counting Sample calls, so the adapter mapping is testable without hardware.
type fakeGpu struct {
	report platform.GpuReport
	calls  int
}

func (f *fakeGpu) Sample(ctx context.Context) platform.GpuReport {
	f.calls++
	return f.report
}

func TestNvidiaAdapterIdentityAndStaticCapabilities(t *testing.T) {
	a := NewNvidiaAdapter(&fakeGpu{})
	if a.ID() != "nvidia-smi" {
		t.Fatalf("ID = %q, want nvidia-smi", a.ID())
	}
	if a.Vendor() != VendorNVIDIA {
		t.Fatalf("Vendor = %q, want nvidia", a.Vendor())
	}
	caps := a.Capabilities()
	for _, m := range []ResourceType{ResourceVRAMTotal, ResourceVRAMUsed, ResourceVRAMFree} {
		if !caps.Supported[m] {
			t.Fatalf("metric %q must be statically supported", m)
		}
	}
	if caps.Supported["gpu_util"] {
		t.Fatalf("gpu_util is not supported by the RES-02A query")
	}
}

func TestNvidiaAdapterMapsOKReport(t *testing.T) {
	g := &fakeGpu{report: platform.GpuReport{
		State: platform.GpuStateOK,
		GPUs: []platform.GpuDevice{
			{UUID: "GPU-aaa", Index: 0, Name: "NVIDIA Test", Driver: "555.99", TotalBytes: 24576 * 1024 * 1024, UsedBytes: 1024 * 1024 * 1024, FreeBytes: 23552 * 1024 * 1024},
			{UUID: "GPU-bbb", Index: 1, Name: "NVIDIA Other", Driver: "555.99", TotalBytes: 100, UsedBytes: 0, FreeBytes: 100},
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}}
	a := NewNvidiaAdapter(g)
	pr := a.Sample(context.Background())

	if g.calls != 1 {
		t.Fatalf("provider Sample called %d times, want 1 per request", g.calls)
	}
	if pr.State != StateOK {
		t.Fatalf("state = %q, want ok", pr.State)
	}
	if pr.ProviderID != "nvidia-smi" || pr.Vendor != VendorNVIDIA {
		t.Fatalf("provider identity = %q/%q", pr.ProviderID, pr.Vendor)
	}
	if pr.Error != "" {
		t.Fatalf("ok report carries error %q", pr.Error)
	}
	if pr.SampledAt != "2026-11-19T00:00:00Z" {
		t.Fatalf("sampled_at = %q, want preserved", pr.SampledAt)
	}
	if len(pr.Devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(pr.Devices))
	}
	d0 := pr.Devices[0]
	if d0.DeviceID != "GPU-aaa" || d0.Index != 0 || d0.Name != "NVIDIA Test" || d0.Driver != "555.99" {
		t.Fatalf("device0 identity = %+v", d0)
	}
	if d0.MemoryClass != MemoryDedicated {
		t.Fatalf("NVIDIA discrete memory_class = %q, want dedicated", d0.MemoryClass)
	}
	if got := *d0.Resources[ResourceVRAMTotal].Value; got != 24576*1024*1024 {
		t.Fatalf("total = %d, want MiB×1048576", got)
	}
	if d0.Resources[ResourceVRAMTotal].Provenance != ProvenanceMeasured {
		t.Fatalf("provenance = %q, want measured", d0.Resources[ResourceVRAMTotal].Provenance)
	}
	// measured zero is a value, not null (D-6).
	d1 := pr.Devices[1]
	if d1.Resources[ResourceVRAMUsed].Value == nil || *d1.Resources[ResourceVRAMUsed].Value != 0 {
		t.Fatalf("measured zero must be 0 not null: %+v", d1.Resources[ResourceVRAMUsed])
	}
	// stable distinct device IDs (D-9).
	if pr.Devices[0].DeviceID == pr.Devices[1].DeviceID {
		t.Fatalf("device IDs are not distinct")
	}
	for _, m := range nvidiaSupportedMetrics {
		lvl := pr.Capabilities.Metrics[m]
		if !lvl.Supported || !lvl.Available || !lvl.Sampled {
			t.Fatalf("ok metric %q levels = %+v, want all true", m, lvl)
		}
	}
}

func TestNvidiaAdapterMapsUnavailableReport(t *testing.T) {
	g := &fakeGpu{report: platform.GpuReport{State: platform.GpuStateUnavailable, GPUs: []platform.GpuDevice{}, Error: "nvidia_smi_not_found"}}
	pr := NewNvidiaAdapter(g).Sample(context.Background())
	if pr.State != StateUnavailable {
		t.Fatalf("state = %q, want unavailable", pr.State)
	}
	if len(pr.Devices) != 0 {
		t.Fatalf("unavailable must have no devices, got %d", len(pr.Devices))
	}
	if pr.Error != "nvidia_smi_not_found" {
		t.Fatalf("error = %q, want preserved bounded token", pr.Error)
	}
	if pr.SampledAt != "" {
		t.Fatalf("unavailable must not carry sampled_at")
	}
	for _, m := range nvidiaSupportedMetrics {
		lvl := pr.Capabilities.Metrics[m]
		if !lvl.Supported {
			t.Fatalf("supported must stay true on unavailable (static fact)")
		}
		if lvl.Available || lvl.Sampled {
			t.Fatalf("unavailable metric %q must be available=false sampled=false, got %+v", m, lvl)
		}
	}
}

func TestNvidiaAdapterMapsFailedReportKeepsAvailable(t *testing.T) {
	g := &fakeGpu{report: platform.GpuReport{State: platform.GpuStateFailed, GPUs: []platform.GpuDevice{}, Error: "measurement_timeout"}}
	pr := NewNvidiaAdapter(g).Sample(context.Background())
	if pr.State != StateMeasurementFailed {
		t.Fatalf("state = %q, want measurement_failed", pr.State)
	}
	if pr.Error != "measurement_timeout" {
		t.Fatalf("error = %q, want preserved", pr.Error)
	}
	// Transient failure: source stays available, only sampled is false (D-3/C-6).
	for _, m := range nvidiaSupportedMetrics {
		lvl := pr.Capabilities.Metrics[m]
		if !lvl.Supported || !lvl.Available {
			t.Fatalf("transient failure must not change supported/available for %q: %+v", m, lvl)
		}
		if lvl.Sampled {
			t.Fatalf("failed sample must not be sampled for %q", m)
		}
	}
}

// --- Real exec path through the adapter with a fake nvidia-smi binary (C-2/C-6).

var fakeBinary struct {
	once sync.Once
	path string
	err  error
}

func fakeExeName() string {
	if runtime.GOOS == "windows" {
		return "nvidia-smi.exe"
	}
	return "nvidia-smi"
}

func buildFakeBinary() {
	dir, err := os.MkdirTemp("", "goal-resource-fake-gpu-")
	if err != nil {
		fakeBinary.err = err
		return
	}
	fakeBinary.path = filepath.Join(dir, fakeExeName())
	cmd := exec.Command("go", "build", "-o", fakeBinary.path, "github.com/dsdred/goal/internal/platform/testdata/fakegpu")
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		fakeBinary.err = fmt.Errorf("go build fakegpu: %w\n%s", buildErr, out)
	}
}

type binarySpec struct {
	CSV      string `json:"csv"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	SleepMS  int    `json:"sleep_ms"`
	Hang     bool   `json:"hang"`
}

func writeBinarySpec(t *testing.T, dir string, s binarySpec) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), data, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
}

// newFakeBinaryAdapter builds a real NvidiaSmiProvider (real discovery seam +
// exec.CommandContext + CSV parse + kill) whose lookup resolves to a per-test
// fake executable, wrapped in the adapter. The returned *string lets a test
// remove the source between requests (set it to "").
func newFakeBinaryAdapter(t *testing.T, timeout time.Duration, s binarySpec) (*NvidiaAdapter, string, *string) {
	t.Helper()
	fakeBinary.once.Do(buildFakeBinary)
	if fakeBinary.err != nil {
		t.Fatalf("build fakegpu: %v", fakeBinary.err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, fakeExeName())
	data, err := os.ReadFile(fakeBinary.path)
	if err != nil {
		t.Fatalf("read fakegpu binary: %v", err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatalf("write fakegpu copy: %v", err)
	}
	writeBinarySpec(t, dir, s)
	path := exe
	p := platform.NewNvidiaSmiProviderWithLookup(timeout, func() (string, error) {
		if path == "" {
			return "", exec.ErrNotFound
		}
		return path, nil
	})
	return NewNvidiaAdapter(p), dir, &path
}

func runCount(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "runs.txt"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read runs.txt: %v", err)
	}
	return len(strings.Fields(strings.TrimSpace(string(data))))
}

const validTwoGPU = "0, GPU-aaa, NVIDIA GeForce RTX 4090, 555.99, 24576, 1024, 23552\n" +
	"1, GPU-bbb, NVIDIA GeForce RTX 3080, 555.99, 10240, 512, 9728\n"

func TestNvidiaAdapterRealBinaryValid(t *testing.T) {
	a, dir, _ := newFakeBinaryAdapter(t, 3*time.Second, binarySpec{CSV: validTwoGPU})
	pr := a.Sample(context.Background())
	if pr.State != StateOK {
		t.Fatalf("state = %q, want ok (error=%q)", pr.State, pr.Error)
	}
	if len(pr.Devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(pr.Devices))
	}
	if got := *pr.Devices[0].Resources[ResourceVRAMTotal].Value; got != 24576*1024*1024 {
		t.Fatalf("total bytes = %d, want MiB×1048576", got)
	}
	if pr.Devices[0].DeviceID != "GPU-aaa" {
		t.Fatalf("device_id = %q, want GPU-aaa", pr.Devices[0].DeviceID)
	}
	if n := runCount(t, dir); n != 1 {
		t.Fatalf("one universal request spawned %d children, want 1", n)
	}
}

func TestNvidiaAdapterRealBinaryNAFails(t *testing.T) {
	a, _, _ := newFakeBinaryAdapter(t, 3*time.Second, binarySpec{CSV: "0, GPU-aaa, Name, 555.99, 24576, N/A, 23552\n"})
	pr := a.Sample(context.Background())
	if pr.State != StateMeasurementFailed || pr.Error != "invalid_numeric_field" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/invalid_numeric_field", pr.State, pr.Error)
	}
	if !pr.Capabilities.Metrics[ResourceVRAMTotal].Available {
		t.Fatalf("found-but-failing source must stay available")
	}
}

func TestNvidiaAdapterRealBinaryMalformedFails(t *testing.T) {
	// Warm-up absorbs the cold first execution of the fresh exe copy (Windows
	// process creation / AV scan can exceed a tight deadline); the cache-free
	// provider re-executes the child for the semantics check. 5s = gpuDefaultTimeout.
	a, dir, _ := newFakeBinaryAdapter(t, 5*time.Second, binarySpec{CSV: validTwoGPU})
	if warm := a.Sample(context.Background()); warm.State != StateOK {
		t.Fatalf("warm-up state/error = %q/%q, want ok", warm.State, warm.Error)
	}
	writeBinarySpec(t, dir, binarySpec{CSV: "0, GPU-aaa, Name\n"})
	pr := a.Sample(context.Background())
	if pr.State != StateMeasurementFailed || pr.Error != "malformed_csv" {
		t.Fatalf("state/error = %q/%q (runs=%d), want measurement_failed/malformed_csv", pr.State, pr.Error, runCount(t, dir))
	}
}

func TestNvidiaAdapterRealBinaryMissingUnavailable(t *testing.T) {
	a, _, path := newFakeBinaryAdapter(t, time.Second, binarySpec{CSV: validTwoGPU})
	*path = "" // source absent
	pr := a.Sample(context.Background())
	if pr.State != StateUnavailable || pr.Error != "nvidia_smi_not_found" {
		t.Fatalf("state/error = %q/%q, want unavailable/nvidia_smi_not_found", pr.State, pr.Error)
	}
	if pr.Capabilities.Metrics[ResourceVRAMTotal].Available {
		t.Fatalf("absent source must not be available")
	}
}

func TestNvidiaAdapterRealBinaryNoDriverUnavailable(t *testing.T) {
	a, _, _ := newFakeBinaryAdapter(t, 3*time.Second, binarySpec{
		Stderr:   "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver.",
		ExitCode: 9,
	})
	pr := a.Sample(context.Background())
	if pr.State != StateUnavailable || pr.Error != "nvidia_driver_unavailable" {
		t.Fatalf("state/error = %q/%q, want unavailable/nvidia_driver_unavailable", pr.State, pr.Error)
	}
}

// C-6: binary discovery is not driver health — a found binary returning an
// unclassified error is measurement_failed with available=true, sampled=false.
func TestNvidiaAdapterRealBinaryFoundButFailing(t *testing.T) {
	a, _, _ := newFakeBinaryAdapter(t, 3*time.Second, binarySpec{Stderr: "unexpected internal error", ExitCode: 1})
	pr := a.Sample(context.Background())
	if pr.State != StateMeasurementFailed || pr.Error != "nvidia_smi_exit_error" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/nvidia_smi_exit_error", pr.State, pr.Error)
	}
	lvl := pr.Capabilities.Metrics[ResourceVRAMTotal]
	if !lvl.Supported || !lvl.Available || lvl.Sampled {
		t.Fatalf("found-but-failing levels = %+v, want supported+available true, sampled false", lvl)
	}
}

func TestNvidiaAdapterRealBinaryHangBounded(t *testing.T) {
	// Warm-up absorbs the cold first execution of the fresh exe copy so the hang
	// child reliably starts (runs guard) within the production 5s deadline. The
	// bounded-execution check keeps an upper limit: 5s + 2s WaitDelay + margin.
	a, dir, _ := newFakeBinaryAdapter(t, 5*time.Second, binarySpec{CSV: validTwoGPU})
	if warm := a.Sample(context.Background()); warm.State != StateOK {
		t.Fatalf("warm-up state/error = %q/%q, want ok", warm.State, warm.Error)
	}
	writeBinarySpec(t, dir, binarySpec{Hang: true})
	before := runCount(t, dir)
	start := time.Now()
	pr := a.Sample(context.Background())
	elapsed := time.Since(start)
	if pr.State != StateMeasurementFailed || pr.Error != "measurement_timeout" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/measurement_timeout", pr.State, pr.Error)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("hanging child not bounded: took %v", elapsed)
	}
	if n := runCount(t, dir); n != before+1 {
		t.Fatalf("runs = %d, want %d (hang child must actually start)", n, before+1)
	}
}

// C-6: a transient failure then success never flips supported/available; only
// sampled changes. No cache: the second request re-samples.
func TestNvidiaAdapterTransientFailureThenSuccess(t *testing.T) {
	a, dir, _ := newFakeBinaryAdapter(t, 3*time.Second, binarySpec{CSV: "0, GPU-aaa, Name, 555.99, 24576, N/A, 23552\n"})
	first := a.Sample(context.Background())
	if first.State != StateMeasurementFailed {
		t.Fatalf("first state = %q, want measurement_failed", first.State)
	}
	writeBinarySpec(t, dir, binarySpec{CSV: validTwoGPU})
	second := a.Sample(context.Background())
	if second.State != StateOK {
		t.Fatalf("second state = %q, want ok (no stale failure)", second.State)
	}
	for _, m := range nvidiaSupportedMetrics {
		f := first.Capabilities.Metrics[m]
		s := second.Capabilities.Metrics[m]
		if !f.Supported || !s.Supported {
			t.Fatalf("supported flipped across transient failure for %q", m)
		}
		if !f.Available || !s.Available {
			t.Fatalf("available flipped across transient failure for %q", m)
		}
		if f.Sampled || !s.Sampled {
			t.Fatalf("sampled should be false then true for %q: %v -> %v", m, f.Sampled, s.Sampled)
		}
	}
	if n := runCount(t, dir); n != 2 {
		t.Fatalf("two sequential requests spawned %d children, want 2 (no cache)", n)
	}
}

// C-6: available changes only because discovery changed (source removed between
// requests), never because of a transient measurement error.
func TestNvidiaAdapterSourceRemovedBetweenRequests(t *testing.T) {
	a, _, path := newFakeBinaryAdapter(t, 3*time.Second, binarySpec{CSV: validTwoGPU})
	first := a.Sample(context.Background())
	if !first.Capabilities.Metrics[ResourceVRAMTotal].Available {
		t.Fatalf("first request source present must be available")
	}
	*path = "" // remove the source
	second := a.Sample(context.Background())
	if second.State != StateUnavailable {
		t.Fatalf("second state = %q, want unavailable after source removal", second.State)
	}
	if second.Capabilities.Metrics[ResourceVRAMTotal].Available {
		t.Fatalf("available must follow discovery (source removed ⇒ false)")
	}
	if !second.Capabilities.Metrics[ResourceVRAMTotal].Supported {
		t.Fatalf("supported is static and must survive source removal")
	}
}
