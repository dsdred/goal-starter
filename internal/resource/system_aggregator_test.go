package resource

import (
	"context"
	"testing"

	"github.com/dsdred/goal/internal/platform"
)

// Aggregator behavior with the system provider present (ADR 026 D-7/D-8.3/D-11,
// RES-03B). The gpu side uses the existing fakeProvider fixtures.

func TestAggregatorSystemPopulated(t *testing.T) {
	gpu := &fakeProvider{id: "nvidia", report: okReport("nvidia", okDevice("GPU-a", 100, 40, 60))}
	sys := NewSystemAdapter(&fakeSysProvider{report: sysOKReport()})
	r := NewAggregator(gpu, sys).Report(context.Background())
	if r.State != StateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.System == nil {
		t.Fatal("system provider ok must populate report.System")
	}
	if r.System.RAM.Total.Value == nil || *r.System.RAM.Total.Value != 16*1024*1024*1024 {
		t.Fatalf("system ram total = %+v", r.System.RAM.Total)
	}
	if len(r.Devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1 (gpu devices unchanged)", len(r.Devices))
	}
	// D-4: system RAM is a distinct memory kind, never summed with VRAM.
	if got := *r.Devices[0].Resources[ResourceVRAMTotal].Value; got != 100 {
		t.Fatalf("device vram total = %d, want 100 (no cross-kind sum)", got)
	}
	if _, ok := r.Capabilities["system"]; !ok {
		t.Fatalf("capabilities missing system provider: %v", r.Capabilities)
	}
}

func TestAggregatorCPUOnlyHealthy(t *testing.T) {
	gpu := &fakeProvider{id: "nvidia", report: unavailableReport("nvidia", "nvidia_smi_not_found")}
	sys := NewSystemAdapter(&fakeSysProvider{report: sysOKReport()})
	r := NewAggregator(gpu, sys).Report(context.Background())
	if r.State != StateOK {
		t.Fatalf("state = %q, want ok (D-8.3 CPU-only is healthy)", r.State)
	}
	if len(r.Devices) != 0 {
		t.Fatalf("len(devices) = %d, want 0", len(r.Devices))
	}
	if r.System == nil {
		t.Fatal("CPU-only report must carry system data")
	}
	if r.SampledAt == "" {
		t.Fatal("ok report must carry sampled_at")
	}
}

func TestAggregatorSystemFailedLeavesSystemNil(t *testing.T) {
	gpu := &fakeProvider{id: "nvidia", report: okReport("nvidia", okDevice("GPU-a", 100, 40, 60))}
	sys := NewSystemAdapter(&fakeSysProvider{report: platform.SystemReport{
		State:    platform.SystemStateFailed,
		RAMTotal: platform.SystemMetric{Available: true, Reason: "meminfo_read_error"},
		Error:    "meminfo_read_error",
	}})
	r := NewAggregator(gpu, sys).Report(context.Background())
	if r.State != StatePartial {
		t.Fatalf("state = %q, want partial (ok + failed)", r.State)
	}
	if r.System != nil {
		t.Fatalf("failed system sample must leave System nil, got %+v", r.System)
	}
	if len(r.Devices) != 1 {
		t.Fatalf("gpu devices must survive a system failure: %d", len(r.Devices))
	}
	found := false
	for _, e := range r.Errors {
		if e.Provider == "system" && e.State == StateMeasurementFailed && e.Reason == "meminfo_read_error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("system failure must appear in errors: %+v", r.Errors)
	}
}

func TestAggregatorWithoutSystemProviderUnchanged(t *testing.T) {
	// RES-03A compatibility: an aggregator with only the gpu provider keeps
	// System nil and its slice-1 state rules.
	gpu := &fakeProvider{id: "nvidia", report: okReport("nvidia", okDevice("GPU-a", 100, 40, 60))}
	r := NewAggregator(gpu).Report(context.Background())
	if r.State != StateOK || r.System != nil {
		t.Fatalf("gpu-only report = state %q system %+v, want ok/nil", r.State, r.System)
	}
}

func TestAggregatorBothUnavailable(t *testing.T) {
	gpu := &fakeProvider{id: "nvidia", report: unavailableReport("nvidia", "nvidia_smi_not_found")}
	sys := NewSystemAdapter(&fakeSysProvider{report: platform.SystemReport{
		State: platform.SystemStateUnavailable,
		Error: "platform_not_supported",
	}})
	r := NewAggregator(gpu, sys).Report(context.Background())
	if r.State != StateUnavailable {
		t.Fatalf("state = %q, want unavailable", r.State)
	}
	if r.System != nil || r.SampledAt != "" {
		t.Fatalf("unavailable report carries system/sampled_at: %+v", r)
	}
	if len(r.Errors) != 2 {
		t.Fatalf("len(errors) = %d, want 2 (both unavailable sources named)", len(r.Errors))
	}
}
