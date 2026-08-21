package domain

import "time"

// LaunchInstanceEntry represents a persisted launch instance DTO.
// It is placed in domain so that process package can reference it without
// depending on storage. The storage package uses this type for JSON serialization.
type LaunchInstanceEntry struct {
	ID               string            `json:"id"`
	ModelID          string            `json:"model_id"`
	ModelName        string            `json:"model_name,omitempty"`
	RuntimeID        string            `json:"runtime_id"`
	Executable       string            `json:"executable,omitempty"`
	Args             []string          `json:"args,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	State            string            `json:"state"`
	PID              int               `json:"pid,omitempty"`
	ExitCode         int               `json:"exit_code,omitempty"`
	ExitClass        string            `json:"exit_class,omitempty"`
	LastError        string            `json:"last_error,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	StoppedAt        time.Time         `json:"stopped_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}
