package platform

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// errSystemMalformed is the bounded parse-failure token: never raw file or
// process content (D-6).
var errSystemMalformed = errors.New("malformed_source")

// System telemetry states (ADR 026 D-7). Unlike the legacy GPU vocabulary there
// is no vendor prefix: unavailable means no measurement source is present (a
// healthy condition), measurement_failed means a present source broke.
const (
	SystemStateOK          = "ok"
	SystemStateUnavailable = "unavailable"
	SystemStateFailed      = "measurement_failed"
)

// Execution bounds (same shape as RES-02A ADR 025 D-3). systemDefaultTimeout is
// the fixed per-sample deadline covering the whole measurement (including the
// macOS top child's own sampling interval); systemWaitDelay bounds I/O closure
// after a child is killed. systemCPUInterval is the fixed in-request window
// between the two CPU counter reads on Windows/Linux — one bounded measurement
// per sample, not background polling (D-3/D-5).
const (
	systemDefaultTimeout = 5 * time.Second
	systemWaitDelay      = 2 * time.Second
	systemCPUInterval    = 250 * time.Millisecond
)

// SystemMetric is one raw system measurement outcome. Available is the D-3
// source-addressable level (the source exists and can be addressed this
// request); OK is the sampled level (a value was obtained). Estimated marks a
// value computed by a named formula rather than read from a counter; Source
// names the counter/formula and Reason is a bounded token (why unavailable, or
// the formula caveat) — never raw process text.
type SystemMetric struct {
	Available bool
	OK        bool
	Value     int64
	Estimated bool
	Source    string
	Reason    string
}

// SystemReport is the complete outcome of one system sample attempt. The core
// measurement is RAM total: the provider is ok only when it was obtained.
// RAMAvailable, CPUCores and CPUUtil are optional — their absence never
// downgrades the state (D-7). SampledAt is present only with state ok.
type SystemReport struct {
	State        string
	RAMTotal     SystemMetric
	RAMAvailable SystemMetric
	CPUCores     SystemMetric
	CPUUtil      SystemMetric
	Error        string
	SampledAt    string
}

// SystemProvider is the seam the resource adapter consumes (ADR 026 D-2/D-3).
// Measurement stays inside internal/platform; the HTTP handler never manages
// exec.Cmd.
type SystemProvider interface {
	Sample(ctx context.Context) SystemReport
}

// OSSystemProvider samples machine-wide RAM/CPU state. It is caller-driven (no
// polling, no timer, no auto-retry), keeps at most one in-flight sample
// (single-flight), and never caches a prior result — the same concurrency and
// freshness bounds as NvidiaSmiProvider (ADR 025 D-3/D-7, ADR 026 D-3).
type OSSystemProvider struct {
	timeout  time.Duration
	interval time.Duration
	measure  func(ctx context.Context, interval time.Duration) SystemReport

	mu       sync.Mutex
	inflight *systemCall
}

// systemCall is one shared in-flight sample. report is written before done is
// closed, so waiters observe it only after the happens-before edge of the close.
type systemCall struct {
	done   chan struct{}
	report SystemReport
}

// NewSystemProvider returns the production provider for the running OS: the
// OS-specific measurement binding and the fixed bounds.
func NewSystemProvider() *OSSystemProvider {
	return &OSSystemProvider{
		timeout:  systemDefaultTimeout,
		interval: systemCPUInterval,
		measure:  osSystemMeasure,
	}
}

// Sample returns one fresh system report. Concurrent callers share the single
// in-flight sample and its deadline; a caller whose own context is cancelled
// stops waiting without disturbing the shared sample. There is no cache: every
// call either joins the current fresh sample or starts a new one.
func (p *OSSystemProvider) Sample(ctx context.Context) SystemReport {
	p.mu.Lock()
	if c := p.inflight; c != nil {
		p.mu.Unlock()
		select {
		case <-c.done:
			return c.report
		case <-ctx.Done():
			return systemFailed("request_cancelled")
		}
	}
	c := &systemCall{done: make(chan struct{})}
	p.inflight = c
	p.mu.Unlock()

	c.report = p.measureSample()

	p.mu.Lock()
	p.inflight = nil
	p.mu.Unlock()
	close(c.done)
	return c.report
}

// measureSample runs one measurement under its own fixed deadline (independent
// of any single caller) so the shared sample has exactly one bounded lifetime.
func (p *OSSystemProvider) measureSample() SystemReport {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	report := p.measure(ctx, p.interval)
	if report.State == SystemStateOK {
		report.SampledAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return report
}

// runSystemChild runs one bounded read-only measurement utility directly with
// []string args and no shell (AGENTS.md). CommandContext owns the single Wait
// via Output; WaitDelay bounds I/O closure after the kill. It returns a bounded
// token on failure, never raw process text.
func runSystemChild(ctx context.Context, path string, args ...string) ([]byte, string) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = systemWaitDelay
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, "measurement_timeout"
	}
	if err != nil {
		return nil, "child_exec_error"
	}
	return out, ""
}

// sleepCtx waits for d or until ctx is cancelled, so the CPU measurement window
// never outlives the sample deadline.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func systemUnavailable(reason string) SystemReport {
	return SystemReport{State: SystemStateUnavailable, Error: reason}
}

func systemFailed(reason string) SystemReport {
	return SystemReport{State: SystemStateFailed, Error: reason}
}

// systemOK builds a successful report. Optional metrics carry their own
// Available/OK levels; their absence never changes the state (D-7).
func systemOK(total, available, cores, util SystemMetric) SystemReport {
	return SystemReport{
		State:        SystemStateOK,
		RAMTotal:     total,
		RAMAvailable: available,
		CPUCores:     cores,
		CPUUtil:      util,
	}
}

// systemReportFromCore derives the provider state from the core measurement
// (RAM total) only: source absent ⇒ unavailable; source present but broke ⇒
// measurement_failed; obtained ⇒ ok. Optional metrics never enter (D-7).
func systemReportFromCore(total, available, cores, util SystemMetric) SystemReport {
	switch {
	case total.OK:
		return systemOK(total, available, cores, util)
	case total.Available:
		r := systemFailed(total.Reason)
		r.RAMTotal, r.RAMAvailable, r.CPUCores, r.CPUUtil = total, available, cores, util
		return r
	default:
		r := systemUnavailable(total.Reason)
		r.RAMTotal, r.RAMAvailable, r.CPUCores, r.CPUUtil = total, available, cores, util
		return r
	}
}
