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

	"github.com/example/goal/internal/config"
	"github.com/example/goal/internal/process"
	"github.com/example/goal/internal/storage"
	"github.com/example/goal/internal/version"
	"github.com/example/goal/internal/webui"
)

func main() {
	configPath := flag.String("config", "goal.json", "path to configuration file")
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

	// Create legacy Manager for backward compatibility (health checks, logs).
	legacyMgr := process.NewManager()

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

	app := webui.NewWithSupervisor(cfg, supervisor, legacyMgr, repo)

	// Start periodic health check goroutine.
	go app.RunHealthChecker(appCtx)

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
