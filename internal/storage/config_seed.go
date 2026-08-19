package storage

import (
	"fmt"
	"log/slog"

	"github.com/dsdred/goal/internal/config"
)

// SeedFromConfig seeds the repository with runtimes and models from
// the configuration file. In v7, old config profiles become models,
// and old config models (physical files) are folded into model args.
// Runtime DefaultArgs and Profile Host/Port are folded into model args.
// Entries that already exist are skipped (matched by ID).
func SeedFromConfig(repo Repository, cfg *config.Config) {
	// Runtimes (no DefaultArgs in v7).
	for _, rt := range cfg.Runtimes {
		if _, err := repo.GetRuntime(rt.ID); err == nil {
			slog.Debug("seed skip existing runtime", "id", rt.ID)
			continue
		}
		entry := &RuntimeEntry{
			ID:               rt.ID,
			Name:             rt.Name,
			Executable:       rt.Executable,
			WorkingDirectory: rt.WorkingDirectory,
			Environment:      rt.Environment,
		}
		if err := repo.CreateRuntime(entry); err != nil {
			slog.Warn("seed runtime failed", "id", rt.ID, "error", err)
		}
	}

	// Build runtime DefaultArgs lookup for folding into model args.
	runtimeArgsMap := make(map[string][]string)
	for _, rt := range cfg.Runtimes {
		runtimeArgsMap[rt.ID] = rt.DefaultArgs
	}

	// Build old model lookup for folding into model args.
	oldModelMap := make(map[string]*config.Model)
	for i := range cfg.Models {
		oldModelMap[cfg.Models[i].ID] = &cfg.Models[i]
	}

	// Profiles → new Models (folding runtime DefaultArgs + old model path + profile Host/Port)
	for _, p := range cfg.Profiles {
		if _, err := repo.GetModel(p.ID); err == nil {
			slog.Debug("seed skip existing model", "id", p.ID)
			continue
		}
		args := make([]string, 0, len(runtimeArgsMap[p.RuntimeID])+len(p.Args)+8)
		args = append(args, runtimeArgsMap[p.RuntimeID]...)
		args = append(args, p.Args...)
		if p.ModelID != "" {
			if m, ok := oldModelMap[p.ModelID]; ok {
				if m.Path != "" {
					args = append(args, "-m", m.Path)
				}
				args = append(args, m.Arguments...)
			}
		}
		if p.Host != "" {
			if !containsFlag(args, "--host", "-a") {
				args = append(args, "--host", p.Host)
			}
		}
		if p.Port > 0 {
			if !containsFlag(args, "--port") {
				args = append(args, "--port", fmt.Sprintf("%d", p.Port))
			}
		}
		entry := &ModelEntry{
			ID:          p.ID,
			Name:        p.Name,
			RuntimeID:   p.RuntimeID,
			Args:        args,
			Environment: p.Environment,
			Active:      true,
		}
		if err := repo.CreateModel(entry); err != nil {
			slog.Warn("seed model failed", "id", p.ID, "error", err)
		}
	}
}

func containsFlag(args []string, flags ...string) bool {
	for _, arg := range args {
		for _, f := range flags {
			if arg == f {
				return true
			}
		}
	}
	return false
}
