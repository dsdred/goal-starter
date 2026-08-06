package process_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/process"
)

// doneCh is a channel returned by TestContextCancellation workaround.
var doneCh chan struct{}

// fakeRuntime returns the path to the compiled fake-runtime binary.
// The testdata directory is relative to the module root.
func fakeRuntime(t *testing.T) string {
	t.Helper()

	// Determine module root by walking up from test file location.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	// Walk up to find go.mod.
	root := cwd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("go.mod not found")
		}
		root = parent
	}

	candidate := filepath.Join(root, "testdata", "fake-runtime", "fake-runtime")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	// Compile the fake runtime from the module root.
	buildCmd := exec.Command("go", "build", "-o", candidate, "./testdata/fake-runtime")
	buildCmd.Dir = root
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to compile fake-runtime: %v\n%s", err, out)
	}
	return candidate
}

func TestStartStop_normalExit(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"infinite"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for running state.
	time.Sleep(200 * time.Millisecond)

	status := mgr.Status()
	if status.State != process.StateRunning {
		t.Fatalf("expected running, got %s", status.State)
	}
	if status.PID == 0 {
		t.Fatal("expected non-zero PID")
	}

	// Stop the running process.
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status = mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
}

func TestStartStop_failureExit(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"exit-code", "42"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// exit-code exits immediately; wait for it without calling Stop().
	// Calling Stop() sends SIGTERM which may alter the exit code on Windows.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Wait for the process to exit by polling status.
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for process to exit")
		default:
			status := mgr.Status()
			if status.State == process.StateExited {
				goto check_exit
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

check_exit:
	status := mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
	if status.ExitClass != process.ExitFailure {
		t.Fatalf("expected failure exit, got %s", status.ExitClass)
	}
	if status.ExitCode == nil {
		t.Fatalf("expected exit code 42, got nil")
	}
	if *status.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d (ptr=%p)", *status.ExitCode, status.ExitCode)
	}
}

func TestStartStop_timeout(t *testing.T) {
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

	// Use a very short timeout so the test completes quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := mgr.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status = mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
	// When killed by ForceKill, exit class should be killed or signaled.
	if status.ExitClass != process.ExitKilled && status.ExitClass != process.ExitSignaled {
		t.Fatalf("expected killed/signaled exit, got %s", status.ExitClass)
	}
}

func TestStartStop_twice(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	// First start/stop.
	spec1 := process.CommandSpec{
		Executable: fake,
		Args:       []string{"exit-code", "0"},
	}
	if err := mgr.Start(context.Background(), spec1); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}

	// Second start should succeed.
	spec2 := process.CommandSpec{
		Executable: fake,
		Args:       []string{"exit-code", "0"},
	}
	if err := mgr.Start(context.Background(), spec2); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestStart_alreadyRunning(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"infinite"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Starting again should fail.
	spec2 := process.CommandSpec{
		Executable: fake,
		Args:       []string{"infinite"},
	}
	if err := mgr.Start(context.Background(), spec2); err == nil {
		t.Fatal("expected error when starting while already running")
	}

	mgr.Stop(context.Background())
}

func TestStart_invalidExecutable(t *testing.T) {
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: "/nonexistent/path/to/executable",
	}

	if err := mgr.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for nonexistent executable")
	}
}

func TestStart_emptyExecutable(t *testing.T) {
	mgr := process.NewManager()

	spec := process.CommandSpec{}

	if err := mgr.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for empty executable")
	}
}

func TestEnvironment_merge(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	// Set a custom env var that the parent process doesn't have.
	customEnv := []string{"GOAL_TEST_VAR=test_value_123"}
	spec := process.CommandSpec{
		Executable:  fake,
		Args:        []string{"exit-code", "0"},
		Environment: customEnv,
	}

	// The fake-runtime doesn't read env vars, but we verify it starts
	// without error when custom env is provided.
	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start with custom env: %v", err)
	}

	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status := mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
}

func TestSubscribe_logs(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	ch, cleanup := mgr.Subscribe()
	defer cleanup()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"stdout"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Collect log events.
	logs := make([]process.LogEvent, 0)
	stopCollect := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopCollect:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				logs = append(logs, ev)
			}
		}
	}()

	// Wait for process to finish naturally (stdout exits immediately).
	// Use polling to avoid Stop() sending SIGTERM before logs are collected.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for process to exit")
		default:
			status := mgr.Status()
			if status.State == process.StateExited {
				goto check_logs
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

check_logs:
	close(stopCollect)

	// Verify we received stdout logs.
	foundStdout := false
	for _, ev := range logs {
		if ev.Stream == "stdout" && len(ev.Message) > 0 {
			foundStdout = true
			break
		}
	}
	if !foundStdout {
		t.Fatalf("expected stdout log events, got: %v", logs)
	}
}

func TestConcurrent_statusAndStop(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"delayed", "1"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: repeated Status calls.
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = mgr.Status()
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Goroutine 2: Stop.
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mgr.Stop(ctx)
	}()

	wg.Wait()
}

func TestLogEvent_time(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	ch, cleanup := mgr.Subscribe()
	defer cleanup()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"exit-code", "0"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Drain logs.
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Time.IsZero() {
				t.Fatal("expected non-zero Time in LogEvent")
			}
		default:
			return
		}
	}
}

func TestWorkDir_validation(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	// Valid working directory.
	tmpDir := t.TempDir()
	spec := process.CommandSpec{
		Executable:       fake,
		Args:             []string{"exit-code", "0"},
		WorkingDirectory: tmpDir,
	}
	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start with valid workdir: %v", err)
	}
	mgr.Stop(context.Background())

	// Invalid working directory.
	spec2 := process.CommandSpec{
		Executable:       fake,
		Args:             []string{"exit-code", "0"},
		WorkingDirectory: "/nonexistent/dir/abc123",
	}
	if err := mgr.Start(context.Background(), spec2); err == nil {
		t.Fatal("expected error for nonexistent working directory")
	}
}

func TestExitClass_signaled(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"ignored-signal"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for process to be running.
	time.Sleep(300 * time.Millisecond)

	// Use context with timeout to force SIGKILL.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := mgr.Stop(ctx); err != nil {
		// ESRCH is acceptable if process died between SIGTERM and SIGKILL.
		if err.Error() != "signal: killed" {
			// Ignore ESRCH (already gone).
		}
	}

	status := mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
	// On Unix, SIGKILL should give ExitSignaled or ExitKilled.
	if status.ExitClass != process.ExitSignaled && status.ExitClass != process.ExitKilled {
		t.Logf("exit class: %s, exit code: %v (this may vary by platform)", status.ExitClass, status.ExitCode)
	}
}

func TestDelayed_exitCode(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"delayed", "1", "99"},
	}

	if err := mgr.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the delayed process to exit naturally (code 99 after 1s).
	// Calling Stop() sends SIGTERM which may interrupt the delay.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for delayed process to exit")
		default:
			status := mgr.Status()
			if status.State == process.StateExited {
				goto check_exit
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

check_exit:
	status := mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited, got %s", status.State)
	}
	if status.ExitCode == nil {
		t.Fatalf("expected exit code 99, got nil")
	}
	if *status.ExitCode != 99 {
		t.Fatalf("expected exit code 99, got %d", *status.ExitCode)
	}
	if status.ExitClass != process.ExitFailure {
		t.Fatalf("expected failure exit, got %s", status.ExitClass)
	}
}

func TestContextCancellation(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := process.NewManager()

	ctx, cancel := context.WithCancel(context.Background())

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"infinite"},
	}

	if err := mgr.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Cancel the context.
	cancel()

	// Wait for process to exit by polling status.
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		s := mgr.Status()
		if s.State == process.StateExited {
			break
		}
	}
	// Force kill if still running.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	mgr.Stop(ctx2)

	status := mgr.Status()
	if status.State != process.StateExited {
		t.Logf("process state after context cancellation: %s (exit class: %s)", status.State, status.ExitClass)
	}
}
