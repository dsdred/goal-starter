package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/security"
)

// InstancesHandler handles instance-related HTTP requests.
type InstancesHandler struct {
	instanceSvc *application.InstanceService
	csrf        *security.CSRF
	sess        *security.SessionStore
	audit       *audit.AuditLogger
}

// NewInstancesHandler creates a new InstancesHandler.
func NewInstancesHandler(instanceSvc *application.InstanceService, csrf *security.CSRF) *InstancesHandler {
	return &InstancesHandler{
		instanceSvc: instanceSvc,
		csrf:        csrf,
	}
}

// WithAudit injects the durable audit logger (ADR 007). A nil logger
// disables audit emission for this handler.
func (h *InstancesHandler) WithAudit(logger *audit.AuditLogger) *InstancesHandler {
	h.audit = logger
	return h
}

// WithSessionStore injects the session store used to resolve the
// authenticated user for audit records.
func (h *InstancesHandler) WithSessionStore(sess *security.SessionStore) *InstancesHandler {
	h.sess = sess
	return h
}

// List handles GET /api/v1/instances
func (h *InstancesHandler) List(w http.ResponseWriter, r *http.Request) {
	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for i := range instances {
		instances[i].Environment = nil
	}
	writeJSON(w, http.StatusOK, instances)
}

// Get handles GET /api/v1/instances/{id}
func (h *InstancesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	inst, err := h.instanceSvc.GetInstanceStatus(r.Context(), domain.InstanceID(id))
	if err != nil {
		writeError(w, 404, "instance not found")
		return
	}
	inst.Environment = nil
	writeJSON(w, http.StatusOK, inst)
}

// StartModel handles POST /api/v1/instances/start
func (h *InstancesHandler) StartModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ModelID string `json:"model_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if body.ModelID == "" {
		writeError(w, 400, "model_id is required")
		return
	}
	inst, err := h.instanceSvc.StartModel(r.Context(), body.ModelID)
	if err != nil {
		// Audited on failure too with a sanitized (bounded) error fragment.
		logAudit(h.audit, h.sess, r, audit.EventInstanceStart, map[string]string{
			"model_id": body.ModelID,
			"error":    sanitizeAuditError(err),
		})
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventInstanceStart, map[string]string{
		"model_id":    body.ModelID,
		"instance_id": string(inst.ID),
	})
	inst.Environment = nil
	writeJSON(w, http.StatusCreated, inst)
}

// sanitizeAuditError bounds the error text recorded in audit Detail:
// identifiers and short diagnostic fragments only, never full request data.
func sanitizeAuditError(err error) string {
	const maxLen = 200
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return msg
}

// StopInstance handles POST /api/v1/instances/{id}/stop
func (h *InstancesHandler) StopInstance(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id = strings.TrimSuffix(id, "/stop")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	if err := h.instanceSvc.StopInstance(r.Context(), domain.InstanceID(id)); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventInstanceStop, map[string]string{"instance_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// RestartInstance handles POST /api/v1/instances/{id}/restart
func (h *InstancesHandler) RestartInstance(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id = strings.TrimSuffix(id, "/restart")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	inst, err := h.instanceSvc.RestartInstance(r.Context(), domain.InstanceID(id))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventInstanceRestart, map[string]string{"instance_id": id})
	inst.Environment = nil
	writeJSON(w, http.StatusOK, inst)
}

// Cleanup handles POST /api/v1/instances/cleanup
func (h *InstancesHandler) Cleanup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode string   `json:"mode"`
		IDs  []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	switch request.Mode {
	case "all_terminal", "older_than_7d", "older_than_30d", "selected":
	default:
		writeError(w, 400, "invalid mode: "+request.Mode)
		return
	}
	if request.Mode == "selected" && len(request.IDs) == 0 {
		writeError(w, 400, "ids is required for selected mode")
		return
	}
	deleted, err := h.instanceSvc.CleanupInstances(r.Context(), request.Mode, request.IDs)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventInstanceCleanup, map[string]string{
		"mode":    request.Mode,
		"deleted": strconv.Itoa(deleted),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "cleaned", "deleted": deleted})
}

// History handles GET /api/v1/history — returns terminal instances from the
// repository (persistent), so records survive GoAl restart.
func (h *InstancesHandler) History(w http.ResponseWriter, r *http.Request) {
	all, err := h.instanceSvc.ListHistory(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for i := range all {
		all[i].Environment = nil
	}
	writeJSON(w, http.StatusOK, all)
}

// Dismiss handles POST /api/v1/instances/{id}/dismiss
// Transitions an orphan instance to stale (reconciled-by-user). No process is touched.
func (h *InstancesHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id = strings.TrimSuffix(id, "/dismiss")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	if err := h.instanceSvc.DismissOrphan(r.Context(), domain.InstanceID(id)); err != nil {
		if strings.Contains(err.Error(), "not in orphan state") {
			writeError(w, 409, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "get instance") {
			writeError(w, 404, "instance not found")
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	logAudit(h.audit, h.sess, r, audit.EventInstanceDismiss, map[string]string{"instance_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

// Status handles GET /api/v1/instances/status
func (h *InstancesHandler) Status(w http.ResponseWriter, r *http.Request) {
	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	activeCount := 0
	for _, inst := range instances {
		if inst.IsActive() {
			activeCount++
		}
	}
	for i := range instances {
		instances[i].Environment = nil
	}
	status := map[string]any{
		"total_instances": len(instances),
		"active":          activeCount,
		"instances":       instances,
	}
	writeJSON(w, http.StatusOK, status)
}
