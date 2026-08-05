package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/webui/security"
)

// SystemHandler handles system-level HTTP requests.
type SystemHandler struct {
	supervisor *process.Supervisor
	mgr        any // unused placeholder for legacy Manager
	sess       *security.SessionStore
	csrf       *security.CSRF
	insSvc     *application.InstanceService
}

// NewSystemHandler creates a new SystemHandler.
func NewSystemHandler(supervisor *process.Supervisor, sess *security.SessionStore, csrf *security.CSRF, insSvc *application.InstanceService) *SystemHandler {
	return &SystemHandler{
		supervisor: supervisor,
		sess:       sess,
		csrf:       csrf,
		insSvc:     insSvc,
	}
}

// Health handles GET /api/v1/health
func (h *SystemHandler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime": time.Since(time.Now().Add(-time.Since(time.Now()))).String(),
	})
}

// Metrics handles GET /api/v1/metrics
func (h *SystemHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	instances, _ := h.insSvc.ListInstances(r.Context())

	running := 0
	stopped := 0
	for _, inst := range instances {
		if inst.IsActive() {
			running++
		} else {
			stopped++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_instances": len(instances),
		"running":         running,
		"stopped":         stopped,
	})
}

// LogsStream handles GET /api/v1/logs/stream
func (h *SystemHandler) LogsStream(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "SSE logs not yet implemented",
	})
}

// QueryLogs handles GET /api/v1/logs
func (h *SystemHandler) QueryLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":    []any{},
		"message": "Historical logs not yet implemented",
	})
}

// InstanceLogs handles GET /api/v1/instances/{id}/logs
func (h *SystemHandler) InstanceLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance_id": id,
		"logs":        []any{},
	})
}

// InstanceLogStream handles GET /api/v1/instances/{id}/logs/stream
func (h *SystemHandler) InstanceLogStream(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance_id": id,
		"stream":      true,
	})
}

// ServeIndex serves the main UI page.
func (h *SystemHandler) ServeIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

// ServeWebStatic serves static web assets.
func (h *SystemHandler) ServeWebStatic(w http.ResponseWriter, r *http.Request) {
	// Simple static file server for /web/static/*
	path := strings.TrimPrefix(r.URL.Path, "/web/static/")
	if path == "" || path == "/" {
		writeError(w, 404, "not found")
		return
	}
	writeError(w, 200, "static file: "+path)
}

// ServeAPI docs.
func (h *SystemHandler) ServeAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": "0.8",
		"api":     "/api/v1/docs",
	})
}

// AdminUsers handles GET /api/v1/admin/users
func (h *SystemHandler) AdminUsers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"users": []any{},
	})
}

// AdminSessions handles GET /api/v1/admin/sessions
func (h *SystemHandler) AdminSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": []any{},
	})
}

// SessionInfo handles GET /api/v1/session
func (h *SystemHandler) SessionInfo(w http.ResponseWriter, r *http.Request) {
	token, err := security.GetSessionToken(r)
	if err != nil || token == "" {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}

	session, err := h.sess.ValidateSession(token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          session.User,
	})
}
