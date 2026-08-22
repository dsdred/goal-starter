package process

import (
	"context"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

// mockProber implements platform.RecoveryProber for testing.
type mockProber struct {
	alive       map[int]bool
	identities  map[int]platform.ProcessIdentity
	aliveErr    error
	identityErr error
}

func newMockProber() *mockProber {
	return &mockProber{
		alive:      make(map[int]bool),
		identities: make(map[int]platform.ProcessIdentity),
	}
}

func (m *mockProber) IsProcessAlive(pid int) (bool, error) {
	if m.aliveErr != nil {
		return false, m.aliveErr
	}
	return m.alive[pid], nil
}

func (m *mockProber) GetProcessIdentity(pid int) (platform.ProcessIdentity, error) {
	if m.identityErr != nil {
		return platform.ProcessIdentity{}, m.identityErr
	}
	return m.identities[pid], nil
}

func TestRecover_PidNotAlive(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[1234] = false

	inst := &domain.LaunchInstance{
		ID:         "inst-1",
		State:      domain.InstanceStateRunning,
		PID:        1234,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-1"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-1"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "pid-not-found" {
		t.Errorf("expected pid-not-found, got %q", entry.RecoveryReason)
	}
}

func TestRecover_PidAlive_IdentityConfirmed(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	startTime := time.Now().Add(-time.Minute)
	prober.alive[5678] = true
	prober.identities[5678] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      startTime,
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-2",
		State:      domain.InstanceStateRunning,
		PID:        5678,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  startTime,
		CreatedAt:  time.Now(),
	}
	store.instances["inst-2"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-2"]
	if entry.State != "orphan" {
		t.Errorf("expected orphan, got %s", entry.State)
	}
	if entry.RecoveryReason != "" {
		t.Errorf("expected empty reason for orphan, got %q", entry.RecoveryReason)
	}
}

func TestRecover_PidAlive_IdentityMismatch(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	startTime := time.Now().Add(-time.Minute)
	prober.alive[9999] = true
	prober.identities[9999] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/some-other-app",
		StartTime:      startTime,
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-3",
		State:      domain.InstanceStateRunning,
		PID:        9999,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  startTime,
		CreatedAt:  time.Now(),
	}
	store.instances["inst-3"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-3"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "identity-unconfirmed" {
		t.Errorf("expected identity-unconfirmed, got %q", entry.RecoveryReason)
	}
}

func TestRecover_PidAlive_StartTimeMismatch(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[1111] = true
	prober.identities[1111] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      time.Now().Add(-24 * time.Hour),
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-4",
		State:      domain.InstanceStateRunning,
		PID:        1111,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-4"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-4"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "identity-unconfirmed" {
		t.Errorf("expected identity-unconfirmed, got %q", entry.RecoveryReason)
	}
}

func TestRecover_ZeroPid(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()

	inst := &domain.LaunchInstance{
		ID:         "inst-5",
		State:      domain.InstanceStateStarting,
		PID:        0,
		Executable: "/usr/bin/fake-runtime",
		CreatedAt:  time.Now(),
	}
	store.instances["inst-5"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-5"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "pid-not-found" {
		t.Errorf("expected pid-not-found, got %q", entry.RecoveryReason)
	}
}

func TestRecover_TerminalStatesUntouched(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()

	for _, state := range []string{"exited", "failed", "stale", "orphan"} {
		id := domain.InstanceID("inst-" + state)
		inst := &domain.LaunchInstance{
			ID:        id,
			State:     domain.InstanceState(state),
			PID:       42,
			CreatedAt: time.Now(),
		}
		store.instances[string(id)] = domain.ToStorageEntry(inst)
	}

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	for _, state := range []string{"exited", "failed", "stale", "orphan"} {
		id := "inst-" + state
		if store.instances[id].State != state {
			t.Errorf("%s: state changed to %s", id, store.instances[id].State)
		}
	}
}

func TestRecover_IdentityProbeError(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[7777] = true
	prober.identityErr = context.DeadlineExceeded

	inst := &domain.LaunchInstance{
		ID:         "inst-6",
		State:      domain.InstanceStateRunning,
		PID:        7777,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-6"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-6"]
	if entry.State != "stale" {
		t.Errorf("expected stale on probe error, got %s", entry.State)
	}
	if entry.RecoveryReason != "identity-unconfirmed" {
		t.Errorf("expected identity-unconfirmed, got %q", entry.RecoveryReason)
	}
}

func TestDismissOrphan_Success(t *testing.T) {
	store := newMockStore()
	inst := &domain.LaunchInstance{
		ID:        "inst-orphan",
		State:     domain.InstanceStateOrphan,
		PID:       42,
		CreatedAt: time.Now(),
	}
	store.instances["inst-orphan"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)

	if err := sup.DismissOrphan(context.Background(), "inst-orphan"); err != nil {
		t.Fatalf("DismissOrphan error: %v", err)
	}

	entry := store.instances["inst-orphan"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "reconciled-by-user" {
		t.Errorf("expected reconciled-by-user, got %q", entry.RecoveryReason)
	}
}

func TestDismissOrphan_NotOrphan(t *testing.T) {
	store := newMockStore()
	inst := &domain.LaunchInstance{
		ID:        "inst-running",
		State:     domain.InstanceStateRunning,
		PID:       42,
		CreatedAt: time.Now(),
	}
	store.instances["inst-running"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)

	err := sup.DismissOrphan(context.Background(), "inst-running")
	if err == nil {
		t.Fatal("expected error for non-orphan instance")
	}
}

func TestDismissOrphan_NotFound(t *testing.T) {
	store := newMockStore()
	sup := NewSupervisor(store)

	err := sup.DismissOrphan(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

// TestRecover_Persistence_AcrossRestarts verifies that after Recover(), the
// classified state + recovery_reason is persisted and remains consistent
// across a second restart (idempotent).
func TestRecover_Persistence_AcrossRestarts(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	startTime := time.Now().Add(-time.Minute)
	prober.alive[6000] = true
	prober.identities[6000] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      startTime,
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-persist",
		State:      domain.InstanceStateRunning,
		PID:        6000,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  startTime,
		CreatedAt:  time.Now(),
	}
	store.instances["inst-persist"] = domain.ToStorageEntry(inst)

	// First restart: classify as orphan.
	sup1 := NewSupervisor(store)
	sup1.prober = prober
	if err := sup1.Recover(context.Background()); err != nil {
		t.Fatalf("first Recover: %v", err)
	}
	if store.instances["inst-persist"].State != "orphan" {
		t.Fatalf("after first Recover: expected orphan, got %s", store.instances["inst-persist"].State)
	}

	// Second restart: orphan is NOT transitional, so Recover leaves it alone.
	prober2 := newMockProber() // fresh prober (simulates new process)
	sup2 := NewSupervisor(store)
	sup2.prober = prober2
	if err := sup2.Recover(context.Background()); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if store.instances["inst-persist"].State != "orphan" {
		t.Errorf("after second Recover: orphan must remain orphan, got %s", store.instances["inst-persist"].State)
	}

	// Dismiss persists.
	if err := sup2.DismissOrphan(context.Background(), "inst-persist"); err != nil {
		t.Fatalf("DismissOrphan: %v", err)
	}
	if store.instances["inst-persist"].State != "stale" {
		t.Errorf("after Dismiss: expected stale, got %s", store.instances["inst-persist"].State)
	}
	if store.instances["inst-persist"].RecoveryReason != "reconciled-by-user" {
		t.Errorf("after Dismiss: expected reconciled-by-user, got %q", store.instances["inst-persist"].RecoveryReason)
	}

	// Third restart: stale is terminal, Recover leaves it alone.
	sup3 := NewSupervisor(store)
	sup3.prober = newMockProber()
	if err := sup3.Recover(context.Background()); err != nil {
		t.Fatalf("third Recover: %v", err)
	}
	if store.instances["inst-persist"].State != "stale" {
		t.Errorf("after third Recover: stale must remain stale, got %s", store.instances["inst-persist"].State)
	}
}

// TestRecover_OrphanNeverBecomesActive verifies that an orphan instance
// never transitions back to an active state during recovery.
func TestRecover_OrphanNeverBecomesActive(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()

	// Start as orphan (already classified).
	inst := &domain.LaunchInstance{
		ID:        "inst-noactive",
		State:     domain.InstanceStateOrphan,
		PID:       7000,
		CreatedAt: time.Now(),
	}
	store.instances["inst-noactive"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	entry := store.instances["inst-noactive"]
	if entry.State != "orphan" {
		t.Errorf("orphan must not become active, got %s", entry.State)
	}
	dom := domain.ToDomain(entry)
	if dom.IsActive() {
		t.Error("orphan must not be active")
	}
}

// TestRecover_DoesNotSignalProcesses verifies that recovery does not
// start, stop, or signal any process. The mock prober tracks if any
// signal-like operations would have been attempted (none exist in the
// RecoveryProber interface — only IsProcessAlive and GetProcessIdentity).
// This is a structural guarantee: the interface has no signal/kill method.
func TestRecover_DoesNotSignalProcesses(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[8000] = true
	prober.identities[8000] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      time.Now().Add(-time.Minute),
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-nosignal",
		State:      domain.InstanceStateRunning,
		PID:        8000,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-nosignal"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// The process is still "alive" per the prober — recovery did not kill it.
	if !prober.alive[8000] {
		t.Error("recovery must not kill/signal the process")
	}
	// State is orphan (detected alive + identity confirmed), not stale.
	if store.instances["inst-nosignal"].State != "orphan" {
		t.Errorf("expected orphan (process untouched), got %s", store.instances["inst-nosignal"].State)
	}
}

// TestIdentityMatrix_StartTimeUnavailable_PathMatches verifies that when the
// prober cannot obtain a start time (HasStartTime=false), path match alone
// is sufficient to confirm orphan status per ADR 005.
func TestIdentityMatrix_StartTimeUnavailable_PathMatches(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[3000] = true
	prober.identities[3000] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		HasStartTime:   false,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-matrix-1",
		State:      domain.InstanceStateRunning,
		PID:        3000,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-matrix-1"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-matrix-1"]
	if entry.State != "orphan" {
		t.Errorf("expected orphan (path match, no start time), got %s", entry.State)
	}
}

// TestIdentityMatrix_BarePID_NeverOrphan verifies that a bare PID (no
// executable path recorded) never yields orphan, per ADR 005.
func TestIdentityMatrix_BarePID_NeverOrphan(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[4000] = true
	prober.identities[4000] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/some-process",
		StartTime:      time.Now().Add(-time.Minute),
		HasStartTime:   true,
	}

	// Instance has PID but NO executable path recorded (bare PID).
	inst := &domain.LaunchInstance{
		ID:         "inst-matrix-2",
		State:      domain.InstanceStateRunning,
		PID:        4000,
		Executable: "", // no path recorded
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-matrix-2"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-matrix-2"]
	if entry.State != "stale" {
		t.Errorf("bare PID must never yield orphan, got %s", entry.State)
	}
	if entry.RecoveryReason != "identity-unconfirmed" {
		t.Errorf("expected identity-unconfirmed, got %q", entry.RecoveryReason)
	}
}

// TestIdentityMatrix_ProberReturnsEmptyIdentity verifies conservative fallback
// when the prober cannot determine the executable path.
func TestIdentityMatrix_ProberReturnsEmptyIdentity(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	prober.alive[5000] = true
	prober.identities[5000] = platform.ProcessIdentity{} // empty identity

	inst := &domain.LaunchInstance{
		ID:         "inst-matrix-3",
		State:      domain.InstanceStateRunning,
		PID:        5000,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now(),
	}
	store.instances["inst-matrix-3"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober

	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover error: %v", err)
	}

	entry := store.instances["inst-matrix-3"]
	if entry.State != "stale" {
		t.Errorf("empty identity must yield stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "identity-unconfirmed" {
		t.Errorf("expected identity-unconfirmed, got %q", entry.RecoveryReason)
	}
}

// TestIdentityMatrix_PersistedIdentitySource documents where the expected
// identity anchors come from in the persistence contract:
//   - PID:            LaunchInstanceEntry.PID (set at launch time)
//   - Executable:     LaunchInstanceEntry.Executable (resolved command path)
//   - StartedAt:      LaunchInstanceEntry.StartedAt (launch timestamp)
func TestIdentityMatrix_PersistedIdentitySource(t *testing.T) {
	entry := &domain.LaunchInstanceEntry{
		ID:         "inst-src",
		State:      "running",
		PID:        9999,
		Executable: "/opt/goal/runtimes/llama-server",
		StartedAt:  time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
		CreatedAt:  time.Now(),
	}

	inst := domain.ToDomain(entry)
	if inst.PID != 9999 {
		t.Errorf("PID source: expected 9999, got %d", inst.PID)
	}
	if inst.Executable != "/opt/goal/runtimes/llama-server" {
		t.Errorf("Executable source: expected /opt/goal/runtimes/llama-server, got %q", inst.Executable)
	}
	if !inst.StartedAt.Equal(time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("StartedAt source: expected 2026-08-23T12:00:00Z, got %v", inst.StartedAt)
	}

	// Round-trip: ToStorageEntry → ToDomain preserves all identity anchors.
	roundTripped := domain.ToDomain(domain.ToStorageEntry(inst))
	if roundTripped.PID != inst.PID || roundTripped.Executable != inst.Executable || !roundTripped.StartedAt.Equal(inst.StartedAt) {
		t.Error("round-trip lost identity anchors")
	}
}

// ─── Active/Concurrency/Autostart Regression ───────────────────────────────

func TestOrphan_NotCountedActive(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	startTime := time.Now().Add(-time.Minute)
	prober.alive[9000] = true
	prober.identities[9000] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      startTime,
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-conc",
		State:      domain.InstanceStateRunning,
		PID:        9000,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  startTime,
		CreatedAt:  time.Now(),
	}
	store.instances["inst-conc"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	active := sup.ActiveInstances()
	if len(active) != 0 {
		t.Errorf("orphan must not appear in ActiveInstances, got %v", active)
	}
}

func TestStale_NotCountedActive(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()

	inst := &domain.LaunchInstance{
		ID:        "inst-stale-conc",
		State:     domain.InstanceStateRunning,
		PID:       0,
		CreatedAt: time.Now(),
	}
	store.instances["inst-stale-conc"] = domain.ToStorageEntry(inst)

	sup := NewSupervisor(store)
	sup.prober = prober
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	active := sup.ActiveInstances()
	if len(active) != 0 {
		t.Errorf("stale must not appear in ActiveInstances, got %v", active)
	}
}

func TestOrphan_DoesNotOccupyConcurrencySlot(t *testing.T) {
	store := newMockStore()
	prober := newMockProber()
	startTime := time.Now().Add(-time.Minute)
	prober.alive[10000] = true
	prober.identities[10000] = platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      startTime,
		HasStartTime:   true,
	}

	inst := &domain.LaunchInstance{
		ID:         "inst-slot",
		State:      domain.InstanceStateRunning,
		PID:        10000,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  startTime,
		CreatedAt:  time.Now(),
	}
	store.instances["inst-slot"] = domain.ToStorageEntry(inst)

	cfg := SupervisorConfig{MaxConcurrent: 1}
	sup := NewSupervisorWithConfig(store, cfg)
	sup.prober = prober
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if count := sup.concurrentCount(); count != 0 {
		t.Errorf("orphan must not occupy a concurrency slot, count=%d", count)
	}
}
