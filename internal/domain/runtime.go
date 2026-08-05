package domain

import (
	"time"
)

// Runtime represents an AI runtime executable (e.g., llama.cpp, ollama).
type Runtime struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks runtime consistency.
func (r *Runtime) Validate() error {
	if r.ID == "" {
		return &ValidationError{Field: "id", Message: "runtime id is required"}
	}
	if r.Name == "" {
		return &ValidationError{Field: "name", Message: "runtime name is required"}
	}
	if r.Executable == "" {
		return &ValidationError{Field: "executable", Message: "runtime executable is required"}
	}
	return nil
}
