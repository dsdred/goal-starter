package domain

import (
	"time"
)

// ValidationError represents a validation error for a specific field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Model represents a configured launch definition.
// It defines HOW to launch: runtime reference + args + environment.
// All launch parameters (including --host, --port, -m, --mmproj) are in Args.
type Model struct {
	ID             string
	Name           string
	RuntimeID      string
	Args           []string
	Environment    map[string]string
	Active         bool
	AutostartDelay int

	// PipelineID is set on the in-memory effective model copy when a Pipeline
	// launch pre-substitutes Args (ADR 010 D2); it is propagated into the
	// created instance. Never persisted on ModelEntry.
	PipelineID string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks model consistency.
func (m *Model) Validate() error {
	if m.ID == "" {
		return &ValidationError{Field: "id", Message: "model id is required"}
	}
	if m.Name == "" {
		return &ValidationError{Field: "name", Message: "model name is required"}
	}
	if m.RuntimeID == "" {
		return &ValidationError{Field: "runtime_id", Message: "runtime_id is required"}
	}
	return nil
}
