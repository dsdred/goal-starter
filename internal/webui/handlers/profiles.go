package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// ProfilesHandler handles profile-related HTTP requests.
type ProfilesHandler struct {
	profileSvc  *application.ProfileService
	instanceSvc *application.InstanceService
	supervisor  *process.Supervisor
	csrf        *security.CSRF
}

// NewProfilesHandler creates a new ProfilesHandler.
func NewProfilesHandler(profileSvc *application.ProfileService, instanceSvc *application.InstanceService, supervisor *process.Supervisor, csrf *security.CSRF) *ProfilesHandler {
	return &ProfilesHandler{
		profileSvc:  profileSvc,
		instanceSvc: instanceSvc,
		supervisor:  supervisor,
		csrf:        csrf,
	}
}

// List handles GET /api/v1/profiles
func (h *ProfilesHandler) List(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.profileSvc.ListProfiles(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

// Get handles GET /api/v1/profiles/{id}
func (h *ProfilesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	p, err := h.profileSvc.GetProfile(r.Context(), id)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Create handles POST /api/v1/profiles
func (h *ProfilesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var entry storage.ProfileEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.profileSvc.CreateProfile(r.Context(), &entry); err != nil {
		if errors.Is(err, apierrors.ErrValidation) {
			writeError(w, 400, "validation failed")
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// Update handles PUT /api/v1/profiles/{id}
func (h *ProfilesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	var entry storage.ProfileEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	entry.ID = id
	if err := h.profileSvc.UpdateProfile(r.Context(), &entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// Delete handles DELETE /api/v1/profiles/{id}
func (h *ProfilesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	if err := h.profileSvc.DeleteProfile(r.Context(), id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Activate handles POST /api/v1/profiles/{id}/activate
func (h *ProfilesHandler) Activate(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/activate")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	if err := h.profileSvc.ActivateProfile(r.Context(), id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "activated"})
}

// Deactivate handles POST /api/v1/profiles/{id}/deactivate
func (h *ProfilesHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/deactivate")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	if err := h.profileSvc.DeactivateProfile(r.Context(), id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}

// Resolve handles POST /api/v1/profiles/{id}/resolve
func (h *ProfilesHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/resolve")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	result, err := h.profileSvc.ResolveWithSupervisor(h.supervisor, id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Start handles POST /api/v1/profiles/{id}/start
func (h *ProfilesHandler) Start(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/start")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}
	inst, err := h.instanceSvc.StartProfile(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// Stop handles POST /api/v1/profiles/{id}/stop
func (h *ProfilesHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/stop")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}

	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for _, inst := range instances {
		if inst.ProfileID == id && inst.IsActive() {
			if err := h.instanceSvc.StopInstance(r.Context(), inst.ID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// Restart handles POST /api/v1/profiles/{id}/restart
func (h *ProfilesHandler) Restart(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/restart")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}

	instances, err := h.instanceSvc.ListInstances(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for _, inst := range instances {
		if inst.ProfileID == id && inst.IsActive() {
			if _, err := h.instanceSvc.RestartInstance(r.Context(), inst.ID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// Status handles GET /api/v1/profiles/{id}/status
func (h *ProfilesHandler) Status(w http.ResponseWriter, r *http.Request) {
	id := profileIDFromActionPath(r.URL.Path, "/status")
	if id == "" {
		writeError(w, 400, "profile ID is required")
		return
	}

	status, err := h.instanceSvc.GetProfileStatus(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// Action handles POST /api/v1/profiles/{id}/action/{action}
func (h *ProfilesHandler) Action(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	parts := strings.SplitN(path, "/action/", 2)
	if len(parts) != 2 {
		writeError(w, 400, "invalid path")
		return
	}
	id, action := parts[0], parts[1]

	switch action {
	case "start":
		inst, err := h.instanceSvc.StartProfile(r.Context(), id)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, inst)
	case "stop":
		instances, _ := h.instanceSvc.ListInstances(r.Context())
		for _, inst := range instances {
			if inst.ProfileID == id && inst.IsActive() {
				if err := h.instanceSvc.StopInstance(r.Context(), inst.ID); err != nil {
					writeError(w, 500, err.Error())
					return
				}
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	case "restart":
		instances, _ := h.instanceSvc.ListInstances(r.Context())
		for _, inst := range instances {
			if inst.ProfileID == id && inst.IsActive() {
				if _, err := h.instanceSvc.RestartInstance(r.Context(), inst.ID); err != nil {
					writeError(w, 500, err.Error())
					return
				}
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	default:
		writeError(w, 400, "unknown action: "+action)
	}
}
