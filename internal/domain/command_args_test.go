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
	model := &Model{
		ID:        "m1",
		Name:      "Qwen3.8-27B",
		RuntimeID: "r1",
		Args: []string{
			"-m", filepath.Join(dir, "model.gguf"),
			"--mmproj", filepath.Join(dir, "mmproj.gguf"),
			"-ngl", "999",
			"-c", "200000",
		},
		Host: "0.0.0.0",
		Port: 8085,
	}
	runtime := &Runtime{
		ID:               "r1",
		Name:             "llama.cpp",
		Executable:       "llama-server",
		WorkingDirectory: dir,
	}

	spec, err := r.Resolve(model, runtime, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	hostCount := 0
	for i, a := range spec.Args {
		if a == "--host" || a == "-a" {
			hostCount++
			if i+1 < len(spec.Args) && spec.Args[i+1] != "0.0.0.0" {
				t.Errorf("--host value = %q, want 0.0.0.0", spec.Args[i+1])
			}
		}
	}
	if hostCount != 1 {
		t.Errorf("expected exactly 1 --host, got %d. Args: %v", hostCount, spec.Args)
	}

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

	mCount := 0
	for _, a := range spec.Args {
		if a == "-m" {
			mCount++
		}
	}
	if mCount != 1 {
		t.Errorf("expected exactly 1 -m, got %d", mCount)
	}

	if !strings.Contains(spec.Executable, "llama-server") {
		t.Errorf("executable should contain llama-server, got %q", spec.Executable)
	}
}

func TestResolve_NoDuplicateWhenRuntimeHasHostPort(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	r := NewLaunchResolver()
	model := &Model{ID: "m1", Name: "t", RuntimeID: "r1", Host: "127.0.0.1", Port: 8080}
	runtime := &Runtime{
		ID:               "r1",
		Name:             "rt",
		Executable:       "server",
		WorkingDirectory: dir,
		DefaultArgs:      []string{"--host", "0.0.0.0", "--port", "1111"},
	}

	spec, err := r.Resolve(model, runtime, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

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

func TestResolveToInstance_ModelArgsInArgs(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "model.gguf")
	_ = os.WriteFile(modelPath, []byte("x"), 0644)

	r := NewLaunchResolver()
	model := &Model{
		ID:        "m1",
		Name:      "m",
		RuntimeID: "r1",
		Args:      []string{"-m", modelPath, "-ngl", "999"},
		Host:      "127.0.0.1",
		Port:      8080,
	}
	runtime := &Runtime{ID: "r1", Name: "rt", Executable: "server", WorkingDirectory: dir}

	inst, err := r.ResolveToInstance(model, runtime, nil, nil)
	if err != nil {
		t.Fatalf("ResolveToInstance: %v", err)
	}

	mCount := 0
	for _, a := range inst.Args {
		if a == "-m" {
			mCount++
		}
	}
	if mCount != 1 {
		t.Errorf("expected 1 -m in instance args, got %d. Args: %v", mCount, inst.Args)
	}

	nglCount := 0
	for _, a := range inst.Args {
		if a == "-ngl" {
			nglCount++
		}
	}
	if nglCount != 1 {
		t.Errorf("expected 1 -ngl, got %d. Args: %v", nglCount, inst.Args)
	}

	if inst.ModelID != "m1" {
		t.Errorf("expected ModelID=m1, got %q", inst.ModelID)
	}
}
