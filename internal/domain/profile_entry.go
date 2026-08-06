package domain

import "time"

// ProfileEntry represents a persisted profile definition DTO.
type ProfileEntry struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	RuntimeID   string            `json:"runtime_id"`
	ModelID     string            `json:"model_id,omitempty"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Active      bool              `json:"active"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// RuntimeEntry represents a persisted runtime definition DTO.
type RuntimeEntry struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ModelEntry represents a persisted model definition DTO.
type ModelEntry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MMProj    string    `json:"mmproj,omitempty"`
	Format    string    `json:"format,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EntryToDomain converts storage entry types to domain types.

// ProfileEntryToDomain converts ProfileEntry to Profile.
func ProfileEntryToDomain(e *ProfileEntry) *Profile {
	return &Profile{
		ID:          e.ID,
		Name:        e.Name,
		RuntimeID:   e.RuntimeID,
		ModelID:     e.ModelID,
		Host:        e.Host,
		Port:        e.Port,
		Args:        e.Args,
		Environment: e.Environment,
		Active:      e.Active,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// RuntimeEntryToDomain converts RuntimeEntry to Runtime.
func RuntimeEntryToDomain(e *RuntimeEntry) *Runtime {
	return &Runtime{
		ID:               e.ID,
		Name:             e.Name,
		Executable:       e.Executable,
		WorkingDirectory: e.WorkingDirectory,
		DefaultArgs:      e.DefaultArgs,
		Environment:      e.Environment,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}
