package application

import (
	"context"
	"fmt"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

// InstanceService wraps Supervisor + Repository for high-level instance operations.
type InstanceService struct {
	supervisor *process.Supervisor
	repo       storage.Repository
}

// NewInstanceService creates a new InstanceService.
func NewInstanceService(supervisor *process.Supervisor, repo storage.Repository) *InstanceService {
	return &InstanceService{
		supervisor: supervisor,
		repo:       repo,
	}
}

// StartProfile starts a profile by resolving runtime/model and calling supervisor.Start.
func (s *InstanceService) StartProfile(ctx context.Context, profileID string) (*domain.LaunchInstance, error) {
	p, err := s.repo.GetProfile(profileID)
	if err != nil {
		return nil, fmt.Errorf("profile not found: %w", err)
	}

	rte, err := s.repo.GetRuntime(p.RuntimeID)
	if err != nil {
		return nil, fmt.Errorf("runtime not found: %w", err)
	}

	var mdl *domain.Model
	if p.ModelID != "" {
		mdlData, err := s.repo.GetModel(p.ModelID)
		if err == nil {
			mdl = &domain.Model{
				ID:     mdlData.ID,
				Name:   mdlData.Name,
				Path:   mdlData.Path,
				MMProj: mdlData.MMProj,
				Format: mdlData.Format,
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

	return s.supervisor.Start(ctx, domainProfile, domainRuntime, mdl, nil, nil)
}

// StopInstance stops a specific instance.
func (s *InstanceService) StopInstance(ctx context.Context, id domain.InstanceID) error {
	return s.supervisor.Stop(ctx, id)
}

// RestartInstance restarts a specific instance.
func (s *InstanceService) RestartInstance(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	return s.supervisor.Restart(ctx, id)
}

// ListInstances returns all instances.
func (s *InstanceService) ListInstances(ctx context.Context) ([]*domain.LaunchInstance, error) {
	return s.supervisor.List()
}

// GetInstanceStatus returns the status of a specific instance.
func (s *InstanceService) GetInstanceStatus(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	return s.supervisor.Status(id)
}

// GetProfileStatus returns instance summary for a specific profile.
func (s *InstanceService) GetProfileStatus(ctx context.Context, profileID string) (*ProfileStatusSummary, error) {
	instances, err := s.repo.ListByProfileID(profileID)
	if err != nil {
		return nil, fmt.Errorf("list instances for profile: %w", err)
	}

	summary := &ProfileStatusSummary{
		ProfileID: profileID,
		Count:     len(instances),
	}

	for _, inst := range instances {
		if inst.State == "running" || inst.State == "starting" {
			summary.Running++
			summary.ActiveInst = inst
		}
	}

	return summary, nil
}

// ProfileStatusSummary holds instance summary for a profile.
type ProfileStatusSummary struct {
	ProfileID  string                       `json:"profile_id"`
	ActiveInst *storage.LaunchInstanceEntry `json:"active_instance,omitempty"`
	Count      int                          `json:"count"`
	Running    int                          `json:"running"`
}

// ProfileResolveResult holds the resolved command spec for a profile.
type ProfileResolveResult struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
	EnvironmentKeys  []string `json:"environmentKeys"`
}
