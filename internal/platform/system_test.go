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
	"sync/atomic"
	"testing"
	"time"
)

// fakeSys is the package-scoped fake vm_stat/top binary, built once for the test
// runner's GOOS/GOARCH. Each test copies it into its own t.TempDir with a
// sibling spec.json, so runs are isolated and no process-global environment is
// mutated (the fakegpu pattern, ADR 025 C-2).
var fakeSys struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func fakeSysExeName() string {
	if runtime.GOOS == "windows" {
		return "fakesys.exe"
	}
	return "fakesys"
}

func buildFakeSys() {
	dir, err := os.MkdirTemp("", "goal-fake-sys-")
	if err != nil {
		fakeSys.err = err
		return
	}
	fakeSys.dir = dir
	fakeSys.path = filepath.Join(dir, fakeSysExeName())
	cmd := exec.Command("go", "build", "-o", fakeSys.path, "./testdata/fakesys")
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		fakeSys.err = fmt.Errorf("go build fakesys: %w\n%s", buildErr, out)
	}
}

type sysSpec struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	SleepMS  int    `json:"sleep_ms"`
	Hang     bool   `json:"hang"`
}

// newFakeSysChild returns a per-test copy of the fake utility bound to spec.
func newFakeSysChild(t *testing.T, s sysSpec) (string, string) {
	t.Helper()
	fakeSys.once.Do(buildFakeSys)
	if fakeSys.err != nil {
		t.Fatalf("build fakesys: %v", fakeSys.err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, fakeSysExeName())
	data, err := os.ReadFile(fakeSys.path)
	if err != nil {
		t.Fatalf("read fakesys binary: %v", err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatalf("write fakesys copy: %v", err)
	}
	spec, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), spec, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return exe, dir
}

func sysRunCount(t *testing.T, dir string) int {
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

func okSystemMeasure() SystemReport {
	return systemOK(
		SystemMetric{Available: true, OK: true, Value: 1024, Source: "fake"},
		SystemMetric{Available: true, OK: true, Value: 512, Source: "fake"},
		SystemMetric{Available: true, OK: true, Value: 4, Source: "fake"},
		SystemMetric{Available: true, OK: true, Value: 10, Source: "fake"},
	)
}

func TestSystemProviderSingleFlight(t *testing.T) {
	var calls int32
	p := NewSystemProviderWithMeasure(5*time.Second, time.Millisecond, func(ctx context.Context, interval time.Duration) SystemReport {
		atomic.AddInt32(&calls, 1)
		if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
			return systemFailed("measurement_timeout")
		}
		return okSystemMeasure()
	})
	const n = 8
	reports := make([]SystemReport, n)
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
		if r.State != SystemStateOK {
			t.Fatalf("caller %d state = %q, want ok", i, r.State)
		}
		if r.SampledAt != reports[0].SampledAt {
			t.Fatalf("caller %d sampled_at = %q, want shared %q (not single-flight)", i, r.SampledAt, reports[0].SampledAt)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("concurrent callers ran %d measurements, want 1 (single-flight)", got)
	}
}

func TestSystemProviderNoCache(t *testing.T) {
	var calls int32
	mode := "ok"
	p := NewSystemProviderWithMeasure(5*time.Second, time.Millisecond, func(ctx context.Context, interval time.Duration) SystemReport {
		atomic.AddInt32(&calls, 1)
		if mode == "fail" {
			return systemFailed("meminfo_read_error")
		}
		// Separate successive ok samples by more than the OS clock granularity
		// so the freshness assertion on sampled_at is deterministic.
		_ = sleepCtx(ctx, 10*time.Millisecond)
		return okSystemMeasure()
	})

	r1 := p.Sample(context.Background())
	if r1.State != SystemStateOK || r1.SampledAt == "" {
		t.Fatalf("first sample = %+v, want ok with sampled_at", r1)
	}
	if _, err := time.Parse(time.RFC3339Nano, r1.SampledAt); err != nil {
		t.Fatalf("sampled_at %q is not RFC3339Nano: %v", r1.SampledAt, err)
	}

	// A subsequent failed read must never return the prior ok (D-3 no cache).
	mode = "fail"
	r2 := p.Sample(context.Background())
	if r2.State != SystemStateFailed || r2.Error != "meminfo_read_error" {
		t.Fatalf("second sample = %+v, want fresh measurement_failed", r2)
	}
	if r2.SampledAt != "" {
		t.Fatalf("failed sample carries sampled_at %q", r2.SampledAt)
	}

	mode = "ok"
	r3 := p.Sample(context.Background())
	if r3.State != SystemStateOK {
		t.Fatalf("third sample state = %q, want ok", r3.State)
	}
	if r3.SampledAt == r1.SampledAt {
		t.Fatalf("sampled_at reused across samples (%q) — cached", r3.SampledAt)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("measurements = %d, want 3 (one per sequential request)", got)
	}
}

func TestSystemProviderWaiterCancellation(t *testing.T) {
	p := NewSystemProviderWithMeasure(5*time.Second, time.Millisecond, func(ctx context.Context, interval time.Duration) SystemReport {
		if err := sleepCtx(ctx, 600*time.Millisecond); err != nil {
			return systemFailed("measurement_timeout")
		}
		return okSystemMeasure()
	})
	ownerDone := make(chan SystemReport, 1)
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
	if r.State != SystemStateFailed || r.Error != "request_cancelled" {
		t.Fatalf("cancelled waiter state/error = %q/%q, want measurement_failed/request_cancelled", r.State, r.Error)
	}

	// The shared sample is undisturbed and still completes ok.
	owner := <-ownerDone
	if owner.State != SystemStateOK {
		t.Fatalf("owner state = %q, want ok (waiter cancellation disturbed the shared sample)", owner.State)
	}
}

func TestSystemProviderSampleDeadline(t *testing.T) {
	p := NewSystemProviderWithMeasure(300*time.Millisecond, time.Millisecond, func(ctx context.Context, interval time.Duration) SystemReport {
		// A measurement that respects its deadline (the real OS measures do).
		if err := sleepCtx(ctx, 10*time.Second); err != nil {
			return systemFailed("measurement_timeout")
		}
		return okSystemMeasure()
	})
	start := time.Now()
	r := p.Sample(context.Background())
	elapsed := time.Since(start)
	if r.State != SystemStateFailed || r.Error != "measurement_timeout" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/measurement_timeout", r.State, r.Error)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("sample not bounded by its deadline: took %v", elapsed)
	}
}

// --- runSystemChild: the real bounded exec path (no shell, []string args) ---

func TestRunSystemChildSuccess(t *testing.T) {
	exe, dir := newFakeSysChild(t, sysSpec{Stdout: "hello\n"})
	out, token := runSystemChild(context.Background(), exe)
	if token != "" || string(out) != "hello\n" {
		t.Fatalf("out/token = %q/%q, want hello/empty", out, token)
	}
	if n := sysRunCount(t, dir); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}

func TestRunSystemChildArgsPassed(t *testing.T) {
	// The fake ignores args; this proves the call form accepts and passes a
	// []string argument list without a shell (top -l 2 -n 0 shape).
	exe, _ := newFakeSysChild(t, sysSpec{Stdout: fakeTop})
	out, token := runSystemChild(context.Background(), exe, "-l", "2", "-n", "0")
	if token != "" || string(out) != fakeTop {
		t.Fatalf("out/token = %q/%q", out, token)
	}
}

func TestRunSystemChildExitError(t *testing.T) {
	exe, _ := newFakeSysChild(t, sysSpec{Stderr: "boom", ExitCode: 3})
	out, token := runSystemChild(context.Background(), exe)
	if token != "child_exec_error" || out != nil {
		t.Fatalf("out/token = %q/%q, want nil/child_exec_error (bounded token, no raw text)", out, token)
	}
}

func TestRunSystemChildMissingBinary(t *testing.T) {
	out, token := runSystemChild(context.Background(), filepath.Join(t.TempDir(), "absent"))
	if token != "child_exec_error" || out != nil {
		t.Fatalf("out/token = %q/%q, want nil/child_exec_error", out, token)
	}
}

func TestRunSystemChildTimeoutKillsChild(t *testing.T) {
	exe, dir := newFakeSysChild(t, sysSpec{Hang: true})
	// The deadline must exceed process-startup latency (notably on Windows
	// under load) so the child reaches its hang loop and is then killed.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	out, token := runSystemChild(ctx, exe)
	elapsed := time.Since(start)
	if token != "measurement_timeout" || out != nil {
		t.Fatalf("out/token = %q/%q, want nil/measurement_timeout", out, token)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("hanging child not bounded: took %v", elapsed)
	}
	if n := sysRunCount(t, dir); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}

// --- macOS measurement over real child processes (fake utilities) ---

// TestMeasureDarwinRealChildren exercises the production exec path
// (runSystemChild) with fake vm_stat/top binaries on any OS: the darwin
// binding's only OS-specific part is the sysctl syscall, faked here.
func TestMeasureDarwinRealChildren(t *testing.T) {
	vmStatExe, vmDir := newFakeSysChild(t, sysSpec{Stdout: fakeVMStat})
	topExe, topDir := newFakeSysChild(t, sysSpec{Stdout: fakeTop})
	sysctl := func(name string) (uint64, error) {
		switch name {
		case "hw.memsize":
			return 16 * 1024 * 1024 * 1024, nil
		case "hw.logicalcpu":
			return 8, nil
		}
		return 0, fmt.Errorf("unknown sysctl %q", name)
	}
	look := func(name string) (string, error) {
		switch name {
		case "vm_stat":
			return vmStatExe, nil
		case "top":
			return topExe, nil
		}
		return "", exec.ErrNotFound
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, runSystemChild)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (error=%q)", r.State, r.Error)
	}
	if !r.RAMAvailable.OK || r.RAMAvailable.Value != 1228800000 || !r.RAMAvailable.Estimated {
		t.Fatalf("available = %+v", r.RAMAvailable)
	}
	if !r.CPUUtil.OK || r.CPUUtil.Value != 20 {
		t.Fatalf("util = %+v, want 20", r.CPUUtil)
	}
	if n := sysRunCount(t, vmDir); n != 1 {
		t.Fatalf("vm_stat runs = %d, want 1", n)
	}
	if n := sysRunCount(t, topDir); n != 1 {
		t.Fatalf("top runs = %d, want 1", n)
	}
}

func TestMeasureDarwinChildTimeout(t *testing.T) {
	vmStatExe, _ := newFakeSysChild(t, sysSpec{Stdout: fakeVMStat})
	topExe, topDir := newFakeSysChild(t, sysSpec{Hang: true})
	sysctl := func(name string) (uint64, error) {
		if name == "hw.memsize" {
			return 16 * 1024 * 1024 * 1024, nil
		}
		return 8, nil
	}
	look := func(name string) (string, error) {
		if name == "vm_stat" {
			return vmStatExe, nil
		}
		return topExe, nil
	}
	// The deadline covers TWO sequential children (vm_stat then top); use the
	// production 5 s bound so load-sensitive process startup cannot consume the
	// whole window before the hanging top child reaches its hang loop.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	r := measureDarwinSystem(ctx, sysctl, look, runSystemChild)
	elapsed := time.Since(start)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (core RAM total succeeded; util is optional)", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "measurement_timeout" {
		t.Fatalf("util = %+v, want unavailable/measurement_timeout", r.CPUUtil)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("hanging top child not bounded: took %v", elapsed)
	}
	if n := sysRunCount(t, topDir); n != 1 {
		t.Fatalf("top runs = %d, want 1", n)
	}
}
