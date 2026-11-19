package platform

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GPU telemetry states (ADR 025 D-6). The endpoint always answers HTTP 200 and
// carries one of these in the body; gpu_unavailable is a normal healthy outcome
// (no NVIDIA hardware / driver), not a request or process failure.
const (
	GpuStateOK          = "ok"
	GpuStateUnavailable = "gpu_unavailable"
	GpuStateFailed      = "measurement_failed"
)

// The fixed nvidia-smi query (ADR 025 D-5). Args are passed as []string, never
// through a shell (D-1). nounits makes the memory columns plain MiB integers.
const (
	gpuQueryArg  = "--query-gpu=index,uuid,name,driver_version,memory.total,memory.used,memory.free"
	gpuFormatArg = "--format=csv,noheader,nounits"
)

// Execution bounds (ADR 025 D-3). gpuDefaultTimeout is the fixed per-sample
// deadline; gpuWaitDelay bounds the wait for I/O closure after the child is
// killed so a timed-out sample never leaks the calling goroutine.
const (
	gpuDefaultTimeout = 5 * time.Second
	gpuWaitDelay      = 2 * time.Second
)

const mib = 1024 * 1024

// GpuDevice is one enumerated GPU with whole-GPU VRAM totals in bytes (D-5, D-9).
// UUID is the stable identity; Index is the current enumeration position (D-8).
type GpuDevice struct {
	UUID       string `json:"uuid"`
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

// GpuReport is the complete outcome of one sample attempt (ADR 025 D-6). The
// three states fully encode every result, so the report is self-describing and
// the handler serializes it directly with HTTP 200 (D-4). GPUs is empty (never
// null) for the non-ok states; Error is a bounded token; SampledAt is present
// only with state ok (D-7).
type GpuReport struct {
	State     string      `json:"state"`
	GPUs      []GpuDevice `json:"gpus"`
	Error     string      `json:"error,omitempty"`
	SampledAt string      `json:"sampled_at,omitempty"`
}

// GpuProvider is the seam the HTTP handler delegates to (ADR 025 D-10). The
// subprocess stays inside internal/platform; the handler never manages exec.Cmd.
type GpuProvider interface {
	Sample(ctx context.Context) GpuReport
}

// NvidiaSmiProvider samples GPU state by invoking nvidia-smi directly (ADR 025
// D-1..D-9). It is caller-driven (no polling, no timer, no auto-retry), keeps at
// most one in-flight child (single-flight), and never caches a prior result.
type NvidiaSmiProvider struct {
	timeout time.Duration
	lookup  func() (string, error)

	mu       sync.Mutex
	inflight *gpuCall
}

// gpuCall is one shared in-flight sample. report is written before done is
// closed, so waiters observe it only after the happens-before edge of the close.
type gpuCall struct {
	done   chan struct{}
	report GpuReport
}

// NewNvidiaSmiProvider returns the production provider: platform binary
// discovery and the fixed 5 s deadline.
func NewNvidiaSmiProvider() *NvidiaSmiProvider {
	return &NvidiaSmiProvider{timeout: gpuDefaultTimeout, lookup: discoverNvidiaSmi}
}

// Sample returns one fresh GPU report. Concurrent callers share the single
// in-flight child and its deadline (D-3); a caller whose own context is cancelled
// stops waiting without disturbing the shared sample. There is no cache: every
// call either joins the current fresh sample or starts a new one (D-7).
func (p *NvidiaSmiProvider) Sample(ctx context.Context) GpuReport {
	p.mu.Lock()
	if c := p.inflight; c != nil {
		p.mu.Unlock()
		select {
		case <-c.done:
			return c.report
		case <-ctx.Done():
			return gpuFailed("request_cancelled")
		}
	}
	c := &gpuCall{done: make(chan struct{})}
	p.inflight = c
	p.mu.Unlock()

	c.report = p.measure()

	p.mu.Lock()
	p.inflight = nil
	p.mu.Unlock()
	close(c.done)
	return c.report
}

// measure runs one bounded nvidia-smi invocation and maps the outcome to a state.
// The child runs under its own fixed deadline (independent of any single caller)
// so the shared sample has exactly one bounded lifetime; CommandContext kills it
// on expiry and WaitDelay bounds I/O closure (D-3). Output owns the single Wait
// (D-1).
func (p *NvidiaSmiProvider) measure() GpuReport {
	path, err := p.lookup()
	if err != nil || path == "" {
		return gpuUnavailable("nvidia_smi_not_found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, gpuQueryArg, gpuFormatArg)
	cmd.WaitDelay = gpuWaitDelay
	out, err := cmd.Output()

	if ctx.Err() == context.DeadlineExceeded {
		return gpuFailed("measurement_timeout")
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Non-zero exit is classified by stderr, never by a numeric code (D-2).
			if isNoDriverStderr(string(exitErr.Stderr)) {
				return gpuUnavailable("nvidia_driver_unavailable")
			}
			return gpuFailed("nvidia_smi_exit_error")
		}
		return gpuFailed("nvidia_smi_exec_error")
	}

	devices, parseErr := parseGpuCSV(string(out))
	if parseErr != nil {
		return gpuFailed(parseErr.Error())
	}
	if len(devices) == 0 {
		return gpuUnavailable("no_devices")
	}
	return GpuReport{
		State:     GpuStateOK,
		GPUs:      devices,
		SampledAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// Bounded parse-failure tokens (D-6): never raw cell content or process text.
var (
	errMalformedCSV    = errors.New("malformed_csv")
	errInvalidNumeric  = errors.New("invalid_numeric_field")
	errMissingIdentity = errors.New("missing_gpu_identity")
)

// parseGpuCSV strictly parses the noheader CSV. Every device row must carry all
// seven columns; a required numeric field that is N/A or unparseable fails the
// whole report (measurement_failed) — partial/zero totals are never ok (D-5).
func parseGpuCSV(s string) ([]GpuDevice, error) {
	devices := []GpuDevice{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != 7 {
			return nil, errMalformedCSV
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}

		index, err := strconv.Atoi(fields[0])
		if err != nil || index < 0 {
			return nil, errInvalidNumeric
		}
		uuid := fields[1]
		if uuid == "" || uuid == "N/A" {
			return nil, errMissingIdentity
		}
		name := fields[2]
		driver := fields[3]
		if name == "" || driver == "" {
			return nil, errMalformedCSV
		}
		total, err := parseMiB(fields[4])
		if err != nil {
			return nil, errInvalidNumeric
		}
		used, err := parseMiB(fields[5])
		if err != nil {
			return nil, errInvalidNumeric
		}
		free, err := parseMiB(fields[6])
		if err != nil {
			return nil, errInvalidNumeric
		}

		devices = append(devices, GpuDevice{
			UUID:       uuid,
			Index:      index,
			Name:       name,
			Driver:     driver,
			TotalBytes: total,
			UsedBytes:  used,
			FreeBytes:  free,
		})
	}
	return devices, nil
}

// parseMiB converts a nounits MiB cell to bytes. N/A, empty, negative or
// non-numeric cells are rejected (D-5 defensive parsing).
func parseMiB(s string) (int64, error) {
	if s == "" || s == "N/A" {
		return 0, errInvalidNumeric
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidNumeric
	}
	return n * mib, nil
}

// isNoDriverStderr classifies a non-zero exit as gpu_unavailable when stderr
// reports the driver/device is unusable (D-2). The markers are bounded substrings;
// the exit code itself is never trusted as a stable value.
func isNoDriverStderr(s string) bool {
	lower := strings.ToLower(s)
	for _, marker := range []string{
		"couldn't communicate with the nvidia driver",
		"no devices were found",
		"nvidia driver is not loaded",
		"failed to initialize nvml",
		"driver/library version mismatch",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func gpuUnavailable(reason string) GpuReport {
	return GpuReport{State: GpuStateUnavailable, GPUs: []GpuDevice{}, Error: reason}
}

func gpuFailed(reason string) GpuReport {
	return GpuReport{State: GpuStateFailed, GPUs: []GpuDevice{}, Error: reason}
}

// discoverNvidiaSmi resolves the binary once per sample attempt (D-2) via the
// platform-specific lookup.
func discoverNvidiaSmi() (string, error) {
	return lookupNvidiaSmi()
}
