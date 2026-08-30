package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

const (
	timeFormatRFC3339 = time.RFC3339

	auditPipelineIDKey = "pipeline_id"
)

// pipelineResponse is the API shape of a pipeline (list + detail).
type pipelineResponse struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Active    bool                    `json:"active"`
	Models    []pipelineModelResponse `json:"models"`
	CreatedAt string                  `json:"created_at"`
	UpdatedAt string                  `json:"updated_at"`
}

type pipelineModelResponse struct {
	ModelID   string   `json:"model_id"`
	ModelName string   `json:"model_name"`
	Args      []string `json:"args,omitempty"`
	AutoStart bool     `json:"auto_start"`
}

func newPipelineResponse(e *storage.PipelineEntry, modelName func(id string) string) *pipelineResponse {
	models := make([]pipelineModelResponse, len(e.Models))
	for i, m := range e.Models {
		models[i] = pipelineModelResponse{
			ModelID:   m.ModelID,
			ModelName: modelName(m.ModelID),
			Args:      m.Args,
			AutoStart: m.AutoStart,
		}
	}
	return &pipelineResponse{
		ID:        e.ID,
		Name:      e.Name,
		Active:    e.Active,
		Models:    models,
		CreatedAt: e.CreatedAt.Format(timeFormatRFC3339),
		UpdatedAt: e.UpdatedAt.Format(timeFormatRFC3339),
	}
}

// pipelineModelStatus is the per-model live status in the detail endpoint.
type pipelineModelStatus struct {
	ModelID     string `json:"model_id"`
	State       string `json:"state"`
	InstanceID  string `json:"instance_id,omitempty"`
	PID         int    `json:"pid,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	AutoStart   bool   `json:"auto_start"`
	HasOverride bool   `json:"has_args_override"`
}

// PipelineHandler handles pipeline HTTP requests (ADR 010 D5).
type PipelineHandler struct {
	svc      *application.PipelineService
	repo     storage.Repository
	instance *application.InstanceService
	csrf     *security.CSRF
	sess     *security.SessionStore
	audit    *audit.AuditLogger
}

func NewPipelineHandler(svc *application.PipelineService, repo storage.Repository, instanceSvc *application.InstanceService, csrf *security.CSRF) *PipelineHandler {
	return &PipelineHandler{
		svc:      svc,
		repo:     repo,
		instance: instanceSvc,
		csrf:     csrf,
	}
}

// WithAudit injects the durable audit logger (ADR 007/010 D6).
func (h *PipelineHandler) WithAudit(logger *audit.AuditLogger) *PipelineHandler {
	h.audit = logger
	return h
}

// WithSessionStore injects the session store used to resolve the
// authenticated user for audit records.
func (h *PipelineHandler) WithSessionStore(sess *security.SessionStore) *PipelineHandler {
	h.sess = sess
	return h
}

func (h *PipelineHandler) modelName(id string) string {
	m, err := h.repo.GetModel(id)
	if err != nil {
		return id
	}
	return m.Name
}

// List handles GET /api/v1/pipelines.
func (h *PipelineHandler) List(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.repo.ListPipelines()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp := make([]*pipelineResponse, 0, len(pipelines))
	for _, p := range pipelines {
		resp = append(resp, newPipelineResponse(p, h.modelName))
	}
	writeJSON(w, http.StatusOK, resp)
}

// Get handles GET /api/v1/pipelines/{id} (pipeline + per-model live status).
func (h *PipelineHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	entry, err := h.repo.GetPipeline(id)
	if err != nil {
		writeError(w, 404, "pipeline not found")
		return
	}

	instances, _ := h.instance.ListInstances(r.Context())
	byModel := make(map[string][]*domain.LaunchInstance)
	for _, inst := range instances {
		byModel[inst.ModelID] = append(byModel[inst.ModelID], inst)
	}

	resp := newPipelineResponse(entry, h.modelName)
	statuses := make([]pipelineModelStatus, 0, len(entry.Models))
	for _, m := range entry.Models {
		st := pipelineModelStatus{
			ModelID:     m.ModelID,
			State:       "stopped",
			AutoStart:   m.AutoStart,
			HasOverride: len(m.Args) > 0,
		}
		for _, inst := range byModel[m.ModelID] {
			if inst.IsActive() {
				st.State = string(inst.State)
				st.InstanceID = string(inst.ID)
				st.PID = inst.PID
				if !inst.StartedAt.IsZero() {
					st.StartedAt = inst.StartedAt.Format(timeFormatRFC3339)
				}
				break
			}
		}
		if st.State == "stopped" {
			for _, inst := range byModel[m.ModelID] {
				if inst.State == domain.InstanceStateOrphan {
					st.State = string(domain.InstanceStateOrphan)
					st.InstanceID = string(inst.ID)
					break
				}
			}
		}
		statuses = append(statuses, st)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": resp, "models": statuses})
}

// Create handles POST /api/v1/pipelines (201 on success).
func (h *PipelineHandler) Create(w http.ResponseWriter, r *http.Request) {
	var entry storage.PipelineEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	entry.ID = ""
	if err := h.svc.CreatePipeline(r.Context(), &entry); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newPipelineResponse(&entry, h.modelName))
}

// Update handles PUT /api/v1/pipelines/{id} (D1.5 integrity rules).
func (h *PipelineHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	var entry storage.PipelineEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	entry.ID = id
	if err := h.svc.UpdatePipeline(r.Context(), &entry); err != nil {
		writeServiceError(w, err)
		return
	}
	saved, err := h.repo.GetPipeline(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newPipelineResponse(saved, h.modelName))
}

// Delete handles DELETE /api/v1/pipelines/{id}.
func (h *PipelineHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	if err := h.svc.DeletePipeline(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Start handles POST /api/v1/pipelines/{id}/start (ADR 010 D3).
func (h *PipelineHandler) Start(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	res, err := h.svc.Start(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventPipelineStart, startAuditDetail(res))
	writeJSON(w, http.StatusOK, res)
}

// Stop handles POST /api/v1/pipelines/{id}/stop (ADR 010 D3, reverse order).
func (h *PipelineHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	res, err := h.svc.Stop(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventPipelineStop, stopAuditDetail(res))
	writeJSON(w, http.StatusOK, res)
}

// Restart handles POST /api/v1/pipelines/{id}/restart (ADR 010 D3).
func (h *PipelineHandler) Restart(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	res, err := h.svc.Restart(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Restart carries the combined set of both phases (ADR 010 D6). The
	// phase-specific counters (started / already_running / orphan_skipped /
	// stopped) keep their distinct keys; the shared `failed` counter is the
	// SUM of the start-phase and stop-phase failures (the two phases cannot
	// overwrite each other's count).
	detail := map[string]string{auditPipelineIDKey: id}
	for _, e := range res.StartResults {
		switch e.Status {
		case application.OutcomeStarted:
			detail["started"] = inc(detail["started"])
		case application.OutcomeAlreadyRunning:
			detail["already_running"] = inc(detail["already_running"])
		case application.OutcomeOrphanSkipped:
			detail["orphan_skipped"] = inc(detail["orphan_skipped"])
		case application.OutcomeFailed:
			detail["failed"] = inc(detail["failed"])
		}
	}
	for _, e := range res.StopResults {
		switch e.Status {
		case application.OutcomeStopped:
			detail["stopped"] = inc(detail["stopped"])
		case application.OutcomeFailed:
			detail["failed"] = inc(detail["failed"])
		}
	}
	logAudit(h.audit, h.sess, r, audit.EventPipelineRestart, detail)
	writeJSON(w, http.StatusOK, res)
}

// ─── Audit detail builders (ADR 010 D6: pipeline_id + bounded counters only) ───

func startAuditDetail(res *application.PipelineStartResult) map[string]string {
	detail := map[string]string{auditPipelineIDKey: res.PipelineID}
	for _, e := range res.Results {
		switch e.Status {
		case application.OutcomeStarted:
			detail["started"] = inc(detail["started"])
		case application.OutcomeAlreadyRunning:
			detail["already_running"] = inc(detail["already_running"])
		case application.OutcomeOrphanSkipped:
			detail["orphan_skipped"] = inc(detail["orphan_skipped"])
		case application.OutcomeFailed:
			detail["failed"] = inc(detail["failed"])
		}
	}
	return detail
}

func stopAuditDetail(res *application.PipelineStopResult) map[string]string {
	detail := map[string]string{auditPipelineIDKey: res.PipelineID}
	for _, e := range res.Results {
		switch e.Status {
		case application.OutcomeStopped:
			detail["stopped"] = inc(detail["stopped"])
		case application.OutcomeFailed:
			detail["failed"] = inc(detail["failed"])
		}
	}
	return detail
}

func inc(v string) string {
	n, _ := strconv.Atoi(v)
	return strconv.Itoa(n + 1)
}

// writeServiceError maps PipelineService APIErrors to the flat
// error/code/details wire contract (API.md).
func writeServiceError(w http.ResponseWriter, err error) {
	var apiErr *apierrors.APIError
	if errors.As(err, &apiErr) {
		status := http.StatusInternalServerError
		switch apiErr.Code {
		case apierrors.CodeBadRequest:
			status = http.StatusBadRequest
		case apierrors.CodeNotFound:
			status = http.StatusNotFound
		case apierrors.CodeConflict:
			status = http.StatusConflict
		}
		writeAPIError(w, status, apiErr)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func pipelineIDFromPath(path string) string {
	const prefix = "/api/v1/pipelines/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if idx := strings.IndexByte(rest, '/'); idx > 0 {
		return rest[:idx]
	}
	return rest
}
