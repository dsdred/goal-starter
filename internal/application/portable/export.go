package portable

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
)

// ExportRoot selects what to export.
type ExportRoot struct {
	RuntimeID  string
	ModelID    string
	PipelineID string
}

// ErrNotFound is returned when the export root entity does not exist.
type ErrNotFound struct {
	Type string
	ID   string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("%s %q not found", e.Type, e.ID)
}

// ErrMultipleRoots is returned when multiple root selectors are provided.
type ErrMultipleRoots struct{}

func (e *ErrMultipleRoots) Error() string {
	return "at most one root selector may be provided"
}

// ExportBundle builds a deterministic portable bundle from the repository
// state for the given root.
func ExportBundle(repo storage.Repository, root ExportRoot) ([]byte, error) {
	roots := []string{}
	if root.RuntimeID != "" {
		roots = append(roots, "runtime_id")
	}
	if root.ModelID != "" {
		roots = append(roots, "model_id")
	}
	if root.PipelineID != "" {
		roots = append(roots, "pipeline_id")
	}
	if len(roots) > 1 {
		return nil, &ErrMultipleRoots{}
	}

	var runtimes []*domain.RuntimeEntry
	var models []*domain.ModelEntry
	var pipelines []*domain.PipelineEntry

	switch {
	case root.RuntimeID != "":
		rt, err := repo.GetRuntime(root.RuntimeID)
		if err != nil {
			return nil, &ErrNotFound{Type: "runtime", ID: root.RuntimeID}
		}
		runtimes = []*domain.RuntimeEntry{rt}

	case root.ModelID != "":
		m, err := repo.GetModel(root.ModelID)
		if err != nil {
			return nil, &ErrNotFound{Type: "model", ID: root.ModelID}
		}
		rt, err := repo.GetRuntime(m.RuntimeID)
		if err != nil {
			return nil, &ErrNotFound{Type: "runtime", ID: m.RuntimeID}
		}
		runtimes = []*domain.RuntimeEntry{rt}
		models = []*domain.ModelEntry{m}

	case root.PipelineID != "":
		p, err := repo.GetPipeline(root.PipelineID)
		if err != nil {
			return nil, &ErrNotFound{Type: "pipeline", ID: root.PipelineID}
		}
		pipelines = []*domain.PipelineEntry{p}

		modelIDs := make(map[string]bool)
		for _, entry := range p.Models {
			if !modelIDs[entry.ModelID] {
				modelIDs[entry.ModelID] = true
				m, err := repo.GetModel(entry.ModelID)
				if err != nil {
					return nil, &ErrNotFound{Type: "model", ID: entry.ModelID}
				}
				models = append(models, m)
			}
		}

		rtIDs := make(map[string]bool)
		for _, m := range models {
			if !rtIDs[m.RuntimeID] {
				rtIDs[m.RuntimeID] = true
				rt, err := repo.GetRuntime(m.RuntimeID)
				if err != nil {
					return nil, &ErrNotFound{Type: "runtime", ID: m.RuntimeID}
				}
				runtimes = append(runtimes, rt)
			}
		}

	default:
		// Export all.
		var err error
		runtimes, err = listAllRuntimes(repo)
		if err != nil {
			return nil, err
		}
		models, err = listAllModels(repo)
		if err != nil {
			return nil, err
		}
		pipelines, err = listAllPipelines(repo)
		if err != nil {
			return nil, err
		}
	}

	bundle := buildBundle(runtimes, models, pipelines)
	return MarshalBundle(bundle)
}

func listAllRuntimes(repo storage.Repository) ([]*domain.RuntimeEntry, error) {
	all, err := repo.ListRuntimes()
	if err != nil {
		return nil, err
	}
	out := make([]*domain.RuntimeEntry, len(all))
	for i, r := range all {
		cp := *r
		out[i] = &cp
	}
	return out, nil
}

func listAllModels(repo storage.Repository) ([]*domain.ModelEntry, error) {
	all, err := repo.ListModels()
	if err != nil {
		return nil, err
	}
	out := make([]*domain.ModelEntry, len(all))
	for i, m := range all {
		cp := *m
		out[i] = &cp
	}
	return out, nil
}

func listAllPipelines(repo storage.Repository) ([]*domain.PipelineEntry, error) {
	all, err := repo.ListPipelines()
	if err != nil {
		return nil, err
	}
	out := make([]*domain.PipelineEntry, len(all))
	for i, p := range all {
		cp := *p
		out[i] = &cp
	}
	return out, nil
}

func buildBundle(runtimes []*domain.RuntimeEntry, models []*domain.ModelEntry, pipelines []*domain.PipelineEntry) *Bundle {
	b := &Bundle{
		Format:  FormatIdentity,
		Version: FormatVersion,
	}

	for _, rt := range runtimes {
		b.Runtimes = append(b.Runtimes, PortableRuntime{
			ID:               rt.ID,
			Name:             rt.Name,
			Executable:       rt.Executable,
			WorkingDirectory: rt.WorkingDirectory,
			EnvironmentKeys:  sortedKeys(rt.Environment),
		})
	}

	for _, m := range models {
		b.Models = append(b.Models, PortableModel{
			ID:              m.ID,
			Name:            m.Name,
			RuntimeID:       m.RuntimeID,
			Args:            m.Args,
			EnvironmentKeys: sortedKeys(m.Environment),
			Active:          m.Active,
			AutostartDelay:  m.AutostartDelay,
		})
	}

	for _, p := range pipelines {
		pipe := PortablePipeline{
			ID:     p.ID,
			Name:   p.Name,
			Active: p.Active,
		}
		for _, e := range p.Models {
			pipe.Models = append(pipe.Models, PortableEntry{
				ID:        e.ID,
				ModelID:   e.ModelID,
				Args:      e.Args,
				AutoStart: e.AutoStart,
			})
		}
		b.Pipelines = append(b.Pipelines, pipe)
	}

	return b
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MarshalBundle serializes a Bundle to deterministic JSON bytes.
func MarshalBundle(b *Bundle) ([]byte, error) {
	return json.MarshalIndent(b, "", "  ")
}
