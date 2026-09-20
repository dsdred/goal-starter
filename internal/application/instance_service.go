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

// StartModel starts a model via the authoritative AdmitAndStart boundary
// (ADR 017). Admission arbitration is owned by the Supervisor; this method
// performs only resolution and delegates.
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

	return s.supervisor.AdmitAndStart(ctx, domainModel, domainRuntime, domain.ManualOwner, nil, nil)
}

func (s *InstanceService) StopInstance(ctx context.Context, id domain.InstanceID) error {
	return s.supervisor.Stop(ctx, id)
}

// PreflightRestart validates a whole selected restart target set against the
// immediately-known restart preconditions (pending / starting / stale /
// unattributable ownership / a restart already in flight) BEFORE any target is
// mutated, so a caller cannot apply part of a set it should never have started.
//
// This is pre-flight atomicity only: it performs no arbitration and no
// reservation, and it is not a transaction — the authoritative decision for
// each target is still made by the Supervisor's ADR 017 arbitration boundary
// when that restart runs, and already-completed process restarts are never
// rolled back.
func (s *InstanceService) PreflightRestart(ctx context.Context, ids []domain.InstanceID) error {
	return s.supervisor.PreflightRestart(ids)
}

// RestartInstance restarts the instance with the CURRENT launch configuration
// resolved from the repository (ownership-aware):
//
//   - MODEL-owned and PIPELINE FROM-MODEL instances relaunch with the current
//     Model.Args;
//   - PIPELINE CUSTOM instances (entry.Args non-empty) relaunch with the
//     current PipelineEntry.Args — all-or-nothing, never merged with or
//     replaced by Model.Args;
//   - the runtime is re-resolved from the current Model.RuntimeID;
//   - the InstanceID, the persisted record, and the PipelineID/PipelineEntryID
//     attribution are preserved (same-ID in-place refresh, new PID).
//
// If the model, the runtime, or the owning pipeline/entry can no longer be
// resolved (including legacy pipeline instances without an entry
// attribution), the restart fails with a bounded error instead of silently
// relaunching the frozen launch snapshot.
func (s *InstanceService) RestartInstance(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	inst, err := s.supervisor.Status(id)
	if err != nil {
		return nil, err
	}

	me, err := s.repo.GetModel(inst.ModelID)
	if err != nil {
		return nil, fmt.Errorf("model not found: %s", inst.ModelID)
	}

	domainModel := domain.ModelEntryToDomain(me)
	if inst.PipelineID != "" {
		if inst.PipelineEntryID == "" {
			return nil, fmt.Errorf("pipeline entry ownership cannot be reconstructed for instance %s (legacy attribution)", id)
		}
		pe, err := s.repo.GetPipeline(inst.PipelineID)
		if err != nil {
			return nil, fmt.Errorf("pipeline not found: %s", inst.PipelineID)
		}
		entry, ok := pipelineEntryByID(pe, inst.PipelineEntryID)
		if !ok {
			return nil, fmt.Errorf("pipeline entry %s not found in pipeline %s", inst.PipelineEntryID, inst.PipelineID)
		}
		if len(entry.Args) > 0 {
			domainModel.Args = entry.Args
		}
	}

	rte, err := s.repo.GetRuntime(domainModel.RuntimeID)
	if err != nil {
		return nil, fmt.Errorf("runtime not found: %s", domainModel.RuntimeID)
	}

	domainRuntime := process.RuntimeToDomain(
		rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory,
		rte.Environment,
	)

	return s.supervisor.RestartWithLaunch(ctx, id, domainModel, domainRuntime, nil, nil)
}

// pipelineEntryByID finds a pipeline entry (ADR 013 D1 identity) by its ID.
func pipelineEntryByID(pe *storage.PipelineEntry, entryID string) (domain.PipelineModel, bool) {
	for _, e := range pe.Models {
		if e.ID == entryID {
			return e, true
		}
	}
	return domain.PipelineModel{}, false
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

// KillOrphan terminates an orphan process with strict identity
// re-verification per ADR 008 and reconciles the instance per the
// post-kill lifecycle contract.
func (s *InstanceService) KillOrphan(ctx context.Context, id domain.InstanceID) (process.KillResult, error) {
	return s.supervisor.KillOrphan(ctx, id)
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
		if domain.InstanceState(inst.State).IsRunningOrStarting() {
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
