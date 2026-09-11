package domain

import "time"

// PipelineModel is one ordered entry of a Pipeline: a reference to an
// existing Model plus an optional all-or-nothing launch-args override and an
// optional autostart flag (ADR 010 D1/D2).
//
// ID identifies the entry itself (ADR 013 D1): a Model may appear in the
// same Pipeline multiple times; the entry is unique, not the ModelID. The
// ID is generated on create, immutable for the entry's lifetime, and
// backfilled deterministically for legacy entries (ADR 013 D6).
type PipelineModel struct {
	ID        string   `json:"id,omitempty"`
	ModelID   string   `json:"model_id"`
	Args      []string `json:"args,omitempty"`
	AutoStart bool     `json:"auto_start"`
}

// Pipeline is a named, ordered group of existing Models with a group
// lifecycle (ADR 010). Launch order is list order; the same model cannot
// appear twice.
type Pipeline struct {
	ID        string
	Name      string
	Active    bool
	Models    []PipelineModel
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PipelineEntry is the persisted form of Pipeline (schema v8, key "pipelines").
type PipelineEntry struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Active    bool            `json:"active"`
	Models    []PipelineModel `json:"models"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PipelineEntryToDomain converts PipelineEntry to Pipeline.
func PipelineEntryToDomain(e *PipelineEntry) *Pipeline {
	models := make([]PipelineModel, len(e.Models))
	copy(models, e.Models)
	return &Pipeline{
		ID:        e.ID,
		Name:      e.Name,
		Active:    e.Active,
		Models:    models,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// PipelineToEntry converts Pipeline to PipelineEntry.
func PipelineToEntry(p *Pipeline) *PipelineEntry {
	models := make([]PipelineModel, len(p.Models))
	copy(models, p.Models)
	return &PipelineEntry{
		ID:        p.ID,
		Name:      p.Name,
		Active:    p.Active,
		Models:    models,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}
