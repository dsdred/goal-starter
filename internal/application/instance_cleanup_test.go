package application

import (
	"context"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/storage"
)

func seedInstance(t *testing.T, repo storage.Repository, id, state string, stoppedAgo time.Duration) {
	t.Helper()
	var stoppedAt time.Time
	if stoppedAgo > 0 {
		stoppedAt = time.Now().Add(-stoppedAgo)
	}
	err := repo.CreateInstance(&storage.LaunchInstanceEntry{
		ID:        id,
		ModelID:   "model-x",
		RuntimeID: "rt-x",
		State:     state,
		StoppedAt: stoppedAt,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateInstance %s: %v", id, err)
	}
}

func listStates(t *testing.T, repo storage.Repository) map[string]string {
	t.Helper()
	instances, err := repo.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	states := make(map[string]string, len(instances))
	for _, inst := range instances {
		states[inst.ID] = inst.State
	}
	return states
}

func TestCleanupInstances_AllTerminal(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	seedInstance(t, repo, "exited-1", "exited", time.Hour)
	seedInstance(t, repo, "failed-1", "failed", time.Hour)
	seedInstance(t, repo, "stale-1", "stale", time.Hour)
	seedInstance(t, repo, "running-1", "running", 0)
	seedInstance(t, repo, "starting-1", "starting", 0)
	seedInstance(t, repo, "stopping-1", "stopping", 0)
	seedInstance(t, repo, "pending-1", "pending", 0)

	deleted, err := svc.CleanupInstances(ctx, "all_terminal", nil)
	if err != nil {
		t.Fatalf("CleanupInstances all_terminal: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted: got %d, want 3", deleted)
	}

	states := listStates(t, repo)
	for _, id := range []string{"exited-1", "failed-1", "stale-1"} {
		if _, ok := states[id]; ok {
			t.Errorf("terminal instance %s should be deleted", id)
		}
	}
	for _, id := range []string{"running-1", "starting-1", "stopping-1", "pending-1"} {
		if _, ok := states[id]; !ok {
			t.Errorf("active instance %s must not be deleted", id)
		}
	}
}

func TestCleanupInstances_OlderThan7d(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	seedInstance(t, repo, "old-exited", "exited", 8*24*time.Hour)
	seedInstance(t, repo, "recent-exited", "exited", 24*time.Hour)
	seedInstance(t, repo, "no-stop-exited", "exited", 0)
	seedInstance(t, repo, "old-running", "running", 8*24*time.Hour)

	deleted, err := svc.CleanupInstances(ctx, "older_than_7d", nil)
	if err != nil {
		t.Fatalf("CleanupInstances older_than_7d: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted: got %d, want 1", deleted)
	}

	states := listStates(t, repo)
	if _, ok := states["old-exited"]; ok {
		t.Error("instance older than 7d should be deleted")
	}
	for _, id := range []string{"recent-exited", "no-stop-exited", "old-running"} {
		if _, ok := states[id]; !ok {
			t.Errorf("instance %s must not be deleted", id)
		}
	}
}

func TestCleanupInstances_OlderThan30d(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	seedInstance(t, repo, "old-exited", "exited", 31*24*time.Hour)
	seedInstance(t, repo, "mid-exited", "exited", 15*24*time.Hour)
	seedInstance(t, repo, "old-failed", "failed", 40*24*time.Hour)

	deleted, err := svc.CleanupInstances(ctx, "older_than_30d", nil)
	if err != nil {
		t.Fatalf("CleanupInstances older_than_30d: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted: got %d, want 2", deleted)
	}

	states := listStates(t, repo)
	for _, id := range []string{"old-exited", "old-failed"} {
		if _, ok := states[id]; ok {
			t.Errorf("instance %s older than 30d should be deleted", id)
		}
	}
	if _, ok := states["mid-exited"]; !ok {
		t.Error("instance within 30d must not be deleted")
	}
}

func TestCleanupInstances_Selected(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	seedInstance(t, repo, "exited-1", "exited", time.Hour)
	seedInstance(t, repo, "exited-2", "exited", time.Hour)
	seedInstance(t, repo, "failed-1", "failed", time.Hour)
	seedInstance(t, repo, "running-1", "running", 0)

	deleted, err := svc.CleanupInstances(ctx, "selected", []string{"exited-1", "running-1"})
	if err != nil {
		t.Fatalf("CleanupInstances selected: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted: got %d, want 1 (running must be skipped)", deleted)
	}

	states := listStates(t, repo)
	if _, ok := states["exited-1"]; ok {
		t.Error("selected terminal instance should be deleted")
	}
	for _, id := range []string{"exited-2", "failed-1", "running-1"} {
		if _, ok := states[id]; !ok {
			t.Errorf("instance %s must not be deleted", id)
		}
	}
}

func TestCleanupInstances_SelectedAllNonTerminal(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	seedInstance(t, repo, "running-1", "running", 0)
	seedInstance(t, repo, "pending-1", "pending", 0)

	deleted, err := svc.CleanupInstances(ctx, "selected", []string{"running-1", "pending-1"})
	if err != nil {
		t.Fatalf("CleanupInstances selected: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted: got %d, want 0 (no terminal instances selected)", deleted)
	}

	states := listStates(t, repo)
	for _, id := range []string{"running-1", "pending-1"} {
		if _, ok := states[id]; !ok {
			t.Errorf("active instance %s must not be deleted", id)
		}
	}
}

func TestCleanupInstances_InvalidMode(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	if _, err := svc.CleanupInstances(ctx, "bogus", nil); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestCleanupInstances_EmptyRepo(t *testing.T) {
	_, _, repo := setupReplaceTest(t)
	ctx := context.Background()
	svc := NewInstanceService(nil, repo)

	deleted, err := svc.CleanupInstances(ctx, "all_terminal", nil)
	if err != nil {
		t.Fatalf("CleanupInstances: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted: got %d, want 0", deleted)
	}
}
