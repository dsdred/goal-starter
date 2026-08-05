package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dsdred/goal/internal/storage"
)

// BuildCommandSpecForPreview constructs a CommandSpec preview from storage entries.
// Unlike LaunchResolver.Resolve, it does NOT validate executable existence.
// This is used for the /profiles/{id}/resolve endpoint.
func BuildCommandSpecForPreview(profile *storage.ProfileEntry, runtime *storage.RuntimeEntry, modelPath, mmprojPath string) (*CommandSpec, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}

	// Deep copy default args to prevent mutation.
	args := make([]string, 0, len(runtime.DefaultArgs)+len(profile.Args)+8)
	args = append(args, runtime.DefaultArgs...)
	args = append(args, profile.Args...)

	// Add model path for llama.cpp-style runtimes.
	if modelPath != "" {
		args = append(args, "-m", modelPath)
	}
	if mmprojPath != "" {
		args = append(args, "--mmproj", mmprojPath)
	}

	// Add host/port if not already present.
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

	// Merge environment: profile env overrides runtime env.
	// On Windows, env keys are case-insensitive.
	envMap := make(map[string]string)
	for k, v := range runtime.Environment {
		envMap[envKey(k)] = v
	}
	for k, v := range profile.Environment {
		envMap[envKey(k)] = v
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	return &CommandSpec{
		Executable:       runtime.Executable,
		Args:             args,
		WorkingDirectory: resolveWorkingDirectory(runtime, profile),
		Environment:      env,
	}, nil
}

// resolveWorkingDirectory determines the working directory for the process.
func resolveWorkingDirectory(runtime *storage.RuntimeEntry, profile *storage.ProfileEntry) string {
	if runtime.WorkingDirectory != "" {
		return runtime.WorkingDirectory
	}
	// Use directory of executable as fallback.
	exePath := runtime.Executable
	if dir := filepath.Dir(exePath); dir != "." && dir != "/" {
		return dir
	}
	return ""
}

// RuntimeExecutableExists checks if the runtime executable exists on disk.
func RuntimeExecutableExists(runtime *storage.RuntimeEntry) error {
	if runtime.Executable == "" {
		return fmt.Errorf("runtime executable is empty")
	}
	if abs, err := filepath.Abs(runtime.Executable); err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	} else if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("runtime executable does not exist: %s: %w", runtime.Executable, err)
	}
	return nil
}

// envKey returns the normalized environment key.
// On Windows, environment variable names are case-insensitive,
// so we normalize to uppercase to avoid duplicates.
func envKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

// envKeyMap merges environment maps with case-insensitive keys on Windows.
func envKeyMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[envKey(k)] = v
	}
	return out
}
