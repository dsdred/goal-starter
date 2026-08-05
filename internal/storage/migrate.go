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
func MigrateFromOldStores(repo Repository, oldDir, repoPath string) error {
	profilePath := filepath.Join(oldDir, "profiles.json")
	runtimePath := filepath.Join(oldDir, "runtimes.json")
	modelPath := filepath.Join(oldDir, "models.json")

	// Load existing data from new repo to avoid duplicates.
	newRuntimes, _ := repo.ListRuntimes()
	newModels, _ := repo.ListModels()
	newProfiles, _ := repo.ListProfiles()

	existingRuntime := make(map[string]bool)
	for _, r := range newRuntimes {
		existingRuntime[r.ID] = true
	}
	existingModel := make(map[string]bool)
	for _, m := range newModels {
		existingModel[m.ID] = true
	}
	existingProfile := make(map[string]bool)
	for _, p := range newProfiles {
		existingProfile[p.ID] = true
	}

	// Migrate runtimes.
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
			if existingRuntime[old.ID] {
				continue
			}
			re := RuntimeEntry{
				ID:               old.ID,
				Name:             old.Name,
				Executable:       old.Executable,
				WorkingDirectory: old.WorkingDirectory,
				DefaultArgs:      old.DefaultArgs,
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

	// Migrate models.
	if _, err := os.Stat(modelPath); err == nil {
		data, err := os.ReadFile(modelPath)
		if err != nil {
			return fmt.Errorf("read models: %w", err)
		}
		var oldModels []oldModelEntry
		if err := json.Unmarshal(data, &oldModels); err != nil {
			return fmt.Errorf("decode models: %w", err)
		}
		for _, old := range oldModels {
			if existingModel[old.ID] {
				continue
			}
			me := ModelEntry{
				ID:        old.ID,
				Name:      old.Name,
				Path:      old.Path,
				MMProj:    old.MMProj,
				Format:    old.Format,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			if err := repo.CreateModel(&me); err != nil {
				return fmt.Errorf("create model %s: %w", old.ID, err)
			}
			existingModel[old.ID] = true
		}
	}

	// Migrate profiles.
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
			if existingProfile[old.ID] {
				continue
			}
			pe := ProfileEntry{
				ID:          old.ID,
				Name:        old.Name,
				RuntimeID:   old.RuntimeID,
				ModelID:     old.ModelID,
				Host:        old.Host,
				Port:        old.Port,
				Args:        old.Args,
				Environment: old.Environment,
				Active:      old.Active,
				CreatedAt:   old.CreatedAt,
				UpdatedAt:   old.UpdatedAt,
			}
			if err := repo.CreateProfile(&pe); err != nil {
				return fmt.Errorf("create profile %s: %w", old.ID, err)
			}
			existingProfile[old.ID] = true
		}
	}

	// Save unified repository.
	if err := repo.SaveUnified(repoPath); err != nil {
		return fmt.Errorf("save unified repository: %w", err)
	}

	return nil
}

// oldRuntimeEntry is the legacy format for runtime data.
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

// oldProfileEntry is the legacy format for profile data.
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

// oldModelEntry is the legacy format for model data.
type oldModelEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	MMProj string `json:"mmproj,omitempty"`
	Format string `json:"format,omitempty"`
}
