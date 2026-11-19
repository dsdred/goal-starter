package resource

import (
	"context"

	"github.com/dsdred/goal/internal/platform"
)

// nvidiaProviderID is the stable provider id (D-3).
const nvidiaProviderID = "nvidia-smi"

// nvidiaSupportedMetrics is the static supported set (D-3 supported level): the
// RES-02A query returns whole-GPU memory.total/used/free only — no gpu_util and
// no per-process attribution (ADR 025 D-9). It changes only if the provider
// implementation changes.
var nvidiaSupportedMetrics = []ResourceType{ResourceVRAMTotal, ResourceVRAMUsed, ResourceVRAMFree}

// NvidiaAdapter is the first ResourceProvider (ADR 026 D-12/D-13): a pure-Go
// translation over the existing RES-02A platform.GpuProvider. It does not
// rewrite or re-launch nvidia-smi; it calls the wrapped provider's Sample exactly
// once per request and maps the report to the universal model. The provider's
// bounded / single-flight / no-cache behavior is unchanged.
type NvidiaAdapter struct {
	provider platform.GpuProvider
}

// NewNvidiaAdapter wraps an existing GPU provider (production:
// platform.NewNvidiaSmiProvider; tests: a fake or a fake-binary-backed provider).
func NewNvidiaAdapter(p platform.GpuProvider) *NvidiaAdapter {
	return &NvidiaAdapter{provider: p}
}

func (a *NvidiaAdapter) ID() string { return nvidiaProviderID }

func (a *NvidiaAdapter) Vendor() Vendor { return VendorNVIDIA }

// Capabilities returns the static supported-metric declaration (D-3).
func (a *NvidiaAdapter) Capabilities() Capabilities {
	supported := make(map[ResourceType]bool, len(nvidiaSupportedMetrics))
	for _, m := range nvidiaSupportedMetrics {
		supported[m] = true
	}
	return Capabilities{Supported: supported}
}

// Sample takes one bounded sample from the wrapped provider and derives the
// three-level capabilities from that same sample (D-3): available from the
// source-discovery outcome, sampled from the measurement outcome. No extra child
// is spawned to determine capabilities. A transient measurement failure leaves
// supported and available unchanged (only sampled becomes false).
func (a *NvidiaAdapter) Sample(ctx context.Context) ProviderReport {
	gr := a.provider.Sample(ctx)

	available := gr.State != platform.GpuStateUnavailable
	sampled := gr.State == platform.GpuStateOK
	metrics := make(map[ResourceType]CapabilityLevel, len(nvidiaSupportedMetrics))
	for _, m := range nvidiaSupportedMetrics {
		metrics[m] = CapabilityLevel{Supported: true, Available: available, Sampled: sampled}
	}

	devices := []ResourceDevice{}
	if gr.State == platform.GpuStateOK {
		for _, d := range gr.GPUs {
			devices = append(devices, ResourceDevice{
				DeviceID:    d.UUID,
				Index:       d.Index,
				Vendor:      VendorNVIDIA,
				Name:        d.Name,
				Driver:      d.Driver,
				MemoryClass: MemoryDedicated,
				Resources: map[ResourceType]Reading{
					ResourceVRAMTotal: MeasuredReading(d.TotalBytes, UnitBytes, "nvidia-smi memory.total"),
					ResourceVRAMUsed:  MeasuredReading(d.UsedBytes, UnitBytes, "nvidia-smi memory.used"),
					ResourceVRAMFree:  MeasuredReading(d.FreeBytes, UnitBytes, "nvidia-smi memory.free"),
				},
			})
		}
	}

	return ProviderReport{
		ProviderID:   nvidiaProviderID,
		Vendor:       VendorNVIDIA,
		State:        nvidiaState(gr.State),
		Devices:      devices,
		Capabilities: ProviderCapabilities{Vendor: VendorNVIDIA, Metrics: metrics},
		Error:        gr.Error,
		SampledAt:    gr.SampledAt,
	}
}

// nvidiaState maps the RES-02A state vocabulary to the universal one. The legacy
// gpu_unavailable (no source) becomes unavailable; measurement_failed is preserved.
func nvidiaState(s string) State {
	switch s {
	case platform.GpuStateOK:
		return StateOK
	case platform.GpuStateUnavailable:
		return StateUnavailable
	default:
		return StateMeasurementFailed
	}
}
