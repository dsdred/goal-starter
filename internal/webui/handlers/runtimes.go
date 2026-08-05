package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

// RuntimesHandler handles runtime-related HTTP requests.
type RuntimesHandler struct {
	runtimeSvc *application.RuntimeService
	instances  *application.InstanceService
	supervisor *process.Supervisor
	csrf       *security.CSRF
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

// List handles GET /api/v1/runtimes
func (h *RuntimesHandler) List(w http.ResponseWriter, r *http.Request) {
	runtimes, err := h.runtimeSvc.ListRuntimes(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runtimes)
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
	writeJSON(w, http.StatusOK, rt)
}

// Create handles POST /api/v1/runtimes
func (h *RuntimesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var entry storage.RuntimeEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.runtimeSvc.CreateRuntime(r.Context(), &entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// Update handles PUT /api/v1/runtimes/{id}
func (h *RuntimesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	var entry storage.RuntimeEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	entry.ID = id
	if err := h.runtimeSvc.UpdateRuntime(r.Context(), &entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// Delete handles DELETE /api/v1/runtimes/{id}
func (h *RuntimesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	if id == "" {
		writeError(w, 400, "runtime ID is required")
		return
	}
	if err := h.runtimeSvc.DeleteRuntime(r.Context(), id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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

	instances, err := h.instances.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	switch action {
	case "start":
		for _, inst := range instances {
			if inst.RuntimeID == id && inst.IsActive() {
				inst, err := h.instances.StartProfile(r.Context(), inst.ProfileID)
				if err != nil {
					writeError(w, 500, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, inst)
				return
			}
		}
		writeError(w, 404, "no running instance for this runtime")
	case "stop":
		for _, inst := range instances {
			if inst.RuntimeID == id && inst.IsActive() {
				if err := h.instances.StopInstance(r.Context(), inst.ID); err != nil {
					writeError(w, 500, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
				return
			}
		}
		writeError(w, 404, "no running instance for this runtime")
	case "restart":
		for _, inst := range instances {
			if inst.RuntimeID == id && inst.IsActive() {
				if _, err := h.instances.RestartInstance(r.Context(), inst.ID); err != nil {
					writeError(w, 500, err.Error())
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

	health := map[string]any{
		"active_instances":  len(instances),
		"running_instances": len(instances),
		"instances":         instances,
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
		if inst.RuntimeID == id && inst.IsActive() {
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
