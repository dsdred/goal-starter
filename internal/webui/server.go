package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/goal/internal/config"
	"github.com/example/goal/internal/domain"
	"github.com/example/goal/internal/process"
	"github.com/example/goal/internal/storage"
	"github.com/example/goal/internal/version"
	"github.com/example/goal/internal/webui/errors"
	"github.com/example/goal/internal/webui/health"
	"github.com/example/goal/internal/webui/metrics"
	"github.com/example/goal/internal/webui/middleware"
	"github.com/example/goal/internal/webui/security"
)

var funcMap = template.FuncMap{
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.Format("2006-01-02 15:04:05")
	},
	"default": func(v any, fallback string) string {
		if v == nil {
			return fallback
		}
		return fmt.Sprintf("%v", v)
	},
}

//go:embed templates/*.html static/*
var assets embed.FS

// App is the main web application struct.
// It owns the composition root, HTTP routes, and templates.
type App struct {
	cfg        config.Config
	mgr        *process.Manager    // Legacy: single process manager (for backward compat)
	supervisor *process.Supervisor // New: multi-instance supervisor
	repo       storage.Repository  // Unified repository
	tpl        *template.Template
	sess       *security.SessionStore
	pass       *security.PasswordStore
	csrf       *security.CSRF
	hc         *health.HealthChecker
	metrics    *metrics.Manager
}

// New creates an App with legacy single-process manager.
func New(cfg config.Config, mgr *process.Manager, repo storage.Repository) *App {
	return NewWithSupervisor(cfg, nil, mgr, repo)
}

// NewWithSupervisor creates an App with a Supervisor for multi-instance support.
func NewWithSupervisor(cfg config.Config, supervisor *process.Supervisor, mgr *process.Manager, repo storage.Repository) *App {
	tpl := template.Must(template.New("").Funcs(funcMap).ParseFS(assets, "templates/*.html"))
	sess := security.NewSessionStore()
	pass := security.NewPasswordStore()
	csrf := security.NewCSRF()
	hc := health.NewHealthChecker()
	met := metrics.NewManager()

	// Initialize default admin user if config has credentials.
	if cfg.AdminUser != "" && cfg.AdminPassword != "" {
		_ = pass.SetPassword(cfg.AdminUser, cfg.AdminPassword)
	}

	app := &App{
		cfg:        cfg,
		supervisor: supervisor,
		mgr:        mgr,
		repo:       repo,
		tpl:        tpl,
		sess:       sess,
		pass:       pass,
		csrf:       csrf,
		hc:         hc,
		metrics:    met,
	}

	return app
}

func (a *App) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// Public endpoints (no auth required).
	mux.HandleFunc("GET /api/v1/health", a.health)
	mux.HandleFunc("GET /api/v1/version", a.versionInfo)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)

	// Authenticated endpoints (session required).
	mux.HandleFunc("GET /api/v1/status", a.requireAuth(a.status))
	mux.HandleFunc("GET /api/v1/logs/stream", a.requireAuth(a.logs))
	mux.HandleFunc("GET /api/v1/logs/query", a.requireAuth(a.logsQuery))
	mux.HandleFunc("GET /api/v1/metrics", a.requireAuth(a.metrics.Handler()))
	mux.HandleFunc("GET /api/v1/instances", a.requireAuth(a.instancesList))
	mux.HandleFunc("GET /api/v1/instances/{id}", a.requireAuth(a.instanceStatus))
	mux.HandleFunc("GET /api/v1/profiles", a.requireAuth(a.profileList))
	mux.HandleFunc("GET /api/v1/profiles/", a.requireAuth(a.profileGet))
	mux.HandleFunc("GET /api/v1/runtimes", a.requireAuth(a.runtimeList))
	mux.HandleFunc("GET /api/v1/runtimes/", a.requireAuth(a.runtimeGet))
	mux.HandleFunc("GET /api/v1/runtimes/health", a.requireAuth(a.runtimeHealthCheck))
	mux.HandleFunc("GET /api/v1/runtimes/health/{id}", a.requireAuth(a.runtimeRuntimeHealth))
	mux.HandleFunc("GET /api/v1/models", a.requireAuth(a.modelList))
	mux.HandleFunc("GET /api/v1/models/", a.requireAuth(a.modelGet))

	// Profile resolve (preview).
	mux.HandleFunc("POST /api/v1/profiles/{id}/resolve", a.requireAuthCSRF(a.profileResolve))

	// Auth endpoints.
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("GET /api/v1/auth/session", a.checkSession)

	// Protected CRUD endpoints (auth + CSRF for mutations).
	mux.HandleFunc("POST /api/v1/profiles", a.requireAuthCSRF(a.profileCreate))
	mux.HandleFunc("PUT /api/v1/profiles/", a.requireAuthCSRF(a.profileUpdate))
	mux.HandleFunc("DELETE /api/v1/profiles/", a.requireAuthCSRF(a.profileDelete))
	mux.HandleFunc("POST /api/v1/profiles/{id}/action/{action}", a.requireAuthCSRF(a.profileAction))
	// Profile process management endpoints.
	mux.HandleFunc("POST /api/v1/profiles/{id}/start", a.requireAuthCSRF(a.profileStart))
	mux.HandleFunc("POST /api/v1/profiles/{id}/stop", a.requireAuthCSRF(a.profileStop))
	mux.HandleFunc("POST /api/v1/profiles/{id}/restart", a.requireAuthCSRF(a.profileRestart))
	mux.HandleFunc("GET /api/v1/profiles/{id}/status", a.requireAuth(a.profileStatus))
	mux.HandleFunc("POST /api/v1/profiles/{id}/activate", a.requireAuthCSRF(a.profileActivate))
	mux.HandleFunc("POST /api/v1/profiles/{id}/deactivate", a.requireAuthCSRF(a.profileDeactivate))
	mux.HandleFunc("POST /api/v1/runtimes", a.requireAuthCSRF(a.runtimeCreate))
	mux.HandleFunc("PUT /api/v1/runtimes/", a.requireAuthCSRF(a.runtimeUpdate))
	mux.HandleFunc("DELETE /api/v1/runtimes/", a.requireAuthCSRF(a.runtimeDelete))
	mux.HandleFunc("POST /api/v1/runtimes/{id}/action/{action}", a.requireAuthCSRF(a.runtimeAction))
	mux.HandleFunc("POST /api/v1/models", a.requireAuthCSRF(a.modelCreate))
	mux.HandleFunc("PUT /api/v1/models/", a.requireAuthCSRF(a.modelUpdate))
	mux.HandleFunc("DELETE /api/v1/models/", a.requireAuthCSRF(a.modelDelete))

	// Index page.
	mux.HandleFunc("GET /", a.requireAuth(a.index))

	// Static files (no auth required for caching).
	mux.Handle("/static/", http.FileServer(http.FS(assets)))

	// Apply middleware chain: logging → rate limit → CSRF → handlers.
	handler := middleware.LoggingMiddleware(mux)
	handler = a.rateLimitHandler(handler)
	handler = a.csrf.Middleware(handler)

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", a.cfg.ListenAddress, a.cfg.WebPort),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// RunHealthChecker starts the periodic health check goroutine.
func (a *App) RunHealthChecker(ctx context.Context) {
	a.startHealthChecker(ctx)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---------- index ----------

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	status := a.mgr.Status()
	profiles, _ := a.repo.ListProfiles()
	runtimes, _ := a.repo.ListRuntimes()
	models, _ := a.repo.ListModels()

	data := map[string]any{
		"Status":   status,
		"Config":   a.cfg,
		"Profiles": profiles,
		"Runtimes": runtimes,
		"Models":   models,
	}
	_ = a.tpl.ExecuteTemplate(w, "index.html", data)
}

// ---------- status ----------

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.mgr.Status())
}

// ---------- version ----------

func (a *App) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   version.Version,
		"gitCommit": version.GitCommit,
		"buildTime": version.BuildTime,
	})
}

// ---------- health ----------

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

// ---------- logs query (filtered + paginated) ----------

func (a *App) logsQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}

	query := process.LogQuery{}

	if stream := r.URL.Query().Get("stream"); stream != "" {
		query.Stream = stream
	}
	if search := r.URL.Query().Get("search"); search != "" {
		query.Search = search
	}
	if from := r.URL.Query().Get("from"); from != "" {
		query.From = from
	}
	if to := r.URL.Query().Get("to"); to != "" {
		query.To = to
	}

	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if page, err := strconv.Atoi(pageStr); err == nil && page > 0 {
			query.Page = page
		}
	}
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if pageSize, err := strconv.Atoi(pageSizeStr); err == nil && pageSize > 0 {
			query.PageSize = pageSize
		}
	}

	logStore := a.mgr.GetLogStore()
	result := logStore.GetLogs(query)

	writeJSON(w, http.StatusOK, result)
}

// ---------- logs SSE ----------

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, cancel := a.mgr.Subscribe()
	defer cancel()
	for {
		select {
		case ev := <-ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// ---------- profiles CRUD ----------

func (a *App) profileList(w http.ResponseWriter, r *http.Request) {
	profiles, err := a.repo.ListProfiles()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (a *App) profileGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	p, err := a.repo.GetProfile(id)
	if err != nil {
		errors.WriteError(w, http.StatusNotFound, errors.ErrProfileNotFound(id))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) profileCreate(w http.ResponseWriter, r *http.Request) {
	var body storage.ProfileEntry
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errors.WriteError(w, http.StatusBadRequest, errors.ErrBadRequest)
		return
	}
	if body.Name == "" {
		errors.WriteError(w, http.StatusBadRequest, errors.NewAPIError(errors.CodeBadRequest, "name is required"))
		return
	}
	if body.RuntimeID == "" {
		errors.WriteError(w, http.StatusBadRequest, errors.NewAPIError(errors.CodeBadRequest, "runtime_id is required"))
		return
	}
	if body.Host == "" || body.Port == 0 {
		errors.WriteError(w, http.StatusBadRequest, errors.NewAPIError(errors.CodeBadRequest, "host and port are required"))
		return
	}

	body.ID = ""
	body.Active = false

	if err := a.repo.CreateProfile(&body); err != nil {
		errors.WriteError(w, http.StatusInternalServerError, errors.NewAPIError(errors.CodeInternalServer, err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, &body)
}

func (a *App) profileUpdate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	var body storage.ProfileEntry
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	body.ID = id

	if err := a.repo.UpdateProfile(&body); err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	writeJSON(w, http.StatusOK, &body)
}

func (a *App) profileDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if err := a.repo.DeleteProfile(id); err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) profileAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	parts := strings.SplitN(path, "/action/", 2)
	if len(parts) != 2 {
		writeError(w, 400, "invalid path: expected /api/v1/profiles/{id}/action/{action}")
		return
	}
	profileID := parts[0]
	action := parts[1]

	var body struct {
		RuntimeID string   `json:"runtime_id"`
		ModelID   string   `json:"model_id"`
		Host      string   `json:"host"`
		Port      int      `json:"port"`
		Args      []string `json:"args"`
	}
	if action == "start" {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	p, err := a.repo.GetProfile(profileID)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch action {
	case "start":
		inst, err := a.startProfile(ctx, p)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "started", "instance_id": string(inst.ID)})

	case "stop":
		instances, _ := a.repo.ListByProfileID(profileID)
		for _, inst := range instances {
			if inst.State == "running" || inst.State == "starting" {
				if err := a.supervisor.Stop(ctx, domain.InstanceID(inst.ID)); err != nil {
					writeError(w, 500, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
				return
			}
		}
		writeError(w, 404, "no active instance for profile")

	case "restart":
		instances, _ := a.repo.ListByProfileID(profileID)
		var targetID domain.InstanceID
		for _, inst := range instances {
			if inst.State == "running" || inst.State == "starting" {
				targetID = domain.InstanceID(inst.ID)
				break
			}
		}
		if targetID != "" {
			if _, err := a.supervisor.Restart(ctx, targetID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
		} else {
			inst, err := a.startProfile(ctx, p)
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "instance_id": string(inst.ID)})
		}

	default:
		writeError(w, 400, "unknown action: "+action)
	}
}

func (a *App) profileResolve(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	p, err := a.repo.GetProfile(id)
	if err != nil {
		errors.WriteError(w, http.StatusNotFound, errors.ErrProfileNotFound(id))
		return
	}

	rte, err := a.repo.GetRuntime(p.RuntimeID)
	if err != nil {
		errors.WriteError(w, http.StatusNotFound, errors.ErrRuntimeNotFound(p.RuntimeID))
		return
	}

	// Resolve model path for preview.
	var modelPath, mmprojPath string
	if p.ModelID != "" {
		mdl, err := a.repo.GetModel(p.ModelID)
		if err == nil {
			modelPath = mdl.Path
			mmprojPath = mdl.MMProj
		}
	}

	// Build command spec (same logic as startProfile).
	spec, err := domain.BuildCommandSpecForPreview(p, rte, modelPath, mmprojPath)
	if err != nil {
		errors.WriteError(w, http.StatusBadRequest, errors.NewAPIError(errors.CodeBadRequest, err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"executable":       spec.Executable,
		"args":             spec.Args,
		"workingDirectory": spec.WorkingDirectory,
		"environmentKeys":  getEnvKeys(spec.Environment),
	})
}

func getEnvKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, ev := range env {
		if k, _, ok := strings.Cut(ev, "="); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func (a *App) profileStart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	p, err := a.repo.GetProfile(id)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	inst, err := a.startProfile(ctx, p)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "instance_id": string(inst.ID)})
}

func (a *App) profileStop(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	instances, _ := a.repo.ListByProfileID(id)
	for _, inst := range instances {
		if inst.State == "running" || inst.State == "starting" {
			if err := a.supervisor.Stop(ctx, domain.InstanceID(inst.ID)); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
			return
		}
	}
	writeError(w, 404, "no active instance for profile")
}

func (a *App) profileRestart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	instances, _ := a.repo.ListByProfileID(id)
	var targetID domain.InstanceID
	for _, inst := range instances {
		if inst.State == "running" || inst.State == "starting" {
			targetID = domain.InstanceID(inst.ID)
			break
		}
	}
	if targetID != "" {
		if _, err := a.supervisor.Restart(ctx, targetID); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	} else {
		p, err := a.repo.GetProfile(id)
		if err != nil {
			writeError(w, 404, "profile not found")
			return
		}
		inst, err := a.startProfile(ctx, p)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "instance_id": string(inst.ID)})
	}
}

func (a *App) profileStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	instances, err := a.repo.ListByProfileID(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	type StatusSummary struct {
		ProfileID  string                       `json:"profile_id"`
		ActiveInst *storage.LaunchInstanceEntry `json:"active_instance,omitempty"`
		Count      int                          `json:"count"`
		Running    int                          `json:"running"`
	}

	summary := StatusSummary{
		ProfileID: id,
		Count:     len(instances),
	}
	for _, inst := range instances {
		if inst.State == "running" || inst.State == "starting" {
			summary.Running++
			summary.ActiveInst = inst
		}
	}

	writeJSON(w, http.StatusOK, summary)
}

func (a *App) profileActivate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	p, err := a.repo.GetProfile(id)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	p.Active = true
	if err := a.repo.UpdateProfile(p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) profileDeactivate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	p, err := a.repo.GetProfile(id)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	p.Active = false
	if err := a.repo.UpdateProfile(p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// startProfile resolves runtime, model, and profile to start a new instance.
func (a *App) startProfile(ctx context.Context, p *storage.ProfileEntry) (*domain.LaunchInstance, error) {
	rte, err := a.repo.GetRuntime(p.RuntimeID)
	if err != nil {
		return nil, fmt.Errorf("runtime not found: %w", err)
	}

	var mdl *domain.Model
	if p.ModelID != "" {
		mdlData, err := a.repo.GetModel(p.ModelID)
		if err == nil {
			mdl = &domain.Model{
				ID:     mdlData.ID,
				Name:   mdlData.Name,
				Path:   mdlData.Path,
				MMProj: mdlData.MMProj,
				Format: mdlData.Format,
			}
		}
	}

	domainProfile := &domain.Profile{
		ID:          p.ID,
		Name:        p.Name,
		RuntimeID:   p.RuntimeID,
		ModelID:     p.ModelID,
		Host:        p.Host,
		Port:        p.Port,
		Args:        p.Args,
		Environment: p.Environment,
		Active:      p.Active,
	}

	domainRuntime := process.RuntimeToDomain(
		rte.ID, rte.Name, rte.Executable, rte.WorkingDirectory,
		rte.DefaultArgs, rte.Environment,
	)

	inst, err := a.supervisor.Start(ctx, domainProfile, domainRuntime, mdl, nil, nil)
	if err != nil {
		return nil, err
	}

	return inst, nil
}

// ---------- runtimes CRUD ----------

func (a *App) runtimeList(w http.ResponseWriter, r *http.Request) {
	runtimes, err := a.repo.ListRuntimes()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runtimes)
}

func (a *App) runtimeGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	rte, err := a.repo.GetRuntime(id)
	if err != nil {
		writeError(w, 404, "runtime not found")
		return
	}
	writeJSON(w, http.StatusOK, rte)
}

func (a *App) runtimeCreate(w http.ResponseWriter, r *http.Request) {
	var body storage.RuntimeEntry
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Name == "" || body.Executable == "" {
		writeError(w, 400, "name and executable are required")
		return
	}
	body.ID = ""
	if err := a.repo.CreateRuntime(&body); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, &body)
}

func (a *App) runtimeUpdate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	var body storage.RuntimeEntry
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	body.ID = id

	if err := a.repo.UpdateRuntime(&body); err != nil {
		writeError(w, 404, "runtime not found")
		return
	}
	writeJSON(w, http.StatusOK, &body)
}

func (a *App) runtimeDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	if err := a.repo.DeleteRuntime(id); err != nil {
		writeError(w, 404, "runtime not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) runtimeAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	parts := strings.SplitN(path, "/action/", 2)
	if len(parts) != 2 {
		writeError(w, 400, "invalid path: expected /api/v1/runtimes/{id}/action/{action}")
		return
	}
	action := parts[1]

	curStatus := a.mgr.Status()
	if curStatus.State == process.StateRunning && action != "stop" {
		writeError(w, 409, "a process is already running")
		return
	}

	switch action {
	case "stop":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.mgr.Stop(ctx); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})

	case "restart":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = a.mgr.Stop(ctx)
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})

	default:
		writeError(w, 400, "unknown action: "+action)
	}
}

// ---------- models CRUD ----------

func (a *App) modelList(w http.ResponseWriter, r *http.Request) {
	models, err := a.repo.ListModels()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (a *App) modelGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	m, err := a.repo.GetModel(id)
	if err != nil {
		writeError(w, 404, "model not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *App) modelCreate(w http.ResponseWriter, r *http.Request) {
	var body storage.ModelEntry
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Name == "" || body.Path == "" {
		writeError(w, 400, "name and path are required")
		return
	}
	body.ID = ""
	if err := a.repo.CreateModel(&body); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, &body)
}

func (a *App) modelUpdate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	var body storage.ModelEntry
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	body.ID = id

	if err := a.repo.UpdateModel(&body); err != nil {
		writeError(w, 404, "model not found")
		return
	}
	writeJSON(w, http.StatusOK, &body)
}

func (a *App) modelDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/models/")
	if err := a.repo.DeleteModel(id); err != nil {
		writeError(w, 404, "model not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- auth endpoints ----------

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if !a.pass.ValidateCredentials(body.Username, body.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	session, err := a.sess.CreateSession(body.Username)
	if err != nil {
		writeError(w, 500, "failed to create session")
		return
	}
	security.SetSessionCookie(w, session.Token)
	newCSRF := a.csrf.RotateToken()
	security.SetCSRFCookie(w, newCSRF)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "csrf_token": newCSRF})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	token, err := security.GetSessionToken(r)
	if err == nil {
		_ = a.sess.DestroySession(token)
	}
	security.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) checkSession(w http.ResponseWriter, r *http.Request) {
	token, err := security.GetSessionToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"authenticated": "false"})
		return
	}
	_, err = a.sess.ValidateSession(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"authenticated": "false"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authenticated": "true"})
}

// ---------- rate limiting ----------

// rateLimiter tracks per-IP request timestamps and enforces a limit within a time window.
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

// newRateLimiter creates a rate limiter WITHOUT a background goroutine.
// Cleanup happens lazily during the allow() check.
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

// allow checks whether the given IP is allowed within the rate limit.
// It lazily cleans up expired entries during each call.
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Lazy cleanup: remove expired entries for all IPs.
	for ipKey, times := range rl.requests {
		var filtered []time.Time
		for _, t := range times {
			if t.After(windowStart) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			delete(rl.requests, ipKey)
		} else {
			rl.requests[ipKey] = filtered
		}
	}

	var times []time.Time
	for _, t := range rl.requests[ip] {
		if t.After(windowStart) {
			times = append(times, t)
		}
	}

	if len(times) >= rl.limit {
		rl.requests[ip] = times
		return false
	}

	rl.requests[ip] = append(times, now)
	return true
}

func (a *App) rateLimitHandler(next http.Handler) http.Handler {
	rl := newRateLimiter(100, 1*time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		if !rl.allow(ip) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		// Enforce body size limit to prevent large JSON body attacks.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
		next.ServeHTTP(w, r)
	})
}

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

// ---------- runtime health checks ----------

func (a *App) runtimeHealthCheck(w http.ResponseWriter, r *http.Request) {
	results := a.hc.CheckAll()
	writeJSON(w, http.StatusOK, results)
}

func (a *App) runtimeRuntimeHealth(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runtimes/")
	id = strings.TrimPrefix(id, "health/")

	result, err := a.hc.CheckRuntime(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) buildRuntimeDefs() []health.RuntimeDef {
	runtimes, err := a.repo.ListRuntimes()
	if err != nil {
		return nil
	}
	defsMap := make(map[string]health.RuntimeDef)
	for _, rt := range runtimes {
		defsMap[rt.ID] = health.RuntimeDef{
			ID:   rt.ID,
			Name: rt.Name,
			Host: "127.0.0.1",
			Port: 0,
		}
	}
	profiles, _ := a.repo.ListProfiles()
	for _, p := range profiles {
		if def, ok := defsMap[p.RuntimeID]; ok {
			defsMap[p.RuntimeID] = health.RuntimeDef{
				ID:   p.RuntimeID,
				Name: def.Name,
				Host: p.Host,
				Port: p.Port,
			}
		}
	}
	defs := make([]health.RuntimeDef, 0, len(defsMap))
	for _, d := range defsMap {
		defs = append(defs, d)
	}
	return defs
}

func (a *App) startHealthChecker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	a.refreshHealthChecks()
	a.performHealthCheck()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshHealthChecks()
			a.performHealthCheck()
		}
	}
}

func (a *App) performHealthCheck() {
	_ = a.hc.CheckAll()
}

func (a *App) refreshHealthChecks() {
	defs := a.buildRuntimeDefs()
	if len(defs) > 0 {
		a.hc.UpdateRuntimes(defs)
	}
}

// ---------- auth middleware ----------

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}
		token, err := security.GetSessionToken(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "missing session")
			return
		}
		_, err = a.sess.ValidateSession(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (a *App) requireAuthCSRF(next http.HandlerFunc) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if err := a.csrf.ValidateRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- instances management ----------

func (a *App) instancesList(w http.ResponseWriter, r *http.Request) {
	instances, err := a.supervisor.List()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, instances)
}

func (a *App) instanceStatus(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id := domain.InstanceID(idStr)
	inst, err := a.supervisor.Status(id)
	if err != nil {
		writeError(w, 404, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (a *App) instanceStop(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id := domain.InstanceID(idStr)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := a.supervisor.Stop(ctx, id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (a *App) instanceRestart(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/")
	id := domain.InstanceID(idStr)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	inst, err := a.supervisor.Restart(ctx, id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "instance_id": string(inst.ID)})
}
