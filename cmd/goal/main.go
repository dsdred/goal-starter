package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/goal/internal/config"
	"github.com/example/goal/internal/process"
	"github.com/example/goal/internal/store"
	"github.com/example/goal/internal/version"
	"github.com/example/goal/internal/webui"
	WebUIStore "github.com/example/goal/internal/webui/store"
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

	// Create stores.
	pdb, err := WebUIStore.NewStore(dataDir)
	if err != nil {
		slog.Error("init profile store", "error", err)
		os.Exit(1)
	}

	mdb, err := WebUIStore.NewModelStore(dataDir)
	if err != nil {
		slog.Error("init model store", "error", err)
		os.Exit(1)
	}

	// Create instance store for launch instances.
	instStore, err := store.NewInstanceStoreJSON(store.InstanceStoreOptions{
		Directory: dataDir,
		Filename:  "instances.json",
	})
	if err != nil {
		slog.Error("init instance store", "error", err)
		os.Exit(1)
	}

	// Create Supervisor with instance store.
	supervisor := process.NewSupervisor(instStore)

	app := webui.NewWithSupervisor(cfg, supervisor, pdb, mdb, instStore)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start periodic health check goroutine.
	go app.RunHealthChecker(ctx)

	if err := app.Run(ctx); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
