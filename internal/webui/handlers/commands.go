package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

// CommandsHandler is the Stage 1 control-command dispatch point (ADR 024). It is a
// thin primitive: it names a command and delegates to the service that already
// performs it. It holds no *process.Supervisor and no lifecycle logic of its own —
// the entire lifecycle is reached only by delegating to InstanceService.StartModel
// (→ Supervisor.AdmitAndStart, the single ADR 017 arbitration boundary). There is no
// second supervisor, no second arbitration path and no lifecycle duplication.
type CommandsHandler struct {
	instanceSvc *application.InstanceService
	repo        storage.Repository
}

func NewCommandsHandler(instanceSvc *application.InstanceService, repo storage.Repository) *CommandsHandler {
	return &CommandsHandler{instanceSvc: instanceSvc, repo: repo}
}

// commandRequest is the Stage 1 command body (ADR 024 D-4): a command name and the
// target identifier. For model.start, targetId is the model ID — the same
// identifier the existing POST /api/v1/models/{id}/start route carries in its path.
type commandRequest struct {
	Command  string `json:"command"`
	TargetID string `json:"targetId"`
}

// cmdModelStart is the one Stage 1 command (ADR 024 D-3, closed vocabulary, exact-match).
const cmdModelStart = "model.start"

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
	switch req.Command {
	case cmdModelStart:
		h.executeModelStart(w, r, req.TargetID)
	default:
		// ADR 024 D-6: a command outside the closed Stage 1 vocabulary is a bounded
		// 400; the message names the rejected value, never a stack trace.
		writeAPIError(w, statusForAPICode(apierrors.CodeUnknownCommand),
			apierrors.NewAPIError(apierrors.CodeUnknownCommand, req.Command))
	}
}

// executeModelStart is the model.start dispatch (ADR 024 D-1/D-5/D-6). It mirrors the
// existing POST /api/v1/models/{id}/start handler so the command surface returns the
// same 200 body and the same lifecycle status+code, and it adds the one
// command-surface distinction D-6 names: an unresolvable target is a bounded 404 here,
// not the 500 the existing route would fall through to. The existing route and
// InstanceService.StartModel are unchanged (ADR 024 C-6).
func (h *CommandsHandler) executeModelStart(w http.ResponseWriter, r *http.Request, modelID string) {
	// D-6: report an unresolvable target as a bounded 404 before delegating.
	if _, err := h.repo.GetModel(modelID); err != nil {
		writeAPIError(w, http.StatusNotFound, apierrors.NewAPIError(apierrors.CodeNotFound, "model not found"))
		return
	}
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
