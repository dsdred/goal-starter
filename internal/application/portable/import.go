package portable

import (
	"fmt"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
)

// ErrConflict is returned when one or more blocking conflicts are detected.
// Result carries the plan that produced them so callers can report created and
// skipped counts alongside the blocking outcome.
type ErrConflict struct {
	Conflicts []Conflict
	Result    *ImportResult
}

func (e *ErrConflict) Error() string {
	return fmt.Sprintf("import rejected: %d blocking conflict(s)", len(e.Conflicts))
}

// ErrPersistence is returned when the durable write fails.
type ErrPersistence struct {
	Err error
}

func (e *ErrPersistence) Error() string {
	return "import persistence failure: " + e.Err.Error()
}

// ImportResult is the outcome of an import plan, for a dry run or a real import.
//
// For a dry run Created/Skipped describe what the import would do; for a real
// import they describe what it actually did. Skipped counts are existing
// entities left untouched, never imported ones.
type ImportResult struct {
	DryRun    bool
	CanImport bool
	Created   storage.ImportCounts
	Skipped   storage.ImportCounts
	Total     storage.ImportCounts
	Blocked   []Conflict
}

// ImportOrchestrator coordinates import validation and execution.
type ImportOrchestrator struct {
	repo storage.Repository
}

func NewImportOrchestrator(repo storage.Repository) *ImportOrchestrator {
	return &ImportOrchestrator{repo: repo}
}

// Import plans a bundle against current repository state and, unless dryRun is
// set, applies it with the SKIP EXISTING policy.
//
// A dry run is advisory: it plans against a snapshot taken without the write
// lock. A real import always rebuilds the plan under that lock inside
// Repository.ImportGraph, so a validation result that went stale in the
// meantime cannot produce an unsafe write.
func (o *ImportOrchestrator) Import(bundle *Bundle, dryRun bool) (*ImportResult, error) {
	if err := validateBundle(bundle); err != nil {
		return nil, err
	}

	runtimes, models, pipelines := convertBundle(bundle)

	if dryRun {
		state, err := o.snapshot()
		if err != nil {
			return nil, err
		}
		plan := storage.PlanGraphImport(state, runtimes, models, pipelines)
		return planToResult(plan, true), nil
	}

	plan, err := o.repo.ImportGraph(runtimes, models, pipelines)
	if err != nil {
		var conflictErr *storage.ErrImportConflict
		if errorsAs(err, &conflictErr) {
			conflicts := make([]Conflict, len(conflictErr.Conflicts))
			for i, c := range conflictErr.Conflicts {
				conflicts[i] = Conflict{
					Type:      c.Type,
					ID:        c.ID,
					Reason:    c.Reason,
					Name:      c.Name,
					RelatedID: c.RelatedID,
				}
			}
			// ImportGraph reports the plan it rejected along with the conflict, so
			// the caller can show the same summary a validation would.
			return nil, &ErrConflict{Conflicts: conflicts, Result: planToResult(plan, false)}
		}
		return nil, &ErrPersistence{Err: err}
	}

	return planToResult(plan, false), nil
}

// snapshot reads the current graph state for an advisory plan. Each list is
// individually consistent; the authoritative plan is rebuilt under the lock.
func (o *ImportOrchestrator) snapshot() (storage.ImportGraphState, error) {
	var s storage.ImportGraphState
	runtimes, err := o.repo.ListRuntimes()
	if err != nil {
		return s, err
	}
	models, err := o.repo.ListModels()
	if err != nil {
		return s, err
	}
	pipelines, err := o.repo.ListPipelines()
	if err != nil {
		return s, err
	}
	return storage.ImportGraphState{Runtimes: runtimes, Models: models, Pipelines: pipelines}, nil
}

func planToResult(plan *storage.ImportGraphPlan, dryRun bool) *ImportResult {
	res := &ImportResult{
		DryRun:    dryRun,
		Created:   plan.Created,
		Skipped:   plan.Skipped,
		Total:     plan.Total,
		CanImport: !plan.HasBlocked() && plan.WillCreate(),
	}
	for _, c := range plan.Blocked {
		res.Blocked = append(res.Blocked, Conflict{
			Type:      c.Type,
			ID:        c.ID,
			Name:      c.Name,
			Reason:    c.Reason,
			RelatedID: c.RelatedID,
		})
	}
	return res
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
