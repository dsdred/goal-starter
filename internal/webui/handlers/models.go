package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// modelResponse is the API response for a model.
type modelResponse struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	RuntimeID       string   `json:"runtime_id"`
	Args            []string `json:"args,omitempty"`
	EnvironmentKeys []string `json:"environment_keys,omitempty"`
	Active          bool     `json:"active"`
	AutostartDelay  int      `json:"autostart_delay,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func newModelResponse(e *storage.ModelEntry) *modelResponse {
	var envKeys []string
	for k := range e.Environment {
		envKeys = append(envKeys, k)
	}
	return &modelResponse{
		ID:              e.ID,
		Name:            e.Name,
		RuntimeID:       e.RuntimeID,
		Args:            e.Args,
		EnvironmentKeys: envKeys,
		Active:          e.Active,
		AutostartDelay:  e.AutostartDelay,
		CreatedAt:       e.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:       e.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ModelsHandler handles model-related HTTP requests.
type ModelsHandler struct {
	modelSvc    *application.ModelService
	instanceSvc *application.InstanceService
	supervisor  *process.Supervisor
	repo        storage.Repository
	csrf        *security.CSRF
	audit       *audit.AuditLogger
	sess        *security.SessionStore
}

func NewModelsHandler(modelSvc *application.ModelService, instanceSvc *application.InstanceService, supervisor *process.Supervisor, repo storage.Repository, csrf *security.CSRF) *ModelsHandler {
	return &ModelsHandler{
		modelSvc:    modelSvc,
		instanceSvc: instanceSvc,
		supervisor:  supervisor,
		repo:        repo,
		csrf:        csrf,
	}
}

// WithAudit injects the durable audit logger (ADR 007 entity extension).
// A nil logger disables audit emission for this handler.
func (h *ModelsHandler) WithAudit(logger *audit.AuditLogger) *ModelsHandler {
	h.audit = logger
	return h
}

// WithSessionStore injects the session store used to resolve the
// authenticated user for audit records.
func (h *ModelsHandler) WithSessionStore(sess *security.SessionStore) *ModelsHandler {
	h.sess = sess
	return h
}

func (h *ModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	models, err := h.modelSvc.ListModels(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp := make([]*modelResponse, 0, len(models))
	for _, m := range models {
		resp = append(resp, newModelResponse(m))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *ModelsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	entry, err := h.modelSvc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, 404, "model not found")
		return
	}
	writeJSON(w, http.StatusOK, newModelResponse(entry))
}

func (h *ModelsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var entry storage.ModelEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.modelSvc.CreateModel(r.Context(), &entry); err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			writeError(w, 400, apiErr.Message)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventModelCreate, map[string]string{"id": entry.ID})
	writeJSON(w, 201, newModelResponse(&entry))
}

func (h *ModelsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	var req struct {
		Name             *string             `json:"name"`
		RuntimeID        *string             `json:"runtime_id"`
		Args             *[]string           `json:"args"`
		Active           *bool               `json:"active"`
		AutostartDelay   *int                `json:"autostart_delay"`
		EnvironmentPatch []domain.EnvPatchOp `json:"environment_patch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if req.EnvironmentPatch != nil {
		if err := application.ValidateEnvPatch(req.EnvironmentPatch); err != nil {
			var apiErr *apierrors.APIError
			if errors.As(err, &apiErr) {
				writeError(w, 400, apiErr.Message)
				return
			}
			writeError(w, 400, err.Error())
			return
		}
	}
	patch := &storage.ModelPatch{
		Name:           req.Name,
		RuntimeID:      req.RuntimeID,
		Args:           req.Args,
		Active:         req.Active,
		AutostartDelay: req.AutostartDelay,
		Environment:    req.EnvironmentPatch,
	}
	if err := h.modelSvc.PatchModel(r.Context(), id, patch); err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			writeError(w, 400, apiErr.Message)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	// Audit: changed field names only, never values (ADR 007 §2/§5,
	// settings.saved precedent).
	changed := map[string]string{"id": id}
	if req.Name != nil {
		changed["name"] = "changed"
	}
	if req.RuntimeID != nil {
		changed["runtime_id"] = "changed"
	}
	if req.Args != nil {
		changed["args"] = "changed"
	}
	if req.Active != nil {
		changed["active"] = "changed"
	}
	if req.AutostartDelay != nil {
		changed["autostart_delay"] = "changed"
	}
	if len(req.EnvironmentPatch) > 0 {
		changed["environment"] = "changed"
	}
	if len(changed) > 1 {
		logAudit(h.audit, h.sess, r, audit.EventModelUpdate, changed)
	}
	entry, err := h.modelSvc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newModelResponse(entry))
}

func (h *ModelsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	if err := h.modelSvc.DeleteModel(r.Context(), id); err != nil {
		var apiErr *apierrors.APIError
		if errors.As(err, &apiErr) {
			status := http.StatusInternalServerError
			switch apiErr.Code {
			case apierrors.CodeConflict:
				status = http.StatusConflict
			case apierrors.CodeNotFound, apierrors.CodeInvalidModel:
				status = http.StatusNotFound
			}
			writeAPIError(w, status, apiErr)
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventModelDelete, map[string]string{"id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ─── Lifecycle ───

func (h *ModelsHandler) Start(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	inst, err := h.instanceSvc.StartModel(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	inst.Environment = nil
	writeJSON(w, http.StatusOK, domain.ToStorageEntry(inst))
}

func (h *ModelsHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	instances, _ := h.instanceSvc.ListInstances(r.Context())
	for _, inst := range instances {
		if inst.ModelID == id && inst.IsActive() {
			if err := h.instanceSvc.StopInstance(r.Context(), inst.ID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *ModelsHandler) Restart(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	instances, _ := h.instanceSvc.ListInstances(r.Context())
	for _, inst := range instances {
		if inst.ModelID == id && inst.IsActive() {
			if _, err := h.instanceSvc.RestartInstance(r.Context(), inst.ID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

func (h *ModelsHandler) Status(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	summary, err := h.instanceSvc.GetModelStatus(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *ModelsHandler) Activate(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	entry, err := h.modelSvc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, 404, "model not found")
		return
	}
	entry.Active = true
	if err := h.modelSvc.UpdateModel(r.Context(), entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventModelActivate, map[string]string{"id": id, "active": "true"})
	writeJSON(w, http.StatusOK, newModelResponse(entry))
}

func (h *ModelsHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	entry, err := h.modelSvc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, 404, "model not found")
		return
	}
	entry.Active = false
	if err := h.modelSvc.UpdateModel(r.Context(), entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventModelDeactivate, map[string]string{"id": id, "active": "false"})
	writeJSON(w, http.StatusOK, newModelResponse(entry))
}

func (h *ModelsHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	id := modelIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	me, err := h.modelSvc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, 404, "model not found")
		return
	}
	rte, err := h.repo.GetRuntime(me.RuntimeID)
	if err != nil {
		writeError(w, 404, "runtime not found")
		return
	}
	domainModel := domain.ModelEntryToDomain(me)
	domainRuntime := domain.RuntimeEntryToDomain(rte)
	spec, err := h.supervisor.ResolvePreview(domainModel, domainRuntime, nil, nil)
	if err != nil {
		if isVariableResolutionError(err) {
			writeError(w, 400, err.Error())
		} else {
			writeError(w, 500, err.Error())
		}
		return
	}
	var envKeys []string
	for _, ev := range spec.Environment {
		k, _, _ := strings.Cut(ev, "=")
		envKeys = append(envKeys, k)
	}
	writeJSON(w, http.StatusOK, &application.ModelResolveResult{
		Executable:       spec.Executable,
		Args:             spec.Args,
		WorkingDirectory: spec.WorkingDirectory,
		EnvironmentKeys:  envKeys,
	})
}

func modelIDFromPath(path string) string {
	const prefix = "/api/v1/models/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	// Strip trailing action suffix.
	if idx := strings.IndexByte(rest, '/'); idx > 0 {
		return rest[:idx]
	}
	return rest
}

func isVariableResolutionError(err error) bool {
	var uve *domain.UndefinedVariableError
	var ivre *domain.InvalidVariableReferenceError
	return errors.As(err, &uve) || errors.As(err, &ivre)
}
