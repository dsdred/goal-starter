package application

import (
	"context"
	"strings"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/errors"
)

// ProfileService wraps Repository for profile CRUD operations.
type ProfileService struct {
	repo storage.Repository
}

// NewProfileService creates a new ProfileService.
func NewProfileService(repo storage.Repository) *ProfileService {
	return &ProfileService{repo: repo}
}

// ListProfiles returns all profiles.
func (s *ProfileService) ListProfiles(ctx context.Context) ([]*storage.ProfileEntry, error) {
	return s.repo.ListProfiles()
}

// GetProfile returns a profile by ID.
func (s *ProfileService) GetProfile(ctx context.Context, id string) (*storage.ProfileEntry, error) {
	return s.repo.GetProfile(id)
}

// CreateProfile creates a new profile.
func (s *ProfileService) CreateProfile(ctx context.Context, entry *storage.ProfileEntry) error {
	if entry.Name == "" {
		return errors.ErrValidation
	}
	return s.repo.CreateProfile(entry)
}

// UpdateProfile updates an existing profile.
func (s *ProfileService) UpdateProfile(ctx context.Context, entry *storage.ProfileEntry) error {
	existing, err := s.repo.GetProfile(entry.ID)
	if err != nil {
		return err
	}
	// The Web UI edits launch fields only. Preserve state and optional values
	// that were omitted from the update instead of silently erasing them.
	entry.CreatedAt = existing.CreatedAt
	entry.Active = existing.Active
	if entry.Environment == nil {
		entry.Environment = existing.Environment
	}
	return s.repo.UpdateProfile(entry)
}

// DeleteProfile deletes a profile by ID.
func (s *ProfileService) DeleteProfile(ctx context.Context, id string) error {
	return s.repo.DeleteProfile(id)
}

// ActivateProfile marks a profile as active.
func (s *ProfileService) ActivateProfile(ctx context.Context, id string) error {
	p, err := s.repo.GetProfile(id)
	if err != nil {
		return err
	}
	p.Active = true
	return s.repo.UpdateProfile(p)
}

// DeactivateProfile marks a profile as inactive.
func (s *ProfileService) DeactivateProfile(ctx context.Context, id string) error {
	p, err := s.repo.GetProfile(id)
	if err != nil {
		return err
	}
	p.Active = false
	return s.repo.UpdateProfile(p)
}

// Resolve returns resolved command spec for a profile.
func (s *ProfileService) Resolve(ctx context.Context, profileID string) (*ProfileResolveResult, error) {
	// This is called from handler; supervisor reference is passed separately.
	return nil, nil
}

// ResolveWithSupervisor resolves a profile using supervisor resolver.
func (s *ProfileService) ResolveWithSupervisor(supervisor *process.Supervisor, profileID string) (*ProfileResolveResult, error) {
	p, err := s.repo.GetProfile(profileID)
	if err != nil {
		return nil, err
	}

	rte, err := s.repo.GetRuntime(p.RuntimeID)
	if err != nil {
		return nil, err
	}

	var mdl *domain.Model
	if p.ModelID != "" {
		mdlData, err := s.repo.GetModel(p.ModelID)
		if err == nil {
			mdl = &domain.Model{
				ID:        mdlData.ID,
				Name:      mdlData.Name,
				Path:      mdlData.Path,
				MMProj:    mdlData.MMProj,
				Format:    mdlData.Format,
				Arguments: mdlData.Arguments,
				RuntimeID: mdlData.RuntimeID,
			}
		}
	}

	domainProfile := &domain.Profile{
		ID:          p.ID,
		Name:        p.Name,
		RuntimeID:   p.RuntimeID,
		ModelID:     p.ModelID,
		Host:        p.Host,
		Port:        p.Port,
		Args:        p.Args,
		Environment: p.Environment,
		Active:      p.Active,
	}

	domainRuntime := process.RuntimeToDomain(
		rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory,
		rte.DefaultArgs, rte.Environment,
	)

	spec, err := supervisor.ResolvePreview(domainProfile, domainRuntime, mdl, nil, nil)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(spec.Environment))
	for _, kv := range spec.Environment {
		key, _, ok := strings.Cut(kv, "=")
		if ok {
			keys = append(keys, key)
		}
	}

	return &ProfileResolveResult{
		Executable:       spec.Executable,
		Args:             spec.Args,
		WorkingDirectory: spec.WorkingDirectory,
		EnvironmentKeys:  keys,
	}, nil
}
