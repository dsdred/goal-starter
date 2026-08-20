package webui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // register hooks in default server
	"runtime"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/version"
	"github.com/dsdred/goal/internal/webui/handlers"
	"github.com/dsdred/goal/internal/webui/health"
	"github.com/dsdred/goal/internal/webui/security"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// App holds server dependencies and state.
type App struct {
	cfg           *config.Config
	supervisor    *process.Supervisor
	instanceSvc   *application.InstanceService
	runtimeSvc    *application.RuntimeService
	modelSvc      *application.ModelService
	repo          storage.Repository
	csrf          *security.CSRF
	passwordStore *security.PasswordStore
	sessionStore  *security.SessionStore
	hc            *health.HealthChecker
	reg           *handlers.RouteRegistry
	authEnabled   bool
}

// NewApp creates server dependencies.
func NewApp(cfg *config.Config, repo storage.Repository, supervisor *process.Supervisor) (*App, error) {
	instanceSvc := application.NewInstanceService(supervisor, repo)
	runtimeSvc := application.NewRuntimeService(repo)
	modelSvc := application.NewModelService(repo)

	a := &App{
		cfg:           cfg,
		supervisor:    supervisor,
		instanceSvc:   instanceSvc,
		runtimeSvc:    runtimeSvc,
		modelSvc:      modelSvc,
		repo:          repo,
		csrf:          security.NewCSRF(),
		passwordStore: security.NewPasswordStore(),
		sessionStore:  security.NewSessionStore(),
		authEnabled:   cfg.AuthEnabled,
	}

	// Set admin password from config.
	if cfg.AdminPassword != "" {
		if err := a.passwordStore.SetPassword(cfg.AdminUser, cfg.AdminPassword); err != nil {
			return nil, fmt.Errorf("set admin password: %w", err)
		}
	}

	// Init health checker.
	a.hc = health.NewHealthChecker()
	a.hc.UpdateRuntimes(a.buildRuntimeDefs())

	return a, nil
}

// Router returns the HTTP handler using the new RouteRegistry architecture.
func (a *App) Router() http.Handler {
	if a.reg != nil {
		return a.reg.Build()
	}
	return http.NotFoundHandler()
}

// InitRegistry creates the route registry.
func (a *App) InitRegistry() {
	a.reg = handlers.NewRouteRegistry(
		a.instanceSvc,
		a.runtimeSvc,
		a.modelSvc,
		a.supervisor,
		a.repo,
		a.csrf,
		a.sessionStore,
		a.passwordStore,
		handlers.WithAuthEnabled(a.authEnabled),
		handlers.WithWebAssets(templateFS, staticFS),
		handlers.WithServerInfo(a.cfg.ListenAddress, a.cfg.WebPort, a.authEnabled),
	)
}

// StartHealthChecker starts background health checks.
func (a *App) StartHealthChecker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	a.refreshHealthChecks()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshHealthChecks()
		}
	}
}

func (a *App) buildRuntimeDefs() []health.RuntimeDef {
	runtimes, err := a.repo.ListRuntimes()
	if err != nil {
		return nil
	}
	rtNames := make(map[string]string)
	for _, rt := range runtimes {
		rtNames[rt.ID] = rt.Name
	}
	models, _ := a.repo.ListModels()
	defsMap := make(map[string]health.RuntimeDef)
	for _, m := range models {
		host, port := extractHostPort(m.Args)
		if port == 0 {
			continue
		}
		name, ok := rtNames[m.RuntimeID]
		if !ok {
			name = m.RuntimeID
		}
		defsMap[m.RuntimeID] = health.RuntimeDef{
			ID:   m.RuntimeID,
			Name: name,
			Host: host,
			Port: port,
		}
	}
	defs := make([]health.RuntimeDef, 0, len(defsMap))
	for _, d := range defsMap {
		defs = append(defs, d)
	}
	return defs
}

func extractHostPort(args []string) (string, int) {
	host := "127.0.0.1"
	port := 0
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--host":
			host = args[i+1]
		case "--port":
			fmt.Sscanf(args[i+1], "%d", &port)
		}
	}
	return host, port
}

func (a *App) refreshHealthChecks() {
	defs := a.buildRuntimeDefs()
	if len(defs) > 0 {
		a.hc.UpdateRuntimes(defs)
	}
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (a *App) Run(ctx context.Context) error {
	if a.reg == nil {
		return fmt.Errorf("route registry not initialized")
	}

	addr := fmt.Sprintf("%s:%d", a.cfg.ListenAddress, a.cfg.WebPort)

	server := &http.Server{
		Addr:              addr,
		Handler:           a.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErr := make(chan error, 1)
	// Start server in goroutine.
	go func() {
		slog.Info("starting HTTP server", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			serverErr <- err
		}
	}()

	// Wait for shutdown signal or a startup/serve failure.
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		return fmt.Errorf("serve HTTP: %w", err)
	}

	slog.Info("shutting down HTTP server...")

	// Shutdown server gracefully.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

// Shutdown gracefully shuts down the HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	slog.Info("shutting down HTTP server...")
	return nil
}

// VersionInfo returns version information.
func VersionInfo() map[string]interface{} {
	return map[string]interface{}{
		"version":   version.Version,
		"gitCommit": version.GitCommit,
		"buildTime": version.BuildTime,
		"goVersion": runtime.Version(),
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
	}
}

func staticEmbedded() []string {
	entries, _ := fs.Glob(staticFS, "**")
	return entries
}

// ServeMetrics returns Prometheus-compatible metrics.
func ServeMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# HELP go_memstats_alloc_bytes Current number of bytes allocated in the application.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_alloc_bytes gauge\n")
	fmt.Fprintf(w, "go_memstats_alloc_bytes %d\n", m.Alloc)
	fmt.Fprintf(w, "# HELP go_memstats_total_alloc Cumulative bytes allocated.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_total_alloc gauge\n")
	fmt.Fprintf(w, "go_memstats_total_alloc %d\n", m.TotalAlloc)
}
