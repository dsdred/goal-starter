//go:build windows

package platform

import (
	"os/exec"
	"testing"
	"time"

	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// TestGracefulStop_GCEInapplicable_NoError verifies the fallback contract of
// GracefulStop: when GenerateConsoleCtrlEvent cannot deliver an event (here
// forced with a dead PID whose process group does not exist — the same
// failure class as a server process running without a console), the stop must
// escalate to Job Object termination and return no error. Before the fix this
// path returned the raw "not supported by windows" (syscall.EWINDOWS) error
// from a broken Process.Signal(os.Interrupt) fallback.
func TestGracefulStop_GCEInapplicable_NoError(t *testing.T) {
	fake := fakeruntime.Path(t)

	// Obtain a process handle whose process group no longer exists.
	short := exec.Command(fake, "exit-code", "0")
	if err := short.Start(); err != nil {
		t.Fatalf("start short-lived process: %v", err)
	}
	shortProc := short.Process
	if err := short.Wait(); err != nil {
		t.Fatalf("wait short-lived process: %v", err)
	}

	cmd := exec.Command(fake, "infinite")
	ctrl, err := prepare(cmd)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer ctrl.Close()

	ctrl.(*windowsControl).proc = shortProc

	if err := ctrl.GracefulStop(); err != nil {
		t.Fatalf("GracefulStop with inapplicable GCE must return nil, got: %v", err)
	}
}

// TestGracefulStop_RealProcess_Terminates verifies the end-to-end contract:
// a managed process is terminated by GracefulStop with no error, either via
// the console control event (server has a console) or via immediate Job
// Object termination (headless server). The "graceful" fake-runtime mode
// exits 0 on SIGINT (CTRL_BREAK is mapped to os.Interrupt by the Go runtime),
// so the process terminates on both paths.
func TestGracefulStop_RealProcess_Terminates(t *testing.T) {
	fake := fakeruntime.Path(t)

	cmd := exec.Command(fake, "graceful")
	ctrl, err := prepare(cmd)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := ctrl.AfterStart(cmd.Process.Pid); err != nil {
		t.Fatalf("after start: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.ForceKill()
		_ = ctrl.Close()
	})

	if err := ctrl.GracefulStop(); err != nil {
		t.Fatalf("GracefulStop: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = ctrl.ForceKill()
		<-done
		t.Fatal("process did not terminate after GracefulStop")
	}
}
