package domain

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func absToolPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `E:\tools\llama10444`
	}
	return "/opt/tools/llama10444"
}

func absOtherPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `E:\other\llama-server.exe`
	}
	return "/opt/other/llama-server"
}

func TestResolveExecutablePath_RelativeWithWorkDir(t *testing.T) {
	wd := absToolPath(t)
	name := filepath.Base(absOtherPath(t))
	got := resolveExecutablePath(name, wd)
	want := filepath.Join(wd, name)
	if got != want {
		t.Errorf("resolveExecutablePath = %q, want %q", got, want)
	}
}

func TestResolveExecutablePath_RelativeWithDotPrefix(t *testing.T) {
	wd := absToolPath(t)
	got := resolveExecutablePath(filepath.Join(".", "llama-server"), wd)
	want := filepath.Join(wd, "llama-server")
	if got != want {
		t.Errorf("resolveExecutablePath = %q, want %q", got, want)
	}
}

func TestResolveExecutablePath_Absolute(t *testing.T) {
	abs := absOtherPath(t)
	if !filepath.IsAbs(abs) {
		t.Fatalf("test path %q is not absolute on %s", abs, runtime.GOOS)
	}
	wd := absToolPath(t)
	got := resolveExecutablePath(abs, wd)
	if got != abs {
		t.Errorf("absolute path should remain unchanged, got %q", got)
	}
}

func TestResolveExecutablePath_EmptyWorkDir(t *testing.T) {
	// With empty WorkingDirectory, relative path is returned as-is
	// (will be resolved by the OS against the process CWD)
	got := resolveExecutablePath("llama-server", "")
	if got != "llama-server" {
		t.Errorf("expected unchanged relative path with empty workdir, got %q", got)
	}
}

func TestResolve_PreviewMatchesExecution_ExecutablePath(t *testing.T) {
	// Create a temp directory with a fake executable
	dir := t.TempDir()
	exePath := filepath.Join(dir, "fake-server")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0"), 0755); err != nil {
		t.Fatal(err)
	}

	r := NewLaunchResolver()
	profile := &Profile{ID: "p1", Name: "test", RuntimeID: "r1", Host: "127.0.0.1", Port: 8085}
	runtime := &Runtime{ID: "r1", Name: "rt", Executable: filepath.Base(exePath), WorkingDirectory: dir}
	model := &Model{ID: "m1", Name: "m", Path: dir}

	// Execution path
	specExec, err := r.Resolve(profile, runtime, model, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Preview path
	specPrev, err := r.Preview(profile, runtime, model, nil, nil)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if specExec.Executable != specPrev.Executable {
		t.Errorf("executable mismatch: Resolve=%q Preview=%q", specExec.Executable, specPrev.Executable)
	}

	// Both should resolve to the absolute path
	expected := filepath.Join(dir, filepath.Base(exePath))
	if specExec.Executable != expected {
		t.Errorf("Resolve executable = %q, want %q", specExec.Executable, expected)
	}
	if specPrev.Executable != expected {
		t.Errorf("Preview executable = %q, want %q", specPrev.Executable, expected)
	}
}

func TestResolve_AbsoluteExecutable(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "my-server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	r := NewLaunchResolver()
	profile := &Profile{ID: "p1", Name: "test", RuntimeID: "r1", Host: "0.0.0.0", Port: 9999}
	runtime := &Runtime{ID: "r1", Name: "rt", Executable: exePath, WorkingDirectory: dir}

	spec, err := r.Resolve(profile, runtime, nil, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.Executable != exePath {
		t.Errorf("expected absolute path unchanged, got %q", spec.Executable)
	}
}
