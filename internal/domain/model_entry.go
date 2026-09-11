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

// EnvPatchOp represents a single environment variable mutation during a model update.
// Action "set": Value is non-nil (may point to empty string).
// Action "delete": Value is nil.
type EnvPatchOp struct {
	Key    string  `json:"key"`
	Action string  `json:"action"` // "set" or "delete"
	Value  *string `json:"value,omitempty"`
}

// ApplyEnvPatch applies patch operations to an existing environment map.
// Returns a new map; does not mutate the input.
func ApplyEnvPatch(existing map[string]string, ops []EnvPatchOp) map[string]string {
	if len(ops) == 0 {
		if existing == nil {
			return nil
		}
		result := make(map[string]string, len(existing))
		for k, v := range existing {
			result[k] = v
		}
		return result
	}
	result := make(map[string]string, len(existing)+len(ops))
	for k, v := range existing {
		result[k] = v
	}
	for _, op := range ops {
		switch op.Action {
		case "set":
			if op.Value == nil {
				result[op.Key] = ""
			} else {
				result[op.Key] = *op.Value
			}
		case "delete":
			delete(result, op.Key)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
