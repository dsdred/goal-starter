package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// v5ProfileCfg is the v5-era profile configuration.
type v5ProfileCfg struct {
	Host        string
	Port        int
	Args        []string
	Environment map[string]string
}

// v5ModelCfg is the v5-era model entry (path, mmproj, raw arguments).
type v5ModelCfg struct {
	Path      string
	MMProj    string
	Arguments []string
}

// v5ResolvedCommand reproduces the v5 launch resolution order:
//   - executable: runtime.Executable resolved against runtime.WorkingDirectory
//   - args: runtime.DefaultArgs + profile.Args + "-m" + model.Path (if non-empty)
//   - "--mmproj" + model.MMProj (if non-empty) + model.Arguments
//   - "--host" + profile.Host (if non-empty and not already in args)
//   - "--port" + profile.Port (if > 0 and not already in args)
//   - working directory: runtime.WorkingDirectory
//   - environment: parent env -> runtime.Environment -> profile.Environment
func v5ResolvedCommand(t *testing.T, rt *Runtime, prof v5ProfileCfg, model v5ModelCfg) *CommandSpec {
	t.Helper()

	exePath := resolveExecutablePath(rt.Executable, rt.WorkingDirectory)

	args := make([]string, 0)
	args = append(args, rt.DefaultArgs...)
	args = append(args, prof.Args...)
	if model.Path != "" {
		args = append(args, "-m", model.Path)
	}
	if model.MMProj != "" {
		args = append(args, "--mmproj", model.MMProj)
	}
	args = append(args, model.Arguments...)

	if prof.Host != "" && !containsAny(args, "--host", "-a") {
		args = append(args, "--host", prof.Host)
	}
	if prof.Port > 0 && !containsAny(args, "--port") {
		args = append(args, "--port", fmt.Sprintf("%d", prof.Port))
	}

	envMap := make(map[string]string)
	for _, ev := range os.Environ() {
		k, v, ok := strings.Cut(ev, "=")
		if ok {
			envMap[k] = v
		}
	}
	for k, v := range rt.Environment {
		envMap[k] = v
	}
	for k, v := range prof.Environment {
		envMap[k] = v
	}
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	return &CommandSpec{
		Executable:       exePath,
		Args:             args,
		WorkingDirectory: rt.WorkingDirectory,
		Environment:      env,
	}
}

// migrateToV6Model simulates the v5->v6 migration folding of a profile + old
// model into a single v6 Model:
//
//	model.Args = profile.Args + "-m" + path + "--mmproj" + mmproj + model.Arguments
//	model.Host = profile.Host
//	model.Port = profile.Port
//	model.Environment = profile.Environment
func migrateToV6Model(prof v5ProfileCfg, model v5ModelCfg) *Model {
	args := make([]string, 0)
	args = append(args, prof.Args...)
	if model.Path != "" {
		args = append(args, "-m", model.Path)
	}
	if model.MMProj != "" {
		args = append(args, "--mmproj", model.MMProj)
	}
	args = append(args, model.Arguments...)

	return &Model{
		ID:             "migrated",
		Name:           "migrated",
		RuntimeID:      "rt1",
		Args:           args,
		Host:           prof.Host,
		Port:           prof.Port,
		Environment:    prof.Environment,
		Active:         true,
		AutostartDelay: 0,
	}
}

func containsAny(args []string, values ...string) bool {
	for _, a := range args {
		for _, v := range values {
			if a == v {
				return true
			}
		}
	}
	return false
}

// assertSpecEquivalent verifies that the v6 CommandSpec is observationally
// equivalent to the v5 resolved command: same executable, args, working
// directory, and the same environment for every variable v5 defined via
// runtime/profile overrides. The parent environment is inherited by the OS at
// spawn time in both v5 and v6, so it is not part of the spec-level comparison.
func assertSpecEquivalent(t *testing.T, r *LaunchResolver, got, want *CommandSpec, rt *Runtime, prof v5ProfileCfg) {
	t.Helper()

	if got.Executable != want.Executable {
		t.Errorf("executable = %q, want %q", got.Executable, want.Executable)
	}
	if got.WorkingDirectory != want.WorkingDirectory {
		t.Errorf("working directory = %q, want %q", got.WorkingDirectory, want.WorkingDirectory)
	}
	if !reflect.DeepEqual(got.Args, want.Args) {
		t.Errorf("args = %#v, want %#v", got.Args, want.Args)
	}

	wantEnv := make(map[string]string)
	for k, v := range rt.Environment {
		wantEnv[r.normalizeKey(k)] = v
	}
	for k, v := range prof.Environment {
		wantEnv[r.normalizeKey(k)] = v
	}
	gotEnv := make(map[string]string, len(got.Environment))
	for _, ev := range got.Environment {
		k, v, ok := strings.Cut(ev, "=")
		if ok {
			gotEnv[r.normalizeKey(k)] = v
		}
	}
	if !reflect.DeepEqual(gotEnv, wantEnv) {
		t.Errorf("environment = %#v, want %#v", gotEnv, wantEnv)
	}
}

func TestCommandEquivalence(t *testing.T) {
	r := NewLaunchResolver()

	t.Run("FullConfig", func(t *testing.T) {
		rt := &Runtime{
			ID:               "rt1",
			Name:             "llama.cpp",
			Executable:       "llama-server",
			WorkingDirectory: filepath.Join("runtimes", "llama.cpp"),
			DefaultArgs:      []string{"--alias", "goal"},
			Environment:      map[string]string{"RT_VAR": "rt", "SHARED": "from-runtime"},
		}
		prof := v5ProfileCfg{
			Host:        "0.0.0.0",
			Port:        8085,
			Args:        []string{"-ngl", "99"},
			Environment: map[string]string{"PROFILE_VAR": "profile", "SHARED": "from-profile"},
		}
		model := v5ModelCfg{
			Path:      filepath.Join("models", "qwen.gguf"),
			MMProj:    filepath.Join("models", "mmproj.gguf"),
			Arguments: []string{"-c", "200000"},
		}

		want := v5ResolvedCommand(t, rt, prof, model)
		got, err := r.Preview(migrateToV6Model(prof, model), rt, nil, nil)
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		assertSpecEquivalent(t, r, got, want, rt, prof)
	})

	t.Run("Minimal", func(t *testing.T) {
		rt := &Runtime{
			ID:               "rt1",
			Name:             "llama.cpp",
			Executable:       "llama-server",
			WorkingDirectory: filepath.Join("runtimes", "llama.cpp"),
		}
		prof := v5ProfileCfg{}
		model := v5ModelCfg{Path: filepath.Join("models", "small.gguf")}

		want := v5ResolvedCommand(t, rt, prof, model)
		got, err := r.Preview(migrateToV6Model(prof, model), rt, nil, nil)
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		assertSpecEquivalent(t, r, got, want, rt, prof)
	})

	t.Run("NoHostPort", func(t *testing.T) {
		rt := &Runtime{
			ID:               "rt1",
			Name:             "llama.cpp",
			Executable:       "llama-server",
			WorkingDirectory: filepath.Join("runtimes", "llama.cpp"),
			DefaultArgs:      []string{"--alias", "goal"},
		}
		prof := v5ProfileCfg{
			Host: "",
			Port: 0,
			Args: []string{"-ngl", "99"},
		}
		model := v5ModelCfg{
			Path:      filepath.Join("models", "qwen.gguf"),
			MMProj:    filepath.Join("models", "mmproj.gguf"),
			Arguments: []string{"-c", "200000"},
		}

		want := v5ResolvedCommand(t, rt, prof, model)
		if containsAny(want.Args, "--host", "--port") {
			t.Fatalf("v5 baseline unexpectedly contains host/port flags: %#v", want.Args)
		}
		got, err := r.Preview(migrateToV6Model(prof, model), rt, nil, nil)
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		assertSpecEquivalent(t, r, got, want, rt, prof)
		if containsAny(got.Args, "--host", "--port") {
			t.Errorf("v6 args unexpectedly contain host/port flags: %#v", got.Args)
		}
	})

	t.Run("HostAlreadyInArgs", func(t *testing.T) {
		rt := &Runtime{
			ID:          "rt1",
			Name:        "llama.cpp",
			Executable:  "llama-server",
			DefaultArgs: []string{"-c", "4096"},
			Environment: map[string]string{"RT_VAR": "rt"},
		}
		prof := v5ProfileCfg{
			Host:        "127.0.0.1",
			Port:        8080,
			Args:        []string{"--host", "0.0.0.0", "-ngl", "99"},
			Environment: map[string]string{"PROFILE_VAR": "profile"},
		}
		model := v5ModelCfg{
			Path:      filepath.Join("models", "qwen.gguf"),
			Arguments: []string{"-c", "200000"},
		}

		want := v5ResolvedCommand(t, rt, prof, model)
		got, err := r.Preview(migrateToV6Model(prof, model), rt, nil, nil)
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		assertSpecEquivalent(t, r, got, want, rt, prof)

		hostCount := 0
		for i, a := range got.Args {
			if a == "--host" || a == "-a" {
				hostCount++
				if i+1 < len(got.Args) && got.Args[i+1] != "0.0.0.0" {
					t.Errorf("first --host value = %q, want 0.0.0.0 (profile args win, no second flag added)", got.Args[i+1])
				}
			}
		}
		if hostCount != 1 {
			t.Errorf("expected exactly 1 --host flag, got %d. Args: %#v", hostCount, got.Args)
		}
	})
}
