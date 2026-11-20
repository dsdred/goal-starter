package resource

import (
	"context"

	"github.com/dsdred/goal/internal/platform"
)

// systemProviderID is the stable provider id (D-3).
const systemProviderID = "system"

// systemSupportedMetrics is the static supported set (D-3 supported level):
// the RES-03B system provider measures machine RAM total/available, logical
// core count and CPU utilization on Windows/Linux/macOS. RAM used is derived
// (total - available). It changes only if the provider implementation changes.
var systemSupportedMetrics = []ResourceType{
	ResourceSystemRAMTotal,
	ResourceSystemRAMAvailable,
	ResourceSystemRAMUsed,
	ResourceCPULogicalCores,
	ResourceCPUUtil,
}

// SystemAdapter is the system ResourceProvider (ADR 026 D-8/D-11, RES-03B): a
// pure-Go translation over the platform system provider. It calls the wrapped
// provider's Sample exactly once per request; the provider's bounded /
// single-flight / no-cache behavior is unchanged. System RAM is a distinct
// memory kind and is never summed with any GPU memory (D-4).
type SystemAdapter struct {
	provider platform.SystemProvider
}

// NewSystemAdapter wraps a system provider (production:
// platform.NewSystemProvider; tests: a fake or an injected-measure provider).
func NewSystemAdapter(p platform.SystemProvider) *SystemAdapter {
	return &SystemAdapter{provider: p}
}

func (a *SystemAdapter) ID() string { return systemProviderID }

func (a *SystemAdapter) Vendor() Vendor { return VendorSystem }

// Capabilities returns the static supported-metric declaration (D-3).
func (a *SystemAdapter) Capabilities() Capabilities {
	supported := make(map[ResourceType]bool, len(systemSupportedMetrics))
	for _, m := range systemSupportedMetrics {
		supported[m] = true
	}
	return Capabilities{Supported: supported}
}

// Sample takes one bounded sample from the wrapped provider and derives the
// three-level capabilities from that same sample (D-3). A transient
// measurement failure leaves supported and available unchanged (only sampled
// becomes false). System is populated only on a successful sample; an absent
// optional metric is an unavailable Reading and never downgrades the state.
func (a *SystemAdapter) Sample(ctx context.Context) ProviderReport {
	sr := a.provider.Sample(ctx)

	usedAvailable := sr.RAMTotal.Available && sr.RAMAvailable.Available
	usedSampled := sr.RAMTotal.OK && sr.RAMAvailable.OK
	metrics := map[ResourceType]CapabilityLevel{
		ResourceSystemRAMTotal:     {Supported: true, Available: sr.RAMTotal.Available, Sampled: sr.RAMTotal.OK},
		ResourceSystemRAMAvailable: {Supported: true, Available: sr.RAMAvailable.Available, Sampled: sr.RAMAvailable.OK},
		ResourceSystemRAMUsed:      {Supported: true, Available: usedAvailable, Sampled: usedSampled},
		ResourceCPULogicalCores:    {Supported: true, Available: sr.CPUCores.Available, Sampled: sr.CPUCores.OK},
		ResourceCPUUtil:            {Supported: true, Available: sr.CPUUtil.Available, Sampled: sr.CPUUtil.OK},
	}

	var system *SystemResources
	if sr.State == platform.SystemStateOK {
		system = &SystemResources{
			RAM: SystemRAM{
				Total:     systemReading(sr.RAMTotal, UnitBytes),
				Available: systemReading(sr.RAMAvailable, UnitBytes),
				Used:      systemRAMUsed(sr.RAMTotal, sr.RAMAvailable),
			},
			CPU: SystemCPU{
				LogicalCores: systemReading(sr.CPUCores, UnitCount),
				Utilization:  systemReading(sr.CPUUtil, UnitPercent),
			},
		}
	}

	return ProviderReport{
		ProviderID:   systemProviderID,
		Vendor:       VendorSystem,
		State:        systemState(sr.State),
		System:       system,
		Capabilities: ProviderCapabilities{Vendor: VendorSystem, Metrics: metrics},
		Error:        sr.Error,
		SampledAt:    sr.SampledAt,
	}
}

// systemReading maps one raw platform measurement to a universal Reading.
// An unobtained value is nil (JSON null) with a bounded reason — never a
// fabricated zero (D-6).
func systemReading(m platform.SystemMetric, unit Unit) Reading {
	if !m.OK {
		return UnavailableReading(unit, m.Source, m.Reason)
	}
	if m.Estimated {
		v := m.Value
		return Reading{Value: &v, Unit: unit, Provenance: ProvenanceEstimated, Source: m.Source, Reason: m.Reason}
	}
	return MeasuredReading(m.Value, unit, m.Source)
}

// systemRAMUsed derives used = total - available: a named estimate, never a
// measured counter (D-6). When either dependency is unavailable, used is
// unavailable too — never computed against a fabricated zero.
func systemRAMUsed(total, available platform.SystemMetric) Reading {
	const source = "system_ram_total - system_ram_available"
	if !total.OK || !available.OK {
		return UnavailableReading(UnitBytes, source, "ram_dependency_unavailable")
	}
	v := total.Value - available.Value
	return Reading{Value: &v, Unit: UnitBytes, Provenance: ProvenanceEstimated, Source: source, Reason: "estimated: total - available"}
}

// systemState maps the platform system vocabulary to the universal one.
func systemState(s string) State {
	switch s {
	case platform.SystemStateOK:
		return StateOK
	case platform.SystemStateUnavailable:
		return StateUnavailable
	default:
		return StateMeasurementFailed
	}
}
