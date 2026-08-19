package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MigrateFromOldStores reads the legacy separate JSON files from oldDir
// and populates the new unified repository at repoPath.
// It skips entities that already exist in the new repository.
// In v6, old profiles become models, and old physical-model files are
// folded into the model's args.
func MigrateFromOldStores(repo Repository, oldDir, repoPath string) error {
	profilePath := filepath.Join(oldDir, "profiles.json")
	runtimePath := filepath.Join(oldDir, "runtimes.json")
	modelPath := filepath.Join(oldDir, "models.json")

	newRuntimes, _ := repo.ListRuntimes()
	newModels, _ := repo.ListModels()

	existingRuntime := make(map[string]bool)
	for _, r := range newRuntimes {
		existingRuntime[r.ID] = true
	}
	existingModel := make(map[string]bool)
	for _, m := range newModels {
		existingModel[m.ID] = true
	}

	// Load old models for reference.
	oldModelMap := make(map[string]*oldModelEntry)
	if _, err := os.Stat(modelPath); err == nil {
		data, err := os.ReadFile(modelPath)
		if err != nil {
			return fmt.Errorf("read models: %w", err)
		}
		var oldModels []oldModelEntry
		if err := json.Unmarshal(data, &oldModels); err != nil {
			return fmt.Errorf("decode models: %w", err)
		}
		for i := range oldModels {
			oldModelMap[oldModels[i].ID] = &oldModels[i]
		}
	}

	// Migrate runtimes.
	runtimeArgsMap := make(map[string][]string)
	if _, err := os.Stat(runtimePath); err == nil {
		data, err := os.ReadFile(runtimePath)
		if err != nil {
			return fmt.Errorf("read runtimes: %w", err)
		}
		var oldRuntimes []oldRuntimeEntry
		if err := json.Unmarshal(data, &oldRuntimes); err != nil {
			return fmt.Errorf("decode runtimes: %w", err)
		}
		for _, old := range oldRuntimes {
			runtimeArgsMap[old.ID] = old.DefaultArgs
			if existingRuntime[old.ID] {
				continue
			}
			re := RuntimeEntry{
				ID:               old.ID,
				Name:             old.Name,
				Executable:       old.Executable,
				WorkingDirectory: old.WorkingDirectory,
				Environment:      old.Environment,
				CreatedAt:        old.CreatedAt,
				UpdatedAt:        old.UpdatedAt,
			}
			if err := repo.CreateRuntime(&re); err != nil {
				return fmt.Errorf("create runtime %s: %w", old.ID, err)
			}
			existingRuntime[old.ID] = true
		}
	}

	// Migrate profiles → new models (folding old model args).
	if _, err := os.Stat(profilePath); err == nil {
		data, err := os.ReadFile(profilePath)
		if err != nil {
			return fmt.Errorf("read profiles: %w", err)
		}
		var oldProfiles []oldProfileEntry
		if err := json.Unmarshal(data, &oldProfiles); err != nil {
			return fmt.Errorf("decode profiles: %w", err)
		}
		for _, old := range oldProfiles {
			if existingModel[old.ID] {
				continue
			}
			args := make([]string, 0, len(runtimeArgsMap[old.RuntimeID])+len(old.Args)+8)
			args = append(args, runtimeArgsMap[old.RuntimeID]...)
			args = append(args, old.Args...)
			if old.ModelID != "" {
				if m, ok := oldModelMap[old.ModelID]; ok {
					if m.Path != "" {
						args = append(args, "-m", m.Path)
					}
					if m.MMProj != "" {
						args = append(args, "--mmproj", m.MMProj)
					}
				}
			}
			if old.Host != "" && !containsV5Flag(args, "--host", "-a") {
				args = append(args, "--host", old.Host)
			}
			if old.Port > 0 && !containsV5Flag(args, "--port") {
				args = append(args, "--port", fmt.Sprintf("%d", old.Port))
			}
			me := ModelEntry{
				ID:          old.ID,
				Name:        old.Name,
				RuntimeID:   old.RuntimeID,
				Args:        args,
				Environment: old.Environment,
				Active:      old.Active,
				CreatedAt:   old.CreatedAt,
				UpdatedAt:   old.UpdatedAt,
			}
			if err := repo.CreateModel(&me); err != nil {
				return fmt.Errorf("create model %s: %w", old.ID, err)
			}
			existingModel[old.ID] = true
		}
	}

	if err := repo.SaveUnified(repoPath); err != nil {
		return fmt.Errorf("save unified repository: %w", err)
	}

	return nil
}

type oldRuntimeEntry struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type oldProfileEntry struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	RuntimeID   string            `json:"runtime_id"`
	ModelID     string            `json:"model_id"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Active      bool              `json:"active"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type oldModelEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	MMProj string `json:"mmproj,omitempty"`
	Format string `json:"format,omitempty"`
}
