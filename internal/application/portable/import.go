package portable

import (
	"fmt"
	"strings"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
)

// ErrConflict is returned when one or more collisions are detected.
type ErrConflict struct {
	Conflicts []Conflict
}

func (e *ErrConflict) Error() string {
	return fmt.Sprintf("import rejected: %d collision(s)", len(e.Conflicts))
}

// ErrPersistence is returned when the durable write fails.
type ErrPersistence struct {
	Err error
}

func (e *ErrPersistence) Error() string {
	return "import persistence failure: " + e.Err.Error()
}

// ImportOrchestrator coordinates import validation and execution.
type ImportOrchestrator struct {
	repo storage.Repository
}

func NewImportOrchestrator(repo storage.Repository) *ImportOrchestrator {
	return &ImportOrchestrator{repo: repo}
}

// ImportResult is the result of a successful import or dry-run.
type ImportResult struct {
	Runtimes  int `json:"runtimes"`
	Models    int `json:"models"`
	Pipelines int `json:"pipelines"`
}

// Import performs a full import of the bundle. If dryRun is true, validation
// and collision detection are performed but zero mutation occurs.
func (o *ImportOrchestrator) Import(bundle *Bundle, dryRun bool) (*ImportResult, error) {
	if err := validateBundle(bundle); err != nil {
		return nil, err
	}

	if dryRun {
		conflicts, err := o.detectConflicts(bundle)
		if err != nil {
			return nil, err
		}
		if len(conflicts) > 0 {
			return nil, &ErrConflict{Conflicts: conflicts}
		}
		return &ImportResult{
			Runtimes:  len(bundle.Runtimes),
			Models:    len(bundle.Models),
			Pipelines: len(bundle.Pipelines),
		}, nil
	}

	runtimes, models, pipelines := convertBundle(bundle)
	if err := o.repo.ImportGraph(runtimes, models, pipelines); err != nil {
		var conflictErr *storage.ErrImportConflict
		if errorsAs(err, &conflictErr) {
			conflicts := make([]Conflict, len(conflictErr.Conflicts))
			for i, c := range conflictErr.Conflicts {
				conflicts[i] = Conflict{Type: c.Type, ID: c.ID, Reason: c.Reason, Name: c.Name}
			}
			return nil, &ErrConflict{Conflicts: conflicts}
		}
		return nil, &ErrPersistence{Err: err}
	}

	return &ImportResult{
		Runtimes:  len(bundle.Runtimes),
		Models:    len(bundle.Models),
		Pipelines: len(bundle.Pipelines),
	}, nil
}

func errorsAs(err error, target **storage.ErrImportConflict) bool {
	for err != nil {
		if c, ok := err.(*storage.ErrImportConflict); ok {
			*target = c
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// detectConflicts checks the bundle against current repository state.
// For dry-run this is advisory (no lock held); for real import the
// authoritative check happens under the write lock inside ImportGraph.
func (o *ImportOrchestrator) detectConflicts(b *Bundle) ([]Conflict, error) {
	var conflicts []Conflict

	existingRuntimes, err := o.repo.ListRuntimes()
	if err != nil {
		return nil, err
	}
	existingModels, err := o.repo.ListModels()
	if err != nil {
		return nil, err
	}
	existingPipelines, err := o.repo.ListPipelines()
	if err != nil {
		return nil, err
	}

	rtIDSet := make(map[string]bool, len(existingRuntimes))
	rtNameSet := make(map[string]string, len(existingRuntimes))
	for _, rt := range existingRuntimes {
		rtIDSet[rt.ID] = true
		rtNameSet[strings.ToLower(rt.Name)] = rt.ID
	}
	mIDSet := make(map[string]bool, len(existingModels))
	for _, m := range existingModels {
		mIDSet[m.ID] = true
	}
	pIDSet := make(map[string]bool, len(existingPipelines))
	for _, p := range existingPipelines {
		pIDSet[p.ID] = true
	}

	for _, rt := range b.Runtimes {
		if rtIDSet[rt.ID] {
			conflicts = append(conflicts, Conflict{Type: "runtime", ID: rt.ID, Reason: "id_exists"})
		}
		if _, exists := rtNameSet[strings.ToLower(rt.Name)]; exists {
			conflicts = append(conflicts, Conflict{Type: "runtime", ID: rt.ID, Reason: "name_exists", Name: rt.Name})
		}
	}
	for _, m := range b.Models {
		if mIDSet[m.ID] {
			conflicts = append(conflicts, Conflict{Type: "model", ID: m.ID, Reason: "id_exists"})
		}
	}
	for _, p := range b.Pipelines {
		if pIDSet[p.ID] {
			conflicts = append(conflicts, Conflict{Type: "pipeline", ID: p.ID, Reason: "id_exists"})
		}
	}

	return conflicts, nil
}

func convertBundle(b *Bundle) ([]*domain.RuntimeEntry, []*domain.ModelEntry, []*domain.PipelineEntry) {
	now := time.Now().UTC()

	runtimes := make([]*domain.RuntimeEntry, 0, len(b.Runtimes))
	for _, rt := range b.Runtimes {
		runtimes = append(runtimes, &domain.RuntimeEntry{
			ID:               rt.ID,
			Name:             rt.Name,
			Executable:       rt.Executable,
			WorkingDirectory: rt.WorkingDirectory,
			Environment:      nil,
			CreatedAt:        now,
			UpdatedAt:        now,
		})
	}

	models := make([]*domain.ModelEntry, 0, len(b.Models))
	for _, m := range b.Models {
		models = append(models, &domain.ModelEntry{
			ID:             m.ID,
			Name:           m.Name,
			RuntimeID:      m.RuntimeID,
			Args:           m.Args,
			Environment:    nil,
			Active:         m.Active,
			AutostartDelay: m.AutostartDelay,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}

	pipelines := make([]*domain.PipelineEntry, 0, len(b.Pipelines))
	for _, p := range b.Pipelines {
		pe := &domain.PipelineEntry{
			ID:        p.ID,
			Name:      p.Name,
			Active:    p.Active,
			CreatedAt: now,
			UpdatedAt: now,
		}
		for _, e := range p.Models {
			pe.Models = append(pe.Models, domain.PipelineModel{
				ID:        e.ID,
				ModelID:   e.ModelID,
				Args:      e.Args,
				AutoStart: e.AutoStart,
			})
		}
		pipelines = append(pipelines, pe)
	}

	return runtimes, models, pipelines
}
