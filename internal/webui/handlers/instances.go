package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/webui/security"
)

// InstancesHandler handles instance-related HTTP requests.
type InstancesHandler struct {
	instanceSvc *application.InstanceService
	csrf        *security.CSRF
}

// NewInstancesHandler creates a new InstancesHandler.
func NewInstancesHandler(instanceSvc *application.InstanceService, csrf *security.CSRF) *InstancesHandler {
	return &InstancesHandler{
		instanceSvc: instanceSvc,
		csrf:        csrf,
	}
}

// List handles GET /api/v1/instances
func (h *InstancesHandler) List(w http.ResponseWriter, r *http.Request) {
	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
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
	writeJSON(w, http.StatusOK, inst)
}

// StartProfile handles POST /api/v1/instances/start
func (h *InstancesHandler) StartProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProfileID string `json:"profile_id"`
		ModelID   string `json:"model_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if body.ProfileID == "" {
		writeError(w, 400, "profile_id is required")
		return
	}
	inst, err := h.instanceSvc.StartProfile(r.Context(), body.ProfileID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inst)
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
	writeJSON(w, http.StatusOK, inst)
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
	status := map[string]any{
		"total_instances": len(instances),
		"active":          activeCount,
		"instances":       instances,
	}
	writeJSON(w, http.StatusOK, status)
}
