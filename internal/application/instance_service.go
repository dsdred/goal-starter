package application

import (
	"context"
	"fmt"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

// InstanceService wraps Supervisor + Repository for high-level instance operations.
type InstanceService struct {
	supervisor *process.Supervisor
	repo       storage.Repository
}

func NewInstanceService(supervisor *process.Supervisor, repo storage.Repository) *InstanceService {
	return &InstanceService{
		supervisor: supervisor,
		repo:       repo,
	}
}

// StartModel starts a model by resolving runtime and calling supervisor.Start.
func (s *InstanceService) StartModel(ctx context.Context, modelID string) (*domain.LaunchInstance, error) {
	me, err := s.repo.GetModel(modelID)
	if err != nil {
		return nil, fmt.Errorf("model not found: %w", err)
	}

	rte, err := s.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		return nil, fmt.Errorf("runtime not found: %w", err)
	}

	domainModel := domain.ModelEntryToDomain(me)
	domainRuntime := process.RuntimeToDomain(
		rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory,
		rte.Environment,
	)

	return s.supervisor.Start(ctx, domainModel, domainRuntime, nil, nil)
}

func (s *InstanceService) StopInstance(ctx context.Context, id domain.InstanceID) error {
	return s.supervisor.Stop(ctx, id)
}

func (s *InstanceService) RestartInstance(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	return s.supervisor.Restart(ctx, id)
}

func (s *InstanceService) ListInstances(ctx context.Context) ([]*domain.LaunchInstance, error) {
	instances, err := s.supervisor.List()
	if err != nil {
		return nil, err
	}

	// Merge orphan instances from the persistent store (not in the in-memory map).
	entries, err := s.repo.ListInstances()
	if err == nil {
		seen := make(map[string]bool, len(instances))
		for _, inst := range instances {
			seen[string(inst.ID)] = true
		}
		for _, e := range entries {
			if seen[e.ID] {
				continue
			}
			dom := domain.ToDomain(e)
			if dom.State == domain.InstanceStateOrphan {
				instances = append(instances, dom)
			}
		}
	}

	return instances, nil
}

func (s *InstanceService) GetInstanceStatus(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	inst, err := s.supervisor.Status(id)
	if err == nil {
		return inst, nil
	}
	if s.repo != nil {
		entry, err := s.repo.GetLaunchInstance(string(id))
		if err == nil {
			return domain.ToDomain(entry), nil
		}
	}
	return nil, fmt.Errorf("instance %s not found", string(id))
}

// DismissOrphan transitions an orphan instance to stale (reconciled-by-user).
func (s *InstanceService) DismissOrphan(ctx context.Context, id domain.InstanceID) error {
	return s.supervisor.DismissOrphan(ctx, id)
}

// CleanupInstances deletes terminal instances matching the filter.
// Active instances are never deleted. Returns the number of instances deleted.
func (s *InstanceService) CleanupInstances(ctx context.Context, mode string, ids []string) (int, error) {
	switch mode {
	case "all_terminal":
		return s.repo.DeleteTerminalInstances(mode, nil, time.Time{})
	case "older_than_7d":
		return s.repo.DeleteTerminalInstances(mode, nil, time.Now().AddDate(0, 0, -7))
	case "older_than_30d":
		return s.repo.DeleteTerminalInstances(mode, nil, time.Now().AddDate(0, 0, -30))
	case "selected":
		return s.repo.DeleteTerminalInstances(mode, ids, time.Time{})
	default:
		return 0, fmt.Errorf("invalid cleanup mode: %s", mode)
	}
}

// GetModelStatus returns instance summary for a specific model.
func (s *InstanceService) GetModelStatus(ctx context.Context, modelID string) (*ModelStatusSummary, error) {
	instances, err := s.repo.ListByModelID(modelID)
	if err != nil {
		return nil, fmt.Errorf("list instances for model: %w", err)
	}

	summary := &ModelStatusSummary{
		ModelID: modelID,
		Count:   len(instances),
	}

	for _, inst := range instances {
		if inst.State == "running" || inst.State == "starting" {
			summary.Running++
			inst.Environment = nil
			summary.ActiveInst = inst
		}
	}

	return summary, nil
}

// ListHistory returns terminal instances from the persistent repository.
// Unlike ListInstances (in-memory supervisor), this survives GoAl restart.
func (s *InstanceService) ListHistory(ctx context.Context) ([]*domain.LaunchInstance, error) {
	entries, err := s.repo.ListInstances()
	if err != nil {
		return nil, err
	}
	out := make([]*domain.LaunchInstance, 0, len(entries))
	for _, e := range entries {
		dom := domain.ToDomain(e)
		if dom.IsTerminal() {
			out = append(out, dom)
		}
	}
	return out, nil
}

// ModelStatusSummary holds instance summary for a model.
type ModelStatusSummary struct {
	ModelID    string                       `json:"model_id"`
	ActiveInst *storage.LaunchInstanceEntry `json:"active_instance,omitempty"`
	Count      int                          `json:"count"`
	Running    int                          `json:"running"`
}

// ModelResolveResult holds the resolved command spec for a model.
type ModelResolveResult struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
	EnvironmentKeys  []string `json:"environmentKeys"`
}
