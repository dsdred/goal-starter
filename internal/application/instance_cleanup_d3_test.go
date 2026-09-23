package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

// === ADR 017 corrective slice D3: BF-07c cleanup ↔ registry coherence =========
//
// These tests drive the real explicit-cleanup boundary
// (InstanceService.CleanupInstances) against a real repository and a real Supervisor, so the
// required ordering is observed end to end: repository deletion FIRST, registry
// reconciliation only after it, and neither side dropped when the other fails.

// d3WaitTerminal polls the Supervisor until the instance is terminal in memory.
// A real process exit has no cross-package synchronization hook, so this is a
// condition poll with a deadline rather than an arbitrary sleep.
func d3WaitTerminal(t *testing.T, e *pipelineEnv, id domain.InstanceID) *domain.LaunchInstance {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		snap, err := e.sup.Status(id)
		if err != nil {
			t.Fatalf("Status %s: %v", id, err)
		}
		if snap.IsTerminal() {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance %s did not become terminal in time (state %q)", id, snap.State)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func d3Record(t *testing.T, repo storage.Repository, id domain.InstanceID) (*storage.LaunchInstanceEntry, error) {
	t.Helper()
	return repo.GetLaunchInstance(string(id))
}

func d3LiveInstances(t *testing.T, e *pipelineEnv) []*storage.LaunchInstanceEntry {
	t.Helper()
	entries, err := e.repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	var live []*storage.LaunchInstanceEntry
	for _, ent := range entries {
		if domain.InstanceState(ent.State).IsInFlight() {
			live = append(live, ent)
		}
	}
	return live
}

// d3FailingDeleteRepo is the T-D3-2 seam: every repository operation is the real
// one, only the cleanup deletion fails.
type d3FailingDeleteRepo struct {
	storage.Repository
}

func (r *d3FailingDeleteRepo) DeleteTerminalInstances(mode string, ids []string, cutoff time.Time) (int, error) {
	return 0, errors.New("simulated cleanup deletion failure")
}

// T-D3-1: successful explicit cleanup removes the history record AND the exact
// matching terminal controller, without resurrecting either side and without
// touching a live instance.
func TestD3Cleanup_SuccessRemovesRecordAndController(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	envPath := filepath.Join(t.TempDir(), "env.txt")

	// m1 exits by itself (terminal historical instance); m2 stays live.
	e.addModel(t, "m1", "one", "env-file", envPath, "CLEAN_ENV")
	e.addModel(t, "m2", "two", "graceful")
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Environment = map[string]string{"CLEAN_ENV": "value-1"}
	})

	terminal, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel m1: %v", err)
	}
	d3WaitTerminal(t, e, terminal.ID)
	if _, err := d3Record(t, e.repo, terminal.ID); err != nil {
		t.Fatalf("terminal record before cleanup: %v", err)
	}
	if _, err := e.sup.Status(terminal.ID); err != nil {
		t.Fatalf("terminal controller before cleanup: %v", err)
	}

	live, err := insSvc.StartModel(ctx, "m2")
	if err != nil {
		t.Fatalf("StartModel m2: %v", err)
	}

	deleted, err := insSvc.CleanupInstances(ctx, "selected", []string{string(terminal.ID)})
	if err != nil {
		t.Fatalf("CleanupInstances: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	// Both sides of the cleaned instance are gone.
	if _, err := d3Record(t, e.repo, terminal.ID); err == nil {
		t.Error("the history record survived a successful cleanup")
	}
	if _, err := e.sup.Status(terminal.ID); !errors.Is(err, process.ErrInstanceNotFound) {
		t.Errorf("Status after cleanup = %v, want ErrInstanceNotFound", err)
	}
	if list, err := e.sup.List(); err != nil {
		t.Fatalf("List: %v", err)
	} else {
		for _, inst := range list {
			if inst.ID == terminal.ID {
				t.Error("the cleaned instance is still listed by the Supervisor")
			}
		}
	}

	// The live instance and its record are untouched, and a repeat cleanup is a
	// no-op (nothing was resurrected).
	if _, err := e.sup.Status(live.ID); err != nil {
		t.Errorf("live controller was affected by cleanup: %v", err)
	}
	if _, err := d3Record(t, e.repo, live.ID); err != nil {
		t.Errorf("live record was affected by cleanup: %v", err)
	}
	again, err := insSvc.CleanupInstances(ctx, "selected", []string{string(terminal.ID)})
	if err != nil {
		t.Fatalf("repeat CleanupInstances: %v", err)
	}
	if again != 0 {
		t.Fatalf("repeat deleted = %d, want 0", again)
	}
	if _, err := e.sup.Status(terminal.ID); !errors.Is(err, process.ErrInstanceNotFound) {
		t.Errorf("the cleaned controller reappeared after a repeat cleanup: %v", err)
	}
}

// T-D3-2: when the repository deletion fails, nothing is reconciled — the record
// stays, the controller stays, and the historical restart capability is not
// silently destroyed.
func TestD3Cleanup_RepositoryFailureKeepsRecordAndController(t *testing.T) {
	e, _ := newRestartEnv(t)
	ctx := context.Background()
	envPath := filepath.Join(t.TempDir(), "env.txt")
	e.addModel(t, "m1", "one", "env-file", envPath, "KEEP_ENV")
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Environment = map[string]string{"KEEP_ENV": "value-1"}
	})
	insSvc := NewInstanceService(e.sup, &d3FailingDeleteRepo{Repository: e.repo})

	inst, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	d3WaitTerminal(t, e, inst.ID)

	if deleted, err := insSvc.CleanupInstances(ctx, "selected", []string{string(inst.ID)}); err == nil {
		t.Fatal("expected the repository deletion failure to be reported")
	} else if deleted != 0 {
		t.Fatalf("deleted = %d on failure, want 0", deleted)
	}

	if _, err := d3Record(t, e.repo, inst.ID); err != nil {
		t.Errorf("the history record was lost although deletion failed: %v", err)
	}
	snap, err := e.sup.Status(inst.ID)
	if err != nil {
		t.Fatalf("the controller was removed although deletion failed: %v", err)
	}
	if !snap.IsTerminal() {
		t.Fatalf("state after failed cleanup = %q, want terminal", snap.State)
	}
	// Historical restart capability intact: the same InstanceID still restarts.
	restarted, err := insSvc.RestartInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RestartInstance after a failed cleanup: %v", err)
	}
	if restarted.ID != inst.ID {
		t.Fatalf("restart changed the InstanceID: %s -> %s", inst.ID, restarted.ID)
	}
}

// T-D3-7 + T-D3-8: the process-scoped historical restart contract keeps working
// before an explicit cleanup, and the cleaned InstanceID stops being
// addressable afterwards with the current bounded unknown-instance result.
func TestD3Cleanup_HistoricalRestartBeforeAndAfterCleanup(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	envPath := filepath.Join(t.TempDir(), "env.txt")
	e.addModel(t, "m1", "one", "env-file", envPath, "GEN_ENV")
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Environment = map[string]string{"GEN_ENV": "value-A"}
	})

	first, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	waitForFileContent(t, envPath, "value-A", 10*time.Second)
	d3WaitTerminal(t, e, first.ID)

	// Before cleanup: the terminal historical InstanceID is restartable, and the
	// new generation proves it (the current configuration is written out).
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Environment = map[string]string{"GEN_ENV": "value-B"}
	})
	restarted, err := insSvc.RestartInstance(ctx, first.ID)
	if err != nil {
		t.Fatalf("RestartInstance before cleanup: %v", err)
	}
	if restarted.ID != first.ID {
		t.Fatalf("restart changed the InstanceID: %s -> %s", first.ID, restarted.ID)
	}
	waitForFileContent(t, envPath, "value-B", 15*time.Second)
	d3WaitTerminal(t, e, first.ID)

	deleted, err := insSvc.CleanupInstances(ctx, "all_terminal", nil)
	if err != nil {
		t.Fatalf("CleanupInstances: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("all_terminal deleted = %d, want 1", deleted)
	}

	// After cleanup: no resurrection of the historical controller or process.
	if _, err := insSvc.RestartInstance(ctx, first.ID); !errors.Is(err, process.ErrInstanceNotFound) {
		t.Fatalf("RestartInstance after cleanup = %v, want ErrInstanceNotFound", err)
	}
	if _, err := e.sup.Status(first.ID); !errors.Is(err, process.ErrInstanceNotFound) {
		t.Fatalf("Status after cleanup = %v, want ErrInstanceNotFound", err)
	}
	// A relaunched generation would write value-C: the rejected restart must
	// have produced no process at all.
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Environment = map[string]string{"GEN_ENV": "value-C"}
	})
	if _, err := insSvc.RestartInstance(ctx, first.ID); !errors.Is(err, process.ErrInstanceNotFound) {
		t.Fatalf("second RestartInstance after cleanup = %v, want ErrInstanceNotFound", err)
	}
	if eff := readFile(t, envPath); !strings.Contains(eff, "value-B") || strings.Contains(eff, "value-C") {
		t.Fatalf("the cleaned instance relaunched a process: %q", eff)
	}
}

// T-D3-9: the canonical BF-01 scenario stays protected through the application
// boundary, including after a cleanup of the historical instance.
func TestD3Cleanup_BF01CanonicalScenario(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	envPath := filepath.Join(t.TempDir(), "env.txt")
	e.addModel(t, "m1", "one", "env-file", envPath, "OLD_ENV")
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Environment = map[string]string{"OLD_ENV": "gen-1"}
	})

	i1, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel (I1): %v", err)
	}
	waitForFileContent(t, envPath, "gen-1", 10*time.Second)
	d3WaitTerminal(t, e, i1.ID)

	// I2: a second, live generation of the same model (the model configuration
	// now keeps the process running).
	updateModel(t, e, "m1", func(m *storage.ModelEntry) {
		m.Args = []string{"graceful"}
	})
	i2, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel (I2): %v", err)
	}
	if i2.ID == i1.ID {
		t.Fatal("I2 reused I1's InstanceID")
	}

	// Restarting the terminal historical I1 must be refused: I2 is live.
	if _, err := insSvc.RestartInstance(ctx, i1.ID); err == nil {
		t.Fatal("expected the same-model conflict to refuse Restart(I1)")
	} else if !process.HasRejectionReason(err, process.RejInFlight) {
		t.Fatalf("err = %v, want an in-flight arbitration rejection", err)
	}
	if live := d3LiveInstances(t, e); len(live) != 1 || live[0].ID != string(i2.ID) {
		t.Fatalf("live instances after the refused restart = %v, want exactly I2", live)
	}

	// An explicit cleanup of I1 removes its record and controller and still
	// leaves D1 arbitration intact for the live generation.
	if _, err := insSvc.CleanupInstances(ctx, "selected", []string{string(i1.ID)}); err != nil {
		t.Fatalf("CleanupInstances(I1): %v", err)
	}
	if _, err := e.sup.Status(i1.ID); !errors.Is(err, process.ErrInstanceNotFound) {
		t.Fatalf("Status(I1) after cleanup = %v, want ErrInstanceNotFound", err)
	}
	if _, err := insSvc.StartModel(ctx, "m1"); !process.HasRejectionReason(err, process.RejInFlight) {
		t.Fatalf("a second live generation was admitted after cleanup: %v", err)
	}
	if live := d3LiveInstances(t, e); len(live) != 1 || live[0].ID != string(i2.ID) {
		t.Fatalf("live instances after cleanup = %v, want exactly I2", live)
	}
}
