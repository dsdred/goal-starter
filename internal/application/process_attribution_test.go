package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/resource"
	"github.com/dsdred/goal/internal/storage"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// ADR 026 Amendment A-026-1 A-2: the InstanceAttributor seam is a read-only
// projection of the existing listing path. It emits exactly the A-1 states
// (starting/running/stopping/orphan), carries the durable launch triple and
// the join IDs verbatim, and adds no lifecycle semantics.

func newAttributionEnv(t *testing.T) (*InstanceService, *process.Supervisor, storage.Repository, string) {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "goal_repo.json"))
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	fakePath, err := filepath.EvalSymlinks(fakeruntime.Path(t))
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-attr", Name: "attr-runtime", Executable: fakePath}); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{
		ID: "m-attr", Name: "attr-model", RuntimeID: "rt-attr", Args: []string{"infinite"},
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	sup := process.NewSupervisor(repo)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = sup.ShutdownWithPersistence(ctx)
	})
	return NewInstanceService(sup, repo), sup, repo, fakePath
}

func findSnapshot(list []resource.TrackedProcessSnapshot, id string) (resource.TrackedProcessSnapshot, bool) {
	for _, s := range list {
		if s.InstanceID == id {
			return s, true
		}
	}
	return resource.TrackedProcessSnapshot{}, false
}

func TestTrackedLiveInstancesEmitsRunningAndOrphanOnly(t *testing.T) {
	svc, _, repo, fakePath := newAttributionEnv(t)
	ctx := context.Background()

	// Store rows: an orphan of a DIFFERENT model (one-live-per-model keeps it
	// out of the launch arbitration) that must surface, and a terminal exited
	// row that must never surface.
	if err := repo.CreateModel(&storage.ModelEntry{
		ID: "m-orphan", Name: "orphan-model", RuntimeID: "rt-attr", Args: []string{"infinite"},
	}); err != nil {
		t.Fatalf("CreateModel m-orphan: %v", err)
	}
	if err := repo.CreateInstance(&storage.LaunchInstanceEntry{
		ID: "orphan-1", ModelID: "m-orphan", RuntimeID: "rt-attr", State: "orphan",
		PID: 4_000_000, Executable: fakePath, StartedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := repo.CreateInstance(&storage.LaunchInstanceEntry{
		ID: "exited-1", ModelID: "m-orphan", RuntimeID: "rt-attr", State: "exited",
		PID: 4_000_001, Executable: fakePath, CreatedAt: time.Now().Add(-2 * time.Hour),
		StartedAt: time.Now().Add(-2 * time.Hour), StoppedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed exited: %v", err)
	}

	inst, err := svc.StartModel(ctx, "m-attr")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}

	// Bounded poll until the listing path reports running (fake-runtime is
	// pre-warmed; the window is a startup upper bound, not a synchronization
	// substitute).
	deadline := time.Now().Add(15 * time.Second)
	var tracked []resource.TrackedProcessSnapshot
	for {
		tracked = svc.TrackedLiveInstances(ctx)
		if s, ok := findSnapshot(tracked, string(inst.ID)); ok && s.PID > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance %s never became tracked: %+v", inst.ID, tracked)
		}
		time.Sleep(20 * time.Millisecond)
	}

	run, _ := findSnapshot(tracked, string(inst.ID))
	if run.ModelID != "m-attr" || run.RuntimeID != "rt-attr" {
		t.Fatalf("running snapshot join IDs = %+v", run)
	}
	if run.Executable != fakePath {
		t.Fatalf("running snapshot executable = %q, want the recorded launch path %q", run.Executable, fakePath)
	}
	if !run.HasStartedAt || run.StartedAt.IsZero() {
		t.Fatalf("running snapshot start-time anchor = %+v, want the recorded StartedAt", run)
	}

	orphan, ok := findSnapshot(tracked, "orphan-1")
	if !ok {
		t.Fatalf("store orphan must be attributable (A-3): %+v", tracked)
	}
	if orphan.PID != 4_000_000 || orphan.Executable != fakePath || !orphan.HasStartedAt {
		t.Fatalf("orphan snapshot = %+v", orphan)
	}

	// The exited store row never appears.
	if _, ok := findSnapshot(tracked, "exited-1"); ok {
		t.Fatal("terminal exited row must not be emitted (A-1 emitted states only)")
	}

	// After the instance reaches a terminal state it stops being tracked; the
	// orphan (a store fact) stays tracked until reconciled.
	if err := svc.StopInstance(ctx, domain.InstanceID(inst.ID)); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	tracked = svc.TrackedLiveInstances(ctx)
	if _, ok := findSnapshot(tracked, string(inst.ID)); ok {
		t.Fatal("stopped instance still tracked after the terminal transition")
	}
	if _, ok := findSnapshot(tracked, "orphan-1"); !ok {
		t.Fatal("orphan disappeared")
	}
}

func TestTrackedLiveInstancesNoInstancesIsHonestEmpty(t *testing.T) {
	svc, _, _, _ := newAttributionEnv(t)
	list := svc.TrackedLiveInstances(context.Background())
	if list == nil || len(list) != 0 {
		t.Fatalf("empty tracked set = %#v, want non-nil empty slice", list)
	}
}
