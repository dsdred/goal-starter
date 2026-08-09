package storage

import (
	"log/slog"

	"github.com/dsdred/goal/internal/config"
)

// SeedFromConfig seeds the repository with runtimes, models, and profiles from
// the configuration file. Entries that already exist in the repository are
// skipped (matched by ID). This allows the config file to act as an initial
// seed without overwriting any subsequent edits via the API.
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

	// Models
	for _, m := range cfg.Models {
		if _, err := repo.GetModel(m.ID); err == nil {
			slog.Debug("seed skip existing model", "id", m.ID)
			continue
		}
		entry := &ModelEntry{
			ID:        m.ID,
			Name:      m.Name,
			Path:      m.Path,
			Arguments: m.Arguments,
			RuntimeID: m.RuntimeID,
		}
		if err := repo.CreateModel(entry); err != nil {
			slog.Warn("seed model failed", "id", m.ID, "error", err)
		}
	}

	// Profiles
	for _, p := range cfg.Profiles {
		if _, err := repo.GetProfile(p.ID); err == nil {
			slog.Debug("seed skip existing profile", "id", p.ID)
			continue
		}
		entry := &ProfileEntry{
			ID:          p.ID,
			Name:        p.Name,
			RuntimeID:   p.RuntimeID,
			ModelID:     p.ModelID,
			Host:        p.Host,
			Port:        p.Port,
			Args:        p.Args,
			Environment: p.Environment,
			Active:      true,
		}
		if err := repo.CreateProfile(entry); err != nil {
			slog.Warn("seed profile failed", "id", p.ID, "error", err)
		}
	}
}
