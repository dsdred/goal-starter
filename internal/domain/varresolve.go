package domain

import (
	"fmt"
	"os"
	"strings"
)

// VariableSource provides variable lookups for resolution.
type VariableSource interface {
	Lookup(name string) (string, bool)
}

// UndefinedVariableError is returned when a syntactically valid ${NAME}
// reference has no value in any source.
type UndefinedVariableError struct {
	Name  string
	Field string
}

func (e *UndefinedVariableError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: undefined variable %s", e.Field, e.Name)
	}
	return fmt.Sprintf("undefined variable %s", e.Name)
}

// InvalidVariableReferenceError is returned when a ${...} sequence has
// malformed syntax (bad name, empty name, unterminated).
type InvalidVariableReferenceError struct {
	Raw   string
	Field string
}

func (e *InvalidVariableReferenceError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: invalid variable reference %q", e.Field, e.Raw)
	}
	return fmt.Sprintf("invalid variable reference %q", e.Raw)
}

// isValidVarName checks that name matches [A-Za-z_][A-Za-z0-9_]*.
func isValidVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if i == 0 {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

// ResolveString performs single-pass, non-recursive variable substitution.
// The source provides variable values. A fast path returns the input
// unchanged if it contains no '$'.
func ResolveString(raw string, source VariableSource) (string, error) {
	if !strings.ContainsRune(raw, '$') {
		return raw, nil
	}

	var result strings.Builder
	result.Grow(len(raw))
	i := 0
	for i < len(raw) {
		if raw[i] == '$' && i+1 < len(raw) {
			if raw[i+1] == '$' {
				result.WriteByte('$')
				i += 2
				continue
			}
			if raw[i+1] == '{' {
				closeIdx := strings.IndexByte(raw[i+2:], '}')
				if closeIdx == -1 {
					return "", &InvalidVariableReferenceError{Raw: raw[i:]}
				}
				name := raw[i+2 : i+2+closeIdx]
				if !isValidVarName(name) {
					return "", &InvalidVariableReferenceError{Raw: raw[i : i+2+closeIdx+1]}
				}
				value, found := source.Lookup(name)
				if !found {
					return "", &UndefinedVariableError{Name: name}
				}
				result.WriteString(value)
				i += 2 + closeIdx + 1
				continue
			}
			result.WriteByte('$')
			i++
		} else {
			result.WriteByte(raw[i])
			i++
		}
	}
	return result.String(), nil
}

// ResolveStringField is like ResolveString but attaches a field context
// to any returned error.
func ResolveStringField(raw, field string, source VariableSource) (string, error) {
	out, err := ResolveString(raw, source)
	if err != nil {
		if uve, ok := err.(*UndefinedVariableError); ok {
			uve.Field = field
			return "", uve
		}
		if ivre, ok := err.(*InvalidVariableReferenceError); ok {
			ivre.Field = field
			return "", ivre
		}
	}
	return out, err
}

// ResolveArgs resolves variable references in each token of an args slice.
// Returns a new slice; does not mutate the input. The fieldPrefix is used
// in error diagnostics (e.g. "model.args" produces "model.args[2]").
func ResolveArgs(args []string, fieldPrefix string, source VariableSource) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	hasVar := false
	for _, a := range args {
		if strings.ContainsRune(a, '$') {
			hasVar = true
			break
		}
	}
	if !hasVar {
		return args, nil
	}
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		if !strings.ContainsRune(a, '$') {
			continue
		}
		resolved, err := ResolveStringField(a, fmt.Sprintf("%s[%d]", fieldPrefix, i), source)
		if err != nil {
			return nil, err
		}
		out[i] = resolved
	}
	return out, nil
}

// ResolveEnvValues resolves variable references in environment map values.
// Keys are never resolved. Returns a new map; does not mutate the input.
func ResolveEnvValues(env map[string]string, fieldPrefix string, source VariableSource) (map[string]string, error) {
	if len(env) == 0 {
		return env, nil
	}
	hasVar := false
	for _, v := range env {
		if strings.ContainsRune(v, '$') {
			hasVar = true
			break
		}
	}
	if !hasVar {
		return env, nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if !strings.ContainsRune(v, '$') {
			out[k] = v
			continue
		}
		resolved, err := ResolveStringField(v, fieldPrefix+"."+k, source)
		if err != nil {
			return nil, err
		}
		out[k] = resolved
	}
	return out, nil
}

// builtinSource implements VariableSource for fixed GoAl built-in variables.
type builtinSource struct {
	vars map[string]string
}

func (b *builtinSource) Lookup(name string) (string, bool) {
	v, ok := b.vars[name]
	return v, ok
}

// envSource implements VariableSource for the GoAl process environment.
type envSource struct{}

func (e *envSource) Lookup(name string) (string, bool) {
	return os.LookupEnv(name)
}

// combinedSource checks built-ins first, then falls through to the next source.
type combinedSource struct {
	primary   VariableSource
	secondary VariableSource
}

func (c *combinedSource) Lookup(name string) (string, bool) {
	if v, ok := c.primary.Lookup(name); ok {
		return v, true
	}
	return c.secondary.Lookup(name)
}

// NewVariableSource creates the combined source: built-in GOAL_DATA (if
// dataDir is non-empty) takes precedence over the process environment.
func NewVariableSource(dataDir string) VariableSource {
	env := &envSource{}
	if dataDir != "" {
		builtin := &builtinSource{vars: map[string]string{
			"GOAL_DATA": dataDir,
		}}
		return &combinedSource{primary: builtin, secondary: env}
	}
	return env
}
