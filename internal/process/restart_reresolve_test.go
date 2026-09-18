package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// TestSupervisorRestartWithLaunch_FreshSpec proves the re-resolved launch
// configuration (args + environment) is what the restarted child process
// receives, while the InstanceID is preserved and the PID changes.
func TestSupervisorRestartWithLaunch_FreshSpec(t *testing.T) {
	tmp := t.TempDir()
	argvPath := filepath.Join(tmp, "argv.txt")

	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	modelA := &domain.Model{ID: "m1", Name: "test", RuntimeID: "rt1", Args: []string{"argv-file", argvPath, "--mark-A"}}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	ctx := context.Background()

	inst, err := sup.start(ctx, modelA, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	firstPID := inst.PID
	if firstPID <= 0 {
		t.Fatal("expected a PID after start")
	}
	if err := waitForFileContentContaining(argvPath, "--mark-A", 5*time.Second); err != nil {
		t.Fatalf("first launch argv not captured: %v", err)
	}
	firstMtime := fileMtime(t, argvPath)
	firstCreatedAt := inst.CreatedAt

	// Current configuration after an owner edit: args A -> B, new env value.
	modelB := &domain.Model{ID: "m1", Name: "test", RuntimeID: "rt1", Args: []string{"argv-file", argvPath, "--mark-B"}}
	restarted, err := sup.RestartWithLaunch(ctx, inst.ID, modelB, rt, nil, map[string]string{"FORENSIC_ENV": "value-B"})
	if err != nil {
		t.Fatalf("restart with launch failed: %v", err)
	}

	if restarted.ID != inst.ID {
		t.Fatalf("InstanceID changed on restart: %s -> %s", inst.ID, restarted.ID)
	}
	if restarted.PID == firstPID {
		t.Fatalf("PID did not change after restart: %d", firstPID)
	}
	if restarted.PID <= 0 {
		t.Fatal("expected a new PID after restart")
	}
	if !restarted.CreatedAt.Equal(firstCreatedAt) {
		t.Fatalf("record identity (CreatedAt) changed on restart: %v -> %v", firstCreatedAt, restarted.CreatedAt)
	}
	if restarted.Environment["FORENSIC_ENV"] != "value-B" {
		t.Fatalf("refreshed environment missing FORENSIC_ENV=value-B: %q", restarted.Environment["FORENSIC_ENV"])
	}

	waitForFileRewrite(t, argvPath, firstMtime, 10*time.Second)
	eff := readTrimmed(t, argvPath)
	if !strings.Contains(eff, "--mark-B") || strings.Contains(eff, "--mark-A") {
		t.Fatalf("child did not receive the fresh args: %q", eff)
	}

	stopCtx, stopCancel := context.WithTimeout(ctx, 10*time.Second)
	defer stopCancel()
	if err := sup.Stop(stopCtx, inst.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	sup.mu.RLock()
	ctrl := sup.instances[inst.ID]
	sup.mu.RUnlock()
	if err := waitForProcess(ctx, ctrl, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestSupervisorRestartWithLaunch_ResolveFailure proves a failed re-resolution
// neither relaunches the frozen snapshot nor mutates the instance.
func TestSupervisorRestartWithLaunch_ResolveFailure(t *testing.T) {
	tmp := t.TempDir()
	argvPath := filepath.Join(tmp, "argv.txt")

	store := newMockStore()
	sup := newTestSupervisor(t, store, SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	model := &domain.Model{ID: "m1", Name: "test", RuntimeID: "rt1", Args: []string{"argv-file", argvPath, "--mark-A"}}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	ctx := context.Background()

	inst, err := sup.start(ctx, model, rt, nil, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := waitForFileContentContaining(argvPath, "--mark-A", 5*time.Second); err != nil {
		t.Fatalf("first launch argv not captured: %v", err)
	}
	firstMtime := fileMtime(t, argvPath)

	// A runtime with an empty executable cannot be resolved.
	badRT := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: ""}
	if _, err := sup.RestartWithLaunch(ctx, inst.ID, model, badRT, nil, nil); err == nil {
		t.Fatal("expected a resolve error, got nil")
	} else if !strings.Contains(err.Error(), "resolve instance") {
		t.Fatalf("expected a bounded resolve error, got: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if st, err := os.Stat(argvPath); err == nil && !st.ModTime().Equal(firstMtime) {
		t.Fatal("frozen snapshot was relaunched after a resolve failure")
	}
	snap, err := sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(snap.Args[len(snap.Args)-1], "--mark-A") {
		t.Fatalf("instance launch fields were mutated by a failed restart: %v", snap.Args)
	}

	stopCtx, stopCancel := context.WithTimeout(ctx, 10*time.Second)
	defer stopCancel()
	if err := sup.Stop(stopCtx, inst.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	sup.mu.RLock()
	ctrl := sup.instances[inst.ID]
	sup.mu.RUnlock()
	if err := waitForProcess(ctx, ctrl, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

func waitForFileContentContaining(path, needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), needle) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return os.ErrNotExist
}

func waitForFileRewrite(t *testing.T, path string, oldMtime time.Time, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, err := os.Stat(path); err == nil && !st.ModTime().Equal(oldMtime) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

func fileMtime(t *testing.T, path string) time.Time {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.ModTime()
}
