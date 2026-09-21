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
	"github.com/dsdred/goal/internal/process"
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
	ID        string   `json:"id,omitempty"`
	ModelID   string   `json:"model_id"`
	ModelName string   `json:"model_name"`
	Args      []string `json:"args,omitempty"`
	AutoStart bool     `json:"auto_start"`
}

func newPipelineResponse(e *storage.PipelineEntry, modelName func(id string) string) *pipelineResponse {
	models := make([]pipelineModelResponse, len(e.Models))
	for i, m := range e.Models {
		models[i] = pipelineModelResponse{
			ID:        m.ID,
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

// pipelineModelStatus is the per-entry live status in the detail endpoint
// (ADR 013 D5). Resolved by (pipeline_id, pipeline_entry_id) with the D4
// legacy fallback: a pipeline-owned instance without an entry attribution
// (pre-upgrade) belongs to the first entry of that model in list order.
type pipelineModelStatus struct {
	ModelID     string `json:"model_id"`
	EntryID     string `json:"entry_id,omitempty"`
	Index       int    `json:"index"`
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
	byEntry := make(map[string][]*domain.LaunchInstance)
	byLegacyModel := make(map[string][]*domain.LaunchInstance)
	for _, inst := range instances {
		if inst.PipelineID != entry.ID {
			continue
		}
		if inst.PipelineEntryID != "" {
			byEntry[inst.PipelineEntryID] = append(byEntry[inst.PipelineEntryID], inst)
		} else {
			byLegacyModel[inst.ModelID] = append(byLegacyModel[inst.ModelID], inst)
		}
	}
	firstOfModel := make(map[string]int, len(entry.Models))
	for i, m := range entry.Models {
		if _, ok := firstOfModel[m.ModelID]; !ok {
			firstOfModel[m.ModelID] = i
		}
	}

	resp := newPipelineResponse(entry, h.modelName)
	statuses := make([]pipelineModelStatus, 0, len(entry.Models))
	consumedLegacy := make(map[string]bool)
	for i, m := range entry.Models {
		st := pipelineModelStatus{
			ModelID:     m.ModelID,
			EntryID:     m.ID,
			Index:       i,
			State:       "stopped",
			AutoStart:   m.AutoStart,
			HasOverride: len(m.Args) > 0,
		}
		list := byEntry[m.ID]
		if len(list) == 0 && firstOfModel[m.ModelID] == i && !consumedLegacy[m.ModelID] {
			list = byLegacyModel[m.ModelID]
			consumedLegacy[m.ModelID] = true
		}
		for _, inst := range list {
			if inst.IsRunningOrStarting() {
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
			for _, inst := range list {
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
	logAudit(h.audit, h.sess, r, audit.EventPipelineCreate, map[string]string{
		"id":      entry.ID,
		"entries": strconv.Itoa(len(entry.Models)),
	})
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
	var prev *storage.PipelineEntry
	if p, err := h.repo.GetPipeline(id); err == nil {
		prev = p
	}
	if err := h.svc.UpdatePipeline(r.Context(), &entry); err != nil {
		writeServiceError(w, err)
		return
	}
	// Audit: changed field names only, never values (ADR 007 §2/§5,
	// settings.saved precedent). Entry Args values are compared in memory
	// but never recorded.
	if prev != nil {
		changed := map[string]string{"id": id}
		if prev.Name != entry.Name {
			changed["name"] = "changed"
		}
		if prev.Active != entry.Active {
			changed["active"] = "changed"
		}
		if pipelineModelsChanged(prev.Models, entry.Models) {
			changed["models"] = "changed"
		}
		if len(changed) > 1 {
			logAudit(h.audit, h.sess, r, audit.EventPipelineUpdate, changed)
		}
	}
	saved, err := h.repo.GetPipeline(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newPipelineResponse(saved, h.modelName))
}

// pipelineModelsChanged reports whether two pipeline entry lists differ in
// structure or content (entry sequence, model references, Args, AutoStart).
// Used only for the bounded "models":"changed" audit marker — values are
// compared in memory and never recorded.
func pipelineModelsChanged(a, b []storage.PipelineModel) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].ModelID != b[i].ModelID || a[i].AutoStart != b[i].AutoStart {
			return true
		}
		if len(a[i].Args) != len(b[i].Args) {
			return true
		}
		for j := range a[i].Args {
			if a[i].Args[j] != b[i].Args[j] {
				return true
			}
		}
	}
	return false
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
	logAudit(h.audit, h.sess, r, audit.EventPipelineDelete, map[string]string{"id": id})
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
		var agg *application.PipelineStartError
		if errors.As(err, &agg) {
			apiErr := groupFailureClass(agg.Phase, err)
			logPipelineFailure(h, r, audit.EventPipelineStart, startAuditDetail(res), apiErr)
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

// Stop handles POST /api/v1/pipelines/{id}/stop (ADR 010 D3, reverse order).
func (h *PipelineHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	res, err := h.svc.Stop(r.Context(), id)
	if err != nil {
		var agg *application.PipelineStopError
		if errors.As(err, &agg) {
			apiErr := groupFailureClass(agg.Phase, err)
			logPipelineFailure(h, r, audit.EventPipelineStop, stopAuditDetail(res), apiErr)
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

// Restart handles POST /api/v1/pipelines/{id}/restart (ADR 010 D3).
func (h *PipelineHandler) Restart(w http.ResponseWriter, r *http.Request) {
	id := pipelineIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, 400, "pipeline ID is required")
		return
	}
	res, err := h.svc.Restart(r.Context(), id)
	if err != nil {
		var agg *application.PipelineStopError
		if errors.As(err, &agg) {
			apiErr := groupFailureClass(agg.Phase, err)
			logPipelineFailure(h, r, audit.EventPipelineRestart, restartAuditDetail(id, res), apiErr)
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

// restartAuditDetail carries the combined set of both phases (ADR 010 D6). The
// phase-specific counters (started / already_running / orphan_skipped /
// stopped) keep their distinct keys; the shared `failed` counter is the SUM of
// the start-phase and stop-phase failures (the two phases cannot overwrite
// each other's count).
func restartAuditDetail(id string, res *application.PipelineRestartResult) map[string]string {
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
	return detail
}

// logPipelineFailure audits and writes an incomplete group lifecycle request
// (BF-02a). The application returned the per-entry body and failure metadata
// only; this layer decides the status, the client token, and how the failures
// are rendered into the body. The audit event keeps the same bounded counters as
// the success event plus the error class (ADR 010 D6: no instance ids, no raw
// error text).
func logPipelineFailure(h *PipelineHandler, r *http.Request, event string, detail map[string]string, apiErr *apierrors.APIError) {
	detail["error"] = apiErr.Message
	logAudit(h.audit, h.sess, r, event, detail)
}

// groupFailureClass classifies an incomplete group lifecycle request into the
// flat error contract. Precedence matches the single-target lifecycle mapping:
// the server-side class outranks the retry-later shutdown class, which
// outranks a caller-visible conflict.
func groupFailureClass(phase string, err error) *apierrors.APIError {
	switch {
	case errors.Is(err, process.ErrPersistenceFailure),
		errors.Is(err, process.ErrTerminationUnconfirmed),
		errors.Is(err, process.ErrRollbackFailed):
		return apierrors.NewAPIError(apierrors.CodeInternalServer, incompleteGroupToken(phase))
	case errors.Is(err, process.ErrLaunchAbortedByShutdown),
		process.HasRejectionReason(err, process.RejShuttingDown):
		return apierrors.NewAPIError(apierrors.CodeServiceUnavailable, tokenShuttingDown)
	default:
		return apierrors.NewAPIError(apierrors.CodeConflict, incompleteGroupToken(phase))
	}
}

func incompleteGroupToken(phase string) string {
	switch phase {
	case application.PhaseStop:
		return "pipeline_stop_incomplete"
	case application.PhaseRestart:
		return "pipeline_restart_incomplete"
	default:
		return "pipeline_start_failed"
	}
}

// ─── Wire bodies of an incomplete group lifecycle request ───
//
// They are the 200 body plus the flat code/error keys, so a caller can see
// which instances DID stop. The embedded result supplies pipeline_id and the
// start rows; the shadowing fields replace the stop rows with the failure-
// attributed ones (depth-0 fields win over the embedded ones).

type pipelineInstanceFailureRow struct {
	InstanceID string `json:"instance_id"`
	Reason     string `json:"reason"`
}

type pipelineStopEntryRow struct {
	application.PipelineEntryStop
	Failures []pipelineInstanceFailureRow `json:"failures,omitempty"`
}

type pipelineStopBody struct {
	application.PipelineStopResult
	Results []pipelineStopEntryRow `json:"results"`
	Code    string                 `json:"code,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

type pipelineRestartBody struct {
	application.PipelineRestartResult
	StopResults []pipelineStopEntryRow `json:"stop_results"`
	Code        string                 `json:"code,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

type pipelineStartBody struct {
	application.PipelineStartResult
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// stopFailureRows attaches the stop-phase failures to their entry rows without
// reordering or dropping any row (the aggregate is keyed by entry index).
func stopFailureRows(entries []application.PipelineEntryStop, failures []application.PipelineFailure) []pipelineStopEntryRow {
	byIndex := make(map[int][]pipelineInstanceFailureRow)
	for _, f := range failures {
		if f.Phase != application.PhaseStop {
			continue
		}
		byIndex[f.Index] = append(byIndex[f.Index], pipelineInstanceFailureRow{
			InstanceID: f.InstanceID, Reason: string(f.Reason),
		})
	}
	rows := make([]pipelineStopEntryRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, pipelineStopEntryRow{PipelineEntryStop: e, Failures: byIndex[e.Index]})
	}
	return rows
}

func inc(v string) string {
	n, _ := strconv.Atoi(v)
	return strconv.Itoa(n + 1)
}

// writeServiceError maps PipelineService errors to the flat
// error/code/details wire contract (API.md).
func writeServiceError(w http.ResponseWriter, err error) {
	var apiErr *apierrors.APIError
	if errors.As(err, &apiErr) {
		writeAPIError(w, statusForAPICode(apiErr.Code), apiErr)
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
