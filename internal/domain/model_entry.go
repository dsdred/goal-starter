package domain

import "time"

// ModelEntry represents a persisted model definition (v6 schema).
// A Model is a configured launch definition: runtime + args + host + port.
type ModelEntry struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	RuntimeID      string            `json:"runtime_id"`
	Args           []string          `json:"args,omitempty"`
	Host           string            `json:"host"`
	Port           int               `json:"port"`
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
	DefaultArgs      []string          `json:"default_args"`
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
		Host:           e.Host,
		Port:           e.Port,
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
		DefaultArgs:      e.DefaultArgs,
		Environment:      e.Environment,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}
