package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/version"
	"github.com/dsdred/goal/internal/webui"
)

func main() {
	defaultConfig := os.Getenv("GOAL_CONFIG")
	if defaultConfig == "" {
		defaultConfig = "goal.json"
	}
	configPath := flag.String("config", defaultConfig, "path to configuration file (env: GOAL_CONFIG)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Info())
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	// Migrate legacy plaintext credentials to bcrypt hash.
	cfg, migrated, err := config.MigrateCredentials(cfg, *configPath)
	if err != nil {
		slog.Error("credential migration failed", "error", err)
		os.Exit(1)
	}
	if migrated {
		slog.Info("credential migrated to bcrypt hash")
	}

	// Validate configuration at startup.
	if err := cfg.ValidateFull(); err != nil {
		slog.Error("config validation failed", "error", err)
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
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
		os.Exit(1)
	}

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

	// Create application-level context for Supervisor lifecycle.
	// All instance processes inherit this context, so HTTP request timeouts
	// do not kill running processes.
	appCtx, appStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer appStop()

	// Create Supervisor with lifecycle context.
	supervisor := process.NewSupervisorWithContext(appCtx, repo)

	// Recover instances from previous runs that were not properly stopped.
	// Marks running/starting/stopping/pending instances as stale.
	if err := supervisor.Recover(context.Background()); err != nil {
		slog.Error("supervisor recovery", "error", err)
		os.Exit(1)
	}

	// Autostart: launch active models after recovery.
	autostartModels(appCtx, repo, supervisor)

	// Create and initialize the web UI app.
	app, err := webui.NewApp(&cfg, repo, supervisor)
	if err != nil {
		slog.Error("init webui", "error", err)
		os.Exit(1)
	}

	// Set config path for settings save endpoint.
	app.SetConfigPath(*configPath)

	// Initialize route registry.
	app.InitRegistry()

	// Start periodic health check goroutine.
	go app.StartHealthChecker(appCtx)

	// Run the application (HTTP server).
	runErr := app.Run(appCtx)

	// Gracefully shutdown all instance processes and persist final states.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shutdownErr := supervisor.ShutdownWithPersistence(shutdownCtx)

	if runErr != nil {
		slog.Error("server stopped", "error", runErr)
	}
	if shutdownErr != nil {
		slog.Error("shutdown error", "error", shutdownErr)
	}

	if runErr != nil || shutdownErr != nil {
		os.Exit(1)
	}
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
