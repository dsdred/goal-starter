//go:build windows || linux || darwin

package platform

import (
	"context"
	"testing"
	"time"
)

// TestSystemProviderLiveOS samples the real OS counters on the running machine
// (Windows locally, Linux in CI). It asserts only universally true invariants;
// exact values are machine-dependent. macOS is covered by the injected-logic
// and fake-child tests — real macOS acceptance remains open.
func TestSystemProviderLiveOS(t *testing.T) {
	p := NewSystemProvider()
	r := p.Sample(context.Background())
	if r.State != SystemStateOK {
		t.Fatalf("live state = %q (error=%q), want ok on a supported OS", r.State, r.Error)
	}
	if !r.RAMTotal.OK || r.RAMTotal.Value <= 0 {
		t.Fatalf("live ram total = %+v, want a positive measured value", r.RAMTotal)
	}
	if !r.RAMAvailable.OK || r.RAMAvailable.Value <= 0 {
		t.Fatalf("live ram available = %+v, want a positive value", r.RAMAvailable)
	}
	if !r.CPUCores.OK || r.CPUCores.Value <= 0 {
		t.Fatalf("live cores = %+v, want a positive count", r.CPUCores)
	}
	if !r.CPUUtil.OK || r.CPUUtil.Value < 0 || r.CPUUtil.Value > 100 {
		t.Fatalf("live util = %+v, want a measured percent in [0,100]", r.CPUUtil)
	}
	if _, err := time.Parse(time.RFC3339Nano, r.SampledAt); err != nil {
		t.Fatalf("sampled_at %q is not RFC3339Nano: %v", r.SampledAt, err)
	}

	// A second request is a fresh sample, never a cached replay.
	r2 := p.Sample(context.Background())
	if r2.State != SystemStateOK {
		t.Fatalf("second live state = %q, want ok", r2.State)
	}
	if r2.SampledAt == r.SampledAt {
		t.Fatalf("sampled_at reused (%q) — cached", r2.SampledAt)
	}
}
