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

// LaunchResolver resolves a Profile + Runtime + Model into a CommandSpec.
type LaunchResolver struct {
	// envCaseInsensitive forces lowercase keys on Windows for merge.
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

// Resolve builds a CommandSpec from profile, runtime, and model.
func (r *LaunchResolver) Resolve(
	profile *Profile,
	runtime *Runtime,
	model *Model,
	customArgs []string,
	customEnv map[string]string,
) (*CommandSpec, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}

	// Validate runtime executable exists.
	if runtime.Executable == "" {
		return nil, fmt.Errorf("runtime executable is empty")
	}
	exePath := resolveExecutablePath(runtime.Executable, runtime.WorkingDirectory)
	if _, err := os.Stat(exePath); err != nil {
		return nil, fmt.Errorf("runtime executable does not exist: %s: %w", exePath, err)
	}

	// Build args: runtime default args + model-specific args + profile args + custom args.
	args := make([]string, 0, len(runtime.DefaultArgs)+len(profile.Args)+len(customArgs)+4)
	args = append(args, runtime.DefaultArgs...)
	args = append(args, profile.Args...)
	args = append(args, customArgs...)

	// Add host/port if not already present (same logic as BuildCommandSpecForPreview).
	if profile.Host != "" {
		hasHostFlag := false
		for _, arg := range args {
			if arg == "--host" || arg == "-a" {
				hasHostFlag = true
				break
			}
		}
		if !hasHostFlag {
			args = append(args, "--host", profile.Host)
		}
	}

	if profile.Port > 0 {
		hasPortFlag := false
		for _, arg := range args {
			if arg == "--port" {
				hasPortFlag = true
				break
			}
		}
		if !hasPortFlag {
			args = append(args, "--port", fmt.Sprintf("%d", profile.Port))
		}
	}

	// Build environment.
	envMap := make(map[string]string)
	// Start with parent process environment.
	for _, ev := range os.Environ() {
		k, v, ok := strings.Cut(ev, "=")
		if !ok {
			continue
		}
		key := r.normalizeKey(k)
		envMap[key] = v
	}
	// Add runtime environment.
	for k, v := range runtime.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	// Add profile environment (overrides runtime).
	for k, v := range profile.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	// Add custom environment (overrides all).
	for k, v := range customEnv {
		envMap[r.normalizeKey(k)] = v
	}

	// Convert map to []string.
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	spec := &CommandSpec{
		Executable:       exePath,
		Args:             args,
		WorkingDirectory: runtime.WorkingDirectory,
		Environment:      env,
	}

	return spec, nil
}

// resolveExecutablePath resolves a relative executable path against the
// runtime's WorkingDirectory. If the path is already absolute, it is returned
// unchanged. If WorkingDirectory is empty, the path is resolved against the
// current working directory.
func resolveExecutablePath(executable, workingDir string) string {
	if filepath.IsAbs(executable) {
		return executable
	}
	if workingDir != "" {
		return filepath.Join(workingDir, executable)
	}
	return executable
}

// resolveModelArgs adds model-specific arguments to the spec.
// This is applied after Resolve when model info is available.
func (r *LaunchResolver) resolveModelArgs(spec *CommandSpec, model *Model) {
	if model == nil {
		return
	}
	// llama.cpp style: -m <path>
	if model.Path != "" {
		spec.Args = append(spec.Args, "-m", model.Path)
	}
	if model.MMProj != "" {
		spec.Args = append(spec.Args, "--mmproj", model.MMProj)
	}
	// Inline arguments (new-style models).
	if len(model.Arguments) > 0 {
		spec.Args = append(spec.Args, model.Arguments...)
	}
}

// AddModelArgs adds the same model-specific arguments used by process launch.
// It is exported so preview can share the exact launch composition.
func (r *LaunchResolver) AddModelArgs(spec *CommandSpec, model *Model) {
	r.resolveModelArgs(spec, model)
}

// normalizeKey returns the environment key in a consistent case.
// On Windows, environment variable names are case-insensitive.
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
	profile *Profile,
	runtime *Runtime,
	model *Model,
) []ResolveError {
	var errs []ResolveError

	if profile == nil {
		errs = append(errs, ResolveError{Field: "profile", Message: "profile is required"})
	}
	if runtime == nil {
		errs = append(errs, ResolveError{Field: "runtime", Message: "runtime is required"})
		return errs
	}

	if runtime.Executable == "" {
		errs = append(errs, ResolveError{Field: "runtime.executable", Message: "runtime executable is empty"})
	}

	if profile.RuntimeID != runtime.ID {
		errs = append(errs, ResolveError{Field: "profile.runtime_id", Message: fmt.Sprintf("profile references runtime %s, but runtime is %s", profile.RuntimeID, runtime.ID)})
	}

	if model != nil {
		if profile.ModelID != "" && model.ID != profile.ModelID {
			errs = append(errs, ResolveError{Field: "profile.model_id", Message: fmt.Sprintf("profile references model %s, but model is %s", profile.ModelID, model.ID)})
		}

		// Validate model paths exist.
		if model.Path != "" {
			if _, err := os.Stat(model.Path); err != nil {
				errs = append(errs, ResolveError{Field: "model.path", Message: fmt.Sprintf("model path does not exist: %s", model.Path)})
			}
		}

		if model.MMProj != "" {
			if _, err := os.Stat(model.MMProj); err != nil {
				errs = append(errs, ResolveError{Field: "model.mmproj", Message: fmt.Sprintf("mmproj path does not exist: %s", model.MMProj)})
			}
		}
	}

	return errs
}

// Preview returns a CommandSpec without validating executable existence.
// This is used for the /profiles/{id}/resolve endpoint to show what would be launched.
func (r *LaunchResolver) Preview(
	profile *Profile,
	runtime *Runtime,
	model *Model,
	customArgs []string,
	customEnv map[string]string,
) (*CommandSpec, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}

	if runtime.Executable == "" {
		return nil, fmt.Errorf("runtime executable is empty")
	}

	exePath := resolveExecutablePath(runtime.Executable, runtime.WorkingDirectory)

	// Build args: runtime default args + model args + profile args + custom args.
	args := make([]string, 0, len(runtime.DefaultArgs)+len(profile.Args)+len(customArgs))
	args = append(args, runtime.DefaultArgs...)
	args = append(args, profile.Args...)
	args = append(args, customArgs...)

	// Add model-specific args.
	if model != nil {
		if model.Path != "" {
			args = append(args, "-m", model.Path)
		}
		if model.MMProj != "" {
			args = append(args, "--mmproj", model.MMProj)
		}
		if len(model.Arguments) > 0 {
			args = append(args, model.Arguments...)
		}
	}

	// Build environment.
	envMap := make(map[string]string)
	for k, v := range runtime.Environment {
		envMap[r.normalizeKey(k)] = v
	}
	for k, v := range profile.Environment {
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
	profile *Profile,
	runtime *Runtime,
	model *Model,
	customArgs []string,
	customEnv map[string]string,
) (*LaunchInstance, error) {
	spec, err := r.Resolve(profile, runtime, model, customArgs, customEnv)
	if err != nil {
		return nil, err
	}

	// Resolve model-specific args into spec.
	r.resolveModelArgs(spec, model)

	// Convert env map for storage.
	envMap := make(map[string]string)
	for _, ev := range spec.Environment {
		k, v, ok := strings.Cut(ev, "=")
		if ok {
			envMap[k] = v
		}
	}

	id := InstanceID(fmt.Sprintf("%s-%d", profile.ID, timeNow().UnixNano()))

	inst := &LaunchInstance{
		ID:               id,
		ProfileID:        profile.ID,
		RuntimeID:        runtime.ID,
		ModelID:          modelIDOrDefault(model),
		State:            InstanceStatePending,
		Executable:       spec.Executable,
		Args:             spec.Args,
		WorkingDirectory: spec.WorkingDirectory,
		Environment:      envMap,
		CreatedAt:        timeNow(),
		UpdatedAt:        timeNow(),
	}

	return inst, nil
}

func modelIDOrDefault(m *Model) string {
	if m == nil {
		return ""
	}
	return m.ID
}
