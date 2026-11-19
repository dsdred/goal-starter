package resource

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeProvider is a deterministic ResourceProvider for aggregator tests (C-1):
// it returns a fixed ProviderReport and counts Sample calls.
type fakeProvider struct {
	id     string
	vendor Vendor
	report ProviderReport
	calls  int
}

func (f *fakeProvider) ID() string     { return f.id }
func (f *fakeProvider) Vendor() Vendor { return f.vendor }
func (f *fakeProvider) Capabilities() Capabilities {
	return Capabilities{Supported: map[ResourceType]bool{}}
}
func (f *fakeProvider) Sample(ctx context.Context) ProviderReport {
	f.calls++
	return f.report
}

func okDevice(id string, total, used, free int64) ResourceDevice {
	return ResourceDevice{
		DeviceID:    id,
		Index:       0,
		Vendor:      VendorNVIDIA,
		Name:        "Fake GPU",
		Driver:      "1.0",
		MemoryClass: MemoryDedicated,
		Resources: map[ResourceType]Reading{
			ResourceVRAMTotal: MeasuredReading(total, UnitBytes, "fake total"),
			ResourceVRAMUsed:  MeasuredReading(used, UnitBytes, "fake used"),
			ResourceVRAMFree:  MeasuredReading(free, UnitBytes, "fake free"),
		},
	}
}

func okReport(id string, devices ...ResourceDevice) ProviderReport {
	return ProviderReport{
		ProviderID:   id,
		Vendor:       VendorNVIDIA,
		State:        StateOK,
		Devices:      devices,
		Capabilities: ProviderCapabilities{Vendor: VendorNVIDIA, Metrics: map[ResourceType]CapabilityLevel{ResourceVRAMTotal: {Supported: true, Available: true, Sampled: true}}},
		SampledAt:    "2026-11-19T00:00:00Z",
	}
}

func unavailableReport(id, reason string) ProviderReport {
	return ProviderReport{
		ProviderID:   id,
		Vendor:       VendorNVIDIA,
		State:        StateUnavailable,
		Devices:      []ResourceDevice{},
		Capabilities: ProviderCapabilities{Vendor: VendorNVIDIA, Metrics: map[ResourceType]CapabilityLevel{ResourceVRAMTotal: {Supported: true, Available: false, Sampled: false}}},
		Error:        reason,
	}
}

func failedReport(id, reason string) ProviderReport {
	return ProviderReport{
		ProviderID:   id,
		Vendor:       VendorNVIDIA,
		State:        StateMeasurementFailed,
		Devices:      []ResourceDevice{},
		Capabilities: ProviderCapabilities{Vendor: VendorNVIDIA, Metrics: map[ResourceType]CapabilityLevel{ResourceVRAMTotal: {Supported: true, Available: true, Sampled: false}}},
		Error:        reason,
	}
}

func TestAggregatorStateRules(t *testing.T) {
	cases := []struct {
		name        string
		providers   []ResourceProvider
		want        State
		wantSampled bool
	}{
		{"zero providers", nil, StateUnavailable, false},
		{"single ok", []ResourceProvider{&fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 100, 40, 60))}}, StateOK, true},
		{"single unavailable", []ResourceProvider{&fakeProvider{id: "a", report: unavailableReport("a", "nvidia_smi_not_found")}}, StateUnavailable, false},
		{"single failed", []ResourceProvider{&fakeProvider{id: "a", report: failedReport("a", "measurement_timeout")}}, StateMeasurementFailed, false},
		{"ok + failed => partial", []ResourceProvider{
			&fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 100, 40, 60))},
			&fakeProvider{id: "b", report: failedReport("b", "measurement_timeout")},
		}, StatePartial, true},
		{"ok + unavailable => ok", []ResourceProvider{
			&fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 100, 40, 60))},
			&fakeProvider{id: "b", report: unavailableReport("b", "no_devices")},
		}, StateOK, true},
		{"failed + unavailable => measurement_failed", []ResourceProvider{
			&fakeProvider{id: "a", report: failedReport("a", "measurement_timeout")},
			&fakeProvider{id: "b", report: unavailableReport("b", "nvidia_smi_not_found")},
		}, StateMeasurementFailed, false},
		{"all unavailable => unavailable", []ResourceProvider{
			&fakeProvider{id: "a", report: unavailableReport("a", "nvidia_smi_not_found")},
			&fakeProvider{id: "b", report: unavailableReport("b", "no_devices")},
		}, StateUnavailable, false},
		{"all failed => measurement_failed", []ResourceProvider{
			&fakeProvider{id: "a", report: failedReport("a", "measurement_timeout")},
			&fakeProvider{id: "b", report: failedReport("b", "malformed_csv")},
		}, StateMeasurementFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agg := NewAggregator(tc.providers...)
			r := agg.Report(context.Background())
			if r.State != tc.want {
				t.Fatalf("state = %q, want %q", r.State, tc.want)
			}
			if (r.SampledAt != "") != tc.wantSampled {
				t.Fatalf("sampled_at present = %v, want %v (value=%q)", r.SampledAt != "", tc.wantSampled, r.SampledAt)
			}
			// Collections are never null (D-11).
			if r.Devices == nil || r.Processes == nil || r.Errors == nil || r.Capabilities == nil {
				t.Fatalf("nil collection: devices=%v processes=%v errors=%v capabilities=%v", r.Devices, r.Processes, r.Errors, r.Capabilities)
			}
			if r.System != nil {
				t.Fatalf("system must be nil in slice 1, got %+v", r.System)
			}
		})
	}
}

func TestAggregatorSamplesEachProviderOnce(t *testing.T) {
	a := &fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 1, 0, 1))}
	b := &fakeProvider{id: "b", report: unavailableReport("b", "no_devices")}
	NewAggregator(a, b).Report(context.Background())
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("calls a=%d b=%d, want 1 each", a.calls, b.calls)
	}
}

func TestAggregatorConcatenatesDevicesWithoutSumming(t *testing.T) {
	a := &fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 100, 40, 60))}
	b := &fakeProvider{id: "b", report: okReport("b", okDevice("GPU-b", 200, 80, 120))}
	r := NewAggregator(a, b).Report(context.Background())
	if len(r.Devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2 (concatenated, not merged)", len(r.Devices))
	}
	// Each device keeps its own totals; no cross-device sum is fabricated.
	byID := map[string]ResourceDevice{}
	for _, d := range r.Devices {
		byID[d.DeviceID] = d
	}
	if got := *byID["GPU-a"].Resources[ResourceVRAMTotal].Value; got != 100 {
		t.Fatalf("GPU-a total = %d, want 100", got)
	}
	if got := *byID["GPU-b"].Resources[ResourceVRAMTotal].Value; got != 200 {
		t.Fatalf("GPU-b total = %d, want 200", got)
	}
}

func TestAggregatorOptionalMetricAbsenceKeepsOK(t *testing.T) {
	dev := okDevice("GPU-a", 100, 40, 60)
	// An optional metric the provider cannot supply is an unavailable Reading,
	// not a provider failure (D-7): state stays ok.
	dev.Resources["gpu_util"] = UnavailableReading(UnitPercent, "fake gpu_util", "not_supported_by_source")
	a := &fakeProvider{id: "a", report: okReport("a", dev)}
	r := NewAggregator(a).Report(context.Background())
	if r.State != StateOK {
		t.Fatalf("state = %q, want ok (optional absence must not downgrade)", r.State)
	}
	if r.Devices[0].Resources["gpu_util"].Value != nil {
		t.Fatalf("absent optional metric must be null")
	}
}

func TestAggregatorErrorsPreserved(t *testing.T) {
	a := &fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 1, 0, 1))}
	b := &fakeProvider{id: "b", report: failedReport("b", "measurement_timeout")}
	c := &fakeProvider{id: "c", report: unavailableReport("c", "nvidia_smi_not_found")}
	r := NewAggregator(a, b, c).Report(context.Background())
	if r.State != StatePartial {
		t.Fatalf("state = %q, want partial", r.State)
	}
	if len(r.Errors) != 2 {
		t.Fatalf("len(errors) = %d, want 2 (failed + unavailable; ok contributes none)", len(r.Errors))
	}
	byProvider := map[string]ProviderError{}
	for _, e := range r.Errors {
		byProvider[e.Provider] = e
	}
	if byProvider["b"].State != StateMeasurementFailed || byProvider["b"].Reason != "measurement_timeout" {
		t.Fatalf("failed error entry = %+v", byProvider["b"])
	}
	if byProvider["c"].State != StateUnavailable || byProvider["c"].Reason != "nvidia_smi_not_found" {
		t.Fatalf("unavailable error entry = %+v", byProvider["c"])
	}
	if _, present := byProvider["a"]; present {
		t.Fatalf("ok provider must not contribute an error entry")
	}
}

func TestAggregatorCapabilitiesPerProvider(t *testing.T) {
	a := &fakeProvider{id: "a", report: okReport("a", okDevice("GPU-a", 1, 0, 1))}
	b := &fakeProvider{id: "b", report: failedReport("b", "measurement_timeout")}
	r := NewAggregator(a, b).Report(context.Background())
	if len(r.Capabilities) != 2 {
		t.Fatalf("len(capabilities) = %d, want 2", len(r.Capabilities))
	}
	if !r.Capabilities["a"].Metrics[ResourceVRAMTotal].Sampled {
		t.Fatalf("ok provider metric must be sampled")
	}
	if r.Capabilities["b"].Metrics[ResourceVRAMTotal].Sampled {
		t.Fatalf("failed provider metric must not be sampled")
	}
	if !r.Capabilities["b"].Metrics[ResourceVRAMTotal].Available {
		t.Fatalf("failed provider source stays available (transient failure)")
	}
}

func TestResourceReportJSONNullContract(t *testing.T) {
	// unavailable reading => null; measured zero => 0 (D-6). CPU-only report =>
	// devices [] never null, system null, processes [] (D-8/D-11).
	dev := okDevice("GPU-a", 100, 0, 100)
	dev.Resources[ResourceVRAMFree] = MeasuredReading(0, UnitBytes, "fake free") // measured zero
	dev.Resources["gpu_util"] = UnavailableReading(UnitPercent, "fake", "absent")
	a := &fakeProvider{id: "a", report: okReport("a", dev)}
	r := NewAggregator(a).Report(context.Background())
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["system"] != nil {
		t.Fatalf("system must serialize to null, got %v", body["system"])
	}
	if _, ok := body["devices"].([]any); !ok {
		t.Fatalf("devices must be an array, got %T", body["devices"])
	}
	if _, ok := body["processes"].([]any); !ok {
		t.Fatalf("processes must be an array, got %T", body["processes"])
	}
	resources := body["devices"].([]any)[0].(map[string]any)["resources"].(map[string]any)
	free := resources["vram_free"].(map[string]any)
	if free["value"] != float64(0) {
		t.Fatalf("measured zero must serialize to 0, got %v", free["value"])
	}
	util := resources["gpu_util"].(map[string]any)
	if util["value"] != nil {
		t.Fatalf("unavailable reading must serialize to null, got %v", util["value"])
	}
	if util["provenance"] != "unavailable" {
		t.Fatalf("provenance = %v, want unavailable", util["provenance"])
	}

	// CPU-only: empty devices still an array, never null.
	empty := NewAggregator(&fakeProvider{id: "a", report: unavailableReport("a", "nvidia_smi_not_found")}).Report(context.Background())
	edata, _ := json.Marshal(empty)
	var ebody map[string]any
	_ = json.Unmarshal(edata, &ebody)
	if arr, ok := ebody["devices"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("cpu-only devices must be [] not null, got %v", ebody["devices"])
	}
	if ebody["state"] != "unavailable" {
		t.Fatalf("cpu-only state = %v, want unavailable", ebody["state"])
	}
}
