package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
)

func TestVarResolve_Start_UsesCurrentProcessEnv(t *testing.T) {
	t.Setenv("GOAL_VAR_START_TEST", "start-val")
	e := newPipelineEnv(t)
	svc := NewInstanceService(e.sup, e.repo)
	e.sup.SetDataDir(t.TempDir())

	e.addModel(t, "m1", "M1", "--tag", "${GOAL_VAR_START_TEST}")

	inst, err := svc.StartModel(context.Background(), "m1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.StopInstance(context.Background(), inst.ID)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, _ := e.sup.Status(inst.ID)
		if snap != nil && len(snap.Args) > 1 {
			found := false
			for _, a := range snap.Args {
				if a == "start-val" {
					found = true
				}
			}
			if found {
				return
			}
			if snap.State == "stopped" || snap.State == "failed" {
				t.Fatalf("instance terminal %q; args=%v (var not resolved)", snap.State, snap.Args)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatal("timed out")
}

func TestVarResolve_Restart_UsesChangedProcessEnv(t *testing.T) {
	e := newPipelineEnv(t)
	svc := NewInstanceService(e.sup, e.repo)
	e.sup.SetDataDir(t.TempDir())

	t.Setenv("GOAL_VAR_RESTART_TEST", "A")
	e.addModel(t, "m1", "M1", "--tag", "${GOAL_VAR_RESTART_TEST}")

	inst, err := svc.StartModel(context.Background(), "m1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.StopInstance(context.Background(), inst.ID)

	// Wait for args to be populated (instance may fail at exec, that's fine)
	waitForInstanceArgs(t, e.sup, inst.ID, 10*time.Second)

	// Change the environment variable and restart
	t.Setenv("GOAL_VAR_RESTART_TEST", "B")
	inst2, err := svc.RestartInstance(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if string(inst2.ID) != string(inst.ID) {
		t.Fatalf("instance ID changed: %s -> %s", inst.ID, inst2.ID)
	}

	// Wait for the restarted instance to have args
	waitForInstanceArgs(t, e.sup, inst2.ID, 10*time.Second)

	snap, _ := e.sup.Status(inst2.ID)
	if snap == nil {
		t.Fatal("status nil")
	}
	found := false
	for _, a := range snap.Args {
		if a == "B" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restart did not pick up new env value; args = %v", snap.Args)
	}
}

func TestVarResolve_PipelineFromModel(t *testing.T) {
	t.Setenv("GOAL_VAR_PIPE_FROM", "pipe-from-val")
	e := newPipelineEnv(t)
	e.sup.SetDataDir(t.TempDir())

	e.addModel(t, "m1", "M1", "--tag", "${GOAL_VAR_PIPE_FROM}")
	pipeID := e.addPipeline(t, "P1", storage.PipelineModel{ID: "pe1", ModelID: "m1", AutoStart: false})

	result, err := e.svc.Start(context.Background(), pipeID)
	if err != nil {
		t.Fatalf("pipeline start: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results = %d", len(result.Results))
	}
	if result.Results[0].Status != "started" {
		t.Fatalf("entry status = %s, err = %s", result.Results[0].Status, result.Results[0].Error)
	}

	instID := domain.InstanceID(result.Results[0].InstanceID)
	waitForInstanceArgs(t, e.sup, instID, 10*time.Second)

	snap, _ := e.sup.Status(instID)
	if snap == nil {
		t.Fatal("instance not found")
	}
	found := false
	for _, a := range snap.Args {
		if a == "pipe-from-val" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pipeline FROM MODEL did not resolve var; args = %v", snap.Args)
	}
	e.svc.Stop(context.Background(), pipeID)
}

func TestVarResolve_PipelineCustomArgs(t *testing.T) {
	t.Setenv("GOAL_VAR_PIPE_CUSTOM", "custom-resolved")
	e := newPipelineEnv(t)
	e.sup.SetDataDir(t.TempDir())

	e.addModel(t, "m1", "M1", "--model-arg", "should-not-appear")
	pipeID := e.addPipeline(t, "P1", storage.PipelineModel{ID: "pe1", ModelID: "m1", Args: []string{"--custom", "${GOAL_VAR_PIPE_CUSTOM}"}, AutoStart: false})

	result, err := e.svc.Start(context.Background(), pipeID)
	if err != nil {
		t.Fatalf("pipeline start: %v", err)
	}
	if result.Results[0].Status != "started" {
		t.Fatalf("entry status = %s", result.Results[0].Status)
	}

	instID := domain.InstanceID(result.Results[0].InstanceID)
	waitForInstanceArgs(t, e.sup, instID, 10*time.Second)

	snap, _ := e.sup.Status(instID)
	if snap == nil {
		t.Fatal("instance not found")
	}
	hasModelArg := false
	hasCustomResolved := false
	for _, a := range snap.Args {
		if a == "should-not-appear" {
			hasModelArg = true
		}
		if a == "custom-resolved" {
			hasCustomResolved = true
		}
	}
	if hasModelArg {
		t.Fatalf("model args leaked into custom launch: %v", snap.Args)
	}
	if !hasCustomResolved {
		t.Fatalf("custom var not resolved: %v", snap.Args)
	}
	e.svc.Stop(context.Background(), pipeID)
}

func TestVarResolve_UndefinedVar_RefusesLaunch(t *testing.T) {
	e := newPipelineEnv(t)
	svc := NewInstanceService(e.sup, e.repo)
	e.sup.SetDataDir(t.TempDir())

	e.addModel(t, "m1", "M1", "--tag", "${DEFINITELY_UNDEFINED_VAR_XYZ_12345}")

	_, err := svc.StartModel(context.Background(), "m1")
	if err == nil {
		t.Fatal("expected launch failure for undefined variable")
	}
	if !strings.Contains(err.Error(), "undefined variable") {
		t.Fatalf("error does not mention undefined variable: %v", err)
	}
	if !strings.Contains(err.Error(), "DEFINITELY_UNDEFINED_VAR_XYZ_12345") {
		t.Fatalf("error does not name the variable: %v", err)
	}
}

func TestVarResolve_MalformedVar_RefusesLaunch(t *testing.T) {
	e := newPipelineEnv(t)
	svc := NewInstanceService(e.sup, e.repo)
	e.sup.SetDataDir(t.TempDir())

	e.addModel(t, "m1", "M1", "--tag", "${123BAD}")

	_, err := svc.StartModel(context.Background(), "m1")
	if err == nil {
		t.Fatal("expected launch failure for malformed variable")
	}
	if !strings.Contains(err.Error(), "invalid variable reference") {
		t.Fatalf("error does not mention invalid variable reference: %v", err)
	}
}

func TestVarResolve_GoalDataInExecutable(t *testing.T) {
	dataDir := t.TempDir()
	e := newPipelineEnv(t)
	svc := NewInstanceService(e.sup, e.repo)
	e.sup.SetDataDir(dataDir)

	rt := &storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "${GOAL_DATA}/nonexistent/server.exe"}
	if err := e.repo.CreateRuntime(rt); err != nil {
		t.Fatal(err)
	}
	m := &storage.ModelEntry{ID: "m1", Name: "M", RuntimeID: "rt1"}
	if err := e.repo.CreateModel(m); err != nil {
		t.Fatal(err)
	}

	_, err := svc.StartModel(context.Background(), "m1")
	if err != nil {
		if strings.Contains(err.Error(), "undefined variable") || strings.Contains(err.Error(), "invalid variable reference") {
			t.Fatalf("variable resolution error instead of exec error: %v", err)
		}
	}
}

func waitForInstanceArgs(t *testing.T, sup interface {
	Status(id domain.InstanceID) (*domain.LaunchInstance, error)
}, id domain.InstanceID, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap, err := sup.Status(id)
		if err == nil && snap != nil && len(snap.Args) > 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap, _ := sup.Status(id)
	if snap != nil {
		t.Fatalf("instance %s: args not populated within %v (state=%q, args=%v, lastError=%q)", id, timeout, snap.State, snap.Args, snap.LastError)
	}
	t.Fatalf("instance %s: status unavailable", id)
}
