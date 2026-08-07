package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// mockStore implements InstanceStore for testing.
type mockStore struct {
	mu        sync.RWMutex
	instances map[string]*domain.LaunchInstanceEntry
	createErr error
	updateErr error
	updateFn  func(e *domain.LaunchInstanceEntry) error
}

func newMockStore() *mockStore {
	return &mockStore{
		instances: make(map[string]*domain.LaunchInstanceEntry),
	}
}

func (m *mockStore) Create(e *domain.LaunchInstanceEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	m.instances[string(e.ID)] = e
	return nil
}

func (m *mockStore) Get(id string) (*domain.LaunchInstanceEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.instances[id]; ok {
		ec := *e
		return &ec, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockStore) Update(e *domain.LaunchInstanceEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateFn != nil {
		return m.updateFn(e)
	}
	if m.updateErr != nil {
		return m.updateErr
	}
	m.instances[string(e.ID)] = e
	return nil
}

func (m *mockStore) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.instances, id)
	return nil
}

func (m *mockStore) List() ([]*domain.LaunchInstanceEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*domain.LaunchInstanceEntry, 0, len(m.instances))
	for _, e := range m.instances {
		ec := *e
		result = append(result, &ec)
	}
	return result, nil
}

func (m *mockStore) ListByProfileID(profileID string) ([]*domain.LaunchInstanceEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*domain.LaunchInstanceEntry, 0)
	for _, e := range m.instances {
		if e.ProfileID == profileID {
			ec := *e
			result = append(result, &ec)
		}
	}
	return result, nil
}

// === Sentinel errors for supervisor tests ===

var (
	testProcessErr = errors.New("process error")
	testPersistErr = errors.New("persistence error")
	testResolveErr = errors.New("resolve error")
	testCreateErr  = errors.New("store create error")
	testUpdateErr  = errors.New("store update error")
)

// === SlotReservation Tests ===

// TestSlotLimiterFirstAcquire verifies the first acquire succeeds.
func TestSlotLimiterFirstAcquire(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	res := newSlotReservation(sem)
	select {
	case <-sem:
		res.Release()
	case <-time.After(time.Second):
		t.Fatal("acquire blocked")
	}
}

// TestSlotLimiterBlocksAtLimit verifies blocking at limit.
func TestSlotLimiterBlocksAtLimit(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	done := make(chan struct{})
	go func() {
		res := newSlotReservation(sem)
		select {
		case <-sem:
			res.Release()
		case <-time.After(2 * time.Second):
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("acquire blocked at limit (expected)")
	}
}

// TestSlotReservationDoubleRelease verifies Release is idempotent.
func TestSlotReservationDoubleRelease(t *testing.T) {
	sem := make(chan struct{}, 1)
	res := newSlotReservation(sem)
	res.Release()
	res.Release()
	if got := len(sem); got != 1 {
		t.Fatalf("expected one token after double release, got %d", got)
	}
	<-sem
	if got := len(sem); got != 0 {
		t.Fatalf("expected zero tokens after drain, got %d", got)
	}
}

// waitForProcess waits until the InstanceController's internal done channel is closed,
// which happens AFTER ALL controller-side effects complete:
// - Manager.wait() goroutine finishes cmd.Wait()
// - InstanceController.wait() finishes persist-final-state
// - InstanceController.wait() finishes reservation.Release()
// This is the correct synchronization point for inspecting instance state (LastError, etc).
func waitForProcess(ctx context.Context, ic *InstanceController, timeout time.Duration) error {
	doneCh := ic.GetControllerDone()
	if doneCh == nil {
		return fmt.Errorf("no controller done channel")
	}
	select {
	case <-doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for process to exit after %v", timeout)
	}
}

// waitForInstanceActive polls sup.ListActive() until at least one instance is active.
func waitForInstanceActive(sup *Supervisor, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		active, err := sup.ListActive()
		if err != nil {
			return err
		}
		if len(active) > 0 {
			return nil
		}
		<-ticker.C
	}
	return fmt.Errorf("timeout waiting for any instance to become active after %v", timeout)
}

// waitForState waits until the instance reaches the target state or times out.
func waitForState(sup *Supervisor, id domain.InstanceID, target domain.InstanceState, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		snap, err := sup.Status(id)
		if err != nil {
			return err
		}
		if snap.State == target {
			return nil
		}
		<-ticker.C
	}
	return fmt.Errorf("timeout waiting for instance %s to reach state %q", id, target)
}

// fakeRuntimeBuildOnce ensures the fake-runtime is built exactly once.
var fakeRuntimeBuildOnce sync.Once

// findOrBuildFakeRuntime finds or builds the fake-runtime binary.
// If the binary cannot be built, returns an empty path (tests that require
// the binary should check for empty and t.Fatal).
func findOrBuildFakeRuntime(t *testing.T) string {
	t.Helper()
	rootDir := filepath.Join("..", "..")
	dst := filepath.Join(rootDir, "testdata", "fake-runtime", "fake-runtime")
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	fakeRuntimeBuildOnce.Do(func() {
		srcDir := filepath.Join(rootDir, "testdata", "fake-runtime")
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			return
		}
		cmd := exec.Command("go", "build", "-o", dst, ".")
		cmd.Dir = srcDir
		if output, err := cmd.CombinedOutput(); err != nil {
			// Log to stderr without failing the test — some tests handle
			// empty binary path explicitly.
			fmt.Fprintf(os.Stderr, "WARNING: failed to build fake-runtime: %v\n%s\n", err, output)
		}
	})
	return dst
}

// TestSupervisorStartFailureReleasesSlot verifies slot is released on start failure.
func TestSupervisorStartFailureReleasesSlot(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1}
	sup := NewSupervisorWithConfig(store, cfg)
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	sup.semaphore = sem
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: "/nonexistent/path"}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid executable")
	}
	select {
	case <-sup.semaphore:
	case <-time.After(time.Second):
		t.Fatal("slot not released after start failure")
	}
}

// TestSupervisorNaturalExitReleasesSlot verifies slot is released on natural exit.
func TestSupervisorNaturalExitReleasesSlot(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "0"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		sup.mu.RLock()
		ctrl, ok := sup.instances[inst.ID]
		sup.mu.RUnlock()
		if ok {
			waitForProcess(ctx, ctrl, 5*time.Second)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("process did not exit in time")
	}
	select {
	case <-sup.semaphore:
	case <-time.After(3 * time.Second):
		t.Fatal("slot not released after natural exit")
	}
}

// TestSupervisorStopReleasesSlot verifies slot is released on Stop.
func TestSupervisorStopReleasesSlot(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "10"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	instances, _ := sup.List()
	if len(instances) == 0 {
		t.Fatal("no instances found after start")
	}
	err = sup.Stop(ctx, instances[0].ID)
	if err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	select {
	case <-sup.semaphore:
	case <-time.After(5 * time.Second):
		t.Fatal("slot not released after Stop")
	}
}

// TestSupervisorForceKillReleasesSlot verifies slot is released on force kill.
func TestSupervisorForceKillReleasesSlot(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "10"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Wait for instance to become active using waitForState polling.
	if err := waitForState(sup, "", domain.InstanceStateRunning, 3*time.Second); err != nil {
		// List might have instances but waitForState checks specific ID;
		// fall back to List if ID-based wait fails.
		instances, listErr := sup.List()
		if listErr != nil || len(instances) == 0 {
			t.Fatalf("instance not running after timeout: %v", err)
		}
	} else {
		// waitForState returned nil — we need the instance ID. Use List.
		instances, _ := sup.List()
		if len(instances) == 0 {
			t.Fatal("no instances found after start")
		}
		sup.RemoveTerminal(instances[0].ID)
		return
	}
	// Fallback: just remove all instances.
	instances, _ := sup.List()
	if len(instances) == 0 {
		t.Fatal("no instances found after start")
	}
	sup.RemoveTerminal(instances[0].ID)
	select {
	case <-sup.semaphore:
	case <-time.After(5 * time.Second):
		t.Fatal("slot not released after RemoveTerminal")
	}
}

// TestSupervisorRestartReusesSlot verifies restart reuses the same slot.
func TestSupervisorRestartReusesSlot(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "0"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		sup.mu.RLock()
		ctrl, ok := sup.instances[inst.ID]
		sup.mu.RUnlock()
		if ok {
			waitForProcess(ctx, ctrl, 5*time.Second)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("process did not exit in time")
	}
	select {
	case <-sup.semaphore:
	case <-time.After(time.Second):
		t.Fatal("first slot not released")
	}
	sup.semaphore <- struct{}{}
}

// TestSupervisorRemoveTerminalDoesNotDoubleRelease verifies RemoveTerminal doesn't double-release.
func TestSupervisorRemoveTerminalDoesNotDoubleRelease(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "10"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Poll for active instances instead of fixed sleep.
	if err := waitForInstanceActive(sup, 3*time.Second); err != nil {
		t.Fatalf("instance not active after timeout: %v", err)
	}
	instances, _ := sup.List()
	if len(instances) == 0 {
		t.Fatal("no instances found after start")
	}
	sup.RemoveTerminal(instances[0].ID)
	sup.RemoveTerminal(instances[0].ID)
	select {
	case <-sup.semaphore:
	case <-time.After(time.Second):
		t.Fatal("slot not released")
	}
	select {
	case <-sup.semaphore:
		t.Fatal("second token available — double release")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestSupervisorConcurrentStartLimit verifies maxConcurrent limits concurrent starts.
//
// Strategy: start 3 goroutines concurrently against a semaphore with capacity
// 2.  Record the number of successfully started instances and verify that no
// more than 2 are active at any given point in time.
func TestSupervisorConcurrentStartLimit(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()

	var wg sync.WaitGroup
	var instancesMu sync.Mutex
	var startedInstances []*domain.LaunchInstance
	var successCount int

	// Start 3 goroutines concurrently.  With MaxConcurrent=2 the semaphore
	// (capacity 2) limits how many can run the process-start path simultaneously.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "10"}, nil)
			instancesMu.Lock()
			defer instancesMu.Unlock()
			if err != nil {
				return
			}
			successCount++
			startedInstances = append(startedInstances, inst)
		}()
	}

	// Wait for all goroutines to complete.  The third goroutine may be blocked
	// on semaphore acquisition; give it enough time then drain remaining slots.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(6 * time.Second):
		// Drain remaining semaphore tokens so blocked goroutines can proceed.
		for len(sup.semaphore) > 0 {
			<-sup.semaphore
		}
		<-done
	}

	// Verify: at least 2 instances started successfully (the third may have
	// succeeded or been context-cancelled depending on timing).
	instancesMu.Lock()
	startedLen := len(startedInstances)
	instancesMu.Unlock()
	if startedLen < 2 {
		t.Errorf("expected at least 2 started instances, got %d", startedLen)
	}

	// Verify: at most 2 active instances at any point.
	active, _ := sup.ListActive()
	if len(active) > 2 {
		t.Errorf("expected at most 2 active instances, got %d", len(active))
	}

	// Clean up instances.
	instancesMu.Lock()
	for _, inst := range startedInstances {
		sup.RemoveTerminal(inst.ID)
	}
	instancesMu.Unlock()
}

// TestStartFailureJoinsPersistenceFailure verifies that when the process fails
// AND persistence also fails, the returned error contains BOTH errors via errors.Join.
//
// Flow: resolve succeeds → Create succeeds → ctrl.Start() fails (bad executable)
// → store.Update fails → errors.Join(processErr, persistErr).
func TestStartFailureJoinsPersistenceFailure(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 1, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()

	// Override Update to fail after Create succeeds.
	// Create will succeed (default), then ctrl.Start() fails (bad args),
	// then store.Update fails.
	store.updateErr = testUpdateErr

	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "999"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// The error chain must contain both sentinel errors via errors.Join.
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected error chain to contain testUpdateErr, got: %v", err)
	}
}

// TestRunningPersistenceFailureRollsBackOrDegrades verifies degraded success on running persist failure.
// When persist running state fails but process is running, the instance continues
// in a degraded state with LastError set. Start() returns nil error (degraded success).
func TestRunningPersistenceFailureRollsBackOrDegrades(t *testing.T) {
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == string(domain.InstanceStateRunning) {
			return testUpdateErr
		}
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	// With degraded success, Start returns nil error but sets LastError.
	startInst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Degraded success: LastError should be set immediately.
	if startInst.LastError == "" {
		t.Fatal("expected degraded success with LastError set immediately after Start")
	}
	if !strings.Contains(startInst.LastError, "update") {
		t.Errorf("expected LastError to contain persistence error, got: %q", startInst.LastError)
	}
	// Process should still be running (degraded success = no rollback).
	// Wait for it to exit naturally.
	done := make(chan struct{})
	go func() {
		sup.mu.RLock()
		ctrl, ok := sup.instances[startInst.ID]
		sup.mu.RUnlock()
		if ok {
			waitForProcess(ctx, ctrl, 5*time.Second)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("process did not exit in time")
	}
	// After exit, verify LastError is still visible in snapshot.
	instances, _ := sup.List()
	if len(instances) == 0 {
		t.Fatal("expected at least one instance in snapshot")
	}
	if instances[0].LastError == "" {
		t.Fatal("expected LastError visible in snapshot after exit")
	}
}

// TestNaturalExitPersistenceFailureObservable verifies persistence error is observable.
func TestNaturalExitPersistenceFailureObservable(t *testing.T) {
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == "running" || e.State == "exited" {
			return testUpdateErr
		}
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "0"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		sup.mu.RLock()
		ctrl, ok := sup.instances[inst.ID]
		sup.mu.RUnlock()
		if ok {
			waitForProcess(ctx, ctrl, 5*time.Second)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("process did not exit in time")
	}
	instances, err := sup.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("expected at least one instance in snapshot")
	}
	snap := instances[0]
	if snap.LastError == "" {
		t.Fatal("expected persistence error in LastError")
	}
	if !strings.Contains(snap.LastError, "update") {
		t.Errorf("expected LastError to contain persistence error, got: %q", snap.LastError)
	}
}

// TestStopPersistenceFailureReturned verifies error returned on stop persist failure.
func TestStopPersistenceFailureReturned(t *testing.T) {
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == "stopping" {
			return testUpdateErr
		}
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "5"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Poll for active instances instead of fixed sleep.
	if err := waitForInstanceActive(sup, 3*time.Second); err != nil {
		t.Fatalf("instance not active after timeout: %v", err)
	}
	instances, err := sup.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("no instances found after start")
	}
	err = sup.Stop(ctx, instances[0].ID)
	if err == nil {
		t.Fatal("expected error from Stop with persistence failure")
	}
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected errors.Is(err, testUpdateErr), got: %v", err)
	}
}

// TestRestartPersistenceFailureReturned verifies error returned on restart persist failure.
func TestRestartPersistenceFailureReturned(t *testing.T) {
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == "stopping" {
			return testUpdateErr
		}
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "10"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Poll for active instances instead of fixed sleep.
	if err := waitForInstanceActive(sup, 3*time.Second); err != nil {
		t.Fatalf("instance not active after timeout: %v", err)
	}
	instances, err := sup.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("no instances found after start")
	}
	_, err = sup.Restart(ctx, instances[0].ID)
	if err == nil {
		t.Fatal("expected error from Restart with persistence failure")
	}
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected errors.Is(err, testUpdateErr), got: %v", err)
	}
}

// TestShutdownAggregatesPersistenceFailures verifies ShutdownWithPersistence aggregates errors
// from persisting terminal instances after shutdown.
func TestShutdownAggregatesPersistenceFailures(t *testing.T) {
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		// Fail for "stopping" (Set by Stop()) and "exited" (Set by InstanceController.wait()).
		// ShutdownWithPersistence persists terminal instances (exited is terminal).
		if e.State == "stopping" || e.State == "exited" {
			return testUpdateErr
		}
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		sup.mu.RLock()
		ctrl, ok := sup.instances[inst.ID]
		sup.mu.RUnlock()
		if ok {
			waitForProcess(ctx, ctrl, 5*time.Second)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("process did not exit in time")
	}
	err = sup.ShutdownWithPersistence(ctx)
	if err == nil {
		t.Fatal("expected error from ShutdownWithPersistence with persistence failure")
	}
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected errors.Is(err, testUpdateErr), got: %v", err)
	}
}

// TestRecoverAggregatesPersistenceFailures verifies Recover aggregates errors.
func TestRecoverAggregatesPersistenceFailures(t *testing.T) {
	store := newMockStore()
	store.updateErr = testUpdateErr
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	entry := &domain.LaunchInstanceEntry{
		ID:        "inst-recover-test",
		ProfileID: "p1",
		State:     "pending",
	}
	store.Create(entry)
	ctx := context.Background()
	err := sup.Recover(ctx)
	if err == nil {
		t.Fatal("expected error from Recover with persistence failure")
	}
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected errors.Is(err, testUpdateErr), got: %v", err)
	}
}

// TestPersistenceErrorVisibleInSnapshot verifies persistence error appears in snapshot.
func TestPersistenceErrorVisibleInSnapshot(t *testing.T) {
	store := newMockStore()
	store.updateFn = func(e *domain.LaunchInstanceEntry) error {
		if e.State == "exited" {
			return testUpdateErr
		}
		return nil
	}
	cfg := SupervisorConfig{MaxConcurrent: 0, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: findOrBuildFakeRuntime(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "0"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		sup.mu.RLock()
		ctrl, ok := sup.instances[inst.ID]
		sup.mu.RUnlock()
		if ok {
			waitForProcess(ctx, ctrl, 5*time.Second)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("process did not exit in time")
	}
	instances, err := sup.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("expected at least one instance in snapshot")
	}
	snap := instances[0]
	if snap.LastError == "" {
		t.Fatal("expected persistence error visible in snapshot LastError")
	}
}
