package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

// TestRecovery_E2E_Orphan_Detect_Dismiss is a real end-to-end acceptance test:
//  1. Start fake-runtime in "infinite" mode (simulates a prior GoAl session)
//  2. Record PID, executable path, start time
//  3. Create a new Supervisor (simulates GoAl restart) with the REAL platform prober
//  4. Call Recover() → instance should be classified as orphan
//  5. Verify orphan is visible and actionable
//  6. Call DismissOrphan() → instance becomes stale
//  7. Verify the process is STILL alive (no signal sent)
//  8. Test harness cleans up the helper process
func TestRecovery_E2E_Orphan_Detect_Dismiss(t *testing.T) {
	fakePath := findFakeRuntime(t)

	// Step 1: Start the helper process (simulates prior session).
	cmd := exec.Command(fakePath, "infinite")
	cmd.Stdout = nil
	cmd.Stderr = nil
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake-runtime: %v", err)
	}
	helperPID := cmd.Process.Pid

	// Ensure cleanup on any exit path.
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			cmd.Process.Kill()
		}
		cmd.Wait()
	})

	// Give the process a moment to fully start.
	time.Sleep(200 * time.Millisecond)

	// Step 2: Verify helper is alive.
	alive, err := platform_IsProcessAlive(helperPID)
	if err != nil || !alive {
		t.Fatalf("helper process %d should be alive: alive=%v err=%v", helperPID, alive, err)
	}

	// Step 3: Set up store with the "prior session" instance entry.
	store := newMockStore()
	resolvedPath, _ := filepath.Abs(fakePath)
	entry := &domain.LaunchInstanceEntry{
		ID:         "e2e-orphan-1",
		State:      "running",
		PID:        helperPID,
		Executable: resolvedPath,
		StartedAt:  startTime,
		CreatedAt:  startTime,
		UpdatedAt:  startTime,
	}
	store.instances["e2e-orphan-1"] = entry

	// Step 4: Create a new Supervisor (simulates restart) with the REAL prober.
	sup := NewSupervisor(store)
	// sup.prober is already set to platform.NewRecoveryProber() in NewSupervisor.

	// Step 5: Run recovery.
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Step 6: Verify classified as orphan.
	classified := store.instances["e2e-orphan-1"]
	if classified.State != "orphan" {
		t.Fatalf("expected orphan, got %s (reason=%q)", classified.State, classified.RecoveryReason)
	}
	t.Log("✓ Instance classified as orphan (identity confirmed)")

	// Step 7: Verify orphan is NOT active.
	dom := domain.ToDomain(classified)
	if dom.IsActive() {
		t.Error("orphan must not be active")
	}
	if dom.IsTerminal() {
		t.Error("orphan must not be terminal")
	}

	// Step 8: Dismiss the orphan.
	if err := sup.DismissOrphan(context.Background(), "e2e-orphan-1"); err != nil {
		t.Fatalf("DismissOrphan: %v", err)
	}

	// Step 9: Verify state is now stale.
	dismissed := store.instances["e2e-orphan-1"]
	if dismissed.State != "stale" {
		t.Errorf("after dismiss: expected stale, got %s", dismissed.State)
	}
	if dismissed.RecoveryReason != "reconciled-by-user" {
		t.Errorf("after dismiss: expected reconciled-by-user, got %q", dismissed.RecoveryReason)
	}
	t.Log("✓ Orphan dismissed → stale (reconciled-by-user)")

	// Step 10: Verify the helper process is STILL alive (no signal was sent).
	aliveAfter, err := platform_IsProcessAlive(helperPID)
	if err != nil || !aliveAfter {
		t.Errorf("helper process must still be alive after Dismiss: alive=%v err=%v", aliveAfter, err)
	}
	t.Log("✓ Helper process still alive after Dismiss (no signal sent)")
}

// findFakeRuntime locates or builds the fake-runtime test binary.
func findFakeRuntime(t *testing.T) string {
	t.Helper()
	var name string
	if runtime.GOOS == "windows" {
		name = "fake-runtime.exe"
	} else {
		name = "fake-runtime"
	}
	binPath := filepath.Join("..", "..", "testdata", "fake-runtime", name)
	srcDir := filepath.Join("..", "..", "testdata", "fake-runtime")

	build := func() error {
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = srcDir
		return cmd.Run()
	}

	if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
		if runtime.GOOS != "windows" {
			if info.Mode()&0111 == 0 {
				if err := build(); err != nil {
					t.Skipf("fake-runtime not executable and rebuild failed: %v", err)
					return ""
				}
			}
		}
		return binPath
	}

	if err := build(); err != nil {
		t.Skipf("fake-runtime binary not found and build failed: %v", err)
		return ""
	}
	return binPath
}

// platform_IsProcessAlive wraps the platform prober for test use.
func platform_IsProcessAlive(pid int) (bool, error) {
	prober := platform.NewRecoveryProber()
	return prober.IsProcessAlive(pid)
}
