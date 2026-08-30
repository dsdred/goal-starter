package application

import (
	"context"
	"strconv"

	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

// ModelService wraps Repository for model CRUD operations.
type ModelService struct {
	repo storage.Repository

	// pipelineRef is set by WithPipelineIntegrity (ADR 010 D1.5) and returns
	// the names of pipelines referencing a model; nil disables the check.
	pipelineRef func(modelID string) []string
}

func NewModelService(repo storage.Repository) *ModelService {
	return &ModelService{repo: repo}
}

// WithPipelineIntegrity enables the D1.5 integrity rule: deleting a model
// referenced by one or more pipelines is a 409 conflict (the user edits or
// deletes the pipeline first; there is no implicit cascade).
func (s *ModelService) WithPipelineIntegrity(ref func(modelID string) []string) *ModelService {
	s.pipelineRef = ref
	return s
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
	if s.pipelineRef != nil {
		if names := s.pipelineRef(id); len(names) > 0 {
			details := make([]string, 0, len(names))
			for _, n := range names {
				details = append(details, n)
			}
			return apierrors.NewAPIError(apierrors.CodeConflict,
				"model is referenced by "+strconv.Itoa(len(names))+" pipeline(s)", details...)
		}
	}
	return s.repo.DeleteModel(id)
}
