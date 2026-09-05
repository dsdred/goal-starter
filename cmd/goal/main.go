package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/version"
	"github.com/dsdred/goal/internal/webui"
)

const (
	serviceDefaultName = "GoAl"
	serviceDisplayName = "GoAl - Local AI Runtime Manager"
	serviceDescription = "GoAl - local AI runtime and model manager: instance lifecycle, Web UI, and audit as a Windows service (ADR 011)."
)

// appRepo and appRepoMu expose the running repository to the SCM Interrogate
// response (ADR 011 D6.4: real state derived from the Supervisor snapshot).
var (
	appRepo   storage.Repository
	appRepoMu sync.RWMutex
)

func main() {
	defaultConfig := os.Getenv("GOAL_CONFIG")
	if defaultConfig == "" {
		defaultConfig = "goal.json"
	}
	configPath := flag.String("config", defaultConfig, "path to configuration file (env: GOAL_CONFIG)")
	showVersion := flag.Bool("version", false, "print version and exit")
	service := flag.String("service", "", "Windows service verb: install, uninstall, start, stop, restart, status, run (Windows only; ADR 011)")
	serviceName := flag.String("service-name", serviceDefaultName, "Windows service name (default GoAl)")
	serviceStart := flag.String("start", "auto", "Windows service start type for install: auto or manual")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Info())
		os.Exit(0)
	}

	if *service != "" {
		os.Exit(serviceMain(*service, *serviceName, *serviceStart, *configPath))
	}

	os.Exit(foregroundMain(*configPath))
}

// foregroundMain is the unchanged foreground entry: the lifecycle context comes
// from OS signals and the application runs the shared lifecycle.
func foregroundMain(configPath string) int {
	appCtx, appStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer appStop()
	if err := runApplication(configPath, appCtx); err != nil {
		return 1
	}
	return 0
}

// serviceMain dispatches the --service verbs (ADR 011 D1). On non-Windows
// platforms every verb returns the bounded "not supported" error.
func serviceMain(verb, name, startType, configPath string) int {
	m := platform.NewServiceManager()
	switch verb {
	case "run":
		return serviceRun(m, name, configPath)
	case "install":
		return serviceInstall(m, name, startType, configPath)
	case "uninstall":
		if err := m.Uninstall(name); err != nil {
			return serviceFail(err)
		}
		fmt.Printf("service %q uninstalled (registration removed; user data untouched)\n", name)
		return 0
	case "start":
		if err := m.Start(name); err != nil {
			return serviceFail(err)
		}
		fmt.Printf("service %q is Running\n", name)
		return 0
	case "stop":
		if err := m.Stop(name); err != nil {
			return serviceFail(err)
		}
		fmt.Printf("service %q is Stopped\n", name)
		return 0
	case "restart":
		if err := m.Restart(name); err != nil {
			return serviceFail(err)
		}
		fmt.Printf("service %q restarted (Stop -> Stopped -> Start -> Running)\n", name)
		return 0
	case "status":
		st, err := m.Status(name)
		if err != nil {
			return serviceFail(err)
		}
		fmt.Printf("service %q: state=%s pid=%d uptime=%s\n", name, st.State, st.PID, st.Uptime)
		return 0
	default:
		return serviceFail(fmt.Errorf("service: unknown verb %q (expected install, uninstall, start, stop, restart, status, run)", verb))
	}
}

func serviceFail(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// serviceRun is the internal SCM entrypoint (ADR 011 D1.2). It is valid only
// under an SCM session; outside one the manager returns a bounded error.
func serviceRun(m platform.ServiceManager, name, configPath string) int {
	addr := ""
	if cfg, err := config.LoadReadOnly(configPath); err == nil {
		addr = net.JoinHostPort(cfg.ListenAddress, strconv.Itoa(cfg.WebPort))
	}
	err := m.RunService(platform.ServiceRunOptions{
		Name:      name,
		ServeAddr: addr,
		RunApp:    func(ctx context.Context) error { return runApplication(configPath, ctx) },
		StatusText: func() string {
			appRepoMu.RLock()
			repo := appRepo
			appRepoMu.RUnlock()
			if repo == nil {
				return "application starting"
			}
			instances, err := repo.ListInstances()
			if err != nil {
				return "instance state unavailable"
			}
			counts := map[string]int{}
			for _, inst := range instances {
				counts[inst.State]++
			}
			parts := make([]string, 0, len(counts))
			for state, n := range counts {
				parts = append(parts, fmt.Sprintf("%s=%d", state, n))
			}
			if len(parts) == 0 {
				return "no instances"
			}
			return "instances: " + strings.Join(parts, ", ")
		},
	})
	if err != nil {
		return serviceFail(err)
	}
	return 0
}

// serviceInstall validates (D3 pre-flight, refuse before register) and then
// registers the service without starting it (D5).
func serviceInstall(m platform.ServiceManager, name, startType, configPath string) int {
	exe, err := os.Executable()
	if err != nil {
		return serviceFail(fmt.Errorf("service: resolve executable: %w", err))
	}
	exe, err = filepath.Abs(filepath.Clean(exe))
	if err != nil {
		return serviceFail(fmt.Errorf("service: resolve executable: %w", err))
	}
	absCfg, problems := serviceInstallPreflight(exe, configPath)
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "service install refused (no registration created, no files written):")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  - "+p)
		}
		return 1
	}
	st := platform.StartTypeAuto
	if startType == "manual" {
		st = platform.StartTypeManual
	}
	req := platform.InstallRequest{
		Name:        name,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		ExePath:     exe,
		ConfigPath:  absCfg,
		StartType:   st,
		StopTimeout: platform.DefaultStopTimeout,
	}
	if err := m.Install(req); err != nil {
		return serviceFail(err)
	}
	fmt.Printf("service %q registered: account LocalSystem, start type %s, stop timeout 45s\n", name, startType)
	fmt.Printf("image: %q\n", serviceImageString(exe, absCfg))
	fmt.Printf("the service was NOT started; start it explicitly: goal --service start --service-name %s\n", name)
	return 0
}

// runApplication executes the one shared application lifecycle used by both
// foreground and service modes (ADR 011 D1.3/D6.1): config load → credential
// migration → ValidateFull → repository init/seed → Recover → ADR 010
// autostart → webui. ctx is the lifecycle context (OS signals in foreground,
// the SCM stop request in service mode). A non-nil return is a failure.
func runApplication(configPath string, ctx context.Context) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		return err
	}

	// Migrate legacy plaintext credentials to bcrypt hash.
	cfg, migrated, err := config.MigrateCredentials(cfg, configPath)
	if err != nil {
		slog.Error("credential migration failed", "error", err)
		return err
	}
	if migrated {
		slog.Info("credential migrated to bcrypt hash")
	}

	// Validate configuration at startup.
	if err := cfg.ValidateFull(); err != nil {
		slog.Error("config validation failed", "error", err)
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		return err
	}

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}

	repoPath := filepath.Join(dataDir, "goal_repo.json")

	// Create unified repository.
	repo, err := storage.NewJSONRepository(repoPath)
	if err != nil {
		slog.Error("init repository", "error", err)
		return err
	}

	appRepoMu.Lock()
	appRepo = repo
	appRepoMu.Unlock()

	// Migrate legacy data if needed.
	oldProfilesPath := filepath.Join(dataDir, "profiles.json")
	oldRuntimesPath := filepath.Join(dataDir, "runtimes.json")

	if _, err := os.Stat(oldProfilesPath); err == nil {
		if _, err := os.Stat(oldRuntimesPath); err == nil {
			if err := storage.MigrateFromOldStores(repo, dataDir, repoPath); err != nil {
				slog.Warn("migration failed (data may already exist)", "error", err)
			}
		}
	}

	// Seed repository from config file (initial runtimes/models/profiles).
	storage.SeedFromConfig(repo, &cfg)

	// Create Supervisor with lifecycle context.
	supervisor := process.NewSupervisorWithContext(ctx, repo)

	// Recover instances from previous runs that were not properly stopped.
	// Marks running/starting/stopping/pending instances as stale.
	if err := supervisor.Recover(context.Background()); err != nil {
		slog.Error("supervisor recovery", "error", err)
		return err
	}

	// Autostart (ADR 010 D4): pipeline autostart runs BEFORE model-level
	// autostart, so a model covered by both mechanisms gets exactly one
	// instance — the pipeline-owned one; model-level autostart then skips
	// it (already-running).
	pipelineSvc := application.NewPipelineService(supervisor, repo)
	autostartPipelines(ctx, repo, pipelineSvc)
	autostartModels(ctx, repo, supervisor)

	// Create and initialize the web UI app.
	app, err := webui.NewApp(&cfg, repo, supervisor)
	if err != nil {
		slog.Error("init webui", "error", err)
		return err
	}

	// Set config path for settings save endpoint.
	app.SetConfigPath(configPath)

	// Initialize route registry.
	app.InitRegistry()

	// Start periodic health check goroutine.
	go app.StartHealthChecker(ctx)

	// Run the application (HTTP server).
	runErr := app.Run(ctx)

	// Gracefully shutdown all instance processes and persist final states.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shutdownErr := supervisor.ShutdownWithPersistence(shutdownCtx)

	// Close the audit log; every event is already fsynced on write (ADR 007).
	if err := app.CloseAudit(); err != nil {
		slog.Warn("close audit log", "error", err)
	}

	if runErr != nil {
		slog.Error("server stopped", "error", runErr)
	}
	if shutdownErr != nil {
		slog.Error("shutdown error", "error", shutdownErr)
	}

	if runErr != nil || shutdownErr != nil {
		if runErr != nil {
			return runErr
		}
		return shutdownErr
	}
	return nil
}

// autostartModels starts all models marked as Active after recovery.
// Order is deterministic (repository order). A failure in one model does not
// block the rest. Each model may have an optional AutostartDelay in seconds.
func autostartModels(ctx context.Context, repo storage.Repository, supervisor *process.Supervisor) {
	models, err := repo.ListModels()
	if err != nil {
		slog.Warn("autostart: list models", "error", err)
		return
	}

	for _, m := range models {
		if !m.Active {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if hasActiveInstance(repo, m.ID) {
			slog.Info("autostart: skipping (active instance exists)", "model", m.Name)
			continue
		}
		if m.AutostartDelay > 0 {
			select {
			case <-time.After(time.Duration(m.AutostartDelay) * time.Second):
			case <-ctx.Done():
				return
			}
		}
		slog.Info("autostart: starting model", "name", m.Name, "id", m.ID)
		domainModel := domain.ModelEntryToDomain(m)
		runtimeEntry, err := repo.GetRuntime(m.RuntimeID)
		if err != nil {
			slog.Error("autostart: runtime not found", "model", m.Name, "error", err)
			continue
		}
		domainRuntime := &domain.Runtime{
			ID:               runtimeEntry.ID,
			Name:             runtimeEntry.Name,
			Executable:       runtimeEntry.Executable,
			WorkingDirectory: runtimeEntry.WorkingDirectory,
			Environment:      runtimeEntry.Environment,
		}
		if _, err := supervisor.Start(ctx, domainModel, domainRuntime, nil, nil); err != nil {
			slog.Error("autostart: start failed", "model", m.Name, "error", err)
		}
	}
}

// autostartPipelines starts entries of Active pipelines after recovery and
// before model-level autostart (ADR 010 D4). Pipelines are processed in
// repository order, entries in list order, sequentially; only entries with
// AutoStart=true are considered. The model-level AutostartDelay is not
// applied on the pipeline path in first scope. Per-entry failures are
// operational logs and never abort the pipeline, the remaining pipelines,
// or startup. Pipeline autostart emits no pipeline.* audit events
// (no user/session context at startup).
func autostartPipelines(ctx context.Context, repo storage.Repository, svc *application.PipelineService) {
	pipelines, err := repo.ListPipelines()
	if err != nil {
		slog.Warn("pipeline autostart: list pipelines", "error", err)
		return
	}
	for _, p := range pipelines {
		if !p.Active {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		res, err := svc.Autostart(ctx, p.ID)
		if err != nil {
			slog.Error("pipeline autostart: pipeline failed", "pipeline", p.ID, "error", err)
			continue
		}
		for _, r := range res.Results {
			if r.Status == application.OutcomeFailed {
				slog.Error("pipeline autostart: entry failed", "pipeline", p.ID, "model", r.ModelID, "reason", r.Error)
			} else {
				slog.Info("pipeline autostart: entry outcome", "pipeline", p.ID, "model", r.ModelID, "status", r.Status)
			}
		}
	}
}

// hasActiveInstance returns true if the model has any instance in a
// non-terminal state (running, starting, stopping, pending) in the repository.
func hasActiveInstance(repo storage.Repository, modelID string) bool {
	instances, err := repo.ListByModelID(modelID)
	if err != nil {
		return false
	}
	for _, inst := range instances {
		switch inst.State {
		case "running", "starting", "stopping", "pending":
			return true
		}
	}
	return false
}
