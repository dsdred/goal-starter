package resource

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/platform"
)

// C-11: the process adapter maps the provider pass onto the A-1 wire shape —
// IDs always present, pipeline IDs omitempty, process_rss always present with
// a null-never-zero unavailable value, exactly one attributor snapshot and one
// provider Sample per adapter request.

type fakeAttributor struct {
	snapshots []TrackedProcessSnapshot
	calls     int32
}

func (f *fakeAttributor) TrackedLiveInstances(context.Context) []TrackedProcessSnapshot {
	atomic.AddInt32(&f.calls, 1)
	return f.snapshots
}

type fakeProcessMemProvider struct {
	report     platform.ProcessMemReport
	calls      int32
	gotTracked []platform.TrackedProcess
}

func (f *fakeProcessMemProvider) Sample(_ context.Context, tracked []platform.TrackedProcess) platform.ProcessMemReport {
	atomic.AddInt32(&f.calls, 1)
	f.gotTracked = tracked
	return f.report
}

func trackedEntry(s platform.TrackedProcess) platform.ProcessMemEntry {
	return platform.ProcessMemEntry{
		TrackedProcess: s,
		OK:             true,
		Value:          int64(s.PID) * 1024,
		Source:         "fake-counter",
	}
}

func TestProcessAdapterMeasuredShape(t *testing.T) {
	attr := &fakeAttributor{snapshots: []TrackedProcessSnapshot{
		{InstanceID: "i1", ModelID: "m1", RuntimeID: "r1", PipelineID: "p1", PipelineEntryID: "pe1", PID: 111, Executable: "/bin/a", StartedAt: time.Unix(1700000000, 0), HasStartedAt: true},
		{InstanceID: "i2", ModelID: "m2", RuntimeID: "r2", PID: 222, Executable: "/bin/b", StartedAt: time.Unix(1700000001, 0), HasStartedAt: true},
	}}
	prov := &fakeProcessMemProvider{report: platform.ProcessMemReport{
		State: platform.ProcessMemStateOK, SourceAvailable: true,
		// The provider echoes the tracked identity onto each entry.
		Entries: []platform.ProcessMemEntry{
			trackedEntry(platform.TrackedProcess{InstanceID: "i1", ModelID: "m1", RuntimeID: "r1", PipelineID: "p1", PipelineEntryID: "pe1", PID: 111}),
			trackedEntry(platform.TrackedProcess{InstanceID: "i2", ModelID: "m2", RuntimeID: "r2", PID: 222}),
		},
		SampledAt: "2026-11-22T00:00:00Z",
	}}
	adapter := NewProcessAdapter(prov, attr)

	if adapter.ID() != "process" || adapter.Vendor() != VendorSystem {
		t.Fatalf("identity = %q/%q", adapter.ID(), adapter.Vendor())
	}
	rep := adapter.Sample(context.Background())
	if rep.State != StateOK {
		t.Fatalf("state = %q", rep.State)
	}
	if atomic.LoadInt32(&attr.calls) != 1 || atomic.LoadInt32(&prov.calls) != 1 {
		t.Fatalf("calls attributor=%d provider=%d, want 1/1 per request", attr.calls, prov.calls)
	}
	// A-2: the snapshot triple crossed the seam intact.
	if len(prov.gotTracked) != 2 || prov.gotTracked[0].Executable != "/bin/a" ||
		!prov.gotTracked[0].StartedAt.Equal(time.Unix(1700000000, 0)) || !prov.gotTracked[0].HasStartedAt {
		t.Fatalf("tracked input = %+v", prov.gotTracked)
	}
	if len(rep.Processes) != 2 {
		t.Fatalf("processes = %+v", rep.Processes)
	}
	p0 := rep.Processes[0]
	rss, present := p0.Resources[ResourceProcessRSS]
	if !present {
		t.Fatal("resources.process_rss must always be present (A-1)")
	}
	if rss.Value == nil || *rss.Value != int64(111*1024) || rss.Provenance != "measured" || rss.Unit != UnitBytes || rss.Source != "fake-counter" {
		t.Fatalf("measured reading = %+v", rss)
	}

	// A-1 omitempty: the JSON for a non-pipeline instance must omit both
	// pipeline keys; a pipeline instance carries them.
	blob, err := json.Marshal(rep.Processes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(blob)
	if !strings.Contains(s, `"pipeline_id":"p1"`) || !strings.Contains(s, `"pipeline_entry_id":"pe1"`) {
		t.Fatalf("pipeline instance must carry join IDs: %s", s)
	}
	if strings.Count(s, `"pipeline_id"`) != 1 || strings.Count(s, `"pipeline_entry_id"`) != 1 {
		t.Fatalf("non-pipeline instance must omit pipeline keys: %s", s)
	}
	// Always-present wire facts.
	for _, want := range []string{`"instance_id":"i2"`, `"model_id":"m2"`, `"runtime_id":"r2"`, `"pid":222`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}

func TestProcessAdapterUnavailableIsNeverZero(t *testing.T) {
	attr := &fakeAttributor{}
	prov := &fakeProcessMemProvider{report: platform.ProcessMemReport{
		State: platform.ProcessMemStateOK, SourceAvailable: true,
		Entries: []platform.ProcessMemEntry{
			{TrackedProcess: platform.TrackedProcess{InstanceID: "i1", ModelID: "m1", RuntimeID: "r1", PID: 5},
				Reason: platform.ProcessMemReasonIdentityMismatch},
		},
		SampledAt: "2026-11-22T00:00:00Z",
	}}
	rep := NewProcessAdapter(prov, attr).Sample(context.Background())
	if rep.State != StateOK {
		t.Fatalf("state = %q, a completed pass with an unavailable entry stays ok", rep.State)
	}
	rss := rep.Processes[0].Resources[ResourceProcessRSS]
	if rss.Value != nil || rss.Provenance != "unavailable" || rss.Reason != platform.ProcessMemReasonIdentityMismatch {
		t.Fatalf("unavailable reading = %+v, want null value with bounded reason (never zero)", rss)
	}
	// sampled=false at the capability level (nothing was measured this pass).
	lvl := rep.Capabilities.Metrics[ResourceProcessRSS]
	if lvl.Sampled {
		t.Fatalf("capabilities.sampled = true with no measured entry: %+v", lvl)
	}
	if lvl.Supported != platform.ProcessMemSupported() || !lvl.Available {
		t.Fatalf("capability levels = %+v", lvl)
	}
}

func TestProcessAdapterEmptyTrackedIsHonestArray(t *testing.T) {
	attr := &fakeAttributor{}
	prov := &fakeProcessMemProvider{report: platform.ProcessMemReport{
		State: platform.ProcessMemStateOK, SourceAvailable: true, SampledAt: "2026-11-22T00:00:00Z",
	}}
	rep := NewProcessAdapter(prov, attr).Sample(context.Background())
	if rep.Processes == nil {
		t.Fatal("processes must be [] never null (A-1)")
	}
	if len(rep.Processes) != 0 || rep.State != StateOK {
		t.Fatalf("empty pass = %+v", rep)
	}
}

func TestProcessAdapterStateMapping(t *testing.T) {
	cases := []struct {
		platform string
		want     State
	}{
		{platform.ProcessMemStateOK, StateOK},
		{platform.ProcessMemStateUnavailable, StateUnavailable},
		{platform.ProcessMemStateFailed, StateMeasurementFailed},
	}
	for _, tc := range cases {
		prov := &fakeProcessMemProvider{report: platform.ProcessMemReport{State: tc.platform, Error: "x"}}
		rep := NewProcessAdapter(prov, &fakeAttributor{}).Sample(context.Background())
		if rep.State != tc.want {
			t.Fatalf("platform %q mapped to %q, want %q", tc.platform, rep.State, tc.want)
		}
	}
}
