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

// PipelineEntryStart is one per-entry result of a pipeline start (pipeline
// order in the response). EntryID/Index identify the entry (ADR 013 D5);
// ModelID stays for compatibility and may repeat for repeatable models.
type PipelineEntryStart struct {
	ModelID    string `json:"model_id"`
	EntryID    string `json:"entry_id,omitempty"`
	Index      int    `json:"index"`
	Status     string `json:"status"`
	InstanceID string `json:"instance_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// PipelineStartResult is the response contract of POST /pipelines/{id}/start.
type PipelineStartResult struct {
	PipelineID string               `json:"pipeline_id"`
	Results    []PipelineEntryStart `json:"results"`
}

// PipelineEntryStop is one per-entry result of a pipeline stop (reverse
// pipeline order in the response). StoppedInstanceIDs lists every instance
// stopped for this entry (ADR 013 D4); InstanceID keeps the last stopped
// id for backward compatibility with the v8 client.
type PipelineEntryStop struct {
	ModelID            string   `json:"model_id"`
	EntryID            string   `json:"entry_id,omitempty"`
	Index              int      `json:"index"`
	InstanceID         string   `json:"instance_id,omitempty"`
	StoppedInstanceIDs []string `json:"stopped_instance_ids,omitempty"`
	Status             string   `json:"status"`
	Error              string   `json:"error,omitempty"`
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

// isInFlightState delegates to the canonical domain predicate
// (pending | starting | running | stopping).
func isInFlightState(state string) bool {
	return domain.InstanceState(state).IsInFlight()
}

// ─── CRUD (integrity rules per ADR 010 D1) ───

// ValidatePipelineEntry enforces the create/update field rules (ADR 013 D1):
// non-empty name, non-empty entry list, every entry references a non-empty
// known model id, and non-empty entry ids are unique. A model MAY appear in
// multiple entries of the same pipeline — the entry is the unit of
// uniqueness, not the model id (ADR 010 D1.3 is superseded).
func ValidatePipelineEntry(repo storage.Repository, entry *storage.PipelineEntry) error {
	if entry == nil || strings.TrimSpace(entry.Name) == "" {
		return apierrors.NewAPIError(apierrors.CodeBadRequest, "pipeline name is required")
	}
	if len(entry.Models) == 0 {
		return apierrors.NewAPIError(apierrors.CodeBadRequest, "pipeline model list is required")
	}
	seenEntryIDs := make(map[string]bool, len(entry.Models))
	for _, m := range entry.Models {
		if m.ModelID == "" {
			return apierrors.NewAPIError(apierrors.CodeBadRequest, "model_id is required in pipeline entries")
		}
		if m.ID != "" {
			if seenEntryIDs[m.ID] {
				return apierrors.NewAPIError(apierrors.CodeBadRequest, "duplicate entry id in pipeline: "+m.ID)
			}
			seenEntryIDs[m.ID] = true
		}
		if _, err := repo.GetModel(m.ModelID); err != nil {
			return apierrors.NewAPIError(apierrors.CodeBadRequest, "unknown model id in pipeline: "+m.ModelID)
		}
	}
	return nil
}

// sanitizeCreateEntryIDs drops client-supplied entry ids on create: the
// server is the only source of entry identity (ADR 013 D1/D5). A nil entry is
// safe (validation still returns a bounded bad_request).
func sanitizeCreateEntryIDs(entry *storage.PipelineEntry) {
	if entry == nil {
		return
	}
	for i := range entry.Models {
		entry.Models[i].ID = ""
	}
}

func (s *PipelineService) CreatePipeline(ctx context.Context, entry *storage.PipelineEntry) error {
	sanitizeCreateEntryIDs(entry)
	if err := ValidatePipelineEntry(s.repo, entry); err != nil {
		return err
	}
	return s.repo.CreatePipeline(entry)
}

// UpdatePipeline applies D1.5: name/args/Active/per-entry AutoStart may
// always change; structural changes (add/remove/reorder of the entry list)
// are rejected with 409 while the pipeline has active owned instances.
func (s *PipelineService) UpdatePipeline(ctx context.Context, entry *storage.PipelineEntry) error {
	current, err := s.repo.GetPipeline(entry.ID)
	if err != nil {
		return apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+entry.ID)
	}
	if err := ValidatePipelineEntry(s.repo, entry); err != nil {
		return err
	}
	// Entry identity (ADR 013 D1): a passed non-empty entry id must reference
	// an existing entry of this pipeline — no silent re-identification.
	currentIDs := make(map[string]bool, len(current.Models))
	for _, m := range current.Models {
		currentIDs[m.ID] = true
	}
	for _, m := range entry.Models {
		if m.ID != "" && !currentIDs[m.ID] {
			return apierrors.NewAPIError(apierrors.CodeBadRequest, "unknown pipeline entry id: "+m.ID)
		}
	}
	if !sameEntrySequence(current.Models, entry.Models) && s.hasActiveOwnedInstances(entry.ID) {
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

// sameEntrySequence reports whether two entry lists reference the same
// entries in the same order (ADR 013 D1/D8: a structural change is a
// different entry-id sequence; args/auto_start edits are non-structural).
// A new entry carries an empty id (server-generated) and therefore always
// counts as structural.
func sameEntrySequence(a, b []domain.PipelineModel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
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
		if inst.PipelineID == pipelineID && isInFlightState(inst.State) {
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
	for i, entry := range p.Models {
		res.Results = append(res.Results, s.startEntry(ctx, pipelineID, i, entry))
	}
	return res, nil
}

// Autostart is the startup path: when GoAl starts, a pipeline whose
// Pipeline.Active is true launches ALL of its entries, sequentially in list
// order, with the same D2 Args semantics and D3 skip/outcome rules as manual
// start. It is therefore behaviorally identical to Start — the pipeline-level
// Active flag (checked by the caller in cmd/goal) is the only autostart gate.
//
// Compatibility note (ADR 013 reconciliation): the legacy per-entry
// PipelineModel.AutoStart field is retained for backward compatibility with
// existing persisted pipelines (it still round-trips through storage and the
// API) but is no longer consulted for any launch decision. A legacy
// AutoStart=false entry therefore now launches whenever the pipeline is
// Active / started — the canonical "all entries" semantics. No schema
// migration is required; the field simply becomes written-but-ignored.
// Launched instances carry the pipeline_id; per-entry failures do not abort
// startup. It emits no audit events (startup has no user/session context).
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
	for i, entry := range p.Models {
		res.Results = append(res.Results, s.startEntry(ctx, pipelineID, i, entry))
	}
	return res, nil
}

// Stop stops exactly the active owned instances, in REVERSE pipeline order
// (ADR 013 D4: per entry; a stop failure on one instance neither aborts the
// entry's remaining stops nor blocks the following entries).
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
	targets := s.stopTargets(p, pipelineID)
	for i := len(p.Models) - 1; i >= 0; i-- {
		res.Results = append(res.Results, s.stopEntry(ctx, i, p.Models[i], targets[i]))
	}
	return res, nil
}

// stopTargets computes, per entry in list order, the set of active owned
// instances the entry stops (ADR 013 D4). Attributed instances
// (pipeline_id + pipeline_entry_id) always target their own entry; legacy
// instances owned by the pipeline without an entry attribution (pre-ADR 013
// files) are assigned to the FIRST entry of that model in list order —
// deterministic, and no active owned instance is left unassigned.
func (s *PipelineService) stopTargets(p *storage.PipelineEntry, pipelineID string) [][]string {
	insts, err := s.repo.ListInstances()
	if err != nil {
		insts = nil
	}
	claimed := make([]bool, len(p.Models))
	for i, m := range p.Models {
		for _, inst := range insts {
			if inst.PipelineID == pipelineID && inst.PipelineEntryID == m.ID && isInFlightState(inst.State) {
				claimed[i] = true
				break
			}
		}
	}
	legacyByModel := make(map[string][]string)
	for _, inst := range insts {
		if inst.PipelineID != pipelineID || inst.PipelineEntryID != "" || !isInFlightState(inst.State) {
			continue
		}
		legacyByModel[inst.ModelID] = append(legacyByModel[inst.ModelID], inst.ID)
	}
	firstOfModel := make(map[string]int, len(p.Models))
	for i, m := range p.Models {
		if _, ok := firstOfModel[m.ModelID]; !ok {
			firstOfModel[m.ModelID] = i
		}
	}
	targets := make([][]string, len(p.Models))
	for i, m := range p.Models {
		if claimed[i] {
			for _, inst := range insts {
				if inst.PipelineID == pipelineID && inst.PipelineEntryID == m.ID && isInFlightState(inst.State) {
					targets[i] = append(targets[i], inst.ID)
				}
			}
		}
		if firstOfModel[m.ModelID] == i {
			// Legacy instances of the model always resolve to the first
			// entry of the model, claimed or not — no owned active instance
			// is left unassigned.
			targets[i] = append(targets[i], legacyByModel[m.ModelID]...)
		}
	}
	return targets
}

// Restart = Stop phase (reverse order) then Start phase (forward order,
// ALWAYS executed for all entries, regardless of individual stop failures).
func (s *PipelineService) Restart(ctx context.Context, pipelineID string) (*PipelineRestartResult, error) {
	defer s.lockPipeline(pipelineID)()
	p, err := s.repo.GetPipeline(pipelineID)
	if err != nil {
		return nil, apierrors.NewAPIError(apierrors.CodeNotFound, "pipeline not found: "+pipelineID)
	}

	targets := s.stopTargets(p, pipelineID)
	stopResults := make([]PipelineEntryStop, 0, len(p.Models))
	for i := len(p.Models) - 1; i >= 0; i-- {
		stopResults = append(stopResults, s.stopEntry(ctx, i, p.Models[i], targets[i]))
	}

	startResults := make([]PipelineEntryStart, 0, len(p.Models))
	for i, entry := range p.Models {
		startResults = append(startResults, s.startEntry(ctx, pipelineID, i, entry))
	}

	return &PipelineRestartResult{
		PipelineID:   pipelineID,
		StopResults:  stopResults,
		StartResults: startResults,
	}, nil
}

// startEntry launches one pipeline entry with the D2 all-or-nothing Args
// override (pre-substitution; the persisted Model.Args is never modified)
// and the ADR 013 D3 per-entry launch gate:
//
//   - per-entry idempotency: an active instance already attributed to this
//     entry (pipeline_id + pipeline_entry_id) → already-running;
//   - model-owner rule: an active instance owned by another pipeline or by
//     a manual launch (empty pipeline_id) → already-running, never adopted;
//     active instances owned by THIS pipeline (any of its entries, or a
//     legacy unattributed one) do not block this entry — within-pipeline
//     repeatable models launch independently;
//   - orphan gate: an orphan of this pipeline (or a legacy unattributed
//     orphan of it) → orphan-skipped; an orphan owned by someone else →
//     already-running (an out-of-GoAl process may hold the model's ports).
//
// Skip outcomes create no instance record; only started/failed do.
func (s *PipelineService) startEntry(ctx context.Context, pipelineID string, index int, entry domain.PipelineModel) PipelineEntryStart {
	out := PipelineEntryStart{ModelID: entry.ModelID, EntryID: entry.ID, Index: index}

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
			if isInFlightState(inst.State) {
				if inst.PipelineID == pipelineID && inst.PipelineEntryID == entry.ID {
					out.Status = OutcomeAlreadyRunning
					return out
				}
				if inst.PipelineID != pipelineID {
					// Foreign owner (another pipeline or manual): no second
					// copy, never adopted.
					out.Status = OutcomeAlreadyRunning
					return out
				}
				if inst.PipelineEntryID == "" {
					// Legacy (pre-ADR 013) unattributed instance owned by
					// this pipeline: the owning entry cannot be determined —
					// conservative, no second copy. It is still stopped by
					// the D4 legacy fallback.
					out.Status = OutcomeAlreadyRunning
					return out
				}
				// Attributed to another entry of this pipeline:
				// within-pipeline independence — this entry may launch its
				// own instance (ADR 013 D3).
			}
			if inst.State == "orphan" {
				if inst.PipelineID == pipelineID || inst.PipelineID == "" {
					// Consistent with the Models-page contract: no Start
					// while an out-of-GoAl process may be running.
					out.Status = OutcomeOrphanSkipped
					return out
				}
				out.Status = OutcomeAlreadyRunning
				return out
			}
		}
	}

	dm := domain.ModelEntryToDomain(me)
	if len(entry.Args) > 0 {
		dm.Args = entry.Args
	}
	dm.PipelineID = pipelineID
	dm.PipelineEntryID = entry.ID
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

// stopEntry stops exactly the target instances of one entry (ADR 013 D4).
// A stop failure on one instance is recorded and does NOT abort the
// entry's remaining stops (the pre-ADR 013 early-return debt is fixed).
// Manual instances (empty pipeline_id), orphan and stale instances are
// never targets (stopTargets only selects owned active instances).
func (s *PipelineService) stopEntry(ctx context.Context, index int, entry domain.PipelineModel, targets []string) PipelineEntryStop {
	out := PipelineEntryStop{ModelID: entry.ModelID, EntryID: entry.ID, Index: index, Status: OutcomeStopped}
	for _, id := range targets {
		if err := s.supervisor.Stop(ctx, domain.InstanceID(id)); err != nil {
			out.Status = OutcomeFailed
			out.InstanceID = id
			out.Error = ReasonStopFailed
			continue
		}
		out.InstanceID = id
		out.StoppedInstanceIDs = append(out.StoppedInstanceIDs, id)
	}
	return out
}
