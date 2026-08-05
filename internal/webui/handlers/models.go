package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

// ModelsHandler handles model-related HTTP requests.
type ModelsHandler struct {
	modelSvc *application.ModelService
	csrf     *security.CSRF
}

// NewModelsHandler creates a new ModelsHandler.
func NewModelsHandler(modelSvc *application.ModelService, csrf *security.CSRF) *ModelsHandler {
	return &ModelsHandler{
		modelSvc: modelSvc,
		csrf:     csrf,
	}
}

// List handles GET /api/v1/models
func (h *ModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	models, err := h.modelSvc.ListModels(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// Get handles GET /api/v1/models/{id}
func (h *ModelsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	m, err := h.modelSvc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, 404, "model not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// Create handles POST /api/v1/models
func (h *ModelsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var entry storage.ModelEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.modelSvc.CreateModel(r.Context(), &entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// Update handles PUT /api/v1/models/{id}
func (h *ModelsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	var entry storage.ModelEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	entry.ID = id
	if err := h.modelSvc.UpdateModel(r.Context(), &entry); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// Delete handles DELETE /api/v1/models/{id}
func (h *ModelsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if id == "" {
		writeError(w, 400, "model ID is required")
		return
	}
	if err := h.modelSvc.DeleteModel(r.Context(), id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
