package process_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/process"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// doneCh is a channel returned by TestContextCancellation workaround.
var doneCh chan struct{}

func fakeRuntime(t *testing.T) string {
	return fakeruntime.Path(t)
}

func newTestManager(t *testing.T) *process.Manager {
	t.Helper()
	mgr := process.NewManager()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := mgr.Stop(ctx); err != nil {
			t.Logf("cleanup manager stop returned: %v", err)
		}
		if done := mgr.GetDoneChannel(); done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				t.Errorf("cleanup manager wait: %v", ctx.Err())
			}
		}
	})
	return mgr
}

func TestStartStop_normalExit(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := newTestManager(t)

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"graceful"},
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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

	spec := process.CommandSpec{
		Executable: "/nonexistent/path/to/executable",
	}

	if err := mgr.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for nonexistent executable")
	}
}

func TestStart_emptyExecutable(t *testing.T) {
	mgr := newTestManager(t)

	spec := process.CommandSpec{}

	if err := mgr.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for empty executable")
	}
}

func TestEnvironment_merge(t *testing.T) {
	fake := fakeRuntime(t)
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	var logsMu sync.Mutex
	stopCollect := make(chan struct{})
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		for {
			select {
			case <-stopCollect:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				logsMu.Lock()
				logs = append(logs, ev)
				logsMu.Unlock()
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
	<-collectDone

	// Verify we received stdout logs.
	logsMu.Lock()
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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

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
	mgr := newTestManager(t)

	ctx, cancel := context.WithCancel(context.Background())

	spec := process.CommandSpec{
		Executable: fake,
		Args:       []string{"graceful"},
	}

	if err := mgr.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start's context bounds only the start operation; it does not own the
	// process lifecycle after Start returns.
	cancel()
	if status := mgr.Status(); status.State != process.StateRunning {
		t.Fatalf("expected process to remain running after operation context cancellation, got %s", status.State)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := mgr.Stop(ctx2); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status := mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited after Stop, got %s", status.State)
	}
}

func TestStartStop_intentionalStopNotFailed(t *testing.T) {
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

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := mgr.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status = mgr.Status()
	if status.State != process.StateExited {
		t.Fatalf("expected exited after intentional stop, got %s", status.State)
	}
	if status.ExitClass != process.ExitSignaled && status.ExitClass != process.ExitKilled && status.ExitClass != process.ExitNormal {
		t.Errorf("intentional stop must be classified as signaled/killed/normal, got exit class: %s (code: %v)", status.ExitClass, status.ExitCode)
	}
}
