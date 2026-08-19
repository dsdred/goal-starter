package storage

import (
	"log/slog"

	"github.com/dsdred/goal/internal/config"
)

// SeedFromConfig seeds the repository with runtimes and models from
// the configuration file. In v6, old config profiles become models,
// and old config models (physical files) are folded into model args.
// Entries that already exist are skipped (matched by ID).
func SeedFromConfig(repo Repository, cfg *config.Config) {
	// Runtimes
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
			DefaultArgs:      rt.DefaultArgs,
			Environment:      rt.Environment,
		}
		if err := repo.CreateRuntime(entry); err != nil {
			slog.Warn("seed runtime failed", "id", rt.ID, "error", err)
		}
	}

	// Build old model lookup for folding into model args.
	oldModelMap := make(map[string]*config.Model)
	for i := range cfg.Models {
		oldModelMap[cfg.Models[i].ID] = &cfg.Models[i]
	}

	// Profiles → new Models (folding old model path/mmproj/args)
	for _, p := range cfg.Profiles {
		if _, err := repo.GetModel(p.ID); err == nil {
			slog.Debug("seed skip existing model", "id", p.ID)
			continue
		}
		args := make([]string, 0, len(p.Args)+8)
		args = append(args, p.Args...)
		if p.ModelID != "" {
			if m, ok := oldModelMap[p.ModelID]; ok {
				if m.Path != "" {
					args = append(args, "-m", m.Path)
				}
				args = append(args, m.Arguments...)
			}
		}
		entry := &ModelEntry{
			ID:          p.ID,
			Name:        p.Name,
			RuntimeID:   p.RuntimeID,
			Host:        p.Host,
			Port:        p.Port,
			Args:        args,
			Environment: p.Environment,
			Active:      true,
		}
		if err := repo.CreateModel(entry); err != nil {
			slog.Warn("seed model failed", "id", p.ID, "error", err)
		}
	}
}
