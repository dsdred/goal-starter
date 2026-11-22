package application

import (
	"context"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/resource"
)

// TrackedLiveInstances implements the read-only resource.InstanceAttributor
// seam (ADR 026 Amendment A-026-1 A-2) over the existing InstanceService
// listing path: the in-memory Supervisor registry merged with store orphans.
// It returns the plain-data snapshots for the A-1 emitted states only —
// starting / running / stopping / orphan; terminal (exited / failed / stale)
// and dismissed rows produce no entries. It adds no lifecycle semantics,
// controls nothing, and caches nothing: aliveness is decided later, per
// request, by the platform identity check (A-3), never inferred from state.
func (s *InstanceService) TrackedLiveInstances(ctx context.Context) []resource.TrackedProcessSnapshot {
	instances, err := s.ListInstances(ctx)
	if err != nil {
		// A failed snapshot yields no entries: the report stays honest via
		// the empty list, never stale or fabricated rows (D-6).
		return nil
	}
	out := make([]resource.TrackedProcessSnapshot, 0, len(instances))
	for _, inst := range instances {
		switch inst.State {
		case domain.InstanceStateStarting, domain.InstanceStateRunning,
			domain.InstanceStateStopping, domain.InstanceStateOrphan:
		default:
			continue
		}
		out = append(out, resource.TrackedProcessSnapshot{
			InstanceID:      string(inst.ID),
			ModelID:         inst.ModelID,
			RuntimeID:       inst.RuntimeID,
			PipelineID:      inst.PipelineID,
			PipelineEntryID: inst.PipelineEntryID,
			PID:             inst.PID,
			Executable:      inst.Executable,
			StartedAt:       inst.StartedAt,
			HasStartedAt:    !inst.StartedAt.IsZero(),
		})
	}
	return out
}
