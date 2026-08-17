package domain

import (
	"time"
)

// Profile represents a launch profile template.
// A Profile defines HOW to launch a runtime with a specific model.
type Profile struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	RuntimeID      string            `json:"runtime_id"`
	ModelID        string            `json:"model_id"`
	Host           string            `json:"host"`
	Port           int               `json:"port"`
	Args           []string          `json:"args,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Active         bool              `json:"active"`
	AutostartDelay int               `json:"autostart_delay,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks profile consistency.
func (p *Profile) Validate() error {
	if p.ID == "" {
		return &ValidationError{Field: "id", Message: "profile id is required"}
	}
	if p.Name == "" {
		return &ValidationError{Field: "name", Message: "profile name is required"}
	}
	if p.RuntimeID == "" {
		return &ValidationError{Field: "runtime_id", Message: "runtime_id is required"}
	}
	return nil
}

// ValidationError represents a validation error for a specific field.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
