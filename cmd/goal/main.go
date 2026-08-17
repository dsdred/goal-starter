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

	// Autostart: launch active profiles after recovery.
	autostartProfiles(appCtx, repo, supervisor)

	// Create and initialize the web UI app.
	app, err := webui.NewApp(&cfg, repo, supervisor)
	if err != nil {
		slog.Error("init webui", "error", err)
		os.Exit(1)
	}

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

// autostartProfiles starts all profiles marked as Active after recovery.
// Order is deterministic (repository order). A failure in one profile does not
// block the rest. Each profile may have an optional AutostartDelay in seconds.
func autostartProfiles(ctx context.Context, repo storage.Repository, supervisor *process.Supervisor) {
	profiles, err := repo.ListProfiles()
	if err != nil {
		slog.Warn("autostart: list profiles", "error", err)
		return
	}

	for _, p := range profiles {
		if !p.Active {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		// Duplicate guard: if a non-terminal instance already exists for this
		// profile (e.g., started earlier in this session), skip.
		if hasActiveInstance(repo, p.ID) {
			slog.Info("autostart: skipping (active instance exists)", "profile", p.Name)
			continue
		}
		if p.AutostartDelay > 0 {
			select {
			case <-time.After(time.Duration(p.AutostartDelay) * time.Second):
			case <-ctx.Done():
				return
			}
		}
		slog.Info("autostart: starting profile", "name", p.Name, "id", p.ID)
		if _, err := supervisor.Start(ctx, profileToDomain(p), runtimeFromRepo(repo, p.RuntimeID), modelFromRepo(repo, p.ModelID), nil, nil); err != nil {
			slog.Error("autostart: start failed", "profile", p.Name, "error", err)
		}
	}
}

// hasActiveInstance returns true if the profile has any instance in a
// non-terminal state (running, starting, stopping, pending) in the repository.
func hasActiveInstance(repo storage.Repository, profileID string) bool {
	instances, err := repo.ListByProfileID(profileID)
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

func profileToDomain(p *storage.ProfileEntry) *domain.Profile {
	return &domain.Profile{
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
}

func runtimeFromRepo(repo storage.Repository, id string) *domain.Runtime {
	rte, err := repo.GetRuntime(id)
	if err != nil {
		return nil
	}
	return &domain.Runtime{
		ID:               rte.ID,
		Name:             rte.Name,
		Executable:       rte.Executable,
		WorkingDirectory: rte.WorkingDirectory,
		DefaultArgs:      rte.DefaultArgs,
		Environment:      rte.Environment,
	}
}

func modelFromRepo(repo storage.Repository, id string) *domain.Model {
	if id == "" {
		return nil
	}
	mde, err := repo.GetModel(id)
	if err != nil {
		return nil
	}
	return &domain.Model{
		ID:        mde.ID,
		Name:      mde.Name,
		Path:      mde.Path,
		MMProj:    mde.MMProj,
		Format:    mde.Format,
		Arguments: mde.Arguments,
		RuntimeID: mde.RuntimeID,
	}
}
