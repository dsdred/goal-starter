package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// runtimeRequest is the writable representation of a runtime. A pointer to
// Environment preserves the distinction between an omitted field and an
// explicit empty object during updates.
type runtimeRequest struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Executable       string             `json:"executable"`
	WorkingDirectory string             `json:"working_directory,omitempty"`
	Environment      *map[string]string `json:"environment"`
}

func (request runtimeRequest) entry() storage.RuntimeEntry {
	var environment map[string]string
	if request.Environment != nil {
		environment = *request.Environment
	}
	return storage.RuntimeEntry{
		ID:               request.ID,
		Name:             request.Name,
		Executable:       request.Executable,
		WorkingDirectory: request.WorkingDirectory,
		Environment:      environment,
	}
}

// runtimeResponse is the public representation of a runtime. Environment
// values are write-only and must never be serialized back to API clients.
type runtimeResponse struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Executable       string    `json:"executable"`
	WorkingDirectory string    `json:"working_directory,omitempty"`
	EnvironmentKeys  []string  `json:"environment_keys"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func newRuntimeResponse(entry *storage.RuntimeEntry) runtimeResponse {
	keys := make([]string, 0, len(entry.Environment))
	for key := range entry.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return runtimeResponse{
		ID:               entry.ID,
		Name:             entry.Name,
		Executable:       entry.Executable,
		WorkingDirectory: entry.WorkingDirectory,
		EnvironmentKeys:  keys,
		CreatedAt:        entry.CreatedAt,
		UpdatedAt:        entry.UpdatedAt,
	}
}

// RuntimesHandler handles runtime-related HTTP requests.
type RuntimesHandler struct {
	runtimeSvc *application.RuntimeService
	instances  *application.InstanceService
	supervisor *process.Supervisor
	csrf       *security.CSRF
	audit      *audit.AuditLogger
	sess       *security.SessionStore
}

// NewRuntimesHandler creates a new RuntimesHandler.
func NewRuntimesHandler(runtimeSvc *application.RuntimeService, instances *application.InstanceService, supervisor *process.Supervisor, csrf *security.CSRF) *RuntimesHandler {
	return &RuntimesHandler{
		runtimeSvc: runtimeSvc,
		instances:  instances,
		supervisor: supervisor,
		csrf:       csrf,
	}
}

// WithAudit injects the durable audit logger (ADR 007 entity extension).
// A nil logger disables audit emission for this handler.
func (h *RuntimesHandler) WithAudit(logger *audit.AuditLogger) *RuntimesHandler {
	h.audit = logger
	return h
}

// WithSessionStore injects the session store used to resolve the
// authenticated user for audit records.
func (h *RuntimesHandler) WithSessionStore(sess *security.SessionStore) *RuntimesHandler {
	h.sess = sess
	return h
}

// List handles GET /api/v1/runtimes
func (h *RuntimesHandler) List(w http.ResponseWriter, r *http.Request) {
	runtimes, err := h.runtimeSvc.ListRuntimes(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	response := make([]runtimeResponse, 0, len(runtimes))
	for _, runtime := range runtimes {
		response = append(response, newRuntimeResponse(runtime))
	}
	writeJSON(w, http.StatusOK, response)
}

// Get handles GET /api/v1/runtimes/{id}
func (h *RuntimesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	rt, err := h.runtimeSvc.GetRuntime(r.Context(), id)
	if err != nil {
		writeError(w, 404, "runtime not found")
		return
	}
	writeJSON(w, http.StatusOK, newRuntimeResponse(rt))
}

// Create handles POST /api/v1/runtimes
func (h *RuntimesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var request runtimeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	entry := request.entry()
	if err := h.runtimeSvc.CreateRuntime(r.Context(), &entry); err != nil {
		if errors.Is(err, apierrors.ErrValidation) {
			writeError(w, 400, "validation failed")
			return
		}
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			writeError(w, 409, apiErr.Message)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventRuntimeCreate, map[string]string{"id": entry.ID})
	writeJSON(w, http.StatusCreated, newRuntimeResponse(&entry))
}

// Update handles PUT /api/v1/runtimes/{id}. Omitted fields are preserved;
// the environment is changed exclusively through environment_patch
// (per-key set/delete — the same lossless-edit contract as model update).
// The legacy whole-map `environment` field is rejected on update: it made
// "untouched" indistinguishable from "cleared" and caused silent environment
// wipes on ordinary edits (D4).
func (h *RuntimesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	var request struct {
		Name             *string             `json:"name"`
		Executable       *string             `json:"executable"`
		WorkingDirectory *string             `json:"working_directory"`
		EnvironmentPatch []domain.EnvPatchOp `json:"environment_patch"`
		LegacyEnv        json.RawMessage     `json:"environment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if len(request.LegacyEnv) > 0 && string(request.LegacyEnv) != "null" {
		writeError(w, 400, "environment is not accepted on runtime update; use environment_patch")
		return
	}
	if request.EnvironmentPatch != nil {
		if err := application.ValidateEnvPatch(request.EnvironmentPatch); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	patch := &storage.RuntimePatch{
		Name:             request.Name,
		Executable:       request.Executable,
		WorkingDirectory: request.WorkingDirectory,
		Environment:      request.EnvironmentPatch,
	}
	if err := h.runtimeSvc.PatchRuntime(r.Context(), id, patch); err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.Code {
			case apierrors.CodeConflict:
				writeError(w, 409, apiErr.Message)
			default:
				writeError(w, 400, apiErr.Message)
			}
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	// Audit: changed field names only, never values (ADR 007 §2/§5,
	// settings.saved precedent).
	changed := map[string]string{"id": id}
	if request.Name != nil {
		changed["name"] = "changed"
	}
	if request.Executable != nil {
		changed["executable"] = "changed"
	}
	if request.WorkingDirectory != nil {
		changed["working_directory"] = "changed"
	}
	if len(request.EnvironmentPatch) > 0 {
		changed["environment"] = "changed"
	}
	if len(changed) > 1 {
		logAudit(h.audit, h.sess, r, audit.EventRuntimeUpdate, changed)
	}
	entry, err := h.runtimeSvc.GetRuntime(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newRuntimeResponse(entry))
}

// Delete handles DELETE /api/v1/runtimes/{id}
func (h *RuntimesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	if err := h.runtimeSvc.DeleteRuntime(r.Context(), id); err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			status := http.StatusInternalServerError
			switch apiErr.Code {
			case apierrors.CodeConflict:
				status = http.StatusConflict
			case apierrors.CodeNotFound, apierrors.CodeInvalidRuntime:
				status = http.StatusNotFound
			}
			writeAPIError(w, status, apiErr)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventRuntimeDelete, map[string]string{"id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Replace handles POST /api/v1/runtimes/{id}/replace
func (h *RuntimesHandler) Replace(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	id = strings.TrimSuffix(id, "/replace")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	var request struct {
		NewRuntimeID string `json:"new_runtime_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if request.NewRuntimeID == "" {
		writeError(w, 400, "new_runtime_id is required")
		return
	}
	moved, err := h.runtimeSvc.ReplaceRuntime(r.Context(), id, request.NewRuntimeID)
	if err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			status := http.StatusInternalServerError
			switch apiErr.Code {
			case apierrors.CodeNotFound, apierrors.CodeInvalidRuntime:
				status = http.StatusNotFound
			case apierrors.CodeBadRequest:
				status = http.StatusBadRequest
			}
			writeAPIError(w, status, apiErr)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	// Cascade (option B): one top-level event + bounded moved count, never
	// per-model events.
	logAudit(h.audit, h.sess, r, audit.EventRuntimeReplace, map[string]string{
		"id":             id,
		"new_runtime_id": request.NewRuntimeID,
		"models_moved":   strconv.Itoa(moved),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "replaced", "models_moved": moved})
}

// CascadeDelete handles POST /api/v1/runtimes/{id}/cascade-delete
func (h *RuntimesHandler) CascadeDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	id = strings.TrimSuffix(id, "/cascade-delete")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	deleted, err := h.runtimeSvc.CascadeDeleteRuntime(r.Context(), id)
	if err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			status := http.StatusInternalServerError
			switch apiErr.Code {
			case apierrors.CodeNotFound, apierrors.CodeInvalidRuntime:
				status = http.StatusNotFound
			case apierrors.CodeBadRequest:
				status = http.StatusBadRequest
			}
			writeAPIError(w, status, apiErr)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	// Cascade (option B): one top-level event + bounded deleted count, never
	// per-model events.
	logAudit(h.audit, h.sess, r, audit.EventRuntimeCascadeDel, map[string]string{
		"id":             id,
		"models_deleted": strconv.Itoa(deleted),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "models_deleted": deleted})
}

// Action handles POST /api/v1/runtimes/{id}/action/{action}
func (h *RuntimesHandler) Action(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	parts := strings.SplitN(path, "/action/", 2)
	if len(parts) != 2 {
		writeError(w, 400, "invalid path")
		return
	}
	id, action := parts[0], parts[1]

	// RB-004: runtime-level start is retired. A Runtime is a launch template
	// (one runtime may serve many models), so no unambiguous ModelID can be
	// derived from a RuntimeID, and the old branch had no reachable success
	// path. Callers must use the canonical model start endpoint. This rejects
	// before any instance lookup or launch operation.
	if action == "start" {
		writeAPIError(w, http.StatusGone, apierrors.NewAPIError(apierrors.CodeGone, "runtime-level start is retired; use POST /api/v1/models/{id}/start"))
		return
	}

	instances, err := h.instances.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	switch action {
	case "stop":
		for _, inst := range instances {
			if inst.RuntimeID == id && inst.IsLive() {
				if err := h.instances.StopInstance(r.Context(), inst.ID); err != nil {
					writeLifecycleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
				return
			}
		}
		writeError(w, 404, "no running instance for this runtime")
	case "restart":
		for _, inst := range instances {
			if inst.RuntimeID == id && inst.IsLive() {
				// ADR 017 D1: preflight the selected target before mutating it,
				// so a target the restart contract refuses (starting, a restart
				// already in flight, unattributable ownership) returns its
				// bounded error instead of a partially applied restart.
				if err := h.instances.PreflightRestart(r.Context(), []domain.InstanceID{inst.ID}); err != nil {
					writeLifecycleError(w, err)
					return
				}
				if _, err := h.instances.RestartInstance(r.Context(), inst.ID); err != nil {
					writeLifecycleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
				return
			}
		}
		writeError(w, 404, "no running instance for this runtime")
	default:
		writeError(w, 400, "unknown action: "+action)
	}
}

// HealthCheck handles GET /api/v1/runtimes/health
func (h *RuntimesHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	instances, _ := h.instances.ListInstances(r.Context())
	active := 0
	redactedInstances := make([]*domain.LaunchInstance, 0, len(instances))
	for _, inst := range instances {
		if inst.IsInFlight() {
			active++
		}
		redacted := *inst
		redacted.Environment = nil
		redactedInstances = append(redactedInstances, &redacted)
	}

	health := map[string]any{
		"active_instances":  active,
		"running_instances": active,
		"instances":         redactedInstances,
	}
	writeJSON(w, http.StatusOK, health)
}

// RuntimeHealth handles GET /api/v1/runtimes/health/{id}
func (h *RuntimesHandler) RuntimeHealth(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/health/")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}

	instances, err := h.instances.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	for _, inst := range instances {
		if inst.RuntimeID == id && inst.IsLive() {
			health := map[string]any{
				"instance_id": inst.ID,
				"runtime_id":  id,
				"pid":         inst.PID,
				"state":       inst.State,
				"uptime":      time.Since(inst.StartedAt).String(),
			}
			writeJSON(w, http.StatusOK, health)
			return
		}
	}
	writeError(w, 404, "runtime instance not found")
}
