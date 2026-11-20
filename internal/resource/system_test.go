package resource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dsdred/goal/internal/platform"
)

// fakeSysProvider is a deterministic platform.SystemProvider for adapter tests.
type fakeSysProvider struct {
	report platform.SystemReport
	calls  int
}

func (f *fakeSysProvider) Sample(ctx context.Context) platform.SystemReport {
	f.calls++
	return f.report
}

func sysOKReport() platform.SystemReport {
	return platform.SystemReport{
		State: platform.SystemStateOK,
		RAMTotal: platform.SystemMetric{
			Available: true, OK: true, Value: 16 * 1024 * 1024 * 1024, Source: "fake total",
		},
		RAMAvailable: platform.SystemMetric{
			Available: true, OK: true, Value: 8 * 1024 * 1024 * 1024, Source: "fake available",
		},
		CPUCores: platform.SystemMetric{
			Available: true, OK: true, Value: 8, Source: "fake cores",
		},
		CPUUtil: platform.SystemMetric{
			Available: true, OK: true, Value: 25, Source: "fake util",
		},
		SampledAt: "2026-11-19T00:00:00Z",
	}
}

func TestSystemAdapterIdentityAndCapabilities(t *testing.T) {
	a := NewSystemAdapter(&fakeSysProvider{report: sysOKReport()})
	if a.ID() != "system" {
		t.Fatalf("id = %q, want system", a.ID())
	}
	if a.Vendor() != VendorSystem {
		t.Fatalf("vendor = %q, want system", a.Vendor())
	}
	caps := a.Capabilities()
	for _, m := range []ResourceType{ResourceSystemRAMTotal, ResourceSystemRAMAvailable, ResourceSystemRAMUsed, ResourceCPULogicalCores, ResourceCPUUtil} {
		if !caps.Supported[m] {
			t.Fatalf("static supported set missing %q", m)
		}
	}
	if len(caps.Supported) != 5 {
		t.Fatalf("supported set size = %d, want 5", len(caps.Supported))
	}
}

func TestSystemAdapterOK(t *testing.T) {
	f := &fakeSysProvider{report: sysOKReport()}
	r := NewSystemAdapter(f).Sample(context.Background())
	if f.calls != 1 {
		t.Fatalf("provider sampled %d times, want exactly 1", f.calls)
	}
	if r.State != StateOK || r.Error != "" || r.SampledAt != "2026-11-19T00:00:00Z" {
		t.Fatalf("report = %+v, want ok with passthrough sampled_at", r)
	}
	if r.System == nil {
		t.Fatal("ok sample must populate System")
	}
	total := r.System.RAM.Total
	if total.Value == nil || *total.Value != 16*1024*1024*1024 || total.Unit != UnitBytes || total.Provenance != ProvenanceMeasured {
		t.Fatalf("ram.total = %+v", total)
	}
	avail := r.System.RAM.Available
	if avail.Value == nil || *avail.Value != 8*1024*1024*1024 || avail.Provenance != ProvenanceMeasured {
		t.Fatalf("ram.available = %+v", avail)
	}
	used := r.System.RAM.Used
	if used.Value == nil || *used.Value != 8*1024*1024*1024 || used.Provenance != ProvenanceEstimated {
		t.Fatalf("ram.used = %+v, want the derived estimate", used)
	}
	if used.Source != "system_ram_total - system_ram_available" {
		t.Fatalf("used source = %q, want the named formula", used.Source)
	}
	cores := r.System.CPU.LogicalCores
	if cores.Value == nil || *cores.Value != 8 || cores.Unit != UnitCount {
		t.Fatalf("cpu.logical_cores = %+v", cores)
	}
	util := r.System.CPU.Utilization
	if util.Value == nil || *util.Value != 25 || util.Unit != UnitPercent {
		t.Fatalf("cpu.utilization = %+v", util)
	}
	for key, m := range r.Capabilities.Metrics {
		if !m.Supported || !m.Available || !m.Sampled {
			t.Fatalf("metric %q levels = %+v, want all true on a full ok sample", key, m)
		}
	}
}

func TestSystemAdapterUnavailable(t *testing.T) {
	f := &fakeSysProvider{report: platform.SystemReport{
		State:    platform.SystemStateUnavailable,
		RAMTotal: platform.SystemMetric{Reason: "platform_not_supported"},
		Error:    "platform_not_supported",
	}}
	r := NewSystemAdapter(f).Sample(context.Background())
	if r.State != StateUnavailable || r.Error != "platform_not_supported" {
		t.Fatalf("report = %+v", r)
	}
	if r.System != nil {
		t.Fatalf("unavailable sample must leave System nil, got %+v", r.System)
	}
	if r.SampledAt != "" {
		t.Fatalf("unavailable sample carries sampled_at %q", r.SampledAt)
	}
	for key, m := range r.Capabilities.Metrics {
		if !m.Supported || m.Available || m.Sampled {
			t.Fatalf("metric %q levels = %+v, want supported-only", key, m)
		}
	}
}

func TestSystemAdapterFailedKeepsAvailable(t *testing.T) {
	f := &fakeSysProvider{report: platform.SystemReport{
		State:    platform.SystemStateFailed,
		RAMTotal: platform.SystemMetric{Available: true, Reason: "meminfo_read_error"},
		Error:    "meminfo_read_error",
	}}
	r := NewSystemAdapter(f).Sample(context.Background())
	if r.State != StateMeasurementFailed {
		t.Fatalf("state = %q, want measurement_failed", r.State)
	}
	if r.System != nil {
		t.Fatalf("failed sample must leave System nil, got %+v", r.System)
	}
	m := r.Capabilities.Metrics[ResourceSystemRAMTotal]
	if !m.Supported || !m.Available || m.Sampled {
		t.Fatalf("transient failure levels = %+v, want supported+available, not sampled", m)
	}
}

func TestSystemAdapterOptionalAbsenceKeepsOK(t *testing.T) {
	sr := sysOKReport()
	sr.CPUUtil = platform.SystemMetric{Available: true, OK: false, Source: "GetSystemTimes", Reason: "counter_delta_invalid"}
	r := NewSystemAdapter(&fakeSysProvider{report: sr}).Sample(context.Background())
	if r.State != StateOK || r.System == nil {
		t.Fatalf("optional absence downgraded the report: %+v", r)
	}
	util := r.System.CPU.Utilization
	if util.Value != nil || util.Provenance != ProvenanceUnavailable || util.Reason != "counter_delta_invalid" {
		t.Fatalf("absent utilization = %+v, want null/unavailable with bounded reason", util)
	}
	m := r.Capabilities.Metrics[ResourceCPUUtil]
	if !m.Available || m.Sampled {
		t.Fatalf("util levels = %+v, want available but not sampled", m)
	}
}

func TestSystemAdapterEstimatedAvailable(t *testing.T) {
	sr := sysOKReport()
	sr.RAMAvailable = platform.SystemMetric{
		Available: true, OK: true, Value: 4096, Estimated: true,
		Source: "vm_stat (free+inactive+speculative)*page_size", Reason: "estimated: (free+inactive+speculative)*page_size",
	}
	r := NewSystemAdapter(&fakeSysProvider{report: sr}).Sample(context.Background())
	avail := r.System.RAM.Available
	if avail.Provenance != ProvenanceEstimated || avail.Reason == "" || avail.Value == nil || *avail.Value != 4096 {
		t.Fatalf("estimated available = %+v", avail)
	}
	used := r.System.RAM.Used
	if used.Value == nil || *used.Value != 16*1024*1024*1024-4096 || used.Provenance != ProvenanceEstimated {
		t.Fatalf("used must derive from the estimated available: %+v", used)
	}
}

func TestSystemAdapterUsedDependencyUnavailable(t *testing.T) {
	sr := sysOKReport()
	sr.RAMAvailable = platform.SystemMetric{Available: true, OK: false, Reason: "memavailable_not_present"}
	r := NewSystemAdapter(&fakeSysProvider{report: sr}).Sample(context.Background())
	if r.State != StateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	used := r.System.RAM.Used
	if used.Value != nil || used.Provenance != ProvenanceUnavailable || used.Reason != "ram_dependency_unavailable" {
		t.Fatalf("used with missing dependency = %+v", used)
	}
	m := r.Capabilities.Metrics[ResourceSystemRAMUsed]
	if !m.Available || m.Sampled {
		t.Fatalf("used levels = %+v, want available (both sources addressable) but not sampled", m)
	}
}

func TestSystemAdapterJSONNullContract(t *testing.T) {
	sr := sysOKReport()
	sr.CPUUtil = platform.SystemMetric{Available: true, OK: false, Reason: "measurement_timeout"}
	// Marshal through the aggregator: ResourceReport is the wire contract.
	r := NewAggregator(NewSystemAdapter(&fakeSysProvider{report: sr})).Report(context.Background())
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	system := body["system"].(map[string]any)
	util := system["cpu"].(map[string]any)["utilization"].(map[string]any)
	if util["value"] != nil {
		t.Fatalf("unavailable reading must serialize to null, got %v", util["value"])
	}
	used := system["ram"].(map[string]any)["used"].(map[string]any)
	if used["provenance"] != "estimated" {
		t.Fatalf("used provenance = %v, want estimated", used["provenance"])
	}
}
