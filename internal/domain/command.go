package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CommandSpec holds the final resolved command to launch a process.
type CommandSpec struct {
	Executable       string
	Args             []string
	WorkingDirectory string
	Environment      []string
}

// LaunchResolver resolves a Model + Runtime into a CommandSpec.
type LaunchResolver struct {
	envCaseInsensitive bool
}

// timeNow is a variable for testing.
var timeNow = time.Now

// NewLaunchResolver creates a new resolver.
func NewLaunchResolver() *LaunchResolver {
	return &LaunchResolver{
		envCaseInsensitive: runtime.GOOS == "windows",
	}
}

// Resolve builds a CommandSpec from model and runtime.
func (r *LaunchResolver) Resolve(
	model *Model,
	runtime *Runtime,
	customArgs []string,
	customEnv map[string]string,
) (*CommandSpec, error) {
	if model == nil {
		return nil, fmt.Errorf("model is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}
	if runtime.Executable == "" {
		return nil, fmt.Errorf("runtime executable is empty")
	}
	exePath := resolveExecutablePath(runtime.Executable, runtime.WorkingDirectory)

	args := make([]string, 0, len(model.Args)+len(customArgs))
	args = append(args, model.Args...)
	args = append(args, customArgs...)

	// Build environment: parent → runtime → model → custom.
	envMap := make(map[string]string)
	for _, ev := range os.Environ() {
		k, v, ok := strings.Cut(ev, "=")
		if !ok {
			continue
		}
		envMap[r.normalizeKey(k)] = v
	}
	for k, v := range runtime.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	for k, v := range model.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	for k, v := range customEnv {
		envMap[r.normalizeKey(k)] = v
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	return &CommandSpec{
		Executable:       exePath,
		Args:             args,
		WorkingDirectory: runtime.WorkingDirectory,
		Environment:      env,
	}, nil
}

// resolveExecutablePath resolves a relative executable path against the
// runtime's WorkingDirectory.
func resolveExecutablePath(executable, workingDir string) string {
	if filepath.IsAbs(executable) {
		return executable
	}
	if workingDir != "" {
		return filepath.Join(workingDir, executable)
	}
	return executable
}

// normalizeKey returns the environment key in a consistent case.
func (r *LaunchResolver) normalizeKey(key string) string {
	if r.envCaseInsensitive {
		return strings.ToUpper(key)
	}
	return key
}

// ResolveError represents a resolution failure with details.
type ResolveError struct {
	Field   string
	Message string
}

// Validate checks that all required fields are populated and references are valid.
func (r *LaunchResolver) Validate(
	model *Model,
	runtime *Runtime,
) []ResolveError {
	var errs []ResolveError

	if model == nil {
		errs = append(errs, ResolveError{Field: "model", Message: "model is required"})
	}
	if runtime == nil {
		errs = append(errs, ResolveError{Field: "runtime", Message: "runtime is required"})
		return errs
	}

	if runtime.Executable == "" {
		errs = append(errs, ResolveError{Field: "runtime.executable", Message: "runtime executable is empty"})
	}

	if model.RuntimeID != runtime.ID {
		errs = append(errs, ResolveError{Field: "model.runtime_id", Message: fmt.Sprintf("model references runtime %s, but runtime is %s", model.RuntimeID, runtime.ID)})
	}

	return errs
}

// Preview returns a CommandSpec without validating executable existence.
func (r *LaunchResolver) Preview(
	model *Model,
	runtime *Runtime,
	customArgs []string,
	customEnv map[string]string,
) (*CommandSpec, error) {
	if model == nil {
		return nil, fmt.Errorf("model is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}
	if runtime.Executable == "" {
		return nil, fmt.Errorf("runtime executable is empty")
	}

	exePath := resolveExecutablePath(runtime.Executable, runtime.WorkingDirectory)

	args := make([]string, 0, len(model.Args)+len(customArgs))
	args = append(args, model.Args...)
	args = append(args, customArgs...)

	envMap := make(map[string]string)
	for k, v := range runtime.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	for k, v := range model.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	for k, v := range customEnv {
		envMap[r.normalizeKey(k)] = v
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	return &CommandSpec{
		Executable:       exePath,
		Args:             args,
		WorkingDirectory: runtime.WorkingDirectory,
		Environment:      env,
	}, nil
}

// ResolveToInstance fills a LaunchInstance with resolved launch details.
func (r *LaunchResolver) ResolveToInstance(
	model *Model,
	runtime *Runtime,
	customArgs []string,
	customEnv map[string]string,
) (*LaunchInstance, error) {
	spec, err := r.Resolve(model, runtime, customArgs, customEnv)
	if err != nil {
		return nil, err
	}

	envMap := make(map[string]string)
	for _, ev := range spec.Environment {
		k, v, ok := strings.Cut(ev, "=")
		if ok {
			envMap[k] = v
		}
	}

	id := InstanceID(fmt.Sprintf("%s-%d", model.ID, timeNow().UnixNano()))

	return &LaunchInstance{
		ID:               id,
		ModelID:          model.ID,
		ModelName:        model.Name,
		RuntimeID:        runtime.ID,
		PipelineID:       model.PipelineID,
		State:            InstanceStatePending,
		Executable:       spec.Executable,
		Args:             spec.Args,
		WorkingDirectory: spec.WorkingDirectory,
		Environment:      envMap,
		CreatedAt:        timeNow(),
		UpdatedAt:        timeNow(),
	}, nil
}
