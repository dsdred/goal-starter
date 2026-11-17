package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// CommandsHandler is the Stage 1 control-command dispatch point (ADR 024). It is a
// thin primitive: it names a command and delegates to the service that already
// performs it. It holds no *process.Supervisor and no lifecycle logic of its own —
// the entire lifecycle (start, stop, restart; model- and pipeline-owned) is reached
// only by delegating to InstanceService and PipelineService, which in turn reach the
// single ADR 017 arbitration boundary (Supervisor.AdmitAndStart / Stop / Restart).
// There is no second supervisor, no second arbitration path and no lifecycle
// duplication: this handler re-implements no admission, ownership or rollback rule.
type CommandsHandler struct {
	instanceSvc *application.InstanceService
	pipelineSvc *application.PipelineService
	repo        storage.Repository
	audit       *audit.AuditLogger
	sess        *security.SessionStore
}

func NewCommandsHandler(instanceSvc *application.InstanceService, pipelineSvc *application.PipelineService, repo storage.Repository) *CommandsHandler {
	return &CommandsHandler{instanceSvc: instanceSvc, pipelineSvc: pipelineSvc, repo: repo}
}

// WithAudit injects the durable audit logger (ADR 007). Pipeline commands emit the
// same control-action audit events as their per-object routes (ADR 024 D-7 parity);
// a nil logger disables that emission (e.g. the direct-invocation test path).
func (h *CommandsHandler) WithAudit(logger *audit.AuditLogger) *CommandsHandler {
	h.audit = logger
	return h
}

// WithSessionStore injects the session store used to resolve the authenticated user
// for pipeline-command audit records.
func (h *CommandsHandler) WithSessionStore(sess *security.SessionStore) *CommandsHandler {
	h.sess = sess
	return h
}

// commandRequest is the Stage 1 command body (ADR 024 D-4): a command name and the
// target identifier. For model.* commands, targetId is the model ID; for pipeline.*
// commands it is the pipeline ID — the same identifier the corresponding per-object
// route carries in its path (ADR 024 D-9).
type commandRequest struct {
	Command  string `json:"command"`
	TargetID string `json:"targetId"`
}

// The closed Stage 1 command vocabulary (ADR 024 D-3, extended by D-8), exact-match,
// case-sensitive. Any other command value is an unknown command (D-6).
const (
	cmdModelStart      = "model.start"
	cmdModelStop       = "model.stop"
	cmdModelRestart    = "model.restart"
	cmdPipelineStart   = "pipeline.start"
	cmdPipelineStop    = "pipeline.stop"
	cmdPipelineRestart = "pipeline.restart"
)

// Execute dispatches one command (POST /api/v1/commands/execute, ADR 024 D-2). It runs
// behind the same requireAuthCSRF boundary as every other mutating route.
func (h *CommandsHandler) Execute(w http.ResponseWriter, r *http.Request) {
	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Command == "" || req.TargetID == "" {
		writeAPIError(w, http.StatusBadRequest, apierrors.NewAPIError(apierrors.CodeBadRequest, "command and targetId are required"))
		return
	}
	if ok, err := h.targetExists(req.Command, req.TargetID); !ok {
		writeAPIError(w, http.StatusNotFound, apierrors.NewAPIError(apierrors.CodeNotFound, err.Error()))
		return
	}
	switch req.Command {
	case cmdModelStart:
		h.executeModelStart(w, r, req.TargetID)
	case cmdModelStop:
		h.executeModelStop(w, r, req.TargetID)
	case cmdModelRestart:
		h.executeModelRestart(w, r, req.TargetID)
	case cmdPipelineStart:
		h.executePipelineStart(w, r, req.TargetID)
	case cmdPipelineStop:
		h.executePipelineStop(w, r, req.TargetID)
	case cmdPipelineRestart:
		h.executePipelineRestart(w, r, req.TargetID)
	default:
		// ADR 024 D-6: a command outside the closed Stage 1 vocabulary is a bounded
		// 400; the message names the rejected value, never a stack trace.
		writeAPIError(w, statusForAPICode(apierrors.CodeUnknownCommand),
			apierrors.NewAPIError(apierrors.CodeUnknownCommand, req.Command))
	}
}

// targetExists reports whether the command's target is resolvable (ADR 024 D-6): a
// model command must reference a known model, a pipeline command a known pipeline.
// An unresolvable target is a bounded 404 on the command surface — the deliberate
// improvement over the per-object routes (model.start falls through to 500, and
// model.stop/restart answer 200 on a ghost model today). The existing routes and
// services are unchanged (ADR 024 C-6); this check is command-surface only. A command
// outside the closed vocabulary is not a resolvable target here: Execute reports it as
// unknown_command (400), not not_found (404).
func (h *CommandsHandler) targetExists(command, id string) (bool, error) {
	switch command {
	case cmdModelStart, cmdModelStop, cmdModelRestart:
		if _, err := h.repo.GetModel(id); err != nil {
			return false, errors.New("model not found")
		}
	case cmdPipelineStart, cmdPipelineStop, cmdPipelineRestart:
		if _, err := h.repo.GetPipeline(id); err != nil {
			return false, errors.New("pipeline not found")
		}
	}
	return true, nil
}

// executeModelStart is the model.start dispatch (ADR 024 D-1/D-5/D-6). It mirrors the
// existing POST /api/v1/models/{id}/start handler so the command surface returns the
// same 200 body and the same lifecycle status+code, and it adds the one
// command-surface distinction D-6 names: an unresolvable target is a bounded 404 here
// (handled in targetExists), not the 500 the existing route would fall through to. The
// existing route and InstanceService.StartModel are unchanged (ADR 024 C-6).
func (h *CommandsHandler) executeModelStart(w http.ResponseWriter, r *http.Request, modelID string) {
	// The same in-flight precheck the model route performs (409 launch_in_flight).
	if instances, err := h.instanceSvc.ListInstances(r.Context()); err == nil {
		for _, inst := range instances {
			if inst.ModelID == modelID && inst.IsInFlight() {
				writeAPIError(w, http.StatusConflict, apierrors.NewAPIError(apierrors.CodeConflict, "launch_in_flight"))
				return
			}
		}
	}
	inst, err := h.instanceSvc.StartModel(r.Context(), modelID)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	inst.Environment = nil
	writeJSON(w, http.StatusOK, domain.ToStorageEntry(inst))
}

// executeModelStop is the model.stop dispatch (ADR 024 D-5/D-8). It mirrors the
// existing POST /api/v1/models/{id}/stop handler: a pending launch of the model is
// launch_in_flight (409), otherwise every live instance of the model is stopped
// (delegated to InstanceService.StopInstance → Supervisor.Stop, the single ADR 017
// boundary) and the same 200 body is returned. The existing route is unchanged (C-6).
func (h *CommandsHandler) executeModelStop(w http.ResponseWriter, r *http.Request, modelID string) {
	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, inst := range instances {
		if inst.ModelID == modelID && inst.State == domain.InstanceStatePending {
			writeAPIError(w, http.StatusConflict, apierrors.NewAPIError(apierrors.CodeConflict, "launch_in_flight"))
			return
		}
	}
	for _, inst := range instances {
		if inst.ModelID == modelID && inst.IsLive() {
			if err := h.instanceSvc.StopInstance(r.Context(), inst.ID); err != nil {
				writeLifecycleError(w, err)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// executeModelRestart is the model.restart dispatch (ADR 024 D-5/D-8). It mirrors the
// existing POST /api/v1/models/{id}/restart handler: the whole in-flight target set is
// selected and preflighted first (InstanceService.PreflightRestart, ADR 017 D1
// pre-flight atomicity) so no instance is mutated and then left un-restarted, then each
// target is restarted (InstanceService.RestartInstance → Supervisor.RestartWithLaunch,
// the single ADR 017 boundary). The same 200 body is returned. The existing route is
// unchanged (C-6).
func (h *CommandsHandler) executeModelRestart(w http.ResponseWriter, r *http.Request, modelID string) {
	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Select the whole target set first and preflight it (ADR 017 D1): a model
	// restart must not mutate some of its instances and then fail on one it should
	// never have touched.
	var targets []domain.InstanceID
	for _, inst := range instances {
		if inst.ModelID == modelID && inst.IsInFlight() {
			targets = append(targets, inst.ID)
		}
	}
	if err := h.instanceSvc.PreflightRestart(r.Context(), targets); err != nil {
		writeLifecycleError(w, err)
		return
	}
	for _, instID := range targets {
		if _, err := h.instanceSvc.RestartInstance(r.Context(), instID); err != nil {
			writeLifecycleError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// executePipelineStart is the pipeline.start dispatch (ADR 024 D-5/D-8). It delegates
// to PipelineService.Start — the same ordered, best-effort group start the per-object
// route performs — and mirrors that route's response and error contract (the aggregate
// body plus the groupFailureClass status) and its control-action audit event (D-7
// parity). The existing route is unchanged (C-6).
func (h *CommandsHandler) executePipelineStart(w http.ResponseWriter, r *http.Request, id string) {
	res, err := h.pipelineSvc.Start(r.Context(), id)
	if err != nil {
		var agg *application.PipelineStartError
		if errors.As(err, &agg) {
			apiErr := groupFailureClass(agg.Phase, err)
			h.auditPipeline(r, audit.EventPipelineStart, startAuditDetail(res), apiErr)
			writeJSON(w, statusForAPICode(apiErr.Code), pipelineStartBody{
				PipelineStartResult: *res,
				Code:                string(apiErr.Code),
				Error:               apiErr.Message,
			})
			return
		}
		writeServiceError(w, err)
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventPipelineStart, startAuditDetail(res))
	writeJSON(w, http.StatusOK, res)
}

// executePipelineStop is the pipeline.stop dispatch (ADR 024 D-5/D-8). It delegates to
// PipelineService.Stop — reverse-order group stop — and mirrors the per-object route's
// response, error contract and control-action audit event (D-7 parity). The existing
// route is unchanged (C-6).
func (h *CommandsHandler) executePipelineStop(w http.ResponseWriter, r *http.Request, id string) {
	res, err := h.pipelineSvc.Stop(r.Context(), id)
	if err != nil {
		var agg *application.PipelineStopError
		if errors.As(err, &agg) {
			apiErr := groupFailureClass(agg.Phase, err)
			h.auditPipeline(r, audit.EventPipelineStop, stopAuditDetail(res), apiErr)
			writeJSON(w, statusForAPICode(apiErr.Code), pipelineStopBody{
				PipelineStopResult: *res,
				Results:            stopFailureRows(res.Results, agg.Failures),
				Code:               string(apiErr.Code),
				Error:              apiErr.Message,
			})
			return
		}
		writeServiceError(w, err)
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventPipelineStop, stopAuditDetail(res))
	writeJSON(w, http.StatusOK, res)
}

// executePipelineRestart is the pipeline.restart dispatch (ADR 024 D-5/D-8). It
// delegates to PipelineService.Restart (stop phase, reverse order, then start phase,
// forward order) and mirrors the per-object route's response, error contract and
// control-action audit event (D-7 parity). The existing route is unchanged (C-6).
func (h *CommandsHandler) executePipelineRestart(w http.ResponseWriter, r *http.Request, id string) {
	res, err := h.pipelineSvc.Restart(r.Context(), id)
	if err != nil {
		var agg *application.PipelineStopError
		if errors.As(err, &agg) {
			apiErr := groupFailureClass(agg.Phase, err)
			h.auditPipeline(r, audit.EventPipelineRestart, restartAuditDetail(id, res), apiErr)
			writeJSON(w, statusForAPICode(apiErr.Code), pipelineRestartBody{
				PipelineRestartResult: *res,
				StopResults:           stopFailureRows(res.StopResults, agg.Failures),
				Code:                  string(apiErr.Code),
				Error:                 apiErr.Message,
			})
			return
		}
		writeServiceError(w, err)
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventPipelineRestart, restartAuditDetail(id, res))
	writeJSON(w, http.StatusOK, res)
}

// auditPipeline records an incomplete group lifecycle request on the command surface
// (the command-surface twin of logPipelineFailure): the same bounded counters as the
// success event plus the error class, never raw error text or instance identity. It is
// the one command-surface audit path; model commands stay unaudited to match their
// unaudited per-object routes (ADR 024 D-7).
func (h *CommandsHandler) auditPipeline(r *http.Request, event string, detail map[string]string, apiErr *apierrors.APIError) {
	detail["error"] = apiErr.Message
	logAudit(h.audit, h.sess, r, event, detail)
}
