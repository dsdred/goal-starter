package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var fixture struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// Path returns a package-scoped fake runtime built for the current test
// runner's GOOS and GOARCH. TestMain must call Cleanup after all tests finish.
func Path(t testing.TB) string {
	t.Helper()
	fixture.once.Do(build)
	if fixture.err != nil {
		t.Fatalf("build fake runtime: %v", fixture.err)
	}
	return fixture.path
}

func build() {
	root, err := moduleRoot()
	if err != nil {
		fixture.err = err
		return
	}

	fixture.dir, err = os.MkdirTemp("", "goal-fake-runtime-")
	if err != nil {
		fixture.err = fmt.Errorf("create temp directory: %w", err)
		return
	}

	name := "fake-runtime"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	fixture.path = filepath.Join(fixture.dir, name)

	cmd := exec.Command("go", "build", "-o", fixture.path, "./testdata/fake-runtime")
	cmd.Dir = root
	if output, buildErr := cmd.CombinedOutput(); buildErr != nil {
		fixture.err = fmt.Errorf("go build: %w\n%s", buildErr, output)
	}
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}

// Cleanup verifies on Windows that no process still holds the executable,
// then removes the package-scoped fixture directory.
func Cleanup() error {
	if fixture.dir == "" {
		return fixture.err
	}
	if runtime.GOOS == "windows" && fixture.err == nil {
		moved := fixture.path + ".moved"
		if err := os.Rename(fixture.path, moved); err != nil {
			return fmt.Errorf("fake runtime is still in use: %w", err)
		}
		if err := os.Rename(moved, fixture.path); err != nil {
			return fmt.Errorf("restore fake runtime after rename check: %w", err)
		}
	}
	if err := os.RemoveAll(fixture.dir); err != nil {
		return fmt.Errorf("remove fake runtime fixture: %w", err)
	}
	return fixture.err
}
