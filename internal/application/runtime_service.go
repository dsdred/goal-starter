package application

import (
	"context"

	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/errors"
)

// RuntimeService wraps Repository for runtime CRUD operations.
type RuntimeService struct {
	repo storage.Repository
}

// NewRuntimeService creates a new RuntimeService.
func NewRuntimeService(repo storage.Repository) *RuntimeService {
	return &RuntimeService{repo: repo}
}

// ListRuntimes returns all runtimes.
func (s *RuntimeService) ListRuntimes(ctx context.Context) ([]*storage.RuntimeEntry, error) {
	return s.repo.ListRuntimes()
}

// GetRuntime returns a runtime by ID.
func (s *RuntimeService) GetRuntime(ctx context.Context, id string) (*storage.RuntimeEntry, error) {
	return s.repo.GetRuntime(id)
}

// CreateRuntime creates a new runtime.
func (s *RuntimeService) CreateRuntime(ctx context.Context, entry *storage.RuntimeEntry) error {
	if entry.Name == "" {
		return errors.ErrValidation
	}
	return s.repo.CreateRuntime(entry)
}

// UpdateRuntime updates an existing runtime.
func (s *RuntimeService) UpdateRuntime(ctx context.Context, entry *storage.RuntimeEntry) error {
	existing, err := s.repo.GetRuntime(entry.ID)
	if err != nil {
		return err
	}
	entry.CreatedAt = existing.CreatedAt
	if entry.Environment == nil {
		entry.Environment = existing.Environment
	}
	return s.repo.UpdateRuntime(entry)
}

// DeleteRuntime deletes a runtime by ID. Returns a conflict error if any model
// or profile references this runtime.
func (s *RuntimeService) DeleteRuntime(ctx context.Context, id string) error {
	models, err := s.repo.ListModels()
	if err != nil {
		return err
	}
	profiles, err := s.repo.ListProfiles()
	if err != nil {
		return err
	}
	var dependents []string
	for _, m := range models {
		if m.RuntimeID == id {
			dependents = append(dependents, "model: "+m.Name)
		}
	}
	for _, p := range profiles {
		if p.RuntimeID == id {
			dependents = append(dependents, "profile: "+p.Name)
		}
	}
	if len(dependents) > 0 {
		return errors.NewAPIError(errors.CodeConflict,
			"runtime is in use", dependents...)
	}
	return s.repo.DeleteRuntime(id)
}
