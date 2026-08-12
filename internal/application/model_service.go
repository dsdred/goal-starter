package application

import (
	"context"

	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/errors"
)

// ModelService wraps Repository for model CRUD operations.
type ModelService struct {
	repo storage.Repository
}

// NewModelService creates a new ModelService.
func NewModelService(repo storage.Repository) *ModelService {
	return &ModelService{repo: repo}
}

// ListModels returns all models.
func (s *ModelService) ListModels(ctx context.Context) ([]*storage.ModelEntry, error) {
	return s.repo.ListModels()
}

// GetModel returns a model by ID.
func (s *ModelService) GetModel(ctx context.Context, id string) (*storage.ModelEntry, error) {
	return s.repo.GetModel(id)
}

// CreateModel creates a new model.
func (s *ModelService) CreateModel(ctx context.Context, entry *storage.ModelEntry) error {
	if entry.Name == "" {
		return errors.ErrValidation
	}
	return s.repo.CreateModel(entry)
}

// UpdateModel updates an existing model.
func (s *ModelService) UpdateModel(ctx context.Context, entry *storage.ModelEntry) error {
	return s.repo.UpdateModel(entry)
}

// DeleteModel deletes a model by ID.
func (s *ModelService) DeleteModel(ctx context.Context, id string) error {
	return s.repo.DeleteModel(id)
}
