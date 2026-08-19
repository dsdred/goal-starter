package application

import (
	"context"

	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

// ModelService wraps Repository for model CRUD operations.
type ModelService struct {
	repo storage.Repository
}

func NewModelService(repo storage.Repository) *ModelService {
	return &ModelService{repo: repo}
}

func (s *ModelService) ListModels(ctx context.Context) ([]*storage.ModelEntry, error) {
	return s.repo.ListModels()
}

func (s *ModelService) GetModel(ctx context.Context, id string) (*storage.ModelEntry, error) {
	return s.repo.GetModel(id)
}

func (s *ModelService) CreateModel(ctx context.Context, entry *storage.ModelEntry) error {
	if entry.Name == "" {
		return apierrors.ErrValidation
	}
	return s.repo.CreateModel(entry)
}

func (s *ModelService) UpdateModel(ctx context.Context, entry *storage.ModelEntry) error {
	return s.repo.UpdateModel(entry)
}

func (s *ModelService) DeleteModel(ctx context.Context, id string) error {
	return s.repo.DeleteModel(id)
}
