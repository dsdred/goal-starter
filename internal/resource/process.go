package resource

import (
	"context"
	"time"

	"github.com/dsdred/goal/internal/platform"
)

// processProviderID is the stable provider id (D-3/A-6).
const processProviderID = "process"

// processSupportedMetrics is the static supported set (D-3 supported level,
// A-6): process_rss on Windows/Linux builds; the supported flag is the
// platform ProcessMemSupported() design fact, never a per-request observation.
func processSupportedMetrics() map[ResourceType]bool {
	return map[ResourceType]bool{ResourceProcessRSS: platform.ProcessMemSupported()}
}

// TrackedProcessSnapshot is the plain-data instance record of the
// InstanceAttributor seam (A-2): the durable launch identity triple plus the
// join IDs, already filtered to the A-1 emitted states. It carries no domain
// pointers and no secret fields.
type TrackedProcessSnapshot struct {
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

// InstanceAttributor is the read-only seam the process adapter consumes
// (A-2): a snapshot of the tracked live instances as plain data, implemented
// in internal/application over the existing InstanceService list path. It
// never changes lifecycle semantics and never controls a process (D-1).
type InstanceAttributor interface {
	TrackedLiveInstances(ctx context.Context) []TrackedProcessSnapshot
}

// ProcessAdapter is the process ResourceProvider (ADR 026 A-026-1 A-6,
// RES-05A): it takes one snapshot per request from the attributor, calls the
// wrapped platform provider exactly once over it, and maps each entry to an
// A-1 ProcessReading. The handler never manages exec.Cmd; the bounded check +
// read stays inside the provider. Per-entry outcomes are optional-metric
// semantics: an unavailable entry never downgrades a completed pass (D-7).
type ProcessAdapter struct {
	provider platform.ProcessMemProvider
	attrib   InstanceAttributor
}

// NewProcessAdapter wraps a process provider and the tracked-instance seam.
func NewProcessAdapter(p platform.ProcessMemProvider, a InstanceAttributor) *ProcessAdapter {
	return &ProcessAdapter{provider: p, attrib: a}
}

func (a *ProcessAdapter) ID() string { return processProviderID }

func (a *ProcessAdapter) Vendor() Vendor { return VendorSystem }

// Capabilities returns the static supported-metric declaration (D-3).
func (a *ProcessAdapter) Capabilities() Capabilities {
	return Capabilities{Supported: processSupportedMetrics()}
}

// Sample takes one snapshot and one bounded provider pass, deriving the
// three-level capabilities from that same pass (A-6). Transient per-entry
// failures never flap supported/available.
func (a *ProcessAdapter) Sample(ctx context.Context) ProviderReport {
	tracked := a.attrib.TrackedLiveInstances(ctx)
	pr := a.provider.Sample(ctx, toPlatformTracked(tracked))

	sampled := false
	processes := make([]ProcessReading, 0, len(pr.Entries))
	for _, e := range pr.Entries {
		if e.OK {
			sampled = true
		}
		processes = append(processes, processReading(e))
	}

	metrics := map[ResourceType]CapabilityLevel{
		ResourceProcessRSS: {
			Supported: platform.ProcessMemSupported(),
			Available: pr.SourceAvailable,
			Sampled:   sampled,
		},
	}

	return ProviderReport{
		ProviderID:   processProviderID,
		Vendor:       VendorSystem,
		State:        processMemState(pr.State),
		Processes:    processes,
		Capabilities: ProviderCapabilities{Vendor: VendorSystem, Metrics: metrics},
		Error:        pr.Error,
		SampledAt:    pr.SampledAt,
	}
}

func toPlatformTracked(snapshots []TrackedProcessSnapshot) []platform.TrackedProcess {
	tracked := make([]platform.TrackedProcess, 0, len(snapshots))
	for _, s := range snapshots {
		tracked = append(tracked, platform.TrackedProcess{
			InstanceID:      s.InstanceID,
			ModelID:         s.ModelID,
			RuntimeID:       s.RuntimeID,
			PipelineID:      s.PipelineID,
			PipelineEntryID: s.PipelineEntryID,
			PID:             s.PID,
			Executable:      s.Executable,
			StartedAt:       s.StartedAt,
			HasStartedAt:    s.HasStartedAt,
		})
	}
	return tracked
}

// processReading maps one provider entry to the A-1 wire reading. An
// unconfirmed or unmeasured entry is null/unavailable with its bounded A-5
// reason — never a fabricated zero (D-6).
func processReading(e platform.ProcessMemEntry) ProcessReading {
	var rss Reading
	if e.OK {
		rss = MeasuredReading(e.Value, UnitBytes, e.Source)
	} else {
		rss = UnavailableReading(UnitBytes, e.Source, e.Reason)
	}
	return ProcessReading{
		InstanceID:      e.InstanceID,
		ModelID:         e.ModelID,
		RuntimeID:       e.RuntimeID,
		PipelineID:      e.PipelineID,
		PipelineEntryID: e.PipelineEntryID,
		PID:             e.PID,
		Resources:       map[ResourceType]Reading{ResourceProcessRSS: rss},
	}
}

// processMemState maps the platform process-mem vocabulary to the universal
// one (A-6).
func processMemState(s string) State {
	switch s {
	case platform.ProcessMemStateOK:
		return StateOK
	case platform.ProcessMemStateUnavailable:
		return StateUnavailable
	default:
		return StateMeasurementFailed
	}
}
