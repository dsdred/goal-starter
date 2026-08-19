package domain

import "time"

// ModelEntry represents a persisted model definition (v7 schema).
// A Model is a configured launch definition: runtime + args + environment.
type ModelEntry struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	RuntimeID      string            `json:"runtime_id"`
	Args           []string          `json:"args,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Active         bool              `json:"active"`
	AutostartDelay int               `json:"autostart_delay,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// RuntimeEntry represents a persisted runtime definition DTO.
type RuntimeEntry struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ModelEntryToDomain converts ModelEntry to Model.
func ModelEntryToDomain(e *ModelEntry) *Model {
	return &Model{
		ID:             e.ID,
		Name:           e.Name,
		RuntimeID:      e.RuntimeID,
		Args:           e.Args,
		Environment:    e.Environment,
		Active:         e.Active,
		AutostartDelay: e.AutostartDelay,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

// RuntimeEntryToDomain converts RuntimeEntry to Runtime.
func RuntimeEntryToDomain(e *RuntimeEntry) *Runtime {
	return &Runtime{
		ID:               e.ID,
		Name:             e.Name,
		Executable:       e.Executable,
		WorkingDirectory: e.WorkingDirectory,
		Environment:      e.Environment,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}
