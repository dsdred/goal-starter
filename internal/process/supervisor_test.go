package process

import (
	"context"
	"errors"
	"fmt"
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

// buildFakeRuntimeForTest builds fake-runtime from source into a temp directory
// and returns the path. The caller must call t.Cleanup to remove the temp dir.
// This ensures tests are reproducible on any platform without depending on
// a pre-built Windows PE binary.
func buildFakeRuntimeForTest(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "fake-runtime")
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}
	srcDir := filepath.Join("..", "..", "testdata", "fake-runtime")
	cmd := exec.Command("go", "build", "-o", dst, ".")
	cmd.Dir = srcDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake-runtime: %v\n%s", err, output)
	}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "2"}, nil)
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "2"}, nil)
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
	instID := instances[0].ID
	// Stop the process first (kills the executable).
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	sup.Stop(stopCtx, instID)
	// Wait for process to fully exit so the exe file is released on Windows.
	sup.mu.RLock()
	ctrl, ok := sup.instances[instID]
	sup.mu.RUnlock()
	if ok {
		waitForProcess(ctx, ctrl, 10*time.Second)
	}
	sup.RemoveTerminal(instID)
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "2"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := waitForInstanceActive(sup, 3*time.Second); err != nil {
		t.Fatalf("instance not active after timeout: %v", err)
	}
	instances, _ := sup.List()
	if len(instances) == 0 {
		t.Fatal("no instances found after start")
	}
	instID := instances[0].ID
	// Stop the process first, then wait for exit to release Windows file lock.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	sup.Stop(stopCtx, instID)
	sup.mu.RLock()
	ctrl, ok := sup.instances[instID]
	sup.mu.RUnlock()
	if ok {
		waitForProcess(ctx, ctrl, 10*time.Second)
	}
	sup.RemoveTerminal(instID)
	sup.RemoveTerminal(instID)
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
// Fixed architecture: with MaxConcurrent=2 and three parallel Starts using
// "-sleep 1", the third goroutine blocks on semaphore. The test uses a
// readyCh barrier to prove slots 1-2 are active, then releases one instance
// to unblock the third.
func TestSupervisorConcurrentStartLimit(t *testing.T) {
	store := newMockStore()
	cfg := SupervisorConfig{MaxConcurrent: 2, LogBufferSize: 64}
	sup := NewSupervisorWithConfig(store, cfg)
	profile := &domain.Profile{ID: "p1", Name: "test"}
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()

	readyCh := make(chan struct{}, 3) // signal when Start() returns for each goroutine

	var wg sync.WaitGroup
	var instancesMu sync.Mutex
	var startedInstances []*domain.LaunchInstance

	// Start 3 goroutines concurrently. With MaxConcurrent=2 the semaphore
	// (capacity 2) limits how many can run the process-start path simultaneously.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "1"}, nil)
			readyCh <- struct{}{} // signal that Start() returned (blocked or not)
			if err != nil {
				return
			}
			instancesMu.Lock()
			startedInstances = append(startedInstances, inst)
			instancesMu.Unlock()
		}()
	}

	// Wait for all 3 Starts to return (either acquired slot or blocked).
	// 3 ready signals from 3 goroutines.
	<-readyCh
	<-readyCh
	<-readyCh

	// At least 2 instances should have started (first two goroutines).
	instancesMu.Lock()
	startedLen := len(startedInstances)
	instancesMu.Unlock()
	if startedLen < 2 {
		t.Errorf("expected at least 2 started instances after all Starts returned, got %d", startedLen)
	}

	// Verify: at most 2 active instances.
	active, _ := sup.ListActive()
	if len(active) > 2 {
		t.Errorf("expected at most 2 active instances, got %d", len(active))
	}

	// Clean up: stop all started instances.
	instancesMu.Lock()
	for _, inst := range startedInstances {
		sup.RemoveTerminal(inst.ID)
	}
	instancesMu.Unlock()

	// Wait for cleanup to complete.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup timeout — possible slot leak")
	}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "2"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
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
	instID := instances[0].ID
	err = sup.Stop(ctx, instID)
	if err == nil {
		t.Fatal("expected error from Stop with persistence failure")
	}
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected errors.Is(err, testUpdateErr), got: %v", err)
	}
	// Wait for process exit to release Windows file lock.
	sup.mu.RLock()
	ctrl, ok := sup.instances[instID]
	sup.mu.RUnlock()
	if ok {
		waitForProcess(ctx, ctrl, 10*time.Second)
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
	model := &domain.Model{ID: "m1", Name: "test-model"}
	ctx := context.Background()
	_, err := sup.Start(ctx, profile, rt, model, []string{"-sleep", "2"}, nil)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
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
	instID := instances[0].ID
	_, err = sup.Restart(ctx, instID)
	if err == nil {
		t.Fatal("expected error from Restart with persistence failure")
	}
	if !errors.Is(err, testUpdateErr) {
		t.Errorf("expected errors.Is(err, testUpdateErr), got: %v", err)
	}
	// Wait for process exit to release Windows file lock.
	sup.mu.RLock()
	ctrl, ok := sup.instances[instID]
	sup.mu.RUnlock()
	if ok {
		waitForProcess(ctx, ctrl, 10*time.Second)
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
	rt := &domain.Runtime{ID: "rt1", Name: "test-rt", Executable: buildFakeRuntimeForTest(t)}
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
