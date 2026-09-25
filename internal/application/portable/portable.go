package portable

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dsdred/goal/internal/domain"
)

const (
	FormatIdentity = "goal-portable-config"
	FormatVersion  = 1
)

// Bundle is the portable configuration document (format v1).
type Bundle struct {
	Format    string             `json:"format"`
	Version   int                `json:"version"`
	Runtimes  []PortableRuntime  `json:"runtimes"`
	Models    []PortableModel    `json:"models"`
	Pipelines []PortablePipeline `json:"pipelines"`
}

// PortableRuntime is the bundle representation of a Runtime.
type PortableRuntime struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Executable       string   `json:"executable"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	EnvironmentKeys  []string `json:"environment_keys,omitempty"`
}

// PortableModel is the bundle representation of a Model.
type PortableModel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	RuntimeID       string   `json:"runtime_id"`
	Args            []string `json:"args,omitempty"`
	EnvironmentKeys []string `json:"environment_keys,omitempty"`
	Active          bool     `json:"active"`
	AutostartDelay  int      `json:"autostart_delay,omitempty"`
}

// PortablePipeline is the bundle representation of a Pipeline.
type PortablePipeline struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Active bool            `json:"active"`
	Models []PortableEntry `json:"models"`
}

// PortableEntry is the bundle representation of a PipelineModel entry.
type PortableEntry struct {
	ID        string   `json:"id"`
	ModelID   string   `json:"model_id"`
	Args      []string `json:"args,omitempty"`
	AutoStart bool     `json:"auto_start"`
}

// ErrMalformedBundle is returned when the input is not valid JSON or has
// structural problems (unknown fields, trailing data).
type ErrMalformedBundle struct {
	Reason string
}

func (e *ErrMalformedBundle) Error() string {
	return "malformed bundle: " + e.Reason
}

// ErrWrongFormat is returned when the format identity is not "goal-portable-config".
type ErrWrongFormat struct {
	Got string
}

func (e *ErrWrongFormat) Error() string {
	return fmt.Sprintf("wrong format: expected %q, got %q", FormatIdentity, e.Got)
}

// ErrUnsupportedVersion is returned when the bundle version is not understood.
type ErrUnsupportedVersion struct {
	Got int
}

func (e *ErrUnsupportedVersion) Error() string {
	return fmt.Sprintf("unsupported bundle version: %d (max supported: %d)", e.Got, FormatVersion)
}

// ErrValidation is returned for semantic validation failures (missing refs,
// duplicate IDs, malformed variable syntax, domain constraint violations).
type ErrValidation struct {
	Reason string
}

func (e *ErrValidation) Error() string {
	return "bundle validation: " + e.Reason
}

// Conflict describes one blocking collision between an imported entity and
// existing repository state. An entity that merely shares an ID with an
// existing entity is not a conflict: it is skipped.
type Conflict struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Name   string `json:"name,omitempty"`
	// RelatedID is the repository or bundle entity that blocks ID.
	RelatedID string `json:"related_id,omitempty"`
}

// ParseBundle strictly parses a portable bundle from raw JSON bytes.
// It rejects malformed JSON, unknown fields, trailing data, wrong format,
// and unsupported versions.
func ParseBundle(data []byte) (*Bundle, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()

	var b Bundle
	if err := decoder.Decode(&b); err != nil {
		return nil, &ErrMalformedBundle{Reason: err.Error()}
	}

	if decoder.More() {
		return nil, &ErrMalformedBundle{Reason: "trailing data after JSON document"}
	}

	if b.Format != FormatIdentity {
		return nil, &ErrWrongFormat{Got: b.Format}
	}
	if b.Version != FormatVersion {
		return nil, &ErrUnsupportedVersion{Got: b.Version}
	}

	return &b, nil
}

// syntaxOnlySource always reports variables as defined (empty value) so that
// ResolveString performs pure syntax validation without producing
// UndefinedVariableError.
type syntaxOnlySource struct{}

func (s *syntaxOnlySource) Lookup(_ string) (string, bool) {
	return "", true
}

// validateVariableSyntax checks that all ${...} sequences in s are
// syntactically well-formed. It reuses the Slice 1 tokenizer. Valid but
// undefined variables are accepted; malformed references are rejected.
func validateVariableSyntax(s, field string) error {
	_, err := domain.ResolveStringField(s, field, &syntaxOnlySource{})
	if err != nil {
		if _, ok := err.(*domain.InvalidVariableReferenceError); ok {
			return &ErrValidation{Reason: err.Error()}
		}
	}
	return nil
}

// validateBundle performs full semantic validation on a parsed bundle.
// This is the self-contained validation (steps 3-12 from the Owner Contract).
func validateBundle(b *Bundle) error {
	// Duplicate ID checks.
	rtIDs := make(map[string]bool, len(b.Runtimes))
	for i, rt := range b.Runtimes {
		if rt.ID == "" {
			return &ErrValidation{Reason: fmt.Sprintf("runtimes[%d]: empty id", i)}
		}
		if rtIDs[rt.ID] {
			return &ErrValidation{Reason: fmt.Sprintf("duplicate runtime id %q", rt.ID)}
		}
		rtIDs[rt.ID] = true
	}

	mIDs := make(map[string]bool, len(b.Models))
	for i, m := range b.Models {
		if m.ID == "" {
			return &ErrValidation{Reason: fmt.Sprintf("models[%d]: empty id", i)}
		}
		if mIDs[m.ID] {
			return &ErrValidation{Reason: fmt.Sprintf("duplicate model id %q", m.ID)}
		}
		mIDs[m.ID] = true
	}

	pIDs := make(map[string]bool, len(b.Pipelines))
	for i, p := range b.Pipelines {
		if p.ID == "" {
			return &ErrValidation{Reason: fmt.Sprintf("pipelines[%d]: empty id", i)}
		}
		if pIDs[p.ID] {
			return &ErrValidation{Reason: fmt.Sprintf("duplicate pipeline id %q", p.ID)}
		}
		pIDs[p.ID] = true
	}

	// Referential integrity (self-contained).
	for i, m := range b.Models {
		if m.RuntimeID == "" {
			return &ErrValidation{Reason: fmt.Sprintf("models[%d]: empty runtime_id", i)}
		}
		if !rtIDs[m.RuntimeID] {
			return &ErrValidation{Reason: fmt.Sprintf("models[%d]: runtime_id %q not found in bundle", i, m.RuntimeID)}
		}
	}

	for i, p := range b.Pipelines {
		entryIDs := make(map[string]bool, len(p.Models))
		for j, e := range p.Models {
			if e.ID == "" {
				return &ErrValidation{Reason: fmt.Sprintf("pipelines[%d].models[%d]: empty entry id", i, j)}
			}
			if entryIDs[e.ID] {
				return &ErrValidation{Reason: fmt.Sprintf("pipelines[%d]: duplicate entry id %q", i, e.ID)}
			}
			entryIDs[e.ID] = true
			if e.ModelID == "" {
				return &ErrValidation{Reason: fmt.Sprintf("pipelines[%d].models[%d]: empty model_id", i, j)}
			}
			if !mIDs[e.ModelID] {
				return &ErrValidation{Reason: fmt.Sprintf("pipelines[%d].models[%d]: model_id %q not found in bundle", i, j, e.ModelID)}
			}
		}
	}

	// Variable syntax validation.
	for i, rt := range b.Runtimes {
		if err := validateVariableSyntax(rt.Executable, fmt.Sprintf("runtimes[%d].executable", i)); err != nil {
			return err
		}
		if rt.WorkingDirectory != "" {
			if err := validateVariableSyntax(rt.WorkingDirectory, fmt.Sprintf("runtimes[%d].working_directory", i)); err != nil {
				return err
			}
		}
	}
	for i, m := range b.Models {
		for j, a := range m.Args {
			if err := validateVariableSyntax(a, fmt.Sprintf("models[%d].args[%d]", i, j)); err != nil {
				return err
			}
		}
	}
	for i, p := range b.Pipelines {
		for j, e := range p.Models {
			for k, a := range e.Args {
				if err := validateVariableSyntax(a, fmt.Sprintf("pipelines[%d].models[%d].args[%d]", i, j, k)); err != nil {
					return err
				}
			}
		}
	}

	// Domain validation.
	for i, rt := range b.Runtimes {
		if rt.Name == "" {
			return &ErrValidation{Reason: fmt.Sprintf("runtimes[%d]: empty name", i)}
		}
		if rt.Executable == "" {
			return &ErrValidation{Reason: fmt.Sprintf("runtimes[%d]: empty executable", i)}
		}
	}
	for i, m := range b.Models {
		if m.Name == "" {
			return &ErrValidation{Reason: fmt.Sprintf("models[%d]: empty name", i)}
		}
	}
	for i, p := range b.Pipelines {
		if p.Name == "" {
			return &ErrValidation{Reason: fmt.Sprintf("pipelines[%d]: empty name", i)}
		}
	}

	// Runtime name uniqueness within the bundle (case-insensitive).
	rtNames := make(map[string]string, len(b.Runtimes))
	for _, rt := range b.Runtimes {
		key := strings.ToLower(rt.Name)
		if prev, exists := rtNames[key]; exists {
			return &ErrValidation{Reason: fmt.Sprintf("duplicate runtime name (case-insensitive): %q and %q", prev, rt.ID)}
		}
		rtNames[key] = rt.ID
	}

	return nil
}
