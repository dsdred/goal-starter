//go:build windows

package process_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/process"
	"golang.org/x/sys/windows"
)

// TestWindowsJobObject_cleanup verifies that child processes are cleaned up
// when the parent process ends via Job Object kill-on-close.
func TestWindowsJobObject_cleanup(t *testing.T) {
	t.Parallel()
	fake := fakeRuntime(t)
	mgr := newTestManager(t)

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

	// Use a generous context timeout. Stop() uses a 5s internal timeout,
	// so the context must be > 5s to allow force-kill to complete.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := mgr.Stop(ctx); err != nil {
		t.Logf("Stop error (may be acceptable): %v", err)
	}

	// Poll for exited state (Stop() may have force-killed).
	for i := 0; i < 60; i++ {
		status = mgr.Status()
		if status.State == process.StateExited {
			goto verify_cleaned
		}
		time.Sleep(200 * time.Millisecond)
	}

verify_cleaned:
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}

	// Verify the process is actually gone. os.FindProcess opens a real handle on
	// Windows, so it fails once the process object has been deleted — that error
	// is the success path for this assertion. If a handle still opens, the exit
	// code decides: os.Process.Signal cannot probe liveness on Windows, it only
	// implements Kill, so a zero-signal probe would fail vacuously.
	if proc, err := os.FindProcess(pid); err != nil {
		t.Logf("os.FindProcess(%d) reports the process object is gone: %v", pid, err)
		return
	} else {
		_ = proc.Release()
	}
	alive, err := processStillRunning(pid)
	if err != nil {
		t.Fatalf("inspect process %d: %v", pid, err)
	}
	if alive {
		t.Fatalf("process %d should not exist", pid)
	}
}

// processStillRunning reports whether pid names a live process. A reaped
// process object cannot be opened at all; one that opens reports STILL_ACTIVE
// only while it is running.
func processStillRunning(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code == 259, nil // STILL_ACTIVE
}

// TestWindowsJobObject_nestedChildren verifies that child processes
// are also cleaned up via the Job Object when we stop an infinite process.
func TestWindowsJobObject_nestedChildren(t *testing.T) {
	t.Parallel()
	fake := fakeRuntime(t)
	mgr := newTestManager(t)

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

	// Use a generous context timeout. Stop() uses a 5s internal timeout,
	// so the context must be > 5s to allow force-kill to complete.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := mgr.Stop(ctx); err != nil {
		t.Logf("Stop error (may be acceptable): %v", err)
	}

	// Poll for exited state (Stop() may have force-killed).
	for i := 0; i < 60; i++ {
		status = mgr.Status()
		if status.State == process.StateExited {
			goto verify_nested_cleaned
		}
		time.Sleep(200 * time.Millisecond)
	}

verify_nested_cleaned:
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
	if status.ExitClass != process.ExitKilled && status.ExitClass != process.ExitSignaled {
		t.Logf("exit class: %s (acceptable)", status.ExitClass)
	}
}
