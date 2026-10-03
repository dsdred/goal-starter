package storage

import (
	"testing"
	"time"
)

// The projection helpers are the ADR 018 D11 equality contract: the listed
// fields decide UNCHANGED versus UPDATE, everything else is never compared, and
// the normalization table is exhaustive — no trimming, folding or sorting.

func TestRuntimeProjectionEqual(t *testing.T) {
	base := &RuntimeEntry{
		ID: "rt1", Name: "RT", Executable: "/bin/rt", WorkingDirectory: "/work",
		Environment: map[string]string{"SECRET": "keep"},
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	equivalent := *base
	equivalent.Environment = map[string]string{"OTHER": "value"}
	equivalent.CreatedAt = base.CreatedAt.Add(72 * time.Hour)
	equivalent.UpdatedAt = time.Now()
	if !runtimeProjectionEqual(base, &equivalent) {
		t.Fatal("environment values and timestamps are outside the projection")
	}

	if !runtimeProjectionEqual(&RuntimeEntry{WorkingDirectory: ""}, &RuntimeEntry{}) {
		t.Fatal("empty and absent working directory must be equivalent")
	}

	for _, tc := range []struct {
		name  string
		field func(*RuntimeEntry)
	}{
		{"name display case", func(e *RuntimeEntry) { e.Name = "rt" }},
		{"executable", func(e *RuntimeEntry) { e.Executable = "/bin/other" }},
		{"working directory", func(e *RuntimeEntry) { e.WorkingDirectory = "" }},
	} {
		other := *base
		tc.field(&other)
		if runtimeProjectionEqual(base, &other) {
			t.Fatalf("%s is restorable and must differ", tc.name)
		}
	}
}

func TestModelProjectionEqual(t *testing.T) {
	base := &ModelEntry{
		ID: "m1", Name: "Model", RuntimeID: "rt1", Args: []string{"--port", "8080"},
		Environment: map[string]string{"SECRET": "keep"}, Active: true, AutostartDelay: 5,
	}
	equivalent := *base
	equivalent.Environment = nil
	equivalent.CreatedAt = time.Now()
	equivalent.UpdatedAt = time.Now().Add(time.Minute)
	if !modelProjectionEqual(base, &equivalent) {
		t.Fatal("environment values and timestamps are outside the projection")
	}

	if !modelProjectionEqual(&ModelEntry{ID: "m"}, &ModelEntry{ID: "m", Args: []string{}}) {
		t.Fatal("nil and empty args must be equivalent")
	}
	if !modelProjectionEqual(&ModelEntry{ID: "m"}, &ModelEntry{ID: "m", AutostartDelay: 0}) {
		t.Fatal("omitted and zero autostart delay must be equivalent")
	}

	for _, tc := range []struct {
		name  string
		field func(*ModelEntry)
	}{
		{"name display case", func(e *ModelEntry) { e.Name = "model" }},
		{"runtime reference", func(e *ModelEntry) { e.RuntimeID = "rt2" }},
		{"args order", func(e *ModelEntry) { e.Args = []string{"8080", "--port"} }},
		{"args extended", func(e *ModelEntry) { e.Args = []string{"--port", "8080", ""} }},
		{"active", func(e *ModelEntry) { e.Active = false }},
		{"autostart delay", func(e *ModelEntry) { e.AutostartDelay = 6 }},
	} {
		other := *base
		tc.field(&other)
		if modelProjectionEqual(base, &other) {
			t.Fatalf("%s is restorable and must differ", tc.name)
		}
	}
}

func TestPipelineProjectionEqual(t *testing.T) {
	base := &PipelineEntry{
		ID: "p1", Name: "Pipe", Active: true,
		Models: []PipelineModel{
			{ID: "e1", ModelID: "m1", Args: []string{"--a"}, AutoStart: true},
			{ID: "e2", ModelID: "m2"},
		},
	}

	// Entry IDs are operational identity and excluded from equality (D10/D11):
	// identical content carried under different entry IDs is UNCHANGED.
	renumbered := *base
	renumbered.Models = []PipelineModel{
		{ID: "p1-legacy-9", ModelID: "m1", Args: []string{"--a"}, AutoStart: true},
		{ID: "", ModelID: "m2"},
	}
	if !pipelineProjectionEqual(base, &renumbered) {
		t.Fatal("entry ids must not affect equality")
	}

	// Timestamps are outside the projection.
	redated := *base
	redated.CreatedAt = time.Now()
	redated.UpdatedAt = time.Now()
	if !pipelineProjectionEqual(base, &redated) {
		t.Fatal("timestamps are outside the projection")
	}

	if !pipelineProjectionEqual(&PipelineEntry{ID: "p"}, &PipelineEntry{ID: "p", Models: []PipelineModel{}}) {
		t.Fatal("nil and empty entry lists must be equivalent")
	}

	for _, tc := range []struct {
		name  string
		field func(*PipelineEntry)
	}{
		{"name display case", func(p *PipelineEntry) { p.Name = "pipe" }},
		{"active", func(p *PipelineEntry) { p.Active = false }},
		{"entry count", func(p *PipelineEntry) { p.Models = p.Models[:1] }},
		{"entry model reference", func(p *PipelineEntry) { p.Models[0].ModelID = "m3" }},
		{"entry args", func(p *PipelineEntry) { p.Models[0].Args = []string{"--b"} }},
		{"entry autostart", func(p *PipelineEntry) { p.Models[0].AutoStart = false }},
	} {
		other := *base
		other.Models = append([]PipelineModel(nil), base.Models...)
		tc.field(&other)
		if pipelineProjectionEqual(base, &other) {
			t.Fatalf("%s is restorable and must differ", tc.name)
		}
	}

	// Launch order is list order, so the same entries in another order differ.
	reordered := *base
	reordered.Models = append([]PipelineModel(nil), base.Models[1], base.Models[0])
	if pipelineProjectionEqual(base, &reordered) {
		t.Fatal("entry order is part of the projection")
	}
}

func TestArgsEqual(t *testing.T) {
	if !argsEqual(nil, []string{}) || !argsEqual([]string{}, nil) {
		t.Fatal("nil and empty args are the same sequence")
	}
	if argsEqual([]string{"a"}, []string{"a", ""}) {
		t.Fatal("an extra empty argument is a difference")
	}
	if argsEqual([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatal("args are compared order-significantly")
	}
	left, right := []string{"--port"}, []string{"--port"}
	if !argsEqual(left, right) {
		t.Fatal("equal args must compare equal")
	}
	if left[0] != "--port" || right[0] != "--port" {
		t.Fatal("comparison must not mutate its operands")
	}
}
