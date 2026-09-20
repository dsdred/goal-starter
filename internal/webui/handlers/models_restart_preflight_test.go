package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// TestModelRestart_PreflightPreventsPartialApply proves the ADR 017 D1
// pre-flight atomicity rule for a model-level restart: the whole selected set
// is checked before ANY target is mutated, so a set that contains one
// immediately non-restartable instance (here a pending sibling entry of the
// same ModelID) must leave every member untouched.
func TestModelRestart_PreflightPreventsPartialApply(t *testing.T) {
	repo := newTestRepo(t)
	insStore := newMockInstanceStoreForTest()
	fakePath := fakeruntime.Path(t)

	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt", Name: "rt", Executable: fakePath}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "mX", Name: "X", RuntimeID: "rt"}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	// One slot: entry-1 holds it, entry-2 stays pending behind it.
	sup := process.NewSupervisorWithConfig(insStore, process.SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64})
	rt := &domain.Runtime{ID: "rt", Name: "rt", Executable: fakePath}
	entry1 := &domain.Model{ID: "mX", Name: "X", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "entry-1"}
	entry2 := &domain.Model{ID: "mX", Name: "X", RuntimeID: "rt", PipelineID: "p1", PipelineEntryID: "entry-2"}

	ctx := context.Background()
	running, err := sup.AdmitAndStart(ctx, entry1, rt, domain.PipelineOwner("p1", "entry-1"), []string{"-sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("start entry-1: %v", err)
	}
	if err := waitForSupervisorState(t, sup, running.ID, domain.InstanceStateRunning, 10*time.Second); err != nil {
		t.Fatalf("wait entry-1 running: %v", err)
	}

	pendingCtx, cancelPending := context.WithCancel(ctx)
	pendingDone := make(chan struct{})
	go func() {
		defer close(pendingDone)
		_, _ = sup.AdmitAndStart(pendingCtx, entry2, rt, domain.PipelineOwner("p1", "entry-2"), []string{"-sleep", "60"}, nil)
	}()
	t.Cleanup(func() {
		cancelPending()
		select {
		case <-pendingDone:
		case <-time.After(10 * time.Second):
			t.Errorf("timeout waiting for the pending start goroutine to finish")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(shutdownCtx)
	})

	pendingID := ""
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && pendingID == "" {
		insts, _ := sup.List()
		for _, inst := range insts {
			if inst.ID != running.ID && inst.State == domain.InstanceStatePending {
				pendingID = string(inst.ID)
				break
			}
		}
		if pendingID == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if pendingID == "" {
		t.Fatal("timeout waiting for entry-2 to reach pending")
	}

	pid := running.PID
	insSvc := application.NewInstanceService(sup, repo)
	hModels := NewModelsHandler(application.NewModelService(repo), insSvc, sup, repo, security.NewCSRF())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/mX/restart", nil)
	w := httptest.NewRecorder()
	hModels.Restart(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "launch_in_flight" {
		t.Errorf("error = %q, want launch_in_flight", body["error"])
	}
	if body["code"] != "conflict" {
		t.Errorf("code = %q, want conflict", body["code"])
	}

	// Zero restart mutation across the selected set: the restartable entry-1 is
	// still the same live generation and no third instance appeared.
	snap, err := sup.Status(running.ID)
	if err != nil {
		t.Fatalf("status entry-1: %v", err)
	}
	if snap.State != domain.InstanceStateRunning || snap.PID != pid {
		t.Fatalf("entry-1 was mutated by the refused restart: state=%q pid=%d (was pid=%d)", snap.State, snap.PID, pid)
	}
	psnap, err := sup.Status(domain.InstanceID(pendingID))
	if err != nil {
		t.Fatalf("status entry-2: %v", err)
	}
	if psnap.State != domain.InstanceStatePending {
		t.Fatalf("entry-2 state = %q, want pending (unchanged)", psnap.State)
	}
	insts, _ := sup.List()
	if len(insts) != 2 {
		t.Fatalf("instance count = %d, want the original 2 (no new generation)", len(insts))
	}
}
