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

	// Create Supervisor with repository.
	supervisor := process.NewSupervisor(repo)

	app := webui.NewWithSupervisor(cfg, supervisor, legacyMgr, repo)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start periodic health check goroutine.
	go app.RunHealthChecker(ctx)

	if err := app.Run(ctx); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
