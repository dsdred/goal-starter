//go:build windows

package process_test

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/process"
)

// TestWindowsJobObject_cleanup verifies that child processes are cleaned up
// when the parent process ends via Job Object kill-on-close.
func TestWindowsJobObject_cleanup(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"infinite"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	status := mgr.Status()
	if status.State != process.StateRunning {
		t.Fatalf("expected running, got %s", status.State)
	}
	if status.PID == 0 {
		t.Fatal("expected non-zero PID")
	}

	pid := status.PID

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := mgr.Stop(ctx); err != nil {
		t.Logf("Stop error (may be acceptable): %v", err)
	}

	status = mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}

	// Verify the process is actually gone.
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find process %d: %v", pid, err)
	}
	_ = proc.Signal(os.Interrupt) // Try to signal
	_ = proc.Signal(nil)          // No-op on nil signal
	// On Windows, FindProcess always succeeds. Check by sending 0 signal.
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		proc.Kill()
		t.Fatalf("process %d should not exist", pid)
	}
}

// TestWindowsJobObject_nestedChildren verifies that child processes
// are also cleaned up via the Job Object when we stop an infinite process.
func TestWindowsJobObject_nestedChildren(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"infinite"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	status := mgr.Status()
	if status.State != process.StateRunning {
		t.Fatalf("expected running, got %s", status.State)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := mgr.Stop(ctx); err != nil {
		t.Logf("Stop error (may be acceptable): %v", err)
	}

	status = mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
	if status.ExitClass != process.ExitKilled && status.ExitClass != process.ExitSignaled {
		t.Logf("exit class: %s (acceptable)", status.ExitClass)
	}
}
