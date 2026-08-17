package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_NoDuplicateHostPort(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	r := NewLaunchResolver()
	profile := &Profile{
		ID:        "p1",
		Name:      "real-scenario",
		RuntimeID: "r1",
		Host:      "0.0.0.0",
		Port:      8085,
		// Profile.Args is empty in the real scenario
	}
	runtime := &Runtime{
		ID:               "r1",
		Name:             "llama.cpp 10444",
		Executable:       "llama-server",
		WorkingDirectory: dir,
		// DefaultArgs is empty in the real scenario
	}
	model := &Model{
		ID:   "m1",
		Name: "Qwen3.8-27B-Q4_K_M",
		Arguments: []string{
			"-ngl", "999",
			"-c", "200000",
			"-np", "1",
			"-ctk", "q8_0",
			"-ctv", "q8_0",
			"-fa", "on",
			"--jinja",
		},
		Path:   filepath.Join(dir, "model.gguf"),
		MMProj: filepath.Join(dir, "mmproj.gguf"),
	}

	// Create fake model files so Resolve doesn't fail on stat
	_ = os.WriteFile(model.Path, []byte("x"), 0644)
	_ = os.WriteFile(model.MMProj, []byte("x"), 0644)

	spec, err := r.Resolve(profile, runtime, model, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Add model args (same as ResolveToInstance does)
	r.AddModelArgs(spec, model)

	// Check exactly one --host
	hostCount := 0
	for i, a := range spec.Args {
		if a == "--host" || a == "-a" {
			hostCount++
			// Next arg should be the value
			if i+1 < len(spec.Args) && spec.Args[i+1] != "0.0.0.0" {
				t.Errorf("--host value = %q, want 0.0.0.0", spec.Args[i+1])
			}
		}
	}
	if hostCount != 1 {
		t.Errorf("expected exactly 1 --host, got %d. Args: %v", hostCount, spec.Args)
	}

	// Check exactly one --port
	portCount := 0
	for i, a := range spec.Args {
		if a == "--port" {
			portCount++
			if i+1 < len(spec.Args) && spec.Args[i+1] != "8085" {
				t.Errorf("--port value = %q, want 8085", spec.Args[i+1])
			}
		}
	}
	if portCount != 1 {
		t.Errorf("expected exactly 1 --port, got %d. Args: %v", portCount, spec.Args)
	}

	// Check exactly one -m (model path)
	mCount := 0
	for _, a := range spec.Args {
		if a == "-m" {
			mCount++
		}
	}
	if mCount != 1 {
		t.Errorf("expected exactly 1 -m, got %d", mCount)
	}

	// Check executable is resolved
	if !strings.Contains(spec.Executable, "llama-server") {
		t.Errorf("executable should contain llama-server, got %q", spec.Executable)
	}

	t.Logf("Effective command:\n  exe: %s\n  args: %v", spec.Executable, spec.Args)
}

func TestResolve_NoDuplicateWhenRuntimeHasHostPort(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	r := NewLaunchResolver()
	profile := &Profile{ID: "p1", Name: "t", RuntimeID: "r1", Host: "127.0.0.1", Port: 8080}
	runtime := &Runtime{
		ID:               "r1",
		Name:             "rt",
		Executable:       "server",
		WorkingDirectory: dir,
		DefaultArgs:      []string{"--host", "0.0.0.0", "--port", "1111"},
	}

	spec, err := r.Resolve(profile, runtime, nil, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Runtime already has --host and --port, so profile values should NOT be added
	hostCount := 0
	portCount := 0
	for _, a := range spec.Args {
		if a == "--host" || a == "-a" {
			hostCount++
		}
		if a == "--port" {
			portCount++
		}
	}
	if hostCount != 1 {
		t.Errorf("expected 1 --host (from runtime defaults), got %d. Args: %v", hostCount, spec.Args)
	}
	if portCount != 1 {
		t.Errorf("expected 1 --port (from runtime defaults), got %d. Args: %v", portCount, spec.Args)
	}
}

func TestResolve_ModelArgsNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "model.gguf")
	_ = os.WriteFile(modelPath, []byte("x"), 0644)

	r := NewLaunchResolver()
	profile := &Profile{ID: "p1", Name: "t", RuntimeID: "r1", Host: "127.0.0.1", Port: 8080}
	runtime := &Runtime{ID: "r1", Name: "rt", Executable: "server", WorkingDirectory: dir}
	model := &Model{ID: "m1", Name: "m", Path: modelPath, Arguments: []string{"-ngl", "999"}}

	// ResolveToInstance calls Resolve + AddModelArgs internally
	inst, err := r.ResolveToInstance(profile, runtime, model, nil, nil)
	if err != nil {
		t.Fatalf("ResolveToInstance: %v", err)
	}

	// Count -m occurrences
	mCount := 0
	for _, a := range inst.Args {
		if a == "-m" {
			mCount++
		}
	}
	if mCount != 1 {
		t.Errorf("expected 1 -m in instance args, got %d. Args: %v", mCount, inst.Args)
	}

	// Count -ngl occurrences (should be 1, from model args only)
	nglCount := 0
	for _, a := range inst.Args {
		if a == "-ngl" {
			nglCount++
		}
	}
	if nglCount != 1 {
		t.Errorf("expected 1 -ngl, got %d. Args: %v", nglCount, inst.Args)
	}
}
