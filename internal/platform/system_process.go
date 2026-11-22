package platform

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"
)

// Per-process RSS attribution (ADR 026 Amendment A-026-1, RES-05A). The
// provider samples read-only OS counters for processes GoAl itself launched;
// it never controls a process and never runs outside a caller's request
// (A-2/A-4, D-1).

// ProcessMem provider states (A-6): ok = a completed enumeration + read pass
// (individual entries may still be unavailable); unavailable = no attribution
// source on this OS; measurement_failed = a present source broke. Same
// vocabulary shape as the system provider (D-7).
const (
	ProcessMemStateOK          = "ok"
	ProcessMemStateUnavailable = "unavailable"
	ProcessMemStateFailed      = "measurement_failed"
)

// A-5 bounded per-entry reason vocabulary. Every one is an unavailable
// outcome, never a measured zero (D-6).
const (
	ProcessMemReasonPIDNotAssigned       = "pid_not_assigned"
	ProcessMemReasonProcessExited        = "process_exited"
	ProcessMemReasonIdentityMismatch     = "identity_mismatch"
	ProcessMemReasonIdentityUnconfirmed  = "identity_unconfirmed"
	ProcessMemReasonCounterUnavailable   = "counter_unavailable"
	ProcessMemReasonPlatformNotSupported = "platform_not_supported"
)

// Internal classification sentinels: an OS binding maps its raw error to one
// of these so the pure measurement logic can apply the A-5 table on any OS.
var (
	errProcessAccessDenied = errors.New("process_access_denied")
	errProcessGone         = errors.New("process_gone")
)

// TrackedProcess is the read-only attribution input for one non-terminal
// instance (A-1/A-2): the durable launch identity triple PID + Executable +
// StartedAt plus the join IDs. The PID is a fact of the launch record, not a
// claim that it is still this instance's process (A-3).
type TrackedProcess struct {
	InstanceID      string
	ModelID         string
	RuntimeID       string
	PipelineID      string
	PipelineEntryID string
	PID             int
	Executable      string
	StartedAt       time.Time
	HasStartedAt    bool
}

// ProcessMemEntry is one per-instance outcome of a sample. OK=false carries a
// bounded A-5 reason and never a number; OK=true carries the measured RSS in
// bytes and the counter name in Source.
type ProcessMemEntry struct {
	TrackedProcess
	OK     bool
	Value  int64
	Source string
	Reason string
}

// ProcessMemReport is the outcome of one process-attribution sample.
// SourceAvailable is the D-3 available level (the OS source is addressable
// this request). Entries carries exactly one entry per tracked process when
// the pass completed.
type ProcessMemReport struct {
	State           string
	SourceAvailable bool
	Entries         []ProcessMemEntry
	Error           string
	SampledAt       string
}

// ProcessMemProvider is the seam the resource adapter consumes (A-2).
// Implementations are caller-driven: no polling, no timer, no cache — the
// identity check and the counter read run inside the current request.
type ProcessMemProvider interface {
	Sample(ctx context.Context, tracked []TrackedProcess) ProcessMemReport
}

// OSProcessMemProvider samples per-process RSS for tracked GoAl launches.
// It mirrors OSSystemProvider's bounds (A-4): caller-driven, at most one
// in-flight sample (single-flight), a fixed 5 s per-sample deadline, and no
// cached result.
type OSProcessMemProvider struct {
	timeout time.Duration
	measure func(ctx context.Context, tracked []TrackedProcess) ProcessMemReport

	mu       sync.Mutex
	inflight *processMemCall
}

// processMemCall is one shared in-flight sample. report is written before
// done is closed, so waiters observe it only after the happens-before edge.
type processMemCall struct {
	done   chan struct{}
	report ProcessMemReport
}

// NewProcessMemProvider returns the production provider for the running OS.
func NewProcessMemProvider() *OSProcessMemProvider {
	return &OSProcessMemProvider{
		timeout: systemDefaultTimeout,
		measure: osProcessMemMeasure,
	}
}

// Sample returns one fresh attribution report for the tracked set. Concurrent
// callers share the single in-flight sample and its deadline; a caller whose
// own context is cancelled stops waiting without disturbing the shared
// sample. There is no cache: every call either joins the current fresh sample
// or starts a new one.
func (p *OSProcessMemProvider) Sample(ctx context.Context, tracked []TrackedProcess) ProcessMemReport {
	p.mu.Lock()
	if c := p.inflight; c != nil {
		p.mu.Unlock()
		select {
		case <-c.done:
			return c.report
		case <-ctx.Done():
			return processMemFailed("request_cancelled")
		}
	}
	c := &processMemCall{done: make(chan struct{})}
	p.inflight = c
	p.mu.Unlock()

	c.report = p.measureSample(tracked)

	p.mu.Lock()
	p.inflight = nil
	p.mu.Unlock()
	close(c.done)
	return c.report
}

// measureSample runs one pass under its own fixed deadline (independent of
// any single caller) so the shared sample has exactly one bounded lifetime.
func (p *OSProcessMemProvider) measureSample(tracked []TrackedProcess) ProcessMemReport {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	report := p.measure(ctx, tracked)
	if report.State == ProcessMemStateOK {
		report.SampledAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return report
}

// ProcessMemSupported is the D-3 supported level: a static design fact per OS
// build (A-6). Windows and Linux attribute RSS; macOS and every other OS do
// not in this slice (A-4). It changes only when the provider implementation
// changes.
func ProcessMemSupported() bool {
	switch runtime.GOOS {
	case "windows", "linux":
		return true
	default:
		return false
	}
}

func processMemOK(entries []ProcessMemEntry) ProcessMemReport {
	if entries == nil {
		entries = []ProcessMemEntry{}
	}
	return ProcessMemReport{State: ProcessMemStateOK, SourceAvailable: true, Entries: entries}
}

func processMemUnavailable(reason string) ProcessMemReport {
	return ProcessMemReport{State: ProcessMemStateUnavailable, Error: reason}
}

func processMemFailed(reason string) ProcessMemReport {
	return ProcessMemReport{State: ProcessMemStateFailed, SourceAvailable: true, Error: reason}
}

// processMemUnavail builds one unavailable entry: a bounded A-5 reason and
// never a number (D-6).
func processMemUnavail(t TrackedProcess, reason string) ProcessMemEntry {
	return ProcessMemEntry{TrackedProcess: t, Reason: reason}
}

// processMemMeasured builds one measured entry from the OS counter value.
func processMemMeasured(t TrackedProcess, value int64, source string) ProcessMemEntry {
	return ProcessMemEntry{TrackedProcess: t, OK: true, Value: value, Source: source}
}
