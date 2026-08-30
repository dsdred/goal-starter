package application

import (
	"context"
	"strings"
	"sync"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

// Per-entry lifecycle outcome vocabulary (ADR 010 D3). Bounded strings.
const (
	OutcomeStarted        = "started"
	OutcomeAlreadyRunning = "already-running"
	OutcomeOrphanSkipped  = "orphan-skipped"
	OutcomeNoRuntime      = "no-runtime"
	OutcomeModelMissing   = "model-missing"
	OutcomeFailed         = "failed"
	OutcomeStopped        = "stopped"
)

// Bounded failure reasons for failed start/stop entries (ADR 010 D3/D6):
// class only, never raw error text.
const (
	ReasonResolveFailed = "resolve-failed"
	ReasonStartFailed   = "start-failed"
	ReasonStopFailed    = "stop-failed"
)

// PipelineEntryStart is one per-model result of a pipeline start (pipeline
// order in the response).
type PipelineEntryStart struct {
	ModelID    string `json:"model_id"`
	Status     string `json:"status"`
	InstanceID string `json:"instance_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// PipelineStartResult is the response contract of POST /pipelines/{id}/start.
type PipelineStartResult struct {
	PipelineID string               `json:"pipeline_id"`
	Results    []PipelineEntryStart `json:"results"`
}

// PipelineEntryStop is one per-model result of a pipeline stop (reverse
// pipeline order in the response).
type PipelineEntryStop struct {
	ModelID    string `json:"model_id"`
	InstanceID string `json:"instance_id,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

// PipelineStopResult is the response contract of POST /pipelines/{id}/stop.
type PipelineStopResult struct {
	PipelineID string              `json:"pipeline_id"`
	Results    []PipelineEntryStop `json:"results"`
}

// PipelineRestartResult is the fixed response contract of
// POST /pipelines/{id}/restart: stop_results (reverse order) +
// start_results (forward order, present for all entries).
type PipelineRestartResult struct {
	PipelineID   string               `json:"pipeline_id"`
	StopResults  []PipelineEntryStop  `json:"stop_results"`
	StartResults []PipelineEntryStart `json:"start_results"`
}

// PipelineService implements the Pipeline group lifecycle (ADR 010 D2/D3):
// ordered, sequential, best-effort start/stop/restart over existing Models,
// reusing the single Supervisor.Start/Stop path unchanged.
type PipelineService struct {
	supervisor *process.Supervisor
	repo       storage.Repository

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

func NewPipelineService(supervisor *process.Supervisor, repo storage.Repository) *PipelineService {
	return &PipelineService{
		supervisor: supervisor,
		repo:       repo,
		locks:      map[string]*sync.Mutex{},
	}
}

// lockPipeline serializes concurrent lifecycle requests for the same
// pipeline (start idempotency; no double-launch race).
func (s *PipelineService) lockPipeline(id string) func() {
	s.locksMu.Lock()
	m, ok := s.locks[id]
	if !ok {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	s.locksMu.Unlock()
	m.Lock()
	return m.Unlock
}

func isActiveInstanceState(state string) bool {
	switch state {
	case "running", "starting", "stopping", "pending":
		return true
	}
	return false
}

// ─── CRUD (integrity rules per ADR 010 D1) ───

// ValidatePipelineEntry enforces the create/update field rules: non-empty
// name, non-empty model list, no duplicate model id, all model ids known.
func ValidatePipelineEntry(repo storage.Repository, entry *storage.PipelineEntry) error {
	if entry == nil || strings.TrimSpace(entry.Name) == "" {
		return apierrors.NewAPIError(apierrors.CodeBadRequest, "pipeline name is required")
	}
	if len(entry.Models) == 0 {
		return apierrors.NewAPIError(apierrors.CodeBadRequest, "pipeline model list is required")
	}
	seen := make(map[string]bool, len(entry.Models))
	for _, m := range entry.Models {
		if m.ModelID == "" {
			return apierrors.NewAPIError(apierrors.CodeBadRequest, "model_id is required in pipeline entries")
		}
		if seen[m.ModelID] {
			return apierrors.NewAPIError(apierrors.CodeBadRequest, "duplicate model in pipeline: "+m.ModelID)
		}
		seen[m.ModelID] = true
		if _, err := repo.GetModel(m.ModelID); err != nil {
			return apierrors.NewAPIError(apierrors.CodeBadRequest, "unknown model id in pipeline: "+m.ModelID)
		}
	}
	return nil
}

func (s *PipelineService) CreatePipeline(ctx context.Context, entry *storage.PipelineEntry) error {
	if err := ValidatePipelineEntry(s.repo, entry); err != nil {
		return err
	}
	return s.repo.CreatePipeline(entry)
}

// UpdatePipeline applies D1.5: name/args/Active/per-entry AutoStart may
// always change; structural changes (add/remove/reorder of the model list)
// are rejected with 409 while the pipeline has active owned instances.
func (s *PipelineService) UpdatePipeline(ctx context.Context, entry *storage.PipelineEntry) error {
	current, err := s.repo.GetPipeline(entry.ID)
	if err != nil {
		return apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+entry.ID)
	}
	if err := ValidatePipelineEntry(s.repo, entry); err != nil {
		return err
	}
	if !sameModelSequence(current.Models, entry.Models) && s.hasActiveOwnedInstances(entry.ID) {
		return apierrors.NewAPIError(apierrors.CodeConflict,
			"pipeline has active owned instances; structural changes are not allowed while active")
	}
	entry.CreatedAt = current.CreatedAt
	return s.repo.UpdatePipeline(entry)
}

// DeletePipeline refuses with 409 while the pipeline has active owned
// instances. Terminal instances keep the historical pipeline_id.
func (s *PipelineService) DeletePipeline(ctx context.Context, id string) error {
	if _, err := s.repo.GetPipeline(id); err != nil {
		return apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+id)
	}
	if s.hasActiveOwnedInstances(id) {
		return apierrors.NewAPIError(apierrors.CodeConflict,
			"pipeline has active owned instances; stop the pipeline before deleting it")
	}
	return s.repo.DeletePipeline(id)
}

// ListPipelinesReferencingModel returns the names of pipelines referencing
// the model (integrity check for model delete, ADR 010 D1.5).
func (s *PipelineService) ListPipelinesReferencingModel(modelID string) []string {
	pipelines, err := s.repo.ListPipelines()
	if err != nil {
		return nil
	}
	var names []string
	for _, p := range pipelines {
		for _, m := range p.Models {
			if m.ModelID == modelID {
				names = append(names, p.Name)
				break
			}
		}
	}
	return names
}

func sameModelSequence(a, b []domain.PipelineModel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ModelID != b[i].ModelID {
			return false
		}
	}
	return true
}

// hasActiveOwnedInstances reports whether any instance in an active state
// carries the given pipeline_id (ADR 010 D1.4).
func (s *PipelineService) hasActiveOwnedInstances(pipelineID string) bool {
	instances, err := s.repo.ListInstances()
	if err != nil {
		return false
	}
	for _, inst := range instances {
		if inst.PipelineID == pipelineID && isActiveInstanceState(inst.State) {
			return true
		}
	}
	return false
}

// ─── Group lifecycle (ADR 010 D3) ───

// Start processes entries sequentially in pipeline order. Best-effort: an
// error in one entry neither cancels nor blocks the following entries.
func (s *PipelineService) Start(ctx context.Context, pipelineID string) (*PipelineStartResult, error) {
	defer s.lockPipeline(pipelineID)()
	p, err := s.repo.GetPipeline(pipelineID)
	if err != nil {
		return nil, apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+pipelineID)
	}
	res := &PipelineStartResult{
		PipelineID: pipelineID,
		Results:    make([]PipelineEntryStart, 0, len(p.Models)),
	}
	for _, entry := range p.Models {
		res.Results = append(res.Results, s.startEntry(ctx, pipelineID, entry))
	}
	return res, nil
}

// Autostart is the startup path (ADR 010 D4): it processes only entries
// with AutoStart=true, sequentially in list order, with the same D2 Args
// semantics and D3 skip/outcome rules as manual start. Launched instances
// carry the pipeline_id; per-entry failures do not abort startup. It emits
// no audit events (startup has no user/session context).
func (s *PipelineService) Autostart(ctx context.Context, pipelineID string) (*PipelineStartResult, error) {
	defer s.lockPipeline(pipelineID)()
	p, err := s.repo.GetPipeline(pipelineID)
	if err != nil {
		return nil, apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+pipelineID)
	}
	res := &PipelineStartResult{
		PipelineID: pipelineID,
		Results:    make([]PipelineEntryStart, 0, len(p.Models)),
	}
	for _, entry := range p.Models {
		if !entry.AutoStart {
			continue
		}
		res.Results = append(res.Results, s.startEntry(ctx, pipelineID, entry))
	}
	return res, nil
}

// Stop stops exactly the active owned instances, in REVERSE pipeline order.
// A stop failure on one entry does not block the remaining entries.
func (s *PipelineService) Stop(ctx context.Context, pipelineID string) (*PipelineStopResult, error) {
	defer s.lockPipeline(pipelineID)()
	p, err := s.repo.GetPipeline(pipelineID)
	if err != nil {
		return nil, apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+pipelineID)
	}
	res := &PipelineStopResult{
		PipelineID: pipelineID,
		Results:    make([]PipelineEntryStop, 0, len(p.Models)),
	}
	for i := len(p.Models) - 1; i >= 0; i-- {
		res.Results = append(res.Results, s.stopEntry(ctx, pipelineID, p.Models[i].ModelID))
	}
	return res, nil
}

// Restart = Stop phase (reverse order) then Start phase (forward order,
// ALWAYS executed for all entries, regardless of individual stop failures).
func (s *PipelineService) Restart(ctx context.Context, pipelineID string) (*PipelineRestartResult, error) {
	defer s.lockPipeline(pipelineID)()
	p, err := s.repo.GetPipeline(pipelineID)
	if err != nil {
		return nil, apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+pipelineID)
	}

	stopResults := make([]PipelineEntryStop, 0, len(p.Models))
	for i := len(p.Models) - 1; i >= 0; i-- {
		stopResults = append(stopResults, s.stopEntry(ctx, pipelineID, p.Models[i].ModelID))
	}

	startResults := make([]PipelineEntryStart, 0, len(p.Models))
	for _, entry := range p.Models {
		startResults = append(startResults, s.startEntry(ctx, pipelineID, entry))
	}

	return &PipelineRestartResult{
		PipelineID:   pipelineID,
		StopResults:  stopResults,
		StartResults: startResults,
	}, nil
}

// startEntry launches one pipeline entry with the D2 all-or-nothing Args
// override (pre-substitution; the persisted Model.Args is never modified).
// Skip outcomes create no instance record; only started/failed do.
func (s *PipelineService) startEntry(ctx context.Context, pipelineID string, entry domain.PipelineModel) PipelineEntryStart {
	out := PipelineEntryStart{ModelID: entry.ModelID}

	me, err := s.repo.GetModel(entry.ModelID)
	if err != nil {
		out.Status = OutcomeModelMissing
		return out
	}
	rte, err := s.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		out.Status = OutcomeNoRuntime
		return out
	}

	insts, err := s.repo.ListByModelID(entry.ModelID)
	if err == nil {
		for _, inst := range insts {
			if isActiveInstanceState(inst.State) {
				// Never a second copy, never adopted: the existing instance
				// keeps its own pipeline_id (empty when manual).
				out.Status = OutcomeAlreadyRunning
				return out
			}
		}
		for _, inst := range insts {
			if inst.State == "orphan" {
				// Consistent with the Models-page contract: no Start while an
				// out-of-GoAl process may be running.
				out.Status = OutcomeOrphanSkipped
				return out
			}
		}
	}

	dm := domain.ModelEntryToDomain(me)
	if len(entry.Args) > 0 {
		dm.Args = entry.Args
	}
	dm.PipelineID = pipelineID
	rt := process.RuntimeToDomain(
		rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory,
		rte.Environment,
	)

	inst, err := s.supervisor.Start(ctx, dm, rt, nil, nil)
	if err != nil {
		// Standard Supervisor failure semantics apply: a terminal failed
		// instance record is persisted. The reason stays a bounded class.
		out.Status = OutcomeFailed
		if strings.Contains(err.Error(), "resolve instance") {
			out.Error = ReasonResolveFailed
		} else {
			out.Error = ReasonStartFailed
		}
		return out
	}
	out.Status = OutcomeStarted
	out.InstanceID = string(inst.ID)
	return out
}

// stopEntry stops all active owned instances of the model. Manual
// instances (empty pipeline_id), orphan and stale instances are untouched.
func (s *PipelineService) stopEntry(ctx context.Context, pipelineID, modelID string) PipelineEntryStop {
	out := PipelineEntryStop{ModelID: modelID}

	insts, err := s.repo.ListByModelID(modelID)
	if err == nil {
		for _, inst := range insts {
			if inst.PipelineID != pipelineID || !isActiveInstanceState(inst.State) {
				continue
			}
			if err := s.supervisor.Stop(ctx, domain.InstanceID(inst.ID)); err != nil {
				out.Status = OutcomeFailed
				out.InstanceID = inst.ID
				out.Error = ReasonStopFailed
				return out
			}
			out.InstanceID = inst.ID
		}
	}
	out.Status = OutcomeStopped
	return out
}
