package resource

import (
	"context"
	"testing"
)

// C-12: universal-state arithmetic with the third provider. A failed process
// sample next to healthy GPU/system is partial (the D-7 rule needs no change
// to become reachable); process unavailability never downgrades anything;
// Processes merge and the arrays stay non-null.

func gpuOK() *fakeProvider {
	return &fakeProvider{id: "nvidia", vendor: VendorNVIDIA, report: okReport("nvidia", okDevice("GPU-0", 100, 40, 60))}
}

func sysOK() *fakeProvider {
	return &fakeProvider{id: "system", vendor: VendorSystem, report: ProviderReport{ProviderID: "system", Vendor: VendorSystem, State: StateOK}}
}

func processReport(state State, processes []ProcessReading) ProviderReport {
	if processes == nil {
		processes = []ProcessReading{}
	}
	return ProviderReport{ProviderID: "process", Vendor: VendorSystem, State: state, Processes: processes}
}

func TestAggregatorPartialWithFailedProcessProvider(t *testing.T) {
	agg := NewAggregator(
		gpuOK(),
		sysOK(),
		&fakeProvider{id: "process", vendor: VendorSystem, report: processReport(StateMeasurementFailed, nil)},
	)
	rep := agg.Report(context.Background())
	if rep.State != StatePartial {
		t.Fatalf("state = %q, want partial (ok + ok + failed, D-7 arithmetic with three providers)", rep.State)
	}
	if len(rep.Capabilities) != 3 {
		t.Fatalf("capabilities = %d providers, want 3", len(rep.Capabilities))
	}
	if rep.Processes == nil {
		t.Fatal("processes must stay a non-null array even on partial")
	}
}

func TestAggregatorUnavailableProcessNeverDowngrades(t *testing.T) {
	// CPU-only machine, macOS-like: gpu unavailable + process unavailable +
	// system ok => ok.
	agg := NewAggregator(
		&fakeProvider{id: "nvidia", vendor: VendorNVIDIA, report: unavailableReport("nvidia", "nvidia_smi_not_found")},
		sysOK(),
		&fakeProvider{id: "process", vendor: VendorSystem, report: processReport(StateUnavailable, nil)},
	)
	rep := agg.Report(context.Background())
	if rep.State != StateOK {
		t.Fatalf("state = %q, want ok (unavailable providers never count as failures)", rep.State)
	}
	if rep.Devices == nil || rep.Processes == nil {
		t.Fatalf("arrays must never be null: devices=%v processes=%v", rep.Devices, rep.Processes)
	}
}

func TestAggregatorMergesProcessesAndKeepsArraysNonNull(t *testing.T) {
	p1 := ProcessReading{InstanceID: "i1", ModelID: "m1", RuntimeID: "r1", PID: 1}
	p2 := ProcessReading{InstanceID: "i2", ModelID: "m2", RuntimeID: "r2", PID: 2}
	agg := NewAggregator(
		gpuOK(),
		&fakeProvider{id: "process-a", vendor: VendorSystem, report: processReport(StateOK, []ProcessReading{p1})},
		&fakeProvider{id: "process-b", vendor: VendorSystem, report: processReport(StateOK, []ProcessReading{p2})},
	)
	rep := agg.Report(context.Background())
	if len(rep.Processes) != 2 || rep.Processes[0].InstanceID != "i1" || rep.Processes[1].InstanceID != "i2" {
		t.Fatalf("merged processes = %+v", rep.Processes)
	}

	// No process provider at all => the published [] shape is preserved.
	agg2 := NewAggregator(gpuOK(), sysOK())
	rep2 := agg2.Report(context.Background())
	if rep2.Processes == nil || len(rep2.Processes) != 0 {
		t.Fatalf("processes without a process provider = %v, want []", rep2.Processes)
	}
	if rep2.Devices == nil {
		t.Fatal("devices must never be null")
	}
}
