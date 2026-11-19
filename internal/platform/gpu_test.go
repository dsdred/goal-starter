package platform

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
)

// fakeGpu is the package-scoped fake nvidia-smi binary, built once for the test
// runner's GOOS/GOARCH. Each test copies it into its own t.TempDir with a sibling
// spec.json, so runs are isolated and no process-global environment is mutated.
var fakeGpu struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func fakeGpuExeName() string {
	if runtime.GOOS == "windows" {
		return "nvidia-smi.exe"
	}
	return "nvidia-smi"
}

func buildFakeGpu() {
	dir, err := os.MkdirTemp("", "goal-fake-gpu-")
	if err != nil {
		fakeGpu.err = err
		return
	}
	fakeGpu.dir = dir
	fakeGpu.path = filepath.Join(dir, fakeGpuExeName())
	cmd := exec.Command("go", "build", "-o", fakeGpu.path, "./testdata/fakegpu")
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		fakeGpu.err = fmt.Errorf("go build fakegpu: %w\n%s", buildErr, out)
	}
}

func cleanupFakeGpu() error {
	if fakeGpu.dir == "" {
		return fakeGpu.err
	}
	if err := os.RemoveAll(fakeGpu.dir); err != nil {
		return err
	}
	return fakeGpu.err
}

type gpuSpec struct {
	CSV      string `json:"csv"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	SleepMS  int    `json:"sleep_ms"`
	Hang     bool   `json:"hang"`
}

func writeGpuSpec(t *testing.T, dir string, s gpuSpec) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), data, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
}

// newFakeGpuProvider builds a real NvidiaSmiProvider whose discovery resolves to
// a per-test copy of the fake executable, exercising the real exec.CommandContext
// + parse + kill path without NVIDIA hardware (ADR 025 C-2).
func newFakeGpuProvider(t *testing.T, timeout time.Duration, s gpuSpec) (*NvidiaSmiProvider, string) {
	t.Helper()
	fakeGpu.once.Do(buildFakeGpu)
	if fakeGpu.err != nil {
		t.Fatalf("build fakegpu: %v", fakeGpu.err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, fakeGpuExeName())
	data, err := os.ReadFile(fakeGpu.path)
	if err != nil {
		t.Fatalf("read fakegpu binary: %v", err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatalf("write fakegpu copy: %v", err)
	}
	writeGpuSpec(t, dir, s)
	p := &NvidiaSmiProvider{timeout: timeout, lookup: func() (string, error) { return exe, nil }}
	return p, dir
}

func gpuRunCount(t *testing.T, dir string) int {
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

func TestGpuProviderValidMultiGPU(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 3*time.Second, gpuSpec{CSV: validTwoGPU})
	r := p.Sample(context.Background())

	if r.State != GpuStateOK {
		t.Fatalf("state = %q, want ok (error=%q)", r.State, r.Error)
	}
	if r.Error != "" {
		t.Fatalf("ok report carries error %q", r.Error)
	}
	if len(r.GPUs) != 2 {
		t.Fatalf("len(gpus) = %d, want 2", len(r.GPUs))
	}
	if r.SampledAt == "" {
		t.Fatal("ok report missing sampled_at")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.SampledAt); err != nil {
		t.Fatalf("sampled_at %q is not RFC3339Nano: %v", r.SampledAt, err)
	}

	d0 := r.GPUs[0]
	if d0.UUID != "GPU-aaa" || d0.Index != 0 || d0.Name != "NVIDIA GeForce RTX 4090" || d0.Driver != "555.99" {
		t.Fatalf("device0 identity = %+v", d0)
	}
	if d0.TotalBytes != 24576*mib || d0.UsedBytes != 1024*mib || d0.FreeBytes != 23552*mib {
		t.Fatalf("device0 bytes = %+v", d0)
	}
	d1 := r.GPUs[1]
	if d1.UUID != "GPU-bbb" || d1.Index != 1 || d1.TotalBytes != 10240*mib {
		t.Fatalf("device1 = %+v", d1)
	}
	if d0.UUID == d1.UUID {
		t.Fatal("multi-GPU UUIDs are not distinct")
	}
}

func TestGpuProviderNAFieldFails(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 3*time.Second, gpuSpec{CSV: "0, GPU-aaa, Name, 555.99, 24576, N/A, 23552\n"})
	r := p.Sample(context.Background())
	if r.State != GpuStateFailed || r.Error != "invalid_numeric_field" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/invalid_numeric_field", r.State, r.Error)
	}
	if len(r.GPUs) != 0 || r.SampledAt != "" {
		t.Fatalf("failed report must have empty gpus and no sampled_at: %+v", r)
	}
}

func TestGpuProviderMalformedFails(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 3*time.Second, gpuSpec{CSV: "0, GPU-aaa, Name\n"})
	r := p.Sample(context.Background())
	if r.State != GpuStateFailed || r.Error != "malformed_csv" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/malformed_csv", r.State, r.Error)
	}
}

func TestGpuProviderMissingBinaryUnavailable(t *testing.T) {
	p := &NvidiaSmiProvider{timeout: time.Second, lookup: func() (string, error) { return "", exec.ErrNotFound }}
	r := p.Sample(context.Background())
	if r.State != GpuStateUnavailable || r.Error != "nvidia_smi_not_found" {
		t.Fatalf("state/error = %q/%q, want gpu_unavailable/nvidia_smi_not_found", r.State, r.Error)
	}
	if len(r.GPUs) != 0 || r.SampledAt != "" {
		t.Fatalf("unavailable report must have empty gpus and no sampled_at: %+v", r)
	}
}

func TestGpuProviderNoDriverExitUnavailable(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 3*time.Second, gpuSpec{
		Stderr:   "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver. Make sure that the latest NVIDIA driver is installed and running.",
		ExitCode: 9,
	})
	r := p.Sample(context.Background())
	if r.State != GpuStateUnavailable || r.Error != "nvidia_driver_unavailable" {
		t.Fatalf("state/error = %q/%q, want gpu_unavailable/nvidia_driver_unavailable", r.State, r.Error)
	}
}

func TestGpuProviderUnclassifiedExitFails(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 3*time.Second, gpuSpec{Stderr: "unexpected internal error", ExitCode: 1})
	r := p.Sample(context.Background())
	if r.State != GpuStateFailed || r.Error != "nvidia_smi_exit_error" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/nvidia_smi_exit_error", r.State, r.Error)
	}
}

func TestGpuProviderEmptyOutputUnavailable(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 3*time.Second, gpuSpec{CSV: "", ExitCode: 0})
	r := p.Sample(context.Background())
	if r.State != GpuStateUnavailable || r.Error != "no_devices" {
		t.Fatalf("state/error = %q/%q, want gpu_unavailable/no_devices", r.State, r.Error)
	}
}

func TestGpuProviderTimeoutKillsChild(t *testing.T) {
	// The deadline must exceed process-startup latency (notably on Windows) so
	// the child reaches its hang loop and is then killed by the timeout.
	p, dir := newFakeGpuProvider(t, 2*time.Second, gpuSpec{Hang: true})
	start := time.Now()
	r := p.Sample(context.Background())
	elapsed := time.Since(start)

	if r.State != GpuStateFailed || r.Error != "measurement_timeout" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/measurement_timeout", r.State, r.Error)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("hanging child not bounded: took %v", elapsed)
	}
	if n := gpuRunCount(t, dir); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}

func TestGpuProviderSingleFlight(t *testing.T) {
	p, dir := newFakeGpuProvider(t, 5*time.Second, gpuSpec{SleepMS: 300, CSV: validTwoGPU})
	const n = 8
	reports := make([]GpuReport, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reports[i] = p.Sample(context.Background())
		}(i)
	}
	wg.Wait()

	for i, r := range reports {
		if r.State != GpuStateOK {
			t.Fatalf("caller %d state = %q, want ok", i, r.State)
		}
		if r.SampledAt != reports[0].SampledAt {
			t.Fatalf("caller %d sampled_at = %q, want shared %q (not single-flight)", i, r.SampledAt, reports[0].SampledAt)
		}
	}
	if got := gpuRunCount(t, dir); got != 1 {
		t.Fatalf("concurrent callers spawned %d children, want 1 (single-flight)", got)
	}
}

func TestGpuProviderNoCache(t *testing.T) {
	p, dir := newFakeGpuProvider(t, 3*time.Second, gpuSpec{CSV: validTwoGPU})
	r1 := p.Sample(context.Background())
	if r1.State != GpuStateOK {
		t.Fatalf("first sample state = %q, want ok", r1.State)
	}

	// A subsequent failed read must never return the prior ok (D-7).
	writeGpuSpec(t, dir, gpuSpec{CSV: "0, GPU-aaa, Name, 555.99, 24576, N/A, 23552\n"})
	r2 := p.Sample(context.Background())
	if r2.State != GpuStateFailed {
		t.Fatalf("second sample state = %q, want measurement_failed (no stale ok)", r2.State)
	}
	if r2.SampledAt != "" || len(r2.GPUs) != 0 {
		t.Fatalf("failed read leaked prior success: %+v", r2)
	}

	// A fresh ok carries a new timestamp, never the stored one.
	writeGpuSpec(t, dir, gpuSpec{CSV: validTwoGPU})
	r3 := p.Sample(context.Background())
	if r3.State != GpuStateOK {
		t.Fatalf("third sample state = %q, want ok", r3.State)
	}
	if r3.SampledAt == r1.SampledAt {
		t.Fatalf("sampled_at reused across samples (%q) — cached", r3.SampledAt)
	}
}

func TestGpuProviderWaiterCancellation(t *testing.T) {
	p, _ := newFakeGpuProvider(t, 5*time.Second, gpuSpec{SleepMS: 600, CSV: validTwoGPU})
	ownerDone := make(chan GpuReport, 1)
	go func() { ownerDone <- p.Sample(context.Background()) }()

	// Wait until the owner's sample is the in-flight call, deterministically.
	deadline := time.Now().Add(3 * time.Second)
	for {
		p.mu.Lock()
		inflight := p.inflight
		p.mu.Unlock()
		if inflight != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner sample never became in-flight")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := p.Sample(ctx)
	if r.State != GpuStateFailed || r.Error != "request_cancelled" {
		t.Fatalf("cancelled waiter state/error = %q/%q, want measurement_failed/request_cancelled", r.State, r.Error)
	}

	// The shared sample is undisturbed and still completes ok.
	owner := <-ownerDone
	if owner.State != GpuStateOK {
		t.Fatalf("owner state = %q, want ok (waiter cancellation disturbed the shared sample)", owner.State)
	}
}

func TestParseGpuCSVStrict(t *testing.T) {
	cases := []struct {
		name    string
		csv     string
		wantErr error
		wantN   int
	}{
		{"valid", "0, GPU-x, Name, 555.99, 1, 1, 0\n", nil, 1},
		{"blank lines skipped", "\n\n0, GPU-x, Name, 555.99, 1, 1, 0\n\n", nil, 1},
		{"negative index", "-1, GPU-x, Name, 555.99, 1, 1, 0\n", errInvalidNumeric, 0},
		{"non-numeric index", "abc, GPU-x, Name, 555.99, 1, 1, 0\n", errInvalidNumeric, 0},
		{"empty uuid", "0, , Name, 555.99, 1, 1, 0\n", errMissingIdentity, 0},
		{"uuid N/A", "0, N/A, Name, 555.99, 1, 1, 0\n", errMissingIdentity, 0},
		{"empty name", "0, GPU-x, , 555.99, 1, 1, 0\n", errMalformedCSV, 0},
		{"empty driver", "0, GPU-x, Name, , 1, 1, 0\n", errMalformedCSV, 0},
		{"total N/A", "0, GPU-x, Name, 555.99, N/A, 1, 0\n", errInvalidNumeric, 0},
		{"used N/A", "0, GPU-x, Name, 555.99, 1, N/A, 0\n", errInvalidNumeric, 0},
		{"free N/A", "0, GPU-x, Name, 555.99, 1, 1, N/A\n", errInvalidNumeric, 0},
		{"negative memory", "0, GPU-x, Name, 555.99, 1, -1, 0\n", errInvalidNumeric, 0},
		{"too few columns", "0, GPU-x, Name, 555.99, 1, 1\n", errMalformedCSV, 0},
		{"too many columns", "0, GPU-x, Name, 555.99, 1, 1, 0, extra\n", errMalformedCSV, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devices, err := parseGpuCSV(tc.csv)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(devices) != tc.wantN {
				t.Fatalf("len = %d, want %d", len(devices), tc.wantN)
			}
		})
	}
}

func TestParseMiBConversion(t *testing.T) {
	got, err := parseMiB("1")
	if err != nil || got != 1048576 {
		t.Fatalf("parseMiB(1) = %d, %v; want 1048576, nil", got, err)
	}
	got, err = parseMiB("24576")
	if err != nil || got != 24576*1024*1024 {
		t.Fatalf("parseMiB(24576) = %d, %v", got, err)
	}
	if _, err := parseMiB("N/A"); err != errInvalidNumeric {
		t.Fatalf("parseMiB(N/A) err = %v, want errInvalidNumeric", err)
	}
}
