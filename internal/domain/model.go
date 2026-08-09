package domain

import (
	"time"
)

// Model represents an AI model file (e.g., GGUF).
type Model struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Path      string            `json:"path"`
	MMProj    string            `json:"mmproj,omitempty"`
	Format    string            `json:"format,omitempty"`
	Arguments []string          `json:"arguments,omitempty"`
	RuntimeID string            `json:"runtime_id,omitempty"`
	Env       map[string]string `json:"environment,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks model consistency.
func (m *Model) Validate() error {
	if m.ID == "" {
		return &ValidationError{Field: "id", Message: "model id is required"}
	}
	if m.Name == "" {
		return &ValidationError{Field: "name", Message: "model name is required"}
	}
	if m.Path == "" && len(m.Arguments) == 0 {
		return &ValidationError{Field: "path", Message: "either path or arguments is required"}
	}
	return nil
}
