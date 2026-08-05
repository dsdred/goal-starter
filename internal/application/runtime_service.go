package application

import (
	"context"
	"fmt"

	"github.com/dsdred/goal/internal/storage"
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
		return fmt.Errorf("runtime name is required")
	}
	return s.repo.CreateRuntime(entry)
}

// UpdateRuntime updates an existing runtime.
func (s *RuntimeService) UpdateRuntime(ctx context.Context, entry *storage.RuntimeEntry) error {
	return s.repo.UpdateRuntime(entry)
}

// DeleteRuntime deletes a runtime by ID.
func (s *RuntimeService) DeleteRuntime(ctx context.Context, id string) error {
	return s.repo.DeleteRuntime(id)
}
