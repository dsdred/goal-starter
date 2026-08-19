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
// It is the user-facing "model" — a runtime plus launch arguments, host, port.
// Physical model files (GGUF, MMProj) are NOT separate fields; they are
// ordinary entries in Args (e.g. "-m <path>", "--mmproj <path>").
type Model struct {
	ID             string
	Name           string
	RuntimeID      string
	Args           []string
	Host           string
	Port           int
	Environment    map[string]string
	Active         bool
	AutostartDelay int

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
