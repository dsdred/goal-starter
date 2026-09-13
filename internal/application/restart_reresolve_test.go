package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// This file is the permanent regression suite for the restart stale-launch-
// snapshot correction: InstanceService.RestartInstance must relaunch with the
// CURRENT repository configuration (args, environment, runtime), ownership-
// aware, preserving InstanceID and pipeline attribution.

func newRestartEnv(t *testing.T) (*pipelineEnv, *InstanceService) {
	t.Helper()
	e := newPipelineEnv(t)
	return e, NewInstanceService(e.sup, e.repo)
}

func waitForFileContent(t *testing.T, path, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), needle) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s did not contain %q within %v", path, needle, timeout)
}

func waitFileRewrite(t *testing.T, path string, old time.Time, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, err := os.Stat(path); err == nil && !st.ModTime().Equal(old) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

// activeOwnedInstance polls the repository until exactly one active instance
// of the model exists (the state transition running-persist is observable
// shortly after Start returns).
func activeOwnedInstance(t *testing.T, e *pipelineEnv, modelID string) *storage.LaunchInstanceEntry {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		insts := e.instancesFor(t, modelID)
		var active []*storage.LaunchInstanceEntry
		for _, i := range insts {
			if isActiveInstanceState(i.State) {
				active = append(active, i)
			}
		}
		if len(active) == 1 {
			return active[0]
		}
		if time.Now().After(deadline) {
			states := make([]string, 0, len(insts))
			for _, i := range insts {
				states = append(states, fmt.Sprintf("%s(last_error=%q)", i.State, i.LastError))
			}
			t.Fatalf("expected one active instance of %s within timeout, got %v", modelID, states)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mtime(t *testing.T, path string) time.Time {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.ModTime()
}

func updateModel(t *testing.T, e *pipelineEnv, id string, mutate func(m *storage.ModelEntry)) {
	t.Helper()
	m, err := e.repo.GetModel(id)
	if err != nil {
		t.Fatalf("GetModel %s: %v", id, err)
	}
	mutate(m)
	if err := e.repo.UpdateModel(m); err != nil {
		t.Fatalf("UpdateModel %s: %v", id, err)
	}
}

// 2. Model Args: start with A, edit to B, restart -> child receives B.
func TestRestartInstance_ReresolvesCurrentModelArgs(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	argvPath := filepath.Join(t.TempDir(), "argv.txt")

	e.addModel(t, "m1", "one", "argv-file", argvPath, "--mark-A")
	inst, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	firstPID := inst.PID
	waitForFileContent(t, argvPath, "--mark-A", 5*time.Second)
	firstMtime := mtime(t, argvPath)

	updateModel(t, e, "m1", func(m *storage.ModelEntry) { m.Args = []string{"argv-file", argvPath, "--mark-B"} })

	restarted, err := insSvc.RestartInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	if restarted.ID != inst.ID {
		t.Fatalf("InstanceID changed: %s -> %s", inst.ID, restarted.ID)
	}
	if restarted.PID == firstPID || restarted.PID <= 0 {
		t.Fatalf("PID did not change after restart (first=%d, now=%d)", firstPID, restarted.PID)
	}

	waitFileRewrite(t, argvPath, firstMtime, 10*time.Second)
	eff := readFile(t, argvPath)
	if !strings.Contains(eff, "--mark-B") || strings.Contains(eff, "--mark-A") {
		t.Fatalf("child did not receive current Model.Args: %q", eff)
	}
}

// 3. Environment: start with env A, edit to B, restart -> child env is B.
// (env-file mode exits after writing, so this also proves the terminal-state
// restart path re-resolves.)
func TestRestartInstance_ReresolvesCurrentEnvironment(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	envPath := filepath.Join(t.TempDir(), "env.txt")

	rt := &storage.RuntimeEntry{ID: "rt1", Name: "rt1", Executable: fakeruntime.Path(t)}
	if err := e.repo.CreateRuntime(rt); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	m := &storage.ModelEntry{ID: "m1", Name: "one", RuntimeID: "rt1", Args: []string{"env-file", envPath, "RESTART_ENV"}, Environment: map[string]string{"RESTART_ENV": "value-A"}}
	if err := e.repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	inst, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	waitForFileContent(t, envPath, "value-A", 5*time.Second)
	firstMtime := mtime(t, envPath)

	// The process has exited (env-file mode) — restart in the terminal state.
	updateModel(t, e, "m1", func(m *storage.ModelEntry) { m.Environment = map[string]string{"RESTART_ENV": "value-B"} })

	if _, err := insSvc.RestartInstance(ctx, inst.ID); err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}

	waitFileRewrite(t, envPath, firstMtime, 10*time.Second)
	eff := readFile(t, envPath)
	if !strings.Contains(eff, "value-B") || strings.Contains(eff, "value-A") {
		t.Fatalf("child did not receive current Model.Environment: %q", eff)
	}
}

// 6. Runtime re-resolution: restart uses the current Model.RuntimeID (runtime
// executable/working directory/environment come from the new runtime).
func TestRestartInstance_ReresolvesRuntime(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	envPath := filepath.Join(t.TempDir(), "env.txt")

	exe := fakeruntime.Path(t)
	dirA := t.TempDir()
	dirB := t.TempDir()
	rtA := &storage.RuntimeEntry{ID: "rtA", Name: "rtA", Executable: exe, WorkingDirectory: dirA, Environment: map[string]string{"RTMARK": "rt-A"}}
	rtB := &storage.RuntimeEntry{ID: "rtB", Name: "rtB", Executable: exe, WorkingDirectory: dirB, Environment: map[string]string{"RTMARK": "rt-B"}}
	if err := e.repo.CreateRuntime(rtA); err != nil {
		t.Fatalf("CreateRuntime rtA: %v", err)
	}
	if err := e.repo.CreateRuntime(rtB); err != nil {
		t.Fatalf("CreateRuntime rtB: %v", err)
	}
	m := &storage.ModelEntry{ID: "m1", Name: "one", RuntimeID: "rtA", Args: []string{"env-file", envPath, "RTMARK"}}
	if err := e.repo.CreateModel(m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	inst, err := insSvc.StartModel(ctx, "m1")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	if inst.RuntimeID != "rtA" {
		t.Fatalf("initial RuntimeID = %q, want rtA", inst.RuntimeID)
	}
	waitForFileContent(t, envPath, "rt-A", 5*time.Second)
	firstMtime := mtime(t, envPath)

	updateModel(t, e, "m1", func(m *storage.ModelEntry) { m.RuntimeID = "rtB" })

	restarted, err := insSvc.RestartInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	if restarted.RuntimeID != "rtB" {
		t.Fatalf("RuntimeID not refreshed: %q", restarted.RuntimeID)
	}

	waitFileRewrite(t, envPath, firstMtime, 10*time.Second)
	eff := readFile(t, envPath)
	if !strings.Contains(eff, "rt-B") || strings.Contains(eff, "rt-A") {
		t.Fatalf("child did not receive the current runtime configuration: %q", eff)
	}
}

// 4. Pipeline FROM MODEL: restart of a pipeline-owned instance picks up the
// current Model.Args; attribution and InstanceID are preserved.
func TestRestartInstance_PipelineOwnedFromModelReresolves(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	argvPath := filepath.Join(t.TempDir(), "argv.txt")

	m1 := e.addModel(t, "m1", "one", "argv-file", argvPath, "--model-A")
	pipe := e.addPipeline(t, "from-model", storage.PipelineModel{ModelID: m1})
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("pipeline Start: %v", err)
	}
	inst0 := activeOwnedInstance(t, e, m1)
	instID := domain.InstanceID(inst0.ID)
	entryID := inst0.PipelineEntryID
	if inst0.PipelineID != pipe || entryID == "" {
		t.Fatalf("attribution missing: %+v", inst0)
	}
	waitForFileContent(t, argvPath, "--model-A", 5*time.Second)
	firstMtime := mtime(t, argvPath)

	updateModel(t, e, m1, func(m *storage.ModelEntry) { m.Args = []string{"argv-file", argvPath, "--model-B"} })

	restarted, err := insSvc.RestartInstance(ctx, instID)
	if err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	if restarted.ID != instID {
		t.Fatalf("InstanceID changed: %s -> %s", instID, restarted.ID)
	}
	if restarted.PipelineID != pipe || restarted.PipelineEntryID != entryID {
		t.Fatalf("attribution not preserved: %+v", restarted)
	}

	waitFileRewrite(t, argvPath, firstMtime, 10*time.Second)
	eff := readFile(t, argvPath)
	if !strings.Contains(eff, "--model-B") || strings.Contains(eff, "--model-A") {
		t.Fatalf("pipeline FROM-MODEL restart did not use current Model.Args: %q", eff)
	}
}

// 5. Pipeline CUSTOM: restart keeps the current PipelineEntry.Args
// (all-or-nothing, no Model.Args fallback or merge), and follows entry-args
// edits.
func TestRestartInstance_PipelineOwnedCustomPreserved(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	argvPath := filepath.Join(t.TempDir(), "argv.txt")

	m1 := e.addModel(t, "m1", "one", "argv-file", argvPath, "--model-A")
	pipe := e.addPipeline(t, "custom", storage.PipelineModel{ModelID: m1, Args: []string{"argv-file", argvPath, "--custom-C"}})
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("pipeline Start: %v", err)
	}
	inst0 := activeOwnedInstance(t, e, m1)
	instID := domain.InstanceID(inst0.ID)
	entryID := inst0.PipelineEntryID
	waitForFileContent(t, argvPath, "--custom-C", 5*time.Second)
	firstMtime := mtime(t, argvPath)

	// Edit the model: a CUSTOM restart must NOT pick this up.
	updateModel(t, e, m1, func(m *storage.ModelEntry) { m.Args = []string{"argv-file", argvPath, "--model-B"} })

	if _, err := insSvc.RestartInstance(ctx, instID); err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	waitFileRewrite(t, argvPath, firstMtime, 10*time.Second)
	eff := readFile(t, argvPath)
	if !strings.Contains(eff, "--custom-C") || strings.Contains(eff, "--model-") {
		t.Fatalf("pipeline CUSTOM restart leaked Model.Args: %q", eff)
	}

	// Edit the entry: the restart must follow the CURRENT entry args.
	pe, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	pe.Models[0].Args = []string{"argv-file", argvPath, "--custom-D"}
	if err := e.svc.UpdatePipeline(ctx, pe); err != nil {
		t.Fatalf("UpdatePipeline: %v", err)
	}
	secondMtime := mtime(t, argvPath)

	restarted, err := insSvc.RestartInstance(ctx, instID)
	if err != nil {
		t.Fatalf("RestartInstance (2nd): %v", err)
	}
	if restarted.PipelineID != pipe || restarted.PipelineEntryID != entryID {
		t.Fatalf("attribution not preserved on 2nd restart: %+v", restarted)
	}
	waitFileRewrite(t, argvPath, secondMtime, 10*time.Second)
	eff = readFile(t, argvPath)
	if !strings.Contains(eff, "--custom-D") || strings.Contains(eff, "--custom-C") {
		t.Fatalf("restart did not follow current PipelineEntry.Args: %q", eff)
	}
}

// 7a. Missing Model: restart fails boundedly and does not relaunch the
// frozen snapshot.
func TestRestartInstance_MissingModelFails(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	argvPath := filepath.Join(t.TempDir(), "argv.txt")

	m1 := e.addModel(t, "m1", "one", "env-file", argvPath, "X")
	inst, err := insSvc.StartModel(ctx, m1)
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	waitForFileContent(t, argvPath, "X", 5*time.Second)
	// env-file mode exits: the instance is terminal but still registered.
	if err := e.repo.DeleteModel(m1); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	firstMtime := mtime(t, argvPath)

	if _, err := insSvc.RestartInstance(ctx, inst.ID); err == nil {
		t.Fatal("expected a bounded error for a missing model, got nil")
	} else if !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("expected 'model not found', got: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if st, err := os.Stat(argvPath); err == nil && !st.ModTime().Equal(firstMtime) {
		t.Fatal("frozen snapshot was relaunched for a missing model")
	}
}

// 7b. Missing Runtime: restart fails boundedly and does not relaunch the
// frozen snapshot.
func TestRestartInstance_MissingRuntimeFails(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	argvPath := filepath.Join(t.TempDir(), "argv.txt")

	m1 := e.addModel(t, "m1", "one", "env-file", argvPath, "X")
	inst, err := insSvc.StartModel(ctx, m1)
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	waitForFileContent(t, argvPath, "X", 5*time.Second)
	if err := e.repo.DeleteRuntime(m1 + "-rt"); err != nil {
		t.Fatalf("DeleteRuntime: %v", err)
	}
	firstMtime := mtime(t, argvPath)

	if _, err := insSvc.RestartInstance(ctx, inst.ID); err == nil {
		t.Fatal("expected a bounded error for a missing runtime, got nil")
	} else if !strings.Contains(err.Error(), "runtime not found") {
		t.Fatalf("expected 'runtime not found', got: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if st, err := os.Stat(argvPath); err == nil && !st.ModTime().Equal(firstMtime) {
		t.Fatal("frozen snapshot was relaunched for a missing runtime")
	}
}

// 7c. Pipeline ownership edges: a pipeline-owned terminal instance whose
// entry was removed, and one whose pipeline was deleted, both fail boundedly
// instead of guessing a launch configuration.
func TestRestartInstance_PipelineOwnershipEdgesFail(t *testing.T) {
	e, insSvc := newRestartEnv(t)
	ctx := context.Background()
	argvPath := filepath.Join(t.TempDir(), "argv.txt")

	m1 := e.addModel(t, "m1", "one", "env-file", argvPath, "X")
	// Two entries of the same model (repeatable, ADR 013) so that one entry
	// can be removed while the pipeline keeps the other.
	pipe := e.addPipeline(t, "edges", storage.PipelineModel{ModelID: m1}, storage.PipelineModel{ModelID: m1})
	pe, err := e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	entry1ID := pe.Models[0].ID
	entry2ID := pe.Models[1].ID
	if entry1ID == "" || entry2ID == "" || entry1ID == entry2ID {
		t.Fatalf("expected distinct backfilled entry ids: %+v", pe.Models)
	}
	if _, err := e.svc.Start(ctx, pipe); err != nil {
		t.Fatalf("pipeline Start: %v", err)
	}
	insts := e.instancesFor(t, m1)
	if len(insts) != 2 {
		t.Fatalf("expected two active instances, got %+v", insts)
	}
	var instA, instB domain.InstanceID
	for _, i := range insts {
		switch i.PipelineEntryID {
		case entry1ID:
			instA = domain.InstanceID(i.ID)
		case entry2ID:
			instB = domain.InstanceID(i.ID)
		}
	}
	if instA == "" || instB == "" {
		t.Fatalf("could not attribute the two instances: %+v", insts)
	}
	waitForFileContent(t, argvPath, "X", 5*time.Second)
	e.stopPipeline(t, pipe)

	// Remove entry1 (allowed: no active owned instances).
	pe, err = e.repo.GetPipeline(pipe)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	pe.Models = pe.Models[1:2]
	if err := e.svc.UpdatePipeline(ctx, pe); err != nil {
		t.Fatalf("UpdatePipeline (remove entry1): %v", err)
	}

	if _, err := insSvc.RestartInstance(ctx, instA); err == nil {
		t.Fatal("expected a bounded error for a removed pipeline entry, got nil")
	} else if !strings.Contains(err.Error(), "pipeline entry") {
		t.Fatalf("expected an entry-ownership error, got: %v", err)
	}

	// Delete the pipeline entirely: the other edge.
	if err := e.svc.DeletePipeline(ctx, pipe); err != nil {
		t.Fatalf("DeletePipeline: %v", err)
	}
	if _, err := insSvc.RestartInstance(ctx, instB); err == nil {
		t.Fatal("expected a bounded error for a deleted pipeline, got nil")
	} else if !strings.Contains(err.Error(), "pipeline not found") {
		t.Fatalf("expected 'pipeline not found', got: %v", err)
	}
}
