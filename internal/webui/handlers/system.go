package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/version"
	"github.com/dsdred/goal/internal/webui/security"
)

// logHistoryWire is the SSE wire format for replayed history events.
type logHistoryWire struct {
	Sequence   uint64    `json:"sequence"`
	Timestamp  time.Time `json:"time"`
	InstanceID string    `json:"instance_id,omitempty"`
	Stream     string    `json:"stream"`
	Message    string    `json:"message"`
}

// SystemHandlerOption configures the SystemHandler.
type SystemHandlerOption func(*SystemHandler)

// WithHeartbeat sets the SSE heartbeat interval.
// A zero value uses the default 30-second interval.
func WithHeartbeat(d time.Duration) SystemHandlerOption {
	return func(h *SystemHandler) {
		h.heartbeat = d
	}
}

// SystemHandler handles system-level HTTP requests.
type SystemHandler struct {
	supervisor *process.Supervisor
	mgr        any // unused placeholder for legacy Manager
	sess       *security.SessionStore
	csrf       *security.CSRF
	insSvc     *application.InstanceService

	tmplFS    fs.FS
	tmplOnce  sync.Once
	tmpl      *template.Template
	tmplErr   error
	heartbeat time.Duration
	startedAt time.Time

	listenAddr  string
	webPort     int
	authEnabled bool
	configPath  string
	passStore   *security.PasswordStore
}

// NewSystemHandler creates a new SystemHandler.
func NewSystemHandler(supervisor *process.Supervisor, sess *security.SessionStore, csrf *security.CSRF, insSvc *application.InstanceService, opts ...SystemHandlerOption) *SystemHandler {
	h := &SystemHandler{
		supervisor: supervisor,
		sess:       sess,
		csrf:       csrf,
		insSvc:     insSvc,
		startedAt:  time.Now(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithTemplateFS injects the embedded filesystem for templates.
func (h *SystemHandler) WithTemplateFS(fsys fs.FS) *SystemHandler {
	h.tmplFS = fsys
	return h
}

// Health handles GET /api/v1/health
func (h *SystemHandler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime": time.Since(h.startedAt).Truncate(time.Millisecond).String(),
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

	resp := map[string]any{
		"total_instances": len(instances),
		"running":         running,
		"stopped":         stopped,
	}
	if h.listenAddr != "" {
		resp["listen_address"] = h.listenAddr
		resp["web_port"] = h.webPort
		resp["auth_enabled"] = h.authEnabled
		if user, passwordSet := h.adminConfigFields(); h.configPath != "" {
			resp["admin_user"] = user
			resp["admin_password_set"] = passwordSet
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// adminConfigFields returns the configured admin username and whether a
// non-empty admin password is stored on disk. It never returns the password
// or its hash — only a boolean signal so the client can offer a
// "leave empty to keep the current password" workflow.
func (h *SystemHandler) adminConfigFields() (user string, passwordSet bool) {
	if h.configPath == "" {
		return "", false
	}
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		return "", false
	}
	var c struct {
		AdminUser         string `json:"adminUser"`
		AdminPasswordHash string `json:"adminPasswordHash"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	return c.AdminUser, c.AdminPasswordHash != ""
}

// isValidListenAddress checks if the address is a valid bind address without
// performing a port bind. Accepts IPv4, IPv6, "*", or a basic hostname.
func isValidListenAddress(addr string) bool {
	if addr == "*" {
		return true
	}
	if ip := net.ParseIP(addr); ip != nil {
		return true
	}
	if len(addr) > 253 {
		return false
	}
	for i, c := range addr {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' || c == ':' {
			return false
		}
		if i == 0 && c == '-' {
			return false
		}
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '.' && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// SaveSettings handles PUT /api/v1/settings — updates server configuration.
func (h *SystemHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if h.configPath == "" {
		writeError(w, 500, "config path not available")
		return
	}
	var body struct {
		ListenAddress string `json:"listen_address"`
		WebPort       int    `json:"web_port"`
		AuthEnabled   bool   `json:"auth_enabled"`
		AdminUser     string `json:"admin_user"`
		AdminPassword string `json:"admin_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if body.ListenAddress == "" {
		writeError(w, 400, "listen_address is required")
		return
	}
	if !isValidListenAddress(body.ListenAddress) {
		writeError(w, 400, "listen_address is not a valid bind address")
		return
	}
	if body.WebPort < 1 || body.WebPort > 65535 {
		writeError(w, 400, "web_port must be 1-65535")
		return
	}
	cfg, err := config.Load(h.configPath)
	if err != nil {
		writeError(w, 500, "failed to load config: "+err.Error())
		return
	}
	prevListen := cfg.ListenAddress
	prevPort := cfg.WebPort
	cfg.ListenAddress = body.ListenAddress
	cfg.WebPort = body.WebPort
	cfg.AuthEnabled = body.AuthEnabled
	if body.AdminUser != "" {
		cfg.AdminUser = body.AdminUser
	}
	if body.AdminPassword != "" {
		if len([]byte(body.AdminPassword)) > config.MaxPasswordLength {
			writeError(w, 400, "password must not exceed 72 bytes")
			return
		}
		hash, err := config.HashPassword(body.AdminPassword)
		if err != nil {
			writeError(w, 500, "failed to hash password")
			return
		}
		cfg.AdminPasswordHash = hash
		cfg.AdminPassword = ""
	}
	if cfg.AuthEnabled && cfg.AdminUser == "" {
		writeError(w, 400, "cannot enable auth: adminUser must be configured")
		return
	}
	if cfg.AuthEnabled && cfg.AdminPasswordHash == "" {
		writeError(w, 400, "cannot enable auth: adminPassword must be configured")
		return
	}
	if err := config.Save(h.configPath, cfg); err != nil {
		writeError(w, 500, "failed to save config: "+err.Error())
		return
	}
	if body.AdminPassword != "" && h.passStore != nil {
		_ = h.passStore.SetHash(cfg.AdminUser, cfg.AdminPasswordHash)
	}
	slog.Info("settings saved", "listen", body.ListenAddress, "port", body.WebPort, "auth", body.AuthEnabled)
	hint := "restart_required"
	if body.AdminPassword != "" && body.ListenAddress == prevListen && body.WebPort == prevPort {
		hint = "ok"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved", "hint": hint})
}

// LogsStream handles GET /api/v1/logs/stream
func (h *SystemHandler) LogsStream(w http.ResponseWriter, r *http.Request) {
	h.serveLogStream(w, r, "")
}

// QueryLogs handles GET /api/v1/logs
func (h *SystemHandler) QueryLogs(w http.ResponseWriter, r *http.Request) {
	q, err := parseLogQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	result := h.supervisor.QueryLogs(q, instanceID)
	writeJSON(w, http.StatusOK, result)
}

// InstanceLogs handles GET /api/v1/instances/{id}/logs
func (h *SystemHandler) InstanceLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id = strings.TrimSuffix(id, "/logs")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	q, err := parseLogQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result := h.supervisor.QueryLogs(q, id)
	writeJSON(w, http.StatusOK, result)
}

// InstanceLogStream handles GET /api/v1/instances/{id}/logs/stream
func (h *SystemHandler) InstanceLogStream(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id = strings.TrimSuffix(id, "/logs/stream")
	if id == "" {
		writeError(w, 400, "instance ID is required")
		return
	}
	h.serveLogStream(w, r, id)
}

// serveLogStream implements SSE log streaming via the Supervisor's LogBroker.
// The handler exits when the request context is cancelled or when the broker
// subscription is closed (via Cancel or broker Shutdown).
func (h *SystemHandler) serveLogStream(w http.ResponseWriter, r *http.Request, instanceID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Replay stored history before entering the live stream.
	history := h.supervisor.QueryLogs(process.LogQuery{Page: 1, PageSize: 1000}, instanceID)
	for _, ev := range history.Items {
		wire := logHistoryWire{
			Sequence:   ev.Sequence,
			Timestamp:  ev.Time,
			InstanceID: instanceID,
			Stream:     ev.Stream,
			Message:    ev.Message,
		}
		data, err := json.Marshal(wire)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Sequence, data)
	}
	flusher.Flush()

	sub := h.supervisor.SubscribeLogs(instanceID)
	if sub == nil {
		return
	}
	defer sub.Cancel()

	interval := h.heartbeat
	if interval == 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctxDone := r.Context().Done()
	subDone := sub.Done()

	// Check sub done first so that a closed subscription terminates the loop
	// even if the request context is not yet cancelled (or is already done).
	// If either fires, we must not write further SSE frames.
	for {
		select {
		case <-ctxDone:
			return
		case <-subDone:
			return
		default:
			// Both active; proceed to event wait.
		}

		select {
		case <-ctxDone:
			return
		case <-subDone:
			return
		case ev := <-sub.Channel():
			if ev.Message == "" && ev.Timestamp.IsZero() {
				continue // channel closed sentinel
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Sequence, data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// parseLogQuery parses HTTP query parameters into a process.LogQuery.
func parseLogQuery(r *http.Request) (process.LogQuery, error) {
	q := process.LogQuery{
		LogFilter: process.LogFilter{
			Stream:   r.URL.Query().Get("stream"),
			Search:   r.URL.Query().Get("search"),
			MinLevel: r.URL.Query().Get("min_level"),
			From:     r.URL.Query().Get("from"),
			To:       r.URL.Query().Get("to"),
		},
	}
	if v := r.URL.Query().Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return q, fmt.Errorf("invalid page: %v", err)
		}
		q.Page = n
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return q, fmt.Errorf("invalid page_size: %v", err)
		}
		q.PageSize = n
	}
	return q, nil
}

// ServeIndex serves the main UI page.
func (h *SystemHandler) ServeIndex(w http.ResponseWriter, r *http.Request) {
	tmpl, err := h.indexTemplate()
	if err != nil {
		slog.Error("render index template", "error", err)
		writeError(w, http.StatusInternalServerError, "template error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := tmpl.Execute(w, map[string]any{
		"Runtimes": []string{},
		"Models":   []string{},
		"Config":   map[string]any{},
		"Status":   map[string]any{},
	}); err != nil {
		slog.Error("execute index template", "error", err)
	}
}

// indexTemplate lazily parses the embedded index.html template.
func (h *SystemHandler) indexTemplate() (*template.Template, error) {
	h.tmplOnce.Do(func() {
		if h.tmplFS == nil {
			h.tmplErr = fs.ErrNotExist
			return
		}
		content, err := fs.ReadFile(h.tmplFS, "templates/index.html")
		if err != nil {
			h.tmplErr = err
			return
		}
		h.tmpl, h.tmplErr = template.New("index.html").Funcs(template.FuncMap{
			"default": func(def, val any) any {
				if val == nil || val == "" {
					return def
				}
				return val
			},
			"formatTime": func(v any) string {
				switch t := v.(type) {
				case time.Time:
					if t.IsZero() {
						return "-"
					}
					return t.Format(time.RFC3339)
				case string:
					return t
				default:
					return "-"
				}
			},
		}).Parse(string(content))
	})
	return h.tmpl, h.tmplErr
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

// Version handles GET /api/v1/version
func (h *SystemHandler) Version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, versionMap())
}

func versionMap() map[string]any {
	return map[string]any{
		"version":   version.Version,
		"gitCommit": version.GitCommit,
		"buildTime": version.BuildTime,
		"goVersion": runtime.Version(),
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
	}
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
