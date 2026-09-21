package process

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

// ADR 016 §8: bounded persistence retry parameters. They are implementation
// parameters (not architectural invariants): the total retry budget stays
// under 2 seconds so slot release and run completion are never blocked
// indefinitely. Exposed as constants (not inlined in the retry loop) for
// testability.
const (
	persistRetryAttempts = 3
	persistRetryBackoff  = 200 * time.Millisecond
)

// persistWithRetry performs the bounded, synchronous, cancellable persist
// retry of ADR 016 §8. Retries are owned by the caller (startCore / wait),
// run while lifecycle/slot ownership is held, are bounded to
// persistRetryAttempts total attempts, and exit immediately when ctx is
// cancelled (supervisor shutdown). Permanent store errors (permission, disk
// full) skip the remaining attempts.
func persistWithRetry(ctx context.Context, persist func() error) error {
	var lastErr error
	for attempt := 1; attempt <= persistRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return errors.Join(lastErr, err)
			}
			return err
		}
		lastErr = persist()
		if lastErr == nil || isPermanentStoreError(lastErr) {
			return lastErr
		}
		if attempt < persistRetryAttempts {
			select {
			case <-ctx.Done():
				return errors.Join(lastErr, ctx.Err())
			case <-time.After(persistRetryBackoff):
			}
		}
	}
	return lastErr
}

// isPermanentStoreError classifies persistence errors that will not succeed
// on retry (ADR 016 §8): permission errors and disk-full conditions.
// Transient I/O errors (EAGAIN/EIO/lock contention) are not permanent and
// are retried.
func isPermanentStoreError(err error) bool {
	switch {
	case errors.Is(err, fs.ErrPermission),
		errors.Is(err, syscall.EACCES),
		errors.Is(err, syscall.EPERM),
		errors.Is(err, syscall.ENOSPC):
		return true
	}
	return false
}

// SupervisorStatus describes the overall supervisor state.
type SupervisorStatus struct {
	ActiveInstances  int               `json:"active_instances"`
	RunningInstances int               `json:"running_instances"`
	Instances        []InstanceSummary `json:"instances,omitempty"`
}

// InstanceSummary is a lightweight summary of an instance state.
type InstanceSummary struct {
	ID      string `json:"id"`
	ModelID string `json:"model_id"`
	State   string `json:"state"`
	PID     int    `json:"pid,omitempty"`
}

// Supervisor manages multiple launch instances.
// Each instance has its own process.Manager, allowing concurrent or sequential
// launches of different models.
//
// Concurrency limiting uses a single buffered semaphore channel as the sole
// source of truth. Each acquired token is wrapped in a slotReservation with
// sync.Once to guarantee exactly-once release.
type Supervisor struct {
	mu            sync.RWMutex
	instances     map[domain.InstanceID]*InstanceController
	resolver      *domain.LaunchResolver
	store         InstanceStore
	maxConcurrent int
	semaphore     chan struct{}
	lifecycleCtx  context.Context
	broker        *LogBroker
	prober        platform.RecoveryProber
	killer        platform.ProcessKiller

	arbMu    sync.Mutex
	arbLocks map[string]*sync.Mutex

	// opSeq mints the monotonic operation/generation identity consumed by the
	// ADR 017 D1 arbitration boundary (see arbitration.go). Restart reuses the
	// InstanceID, so this counter — never a random ID — is what binds a spawn
	// claim to exactly one generation.
	opSeq atomic.Uint64

	// RB-015b pre-spawn shutdown drain. launchMu serializes admission
	// registration (A), shutdown drain-start (D), and spawn commit (C).
	// draining is a one-way latch; preSpawnInFlight counts admitted launches
	// that have not yet committed (C) or been aborted; both are accessed only
	// under launchMu. zeroSig is closed exactly once (drainClosedOnce) when
	// draining && preSpawnInFlight == 0, letting Shutdown's lock-free drain
	// wait complete.
	launchMu         sync.Mutex
	draining         bool
	preSpawnInFlight int
	zeroSig          chan struct{}
	drainClosedOnce  sync.Once

	// managerStartHook is a test seam (nil in production) that installs a
	// Manager start override on every newly created controller, so tests can
	// deterministically count spawn attempts for the "no manager.Start after
	// successful shutdown" contract.
	managerStartHook func(m *Manager)
}

// InstanceStore persists and retrieves launch instances.
// Uses domain.LaunchInstanceEntry to avoid dependency on storage package.
type InstanceStore interface {
	Create(e *domain.LaunchInstanceEntry) error
	Get(id string) (*domain.LaunchInstanceEntry, error)
	Update(e *domain.LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*domain.LaunchInstanceEntry, error)
	ListByModelID(modelID string) ([]*domain.LaunchInstanceEntry, error)
}

// SupervisorConfig holds configuration for Supervisor.
type SupervisorConfig struct {
	MaxConcurrent int // 0 = unlimited
	LogBufferSize int // 0 = default (4096)
}

// NewSupervisor creates a Supervisor.
func NewSupervisor(store InstanceStore) *Supervisor {
	return &Supervisor{
		instances: make(map[domain.InstanceID]*InstanceController),
		resolver:  domain.NewLaunchResolver(),
		store:     store,
		semaphore: make(chan struct{}, 0),
		broker:    NewLogBroker(4096),
		prober:    platform.NewRecoveryProber(),
		killer:    platform.NewProcessKiller(),
		arbLocks:  make(map[string]*sync.Mutex),
		zeroSig:   make(chan struct{}),
	}
}

// newSemaphore creates a buffered semaphore channel pre-filled with maxConcurrent tokens.
// Each token represents one available concurrency slot. Acquire takes a token; Release returns it.
// Pre-filling ensures Release() is guaranteed to succeed (no blocking, no silent drop).
func newSemaphore(capacity int) chan struct{} {
	sem := make(chan struct{}, capacity)
	for i := 0; i < capacity; i++ {
		sem <- struct{}{}
	}
	return sem
}

// NewSupervisorWithContext creates a Supervisor with an application-level lifecycle context.
// The lifecycle context is used for all instance processes, so HTTP request
// timeouts do not kill running processes.
func NewSupervisorWithContext(lifecycleCtx context.Context, store InstanceStore) *Supervisor {
	s := NewSupervisor(store)
	s.lifecycleCtx = lifecycleCtx
	return s
}

// SetRecoveryProber replaces the platform prober (test injection).
func (s *Supervisor) SetRecoveryProber(p platform.RecoveryProber) {
	s.prober = p
}

// SetDataDir configures the GOAL_DATA built-in variable for variable resolution.
func (s *Supervisor) SetDataDir(dir string) {
	s.resolver.SetDataDir(dir)
}

// SetProcessKiller replaces the platform killer (test injection).
func (s *Supervisor) SetProcessKiller(k platform.ProcessKiller) {
	s.killer = k
}

// lifecycleContext returns the application lifecycle context, or a background
// context if none was explicitly set.
func (s *Supervisor) lifecycleContext() context.Context {
	if s.lifecycleCtx != nil {
		return s.lifecycleCtx
	}
	return context.Background()
}

// NewSupervisorWithConfig creates a Supervisor with config.
func NewSupervisorWithConfig(store InstanceStore, cfg SupervisorConfig) *Supervisor {
	s := NewSupervisor(store)
	if cfg.MaxConcurrent > 0 {
		s.maxConcurrent = cfg.MaxConcurrent
		s.semaphore = newSemaphore(cfg.MaxConcurrent)
	}
	if cfg.LogBufferSize > 0 {
		s.broker = NewLogBroker(cfg.LogBufferSize)
	}
	return s
}

// signalPreSpawnZeroLocked closes zeroSig exactly once if the drain is armed
// and no pre-spawn launches remain. The caller MUST hold launchMu.
func (s *Supervisor) signalPreSpawnZeroLocked() {
	if s.draining && s.preSpawnInFlight == 0 {
		s.drainClosedOnce.Do(func() { close(s.zeroSig) })
	}
}

// beginDrain sets the one-way draining latch and, if no pre-spawn launches
// remain, signals drain-complete immediately. It acquires launchMu itself so
// D is linearizable against admission registration (A) and spawn commit (C).
func (s *Supervisor) beginDrain() {
	s.launchMu.Lock()
	s.draining = true
	s.signalPreSpawnZeroLocked()
	s.launchMu.Unlock()
}

// abortPreSpawnCleanup finalizes an admitted launch that is aborted PRE-SPAWN
// (before manager.Start). It releases the reservation (if any), optionally
// terminalizes + persists the controller (terminalize=true when a durable
// pending record exists), removes it from the registry, and consumes the
// PRE-SPAWN token exactly once as ABORTED (the final safe point). The caller
// MUST NOT hold launchMu (this acquires it only for the final decrement).
func (s *Supervisor) abortPreSpawnCleanup(instID domain.InstanceID, ctrl *InstanceController, reservation *slotReservation, terminalize bool) {
	if reservation != nil {
		reservation.Release()
	}
	if terminalize && ctrl != nil {
		ctrl.mu.Lock()
		ctrl.instance.Fail(ErrLaunchAbortedByShutdown.Error(), domain.InstanceExitError)
		if s.store != nil {
			s.store.Update(domain.ToStorageEntry(ctrl.instance))
		}
		ctrl.mu.Unlock()
	}
	s.mu.Lock()
	delete(s.instances, instID)
	s.mu.Unlock()
	s.launchMu.Lock()
	if ctrl != nil && ctrl.preSpawnToken {
		s.preSpawnInFlight--
		ctrl.preSpawnToken = false
		s.signalPreSpawnZeroLocked()
	}
	s.launchMu.Unlock()
}

// failUnspawnedController terminalizes a launch that never produced a
// supervised OS process (never reached a successful manager.Start), so no
// wait() goroutine owns the instance. The controller shares its
// *domain.LaunchInstance with Snapshot/List/ListActive/Status readers, so the
// mutation and the storage snapshot are taken under ic.mu; the repository write
// runs AFTER the unlock because repository implementations hold their own lock
// across the file write and must never be nested under ic.mu.
//
// The caller MUST NOT hold ic.mu, s.mu or launchMu. It MUST NOT use this for an
// ADR 016 residual (C/D) outcome: there the process may be alive and ownership
// belongs to wait().
func (s *Supervisor) failUnspawnedController(ctrl *InstanceController, cause string) error {
	ctrl.mu.Lock()
	ctrl.instance.Fail(cause, domain.InstanceExitError)
	entry := domain.ToStorageEntry(ctrl.instance)
	ctrl.mu.Unlock()

	if s.store == nil {
		return nil
	}
	return s.store.Update(entry)
}

// forgetController removes one exact controller from the active registry. The
// identity comparison keeps a re-published controller (RemoveTerminal re-inserts
// on persist failure) from being dropped by a stale cleanup. Safe when the
// controller is already absent. The caller MUST NOT hold ic.mu or s.mu.
func (s *Supervisor) forgetController(ctrl *InstanceController, instID domain.InstanceID) {
	s.mu.Lock()
	if s.instances[instID] == ctrl {
		delete(s.instances, instID)
	}
	s.mu.Unlock()
}

// concurrentCount returns the number of currently held slots.
// For a buffered semaphore, this is len(semaphore) which counts unacquired tokens.
// Held = maxConcurrent - len(semaphore).
func (s *Supervisor) concurrentCount() int {
	if s.semaphore == nil {
		return 0
	}
	return s.maxConcurrent - len(s.semaphore)
}

func (s *Supervisor) acquireSlot(ctx context.Context) (*slotReservation, error) {
	if s.maxConcurrent <= 0 {
		return nil, nil
	}
	lc := s.lifecycleContext()
	select {
	case <-s.semaphore:
		return newSlotReservation(s.semaphore), nil
	case <-lc.Done():
		// Already-admitted launch: shutdown won the pre-spawn window. This is
		// NOT an admission rejection (admission already occurred) — the launch
		// is aborted PRE-SPAWN by the caller (RB-015b).
		return nil, ErrLaunchAbortedByShutdown
	case <-ctx.Done():
		return nil, fmt.Errorf("acquire concurrency slot: %w", ctx.Err())
	}
}

// arbitrationLock returns (and lazily creates) the per-ModelID arbitration
// lock. The lock is never removed (bounded by the number of distinct ModelIDs).
func (s *Supervisor) arbitrationLock(modelID string) *sync.Mutex {
	s.arbMu.Lock()
	m, ok := s.arbLocks[modelID]
	if !ok {
		m = &sync.Mutex{}
		s.arbLocks[modelID] = m
	}
	s.arbMu.Unlock()
	return m
}

// compatible reports whether a new claim with the given owner is admissible
// against an existing in-flight instance (ADR 017 compatibility matrix).
func compatible(newOwner domain.LaunchOwner, existing *domain.LaunchInstance) bool {
	existingPipeline := existing.PipelineID != ""
	switch {
	case newOwner.Kind == domain.OwnerManual:
		return false
	case newOwner.Kind == domain.OwnerPipeline:
		if !existingPipeline {
			return false
		}
		if existing.PipelineID != newOwner.PipelineID {
			return false
		}
		if existing.PipelineEntryID == "" {
			return false
		}
		if existing.PipelineEntryID == newOwner.PipelineEntryID {
			return false
		}
		return true
	default:
		return false
	}
}

// LogBroker returns the log broker for multi-instance subscriptions.
// Returns nil if broker was not configured.
func (s *Supervisor) LogBroker() *LogBroker {
	return s.broker
}

// Resolver returns the LaunchResolver.
func (s *Supervisor) Resolver() *domain.LaunchResolver {
	return s.resolver
}

// Resolve returns a resolved CommandSpec for the given model and runtime.
func (s *Supervisor) Resolve(model *domain.Model, runtime *domain.Runtime, customArgs []string, customEnv map[string]string) (*domain.CommandSpec, error) {
	return s.resolver.Resolve(model, runtime, customArgs, customEnv)
}

// ResolvePreview returns a resolved CommandSpec without creating an instance.
func (s *Supervisor) ResolvePreview(model *domain.Model, runtime *domain.Runtime, customArgs []string, customEnv map[string]string) (*domain.CommandSpec, error) {
	return s.resolver.Preview(model, runtime, customArgs, customEnv)
}

// RuntimeToDomain converts storage.RuntimeEntry to domain.Runtime.
func RuntimeToDomain(id, name, executable, workingDir string, environment map[string]string) *domain.Runtime {
	return &domain.Runtime{
		ID:               id,
		Name:             name,
		Executable:       executable,
		WorkingDirectory: workingDir,
		Environment:      environment,
	}
}

// newSlotReservation creates a slotReservation for the given semaphore channel.
// The semaphore must be a buffered channel of capacity >= 1, pre-filled with tokens.
func newSlotReservation(sem chan struct{}) *slotReservation {
	return &slotReservation{
		semaphore: sem,
	}
}

// slotReservation is a token for a concurrency slot. Release() must be called
// exactly once — via sync.Once — when the instance exits or is removed.
type slotReservation struct {
	releaseOnce sync.Once
	semaphore   chan struct{} // the semaphore channel to return the token to
}

// Release frees the concurrency slot by returning the token to the semaphore channel.
// Uses sync.Once to guarantee exactly-once release.
// Because the semaphore channel is pre-filled with exactly maxConcurrent tokens,
// Release() is guaranteed to succeed — there is always exactly one token slot available.
func (r *slotReservation) Release() {
	r.releaseOnce.Do(func() {
		if r.semaphore == nil {
			return
		}
		// Guaranteed send: the channel has exactly maxConcurrent capacity,
		// and at most maxConcurrent tokens can be outstanding simultaneously.
		r.semaphore <- struct{}{}
	})
}

// AdmitAndStart is the authoritative launch admission + start operation
// (ADR 017). It delegates the whole arbitration decision to
// Supervisor.arbitrate, which atomically checks the shutdown/lifecycle state,
// conflicting same-model operations and repository orphans, materializes the
// pending claim in s.instances (the linearization point), and mints the
// generation-bound spawn authorization this launch consumes at commit C.
//
// The per-ModelID arbitration lock serializes check+claim for the same model.
// It is released inside arbitrate, before slot acquisition, persistence, or
// spawn.
func (s *Supervisor) AdmitAndStart(ctx context.Context, model *domain.Model, runtime *domain.Runtime, owner domain.LaunchOwner, customArgs []string, customEnv map[string]string) (*domain.LaunchInstance, error) {
	inst, err := s.resolver.ResolveToInstance(model, runtime, customArgs, customEnv)
	if err != nil {
		return nil, fmt.Errorf("resolve instance: %w", err)
	}

	ctrl := s.newController(inst)

	claim, err := s.arbitrate(newAdmissionClaimant(model.ID, inst, ctrl, owner))
	if err != nil {
		return nil, err
	}

	return s.startPostAdmit(ctx, inst, ctrl, claim)
}

// startPostAdmit performs the post-admission launch sequence: slot acquisition,
// pending persistence, startCore, and ADR 016 lifecycle. The instance MUST
// already be in s.instances (admission linearized) and hold a published spawn
// claim.
func (s *Supervisor) startPostAdmit(ctx context.Context, inst *domain.LaunchInstance, ctrl *InstanceController, claim *spawnClaim) (*domain.LaunchInstance, error) {
	// The operation record is released when this launch attempt ends — after the
	// spawn has committed and transferred ownership to a lifecycle-owned
	// generation, or on final failure. Releasing it earlier would reopen an
	// externally admissible window inside a launch that is still in flight.
	defer ctrl.releaseLaunchOperation(claim.opID)

	reservation, err := s.acquireSlot(ctx)
	if err != nil {
		// PRE-SLOT abort: no reservation held, no durable record yet. Consume
		// the PRE-SPAWN token ABORTED and remove the admitted controller. The
		// returned error is preserved (ErrLaunchAbortedByShutdown when the
		// supervisor lifecycle shutdown won; the caller-ctx error otherwise).
		s.abortPreSpawnCleanup(inst.ID, ctrl, nil, false)
		return nil, err
	}

	if s.store != nil {
		entry := domain.ToStorageEntry(inst)
		if err := s.store.Create(entry); err != nil {
			// POST-SLOT / POST-Create PRE-C abort: release the slot, no durable
			// pending record to terminalize, consume token ABORTED.
			s.abortPreSpawnCleanup(inst.ID, ctrl, reservation, false)
			return nil, fmt.Errorf("persist instance: %w", err)
		}
	}

	ctrlInst, err := ctrl.startWithReservation(s.lifecycleContext(), reservation, claim)
	if err != nil {
		if errors.Is(err, ErrLaunchAbortedByShutdown) {
			// Commit-C (D<C) abort: startCore already terminalized, deleted,
			// released the slot, and consumed the token ABORTED. No further
			// cleanup or accounting here.
			return nil, err
		}
		if ctrlInst != nil {
			return ctrlInst, fmt.Errorf("start instance %s: %w", inst.ID, err)
		}
		// Post-C failure (PRE-SPAWN token already COMMITTED at C): the existing
		// ADR 016 cleanup below is authoritative and MUST NOT touch
		// preSpawnInFlight.
		if !errors.Is(err, ErrPersistenceFailure) {
			if persistErr := s.failUnspawnedController(ctrl, err.Error()); persistErr != nil {
				err = errors.Join(err, fmt.Errorf("persist start error: %w", persistErr))
			}
		}
		s.forgetController(ctrl, inst.ID)
		return nil, fmt.Errorf("start instance %s: %w", inst.ID, err)
	}

	snapshot := ctrl.Snapshot()
	return &snapshot, nil
}

// start creates a new launch instance and starts its process without admission
// arbitration. Package-internal TEST SEAM only (the ADR 016/017 lifecycle
// tests): production code MUST use AdmitAndStart (ADR 017). Because the spawn
// boundary requires a claim, the seam publishes one through the single
// package-private minting helper instead of arbitration.
func (s *Supervisor) start(ctx context.Context, model *domain.Model, runtime *domain.Runtime, customArgs []string, customEnv map[string]string) (*domain.LaunchInstance, error) {
	inst, err := s.resolver.ResolveToInstance(model, runtime, customArgs, customEnv)
	if err != nil {
		return nil, fmt.Errorf("resolve instance: %w", err)
	}

	ctrl := s.newController(inst)

	s.mu.Lock()
	s.instances[inst.ID] = ctrl
	s.mu.Unlock()

	return s.startPostAdmit(ctx, inst, ctrl, s.mintTestSpawnClaim(ctrl, domain.ManualOwner))
}

// controllerFor returns the registered controller for an instance ID.
func (s *Supervisor) controllerFor(id domain.InstanceID) (*InstanceController, error) {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	return ctrl, nil
}

// Stop stops a specific instance by ID.
func (s *Supervisor) Stop(ctx context.Context, id domain.InstanceID) error {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}

	return ctrl.Stop(ctx)
}

// Restart restarts a specific instance, reusing the instance's frozen launch
// fields from the original resolve. The restart passes through the SAME ADR 017
// arbitration boundary as a new launch (D1/BF-01) and receives its own
// generation-bound spawn authorization.
func (s *Supervisor) Restart(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	ctrl, err := s.controllerFor(id)
	if err != nil {
		return nil, err
	}
	return s.restart(ctx, ctrl, nil, "")
}

// RestartWithLaunch restarts a specific instance using a freshly resolved
// launch specification built from the current model/runtime configuration. The
// InstanceID, the persisted record, and the PipelineID/PipelineEntryID
// attribution are preserved (no new ID is minted); only the launch-affecting
// fields (RuntimeID, Executable, Args, WorkingDirectory, Environment) are
// refreshed before the new process generation starts. ModelName is
// intentionally NOT refreshed (display metadata).
//
// The fresh spec is resolved BEFORE arbitration so a resolution failure costs
// no reservation; arbitration then decides admission exactly as for a new
// launch (D1/BF-01).
func (s *Supervisor) RestartWithLaunch(ctx context.Context, id domain.InstanceID, model *domain.Model, runtime *domain.Runtime, customArgs []string, customEnv map[string]string) (*domain.LaunchInstance, error) {
	ctrl, err := s.controllerFor(id)
	if err != nil {
		return nil, err
	}

	spec, err := ctrl.resolver.Resolve(model, runtime, customArgs, customEnv)
	if err != nil {
		return nil, fmt.Errorf("resolve instance: %w", err)
	}

	return s.restart(ctx, ctrl, spec, runtime.ID)
}

// restart is the single restart entry: it derives the owner from the TARGET
// instance, arbitrates under the per-ModelID arbLock, and only then hands the
// winning claim to the lifecycle work. arbLock is already released when
// restartWithRefresh acquires lifecycleMu (no arbLock -> lifecycleMu edge).
func (s *Supervisor) restart(ctx context.Context, ctrl *InstanceController, spec *domain.CommandSpec, runtimeID string) (*domain.LaunchInstance, error) {
	snap := ctrl.Snapshot()
	owner, err := instanceOwner(&snap)
	if err != nil {
		return nil, err
	}

	claim, err := s.arbitrate(newRestartClaimant(ctrl, snap.ModelID, owner))
	if err != nil {
		return nil, err
	}

	return ctrl.restartWithRefresh(ctx, spec, runtimeID, claim)
}

// Status returns a snapshot of a specific instance.
func (s *Supervisor) Status(id domain.InstanceID) (*domain.LaunchInstance, error) {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}

	snap := ctrl.Snapshot()
	return &snap, nil
}

// List returns snapshots of all instances.
func (s *Supervisor) List() ([]*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*domain.LaunchInstance, 0, len(s.instances))
	for _, ctrl := range s.instances {
		snap := ctrl.Snapshot()
		result = append(result, &snap)
	}
	return result, nil
}

// ListActive returns snapshots of only active instances.
func (s *Supervisor) ListActive() ([]*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*domain.LaunchInstance, 0)
	for _, ctrl := range s.instances {
		if ctrl.IsRunning() {
			snap := ctrl.Snapshot()
			result = append(result, &snap)
		}
	}
	return result, nil
}

// ListByModelID returns instances for a specific model.
func (s *Supervisor) ListByModelID(modelID string) ([]*domain.LaunchInstance, error) {
	if s.store == nil {
		return nil, nil
	}
	entries, err := s.store.ListByModelID(modelID)
	if err != nil {
		return nil, err
	}
	result := make([]*domain.LaunchInstance, 0, len(entries))
	for _, e := range entries {
		result = append(result, domain.ToDomain(e))
	}
	return result, nil
}

// Shutdown stops all active instances gracefully. This includes instances in
// the ADR 016 residual-ownership outcomes (C/D): they remain live-state
// (starting) and stoppable until wait() confirms exit.
//
// A kill request accepted (or refused) by the OS during shutdown is NOT an
// exit confirmation: if termination remains unconfirmed, the instance keeps
// its residual ownership (slot/run held by wait(), non-terminal state) and
// Stop's error is surfaced — Shutdown never manufactures a terminal
// confirmation.
//
// RB-015b: before enumerating and stopping lifecycle-visible controllers,
// Shutdown first starts the pre-spawn drain (D) and waits for every already
// admitted PRE-SPAWN launch to either commit (C) or abort. Only then is a
// successful return possible, which guarantees no admitted PRE-SPAWN launch
// can subsequently reach manager.Start. The drain wait holds no mutex and
// Shutdown never holds launchMu while stopping controllers.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	// 1. Drain-start (D): set the one-way draining latch (linearized on
	// launchMu against admission A and commit C). From now on, any new
	// admission is rejected and any in-flight pre-spawn commit aborts.
	s.beginDrain()

	// 2. Wait for pre-spawn drain to complete (lock-free). Bounded by the
	// shutdown context deadline; if it wins, Shutdown reports an error and
	// does NOT assert the successful-return guarantee.
	select {
	case <-s.zeroSig:
	case <-ctx.Done():
		return fmt.Errorf("shutdown pre-spawn drain: %w", ctx.Err())
	}

	// 3. Snapshot the lifecycle-visible controllers AFTER the drain: every
	// committed launch is now starting/running (stoppable) and no admitted
	// pre-spawn launch remains.
	s.mu.Lock()
	controllers := make([]*InstanceController, 0, len(s.instances))
	for _, ctrl := range s.instances {
		controllers = append(controllers, ctrl)
	}
	s.mu.Unlock()

	var firstErr error
	for _, ctrl := range controllers {
		if ctrl.IsRunning() {
			if err := ctrl.Stop(ctx); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

// RemoveTerminal removes terminal instances from the active registry and persists state.
// Returns an error if the terminal state could not be persisted.
//
// Slot release: calls the instance's reservation.Release() which uses sync.Once
// to guarantee exactly-once release. If the instance was never started (no
// reservation), this is a no-op.
func (s *Supervisor) RemoveTerminal(id domain.InstanceID) error {
	s.mu.Lock()
	ctrl, ok := s.instances[id]
	if ok {
		delete(s.instances, id)
	}
	s.mu.Unlock()

	if !ok {
		return nil
	}

	if s.store != nil {
		snap := ctrl.Snapshot()
		if err := s.store.Update(domain.ToStorageEntry(&snap)); err != nil {
			slog.Error("failed to persist terminal instance", "instance_id", string(id), "error", err)
			s.mu.Lock()
			s.instances[id] = ctrl
			s.mu.Unlock()
			return fmt.Errorf("persist terminal instance %s: %w", string(id), err)
		}
	}

	// Release the terminal run's reservation. This is idempotent and safe if
	// wait() already released it.
	if run := ctrl.currentRun(); run != nil {
		run.releaseSlot()
	}

	return nil
}

// ShutdownWithPersistence stops all active instances and persists terminal instances.
// Returns an aggregated error if any persistence operation failed, while attempting
// to persist all instances even if some fail.
func (s *Supervisor) ShutdownWithPersistence(ctx context.Context) error {
	// Stop all active instances.
	shutdownErr := s.Shutdown(ctx)

	// Persist terminal instances after shutdown.
	var persistErrs []error
	if s.store != nil {
		s.mu.RLock()
		for _, ctrl := range s.instances {
			snap := ctrl.Snapshot()
			if snap.IsTerminal() {
				if err := s.store.Update(domain.ToStorageEntry(&snap)); err != nil {
					persistErrs = append(persistErrs, fmt.Errorf("persist instance %s: %w", string(snap.ID), err))
				}
			}
		}
		s.mu.RUnlock()
	}

	// Combine shutdown and persistence errors.
	var allErrs []error
	if shutdownErr != nil {
		allErrs = append(allErrs, fmt.Errorf("shutdown instances: %w", shutdownErr))
	}
	if len(persistErrs) > 0 {
		allErrs = append(allErrs, fmt.Errorf("persist terminal instances: %w", errors.Join(persistErrs...)))
	}
	if len(allErrs) == 0 {
		return nil
	}
	return errors.Join(allErrs...)
}

// SubscribeLogs returns a log subscription via the LogBroker.
// If no broker is configured, returns a safe no-op subscription whose
// Cancel() is idempotent and never panics on repeated calls.
func (s *Supervisor) SubscribeLogs(instanceID string) *LogSubscription {
	if s.broker == nil {
		// Fallback: create a no-op subscription with closed.Swap(true) for idempotent Cancel.
		done := make(chan struct{})
		return &LogSubscription{
			ch:     make(chan LogStreamEvent, 1),
			done:   done,
			broker: nil, // nil broker -> Cancel() takes the nil-lsub fast path
			lsub: &logSubscriber{
				ch:     make(chan LogStreamEvent, 1),
				closed: atomic.Bool{},
			},
		}
	}
	return s.broker.Subscribe(instanceID)
}

// QueryLogs performs a filtered, paginated log query across all instances.
// Entries are sorted DESC by timestamp, then ASC by instance ID for determinism.
// Pagination is applied once after aggregation (not per-instance).
func (s *Supervisor) QueryLogs(q LogQuery, instanceIDFilter string) *LogResult {
	var allEntries []AggregatedLogEntry
	s.mu.RLock()
	for _, ctrl := range s.instances {
		if ctrl.manager != nil {
			logStore := ctrl.manager.GetLogStore()
			if logStore != nil {
				entries := logStore.CollectAllWithInstanceID(string(ctrl.instanceID))
				allEntries = append(allEntries, entries...)
			}
		}
	}
	s.mu.RUnlock()

	// Apply instanceID filter after aggregation.
	if instanceIDFilter != "" {
		var filtered []AggregatedLogEntry
		for _, e := range allEntries {
			if e.InstanceID == instanceIDFilter {
				filtered = append(filtered, e)
			}
		}
		allEntries = filtered
	}

	return QueryAggregatedLogs(allEntries, q)
}

// Recover restores instances from the store and performs identity-verified
// liveness detection on previously-transitional instances.
// Per ADR 005: PID gone → stale(pid-not-found); alive + identity confirmed →
// orphan; alive + identity unconfirmed → stale(identity-unconfirmed).
// No process is started, stopped, or signaled during recovery.
func (s *Supervisor) Recover(ctx context.Context) error {
	if s.store == nil {
		return nil
	}

	entries, err := s.store.List()
	if err != nil {
		return fmt.Errorf("list instances for recovery: %w", err)
	}

	var persistErrs []error
	for _, entry := range entries {
		inst := domain.ToDomain(entry)

		switch {
		case inst.State.IsInFlight():
			newState, reason := s.classifyForRecovery(inst)
			inst.UpdateState(newState)
			inst.RecoveryReason = reason
			if s.store != nil {
				if err := s.store.Update(domain.ToStorageEntry(inst)); err != nil {
					persistErrs = append(persistErrs, fmt.Errorf("persist recovered instance %s: %w", string(inst.ID), err))
					slog.Error("failed to persist recovered instance", "instance_id", string(inst.ID), "error", err)
					continue
				}
			}
			slog.Info("recovery classified instance",
				"instance_id", string(inst.ID),
				"state", string(newState),
				"reason", reason,
			)
		}
	}

	if len(persistErrs) > 0 {
		return fmt.Errorf("recover: %w", errors.Join(persistErrs...))
	}
	return nil
}

// classifyForRecovery applies the ADR 005 identity contract to determine
// whether a transitional instance is orphan or stale.
func (s *Supervisor) classifyForRecovery(inst *domain.LaunchInstance) (domain.InstanceState, string) {
	if inst.PID <= 0 {
		return domain.InstanceStateStale, "pid-not-found"
	}

	prober := s.prober
	if prober == nil {
		return domain.InstanceStateStale, "identity-unconfirmed"
	}

	alive, err := prober.IsProcessAlive(inst.PID)
	if err != nil || !alive {
		return domain.InstanceStateStale, "pid-not-found"
	}

	identity, err := prober.GetProcessIdentity(inst.PID)
	if err != nil {
		return domain.InstanceStateStale, "identity-unconfirmed"
	}

	if !verifyIdentity(inst, identity) {
		return domain.InstanceStateStale, "identity-unconfirmed"
	}

	return domain.InstanceStateOrphan, ""
}

// verifyIdentity checks the recorded instance against the probed process
// identity using the strongest available anchors per ADR 005.
func verifyIdentity(inst *domain.LaunchInstance, id platform.ProcessIdentity) bool {
	if id.ExecutablePath == "" {
		return false
	}
	if inst.Executable == "" {
		return false
	}
	if !pathsEqual(inst.Executable, id.ExecutablePath) {
		return false
	}
	if id.HasStartTime && !inst.StartedAt.IsZero() {
		if !timesApproximatelyEqual(inst.StartedAt, id.StartTime) {
			return false
		}
	}
	return true
}

// pathsEqual compares two filesystem paths using platform-aware equality.
// On Windows, the .exe extension is optional and paths are case-insensitive.
func pathsEqual(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" {
		fa, fb := strings.ToLower(a), strings.ToLower(b)
		if fa == fb {
			return true
		}
		// Windows: "foo.exe" == "foo"
		const exe = ".exe"
		if strings.HasSuffix(fb, exe) && fa == fb[:len(fb)-len(exe)] {
			return true
		}
		if strings.HasSuffix(fa, exe) && fb == fa[:len(fa)-len(exe)] {
			return true
		}
	}
	return false
}

// timesApproximatelyEqual reports whether two times are within a 5-second window.
func timesApproximatelyEqual(a, b time.Time) bool {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 5*time.Second
}

// DismissOrphan transitions an orphan instance to stale (reconciled-by-user).
// No process is touched. Returns an error if the instance is not in orphan state.
func (s *Supervisor) DismissOrphan(ctx context.Context, instanceID domain.InstanceID) error {
	if s.store == nil {
		return fmt.Errorf("no store configured")
	}

	entry, err := s.store.Get(string(instanceID))
	if err != nil {
		return fmt.Errorf("get instance %s: %w", string(instanceID), err)
	}

	inst := domain.ToDomain(entry)
	if inst.State != domain.InstanceStateOrphan {
		return fmt.Errorf("instance %s is not in orphan state (current: %s)", string(instanceID), string(inst.State))
	}

	inst.UpdateState(domain.InstanceStateStale)
	inst.RecoveryReason = "reconciled-by-user"

	if err := s.store.Update(domain.ToStorageEntry(inst)); err != nil {
		return fmt.Errorf("persist dismissed orphan %s: %w", string(instanceID), err)
	}

	slog.Info("orphan dismissed by user", "instance_id", string(instanceID))
	return nil
}

// ActiveInstances returns a snapshot of active instance IDs for counting.
func (s *Supervisor) ActiveInstances() []domain.InstanceID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []domain.InstanceID
	for id, ctrl := range s.instances {
		if ctrl.IsRunning() {
			ids = append(ids, id)
		}
	}
	return ids
}

// InstanceController controls a single launch instance.
type InstanceController struct {
	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	instance    *domain.LaunchInstance
	instanceID  domain.InstanceID
	// modelID is the immutable ModelID of the controlled instance. The
	// arbitration boundary binds a spawn claim to it so a claim minted for one
	// model can never authorize another controller's spawn (ADR 017 D1).
	modelID string
	manager *Manager
	store   InstanceStore
	// resolver is the launch resolver used to re-resolve a restart's fresh
	// command spec before arbitration (see Supervisor.RestartWithLaunch).
	resolver *domain.LaunchResolver
	// active is the one launch/restart operation currently holding this
	// instance's spawn authorization (ADR 017 D1). It is published by
	// Supervisor.arbitrate under the per-ModelID arbLock + launchMu, read under
	// ic.mu, and released by the owning operation exactly once. For a restart it
	// is the reservation that keeps the operation conflict-visible across the
	// stop -> terminal interval -> spawn window.
	active        *launchOperation
	run           *instanceRunState
	broker        *LogBroker
	supervisorRef *Supervisor
	// preSpawnToken is true iff this launch went through AdmitAndStart
	// registration A (a PRE-SPAWN token was registered in
	// preSpawnInFlight). It is read/cleared only under launchMu, so the token
	// is consumed exactly once. Transitional launches (startWithReservation
	// without admission) never set it, so they never touch the counter.
	preSpawnToken bool
}

// instanceRunState owns all synchronization primitives that belong to one
// process generation. A wait goroutine receives the exact state created for
// its generation and never reads completion or reservation state through the
// reusable InstanceController.
type instanceRunState struct {
	done        chan struct{}
	doneOnce    sync.Once
	managerDone <-chan struct{}
	reservation *slotReservation
}

func newInstanceRunState(reservation *slotReservation) *instanceRunState {
	return &instanceRunState{
		done:        make(chan struct{}),
		reservation: reservation,
	}
}

func (run *instanceRunState) complete() {
	run.doneOnce.Do(func() { close(run.done) })
}

func (run *instanceRunState) releaseSlot() {
	if run.reservation != nil {
		run.reservation.Release()
	}
}

// newController constructs the controller for an admitted launch and wires the
// supervisor back-reference. It is the single production path for controller
// creation so the optional test managerStartHook (RB-015b) applies uniformly.
func (s *Supervisor) newController(inst *domain.LaunchInstance) *InstanceController {
	ctrl := NewInstanceController(inst, s.store, s.resolver, s.broker)
	ctrl.supervisorRef = s
	if s.managerStartHook != nil {
		s.managerStartHook(ctrl.manager)
	}
	return ctrl
}

// NewInstanceController creates a controller for an instance.
func NewInstanceController(inst *domain.LaunchInstance, store InstanceStore, resolver *domain.LaunchResolver, broker *LogBroker) *InstanceController {
	return &InstanceController{
		instance:      inst,
		instanceID:    inst.ID,
		modelID:       inst.ModelID,
		manager:       NewManager(),
		store:         store,
		resolver:      resolver,
		broker:        broker,
		supervisorRef: nil, // set by Supervisor.newController after construction
	}
}

// IsRunning returns true if the instance is in a live state.
func (ic *InstanceController) IsRunning() bool {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.instance.IsLive()
}

// startWithReservation is the lifecycle-wrapped spawn of one generation. The
// caller MUST present the spawn claim minted by Supervisor.arbitrate for the
// operation that currently owns this controller; without it no spawn is
// possible (ADR 017 D1).
//
// The operationCtx parameter is used only for the Start() operation timeout.
// Post-spawn lifecycle operations (running persist + retry, rollback kill,
// exit confirmation, failure persistence) use the supervisor lifecycle
// context, never the caller's request context (ADR 016 §8.1).
//
// ADR 016 S1/F1: a nil error is returned ONLY after the full running identity
// (state=running, PID, StartedAt) is durably persisted. On running persist
// failure the fail-closed rollback applies: confirmed-dead outcomes (A/B)
// return (nil, ErrPersistenceFailure); residual outcomes (C/D, termination
// unconfirmed / kill refused) return (instance, error) with the instance kept
// in the supervisor registry and slot/run ownership held by the wait()
// goroutine until it confirms exit.
func (ic *InstanceController) startWithReservation(operationCtx context.Context, reservation *slotReservation, claim *spawnClaim) (*domain.LaunchInstance, error) {
	ic.lifecycleMu.Lock()
	defer ic.lifecycleMu.Unlock()
	return ic.startCore(operationCtx, reservation, claim)
}

// abortPreSpawn is the commit-C (D<C) pre-spawn abort: shutdown drain won
// before the spawn commit. The claim is already SPENT and stays spent.
func (ic *InstanceController) abortPreSpawn(reservation *slotReservation, claim *spawnClaim) {
	if claim.restart {
		// A restart reuses an existing controller and durable record: there is
		// no pre-spawn token to consume, the controller must NOT leave the
		// registry, and the terminal state already established by the previous
		// generation is the final state. Only the acquired slot is returned.
		// The restart reservation is released by its owner after this final
		// state, never here.
		if reservation != nil {
			reservation.Release()
		}
		return
	}
	// New admission: terminalize the durable pending record, remove the
	// controller, release the slot and consume the PRE-SPAWN token ABORTED.
	ic.supervisorRef.abortPreSpawnCleanup(ic.instanceID, ic, reservation, true)
}

// startCore spawns this generation's process. It is the single commit-C
// boundary: the generation-bound spawn claim is validated and consumed exactly
// once under launchMu before the shutdown drain condition is evaluated, so a
// path that reaches manager.Start has necessarily passed the ADR 017
// arbitration boundary (D1/BF-01).
func (ic *InstanceController) startCore(operationCtx context.Context, reservation *slotReservation, claim *spawnClaim) (*domain.LaunchInstance, error) {
	s := ic.supervisorRef
	if s == nil {
		// Without a Supervisor there is no arbitration boundary, therefore no
		// valid claim can exist for this controller.
		return nil, fmt.Errorf("start instance %s: %w: controller is not supervised", string(ic.instanceID), errSpawnClaimRejected)
	}

	// RB-015b commit C: acquire launchMu BEFORE ic.mu. This is the
	// spawn-commit linearization point against shutdown drain-start (D).
	s.launchMu.Lock()
	ic.mu.Lock()

	// Consume the spawn authorization first (identity-validated, CAS-spent). A
	// spent claim is never restored: a retry needs fresh arbitration, a fresh
	// operation identity and a fresh claim.
	if err := ic.consumeSpawnClaimLocked(claim); err != nil {
		ic.mu.Unlock()
		s.launchMu.Unlock()
		return nil, err
	}

	if s.draining {
		// D < C: shutdown won. The claim stays SPENT. Do NOT publish starting,
		// do NOT call manager.Start; abort PRE-SPAWN (the cleanup releases
		// launchMu before any I/O / s.mu / reservation work).
		ic.mu.Unlock()
		s.launchMu.Unlock()
		ic.abortPreSpawn(reservation, claim)
		return nil, ErrLaunchAbortedByShutdown
	}
	// C < D: publish the pending->starting transition while launchMu is
	// still held so the starting publication happens-before the counter
	// handoff, and consume the PRE-SPAWN token as COMMITTED. After this
	// point the token no longer exists: no later startCore/ADR 016
	// failure path may touch preSpawnInFlight.
	ic.instance.UpdateState(domain.InstanceStateStarting)
	if ic.preSpawnToken {
		s.preSpawnInFlight--
		ic.preSpawnToken = false
		s.signalPreSpawnZeroLocked()
	}
	s.launchMu.Unlock()

	run := newInstanceRunState(reservation)
	ic.run = run
	// Persist the starting state while ic.mu is held so wait() reads consistent ic.instance.
	if err := ic.persistStateLocked(); err != nil {
		ic.mu.Unlock()
		run.releaseSlot()
		run.complete()
		// C already committed the token: removing the not-yet-spawned
		// controller keeps the registry consistent. No token accounting
		// here — the token was consumed at C.
		s.mu.Lock()
		delete(s.instances, ic.instanceID)
		s.mu.Unlock()
		return nil, fmt.Errorf("persist starting state: %w", err)
	}

	// Resolve the command spec from stored instance data.
	spec := &CommandSpec{
		Executable:       ic.instance.Executable,
		Args:             ic.instance.Args,
		WorkingDirectory: ic.instance.WorkingDirectory,
		Environment:      ic.instance.EnvironmentToList(),
	}
	logCh, cancelLogs := ic.manager.Subscribe()

	if err := ic.manager.Start(operationCtx, *spec); err != nil {
		cancelLogs()
		ic.instance.Fail(err.Error(), domain.InstanceExitError)
		slog.Error("instance start failed", "instance_id", string(ic.instance.ID), "model_id", ic.instance.ModelID, "error", err.Error())
		if persistErr := ic.persistStateLocked(); persistErr != nil {
			slog.Error("persist start failure state", "instance_id", string(ic.instance.ID), "persist_error", persistErr)
		}
		ic.mu.Unlock()
		run.releaseSlot()
		run.complete()
		return nil, err
	}
	run.managerDone = ic.manager.GetDoneChannel()

	// Copy PID from manager immediately after successful start.
	// (ic.mu already held from line 617 — no need to re-acquire)
	status := ic.manager.Status()
	ic.instance.PID = status.PID
	ic.instance.StartedAt = status.StartedAt
	ic.instance.UpdateState(domain.InstanceStateRunning)

	// Persist running state with the full running identity (state=running,
	// PID, StartedAt) — ADR 016 S1/S2: Start() may return success ONLY after
	// this record is durable. Bounded synchronous retry on the supervisor
	// lifecycle context (ADR 016 §8/§8.1); the caller's request context is
	// NOT used for post-spawn operations.
	if persistErr := persistWithRetry(ic.lifecycleContext(), ic.persistStateLocked); persistErr != nil {
		// Fail-closed (ADR 016 §2/§4): degraded success is rejected. If the
		// full running identity is not durable, Start() returns an error and
		// the rollback contract applies.
		slog.Error("failed to persist running state; failing closed",
			"instance_id", string(ic.instance.ID),
			"model_id", ic.instance.ModelID,
			"pid", ic.instance.PID,
			"executable", ic.instance.Executable,
			"error", persistErr)
		residual, rollbackErr := ic.rollbackRunningPersistenceLocked(persistErr)
		ic.mu.Unlock()
		go ic.forwardLogs(logCh)
		go ic.wait(run, cancelLogs)
		if !residual {
			// Outcome A/B: exit is confirmed. startCore releases the slot
			// and completes the run (ADR 016 §9). The wait() goroutine
			// detects the already-dead process; its release/complete are
			// no-ops (sync.Once).
			run.releaseSlot()
			run.complete()
			return nil, rollbackErr
		}
		// Outcome C/D: slot and run ownership stay with the wait()
		// goroutine until it confirms exit (exactly once, sync.Once).
		return ic.instance, rollbackErr
	}

	// Publish start event via broker.
	ic.publishBrokerEvent(LogStreamSystem, "instance started")

	// Release ic.mu before starting wait goroutine and returning.
	// wait() will acquire ic.mu after process exit, and Sup.Start() will
	// acquire ctrl.mu (same as ic.mu) to set reservation.  Without this
	// unlock, both wait() and Sup.Start() block forever on the same mutex.
	ic.mu.Unlock()

	// Start wait goroutine.
	go ic.forwardLogs(logCh)
	go ic.wait(run, cancelLogs)

	return ic.instance, nil
}

// rollbackRunningPersistenceLocked implements the ADR 016 §4 fail-closed
// rollback: the process was spawned but persist(running+PID) exhausted its
// bounded retry, so the running identity is NOT durable. The caller MUST
// hold ic.mu, and the caller performs the unlock and starts the wait()
// goroutine after this returns.
//
// Outcome matrix (ADR 016 §2.1). The boolean result reports whether slot
// and run ownership is residual (held by the wait() goroutine until it
// confirms exit):
//   - A/B (residual=false): kill accepted (or the process was already gone)
//     and exit is CONFIRMED. The state becomes failed, the failed state is
//     persisted best-effort. Returns ErrPersistenceFailure.
//   - C (residual=true): kill accepted by the OS, termination NOT confirmed.
//     State stays starting; nothing is persisted. Returns
//     ErrPersistenceFailure + ErrTerminationUnconfirmed.
//   - D (residual=true): kill refused by the OS (genuine refusal; the
//     process may be alive). Same residual ownership as C. Returns
//     ErrPersistenceFailure + ErrRollbackFailed.
//
// Kill() returning nil is NEVER treated as a confirmed process exit: exit is
// confirmed only when the Manager's done channel closes (the single
// cmd.Wait() owner), checked through confirmExit.
func (ic *InstanceController) rollbackRunningPersistenceLocked(persistErr error) (residual bool, rollbackErr error) {
	lcCtx := ic.lifecycleContext()

	killErr := ic.manager.Kill()
	var confirmed bool
	switch {
	case killErr == nil:
		// Kill accepted by the OS: wait for CONFIRMED exit within a bounded
		// window (ADR 016 §4 step 2, §8.1).
		confirmed = ic.confirmExit(lcCtx, rollbackExitConfirmWindow)
	case errors.Is(killErr, platform.ErrKillAlreadyGone):
		// The process exited between the persist failure and the kill
		// attempt: reclassified as confirmed dead (ADR 016 §2.1 note),
		// NOT Outcome D.
		confirmed = true
	}

	if confirmed {
		// Outcome A/B.
		ic.instance.UpdateState(domain.InstanceStateFailed)
		ic.instance.LastError = fmt.Sprintf("persist running state failed: %v; rollback: terminated", persistErr)
		if ferr := ic.persistStateLocked(); ferr != nil {
			// Outcome B: the failed state is not durable either. The
			// repository retains starting (no PID); recovery classifies
			// the dead process as stale (pid-not-found) — correct.
			slog.Error("failed to persist rolled-back failed state",
				"instance_id", string(ic.instance.ID), "error", ferr)
		}
		return false, errors.Join(ErrPersistenceFailure, persistErr)
	}

	// Outcomes C/D (unified residual-ownership contract, ADR 016 §2.1):
	// termination is not confirmed, so the process may be alive. Do NOT set
	// a terminal state, do NOT persist, do NOT release the slot, do NOT
	// complete the run. The running identity was never durable, so the
	// in-memory state is reverted to starting (ADR §2.1 matrix: Controller
	// Memory = starting); the PID is retained in memory for the wait()
	// goroutine and the ERROR log. Ownership moves to the wait() goroutine,
	// which releases the slot and completes the run exactly once when it
	// confirms exit.
	ic.instance.UpdateState(domain.InstanceStateStarting)
	if killErr == nil {
		ic.instance.UpdateError(fmt.Sprintf("persist running state failed: %v; rollback: unconfirmed", persistErr), "")
		slog.Error("rollback: kill accepted but termination unconfirmed; holding slot and run ownership",
			"instance_id", string(ic.instance.ID), "model_id", ic.instance.ModelID,
			"pid", ic.instance.PID, "executable", ic.instance.Executable)
		return true, errors.Join(ErrPersistenceFailure, persistErr, ErrTerminationUnconfirmed)
	}
	ic.instance.UpdateError(fmt.Sprintf("persist running state failed: %v; rollback: failed: %v", persistErr, killErr), "")
	slog.Error("rollback: kill refused by OS; holding slot and run ownership",
		"instance_id", string(ic.instance.ID), "model_id", ic.instance.ModelID,
		"pid", ic.instance.PID, "executable", ic.instance.Executable, "kill_error", killErr)
	return true, errors.Join(ErrPersistenceFailure, persistErr, ErrRollbackFailed, killErr)
}

// Stop requests graceful shutdown of the instance process.
func (ic *InstanceController) Stop(ctx context.Context) error {
	ic.lifecycleMu.Lock()
	defer ic.lifecycleMu.Unlock()
	return ic.stop(ctx)
}

func (ic *InstanceController) stop(ctx context.Context) error {
	ic.mu.Lock()
	if ic.instance.IsTerminal() {
		ic.mu.Unlock()
		return nil
	}
	// A pending instance has no process yet: its launch is still in flight
	// (slot acquisition / spawn). Stopping it here would be silently
	// invalidated by the in-flight start, so refuse with a bounded error.
	if ic.instance.State == domain.InstanceStatePending {
		ic.mu.Unlock()
		return ErrLaunchInFlight
	}
	ic.instance.UpdateState(domain.InstanceStateStopping)
	ic.mu.Unlock()

	return ic.stopCore(ctx)
}

// stopCore is the internal stop logic. It manages its own locking.
// Callers that already hold ic.mu should call stopCoreNoLock instead.
//
// Locking order:
//  1. Acquire ic.mu
//  2. Persist "stopping" state (mock store may reject this — error returned)
//  3. Release ic.mu so wait() can acquire
//  4. Wait on the current run's done channel
//  5. Re-acquire ic.mu, update LastError, persist final state
//  6. Release ic.mu
func (ic *InstanceController) stopCore(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Acquire lock for the persist operations.
	ic.mu.Lock()

	// Signal the process to stop (state is already "stopping" from Stop()).
	stopErr := ic.manager.Stop(ctx)

	// Persist "stopping" state before releasing lock — this is where the
	// mock store in TestStopPersistenceFailureReturned rejects with error.
	persistErr := ic.persistStateLocked()
	if persistErr != nil {
		ic.mu.Unlock()
		return persistErr
	}

	// Release lock so wait() can acquire.
	// wait() needs ic.mu to write ic.instance fields and call store.Update.
	ic.mu.Unlock()

	// Wait for the InstanceController goroutine to fully exit to ensure
	// wait() has finished updating ic.instance fields and persisting.
	// We wait on the run-specific done channel (closed by wait() after ALL field writes),
	// NOT ic.manager.done (signaled when the process exits), to prevent
	// a data race between Stop()'s persistState() and wait()'s
	// ic.instance field writes.
	// After receiving on run.done, take ic.mu to establish happens-before:
	// wait() releases ic.mu BEFORE close(run.done), so Stop()'s ic.mu lock
	// synchronizes with wait()'s ic.mu unlock, ensuring we see all field writes.
	done := ic.GetControllerDone()
	if done != nil {
		select {
		case <-done:
			// Synchronize with wait()'s ic.mu release to see all field writes.
			ic.mu.Lock()
			ic.mu.Unlock()
		case <-ctx.Done():
			if stopErr == nil {
				stopErr = ctx.Err()
			}
		}
	}

	// Update instance error if stop failed.
	ic.mu.Lock()
	if stopErr != nil {
		ic.instance.UpdateError(stopErr.Error(), domain.InstanceExitError)
	}
	ic.mu.Unlock()

	// Persist final state. If persistence fails, return error.
	// Hold ic.mu to synchronize with wait()'s field writes.
	ic.mu.Lock()
	persistErr = ic.persistState()
	if persistErr != nil {
		ic.mu.Unlock()
		return persistErr
	}
	ic.mu.Unlock()

	return stopErr
}

// restartWithRefresh performs the restart lifecycle for one operation that has
// already won arbitration and holds the generation-bound spawn claim (the
// controller's published reservation). It is package-private and unexported on
// purpose: the only way to reach it is Supervisor.Restart / RestartWithLaunch,
// which arbitrate first (D1/BF-01).
//
// Lock contract: lifecycleMu is acquired here, strictly after arbLock was
// released by arbitrate. The reservation is released exactly once at return —
// after safe ownership transfer to the new generation or on final failure —
// never merely because the old generation became terminal.
func (ic *InstanceController) restartWithRefresh(ctx context.Context, spec *domain.CommandSpec, runtimeID string, claim *spawnClaim) (*domain.LaunchInstance, error) {
	ic.lifecycleMu.Lock()
	defer ic.lifecycleMu.Unlock()

	// Release the reservation only at the end of this attempt (exact-operation,
	// idempotent), so the restart stays conflict-visible through the stop, the
	// terminal interval and the slot wait.
	defer ic.releaseLaunchOperation(claim.opID)

	// Revalidate under lifecycleMu that this operation still owns the
	// controller: lifecycle serialization (a concurrent Stop or a second
	// restart attempt) must not have transferred ownership.
	if err := ic.revalidateOperation(claim); err != nil {
		return nil, err
	}

	ic.mu.RLock()
	running := ic.instance.State == domain.InstanceStateRunning
	previousRun := ic.run
	ic.mu.RUnlock()
	if running {
		if err := ic.stop(ctx); err != nil {
			return nil, err
		}
	} else if previousRun != nil {
		// Either the instance is stopping — an existing stop owner is still
		// finalizing it — or it is already terminal while its wait goroutine is
		// still persisting final state and releasing the run's slot. Await that
		// owner instead of becoming a second stop owner or publishing a new
		// generation through a live run.
		select {
		case <-previousRun.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for previous process run: %w", ctx.Err())
		}
	}

	if spec != nil {
		// Refresh the frozen launch fields under ic.mu: startCore reads them
		// while holding the same lock, and lifecycleMu orders this refresh
		// before the new run is published — no race with the new startCore.
		ic.mu.Lock()
		if runtimeID != "" {
			ic.instance.RuntimeID = runtimeID
		}
		ic.instance.Executable = spec.Executable
		ic.instance.Args = spec.Args
		ic.instance.WorkingDirectory = spec.WorkingDirectory
		ic.instance.Environment = specEnvironmentMap(spec.Environment)
		ic.mu.Unlock()
	}

	reservation, err := ic.supervisorRef.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}

	// BF-09: the new process generation is owned by the supervisor lifecycle
	// context, never by this request. The request context stays in use only for
	// the bounded pre-spawn waits above (stop, previous-run wait, slot wait).
	if _, err := ic.startCore(ic.lifecycleContext(), reservation, claim); err != nil {
		return nil, err
	}

	snap := ic.Snapshot()
	return &snap, nil
}

// specEnvironmentMap converts the resolved "k=v" launch environment into the
// instance environment map (same conversion as ResolveToInstance).
func specEnvironmentMap(env []string) map[string]string {
	envMap := make(map[string]string, len(env))
	for _, ev := range env {
		if k, v, ok := strings.Cut(ev, "="); ok {
			envMap[k] = v
		}
	}
	return envMap
}

// Snapshot returns a copy of the current instance state.
func (ic *InstanceController) Snapshot() domain.LaunchInstance {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	snap := *ic.instance
	snap.Environment = make(map[string]string, len(ic.instance.Environment))
	for k, v := range ic.instance.Environment {
		snap.Environment[k] = v
	}
	snap.Args = make([]string, len(ic.instance.Args))
	copy(snap.Args, ic.instance.Args)
	return snap
}

// wait monitors the process and updates instance state.
// It maps the Manager exit class to the domain exit class and transitions
// the instance to either "exited" (for normal/user-initiated stops) or
// "failed" (for unexpected exits).
func (ic *InstanceController) wait(run *instanceRunState, cancelLogs func()) {
	done := run.managerDone
	if done == nil {
		if cancelLogs != nil {
			cancelLogs()
		}
		run.releaseSlot()
		run.complete()
		return
	}
	<-done
	if cancelLogs != nil {
		cancelLogs()
	}
	// Signal that InstanceController.wait is fully complete (all ic.instance
	// fields written under the lock below). This unblocks Stop() which is
	// reading these same fields via persistState().
	// Use sync.Once to allow multiple wait() calls (e.g. during Restart).
	// Step 1: Acquire lock once to update all state atomically.
	// We collect all data under lock, then release it before doing
	// external side effects (store.Update, reservation.Release).
	ic.mu.Lock()

	finalStatus := ic.manager.Status()

	ic.instance.PID = finalStatus.PID
	ic.instance.ExitCode = finalStatus.ExitCode

	var domainExitClass domain.InstanceExitClass
	var targetState domain.InstanceState

	switch finalStatus.ExitClass {
	case processExitNormal:
		domainExitClass = domain.InstanceExitNormal
		targetState = domain.InstanceStateExited
	case processExitKilled:
		domainExitClass = domain.InstanceExitKilled
		targetState = domain.InstanceStateExited
	case processExitSignaled:
		domainExitClass = domain.InstanceExitSignaled
		targetState = domain.InstanceStateExited
	case processExitContext:
		domainExitClass = domain.InstanceExitContext
		targetState = domain.InstanceStateExited
	case processExitTimeout:
		domainExitClass = domain.InstanceExitTimeout
		targetState = domain.InstanceStateFailed
	case processExitError:
		domainExitClass = domain.InstanceExitError
		targetState = domain.InstanceStateFailed
	case processExitFailure:
		domainExitClass = domain.InstanceExitFailure
		targetState = domain.InstanceStateFailed
	default:
		domainExitClass = domain.InstanceExitFailure
		targetState = domain.InstanceStateFailed
	}

	ic.instance.ExitClass = domainExitClass
	if finalStatus.LastError != "" {
		if ic.instance.LastError == "" {
			ic.instance.LastError = finalStatus.LastError
		} else {
			ic.instance.LastError = ic.instance.LastError + "; " + finalStatus.LastError
		}
	}
	ic.instance.State = targetState
	ic.instance.StoppedAt = time.Now()
	ic.instance.UpdatedAt = ic.instance.StoppedAt

	if ic.store != nil {
		// The terminal state in memory is authoritative (ADR 016 T3): the
		// entry is captured under the lock above and is not re-derived after
		// a persist failure.
		entry := domain.ToStorageEntry(ic.instance)
		ic.mu.Unlock()

		// Persist final state OUTSIDE the lock to avoid holding mutex during
		// I/O, with bounded retry on the supervisor lifecycle context
		// (ADR 016 §8, RB-003). A cancelled context (shutdown) exits the
		// retry immediately; ShutdownWithPersistence retries later
		// best-effort (T4).
		if err := persistWithRetry(ic.lifecycleContext(), func() error {
			return ic.store.Update(entry)
		}); err != nil {
			// Explicit terminal persistence exhaustion (ADR 016 RB-003):
			// the run is STILL fully finalized below (T1/T2/T3). The durable
			// record stays at the last successful state (typically running
			// with PID); after a crash, Recover classifies the dead PID as
			// stale (T5) — recovery reconciliation, not a persisted
			// fallback.
			persistErr := fmt.Errorf("persist final state: %w", err)
			slog.Error("terminal state persistence failed after retry; run finalizes with stale durable record",
				"instance_id", string(ic.instance.ID), "state", string(targetState), "error", err)
			// Update LastError — caller has already seen the snapshot,
			// but we record it for monitoring.
			ic.mu.Lock()
			if ic.instance.LastError != "" {
				ic.instance.LastError = ic.instance.LastError + "; " + persistErr.Error()
			} else {
				ic.instance.LastError = persistErr.Error()
			}
			ic.mu.Unlock()
		}
	} else {
		ic.mu.Unlock()
	}

	// Release the concurrency slot OUTSIDE the lock to avoid holding mutex during I/O.
	run.releaseSlot()

	// Publish exit event via broker (no lock needed).
	exitCode := 0
	if ic.instance.ExitCode != nil {
		exitCode = *ic.instance.ExitCode
	}
	ic.publishBrokerEvent(LogStreamSystem, fmt.Sprintf("instance exited: code=%d class=%s", exitCode, ic.instance.ExitClass))

	// Completion is the final run-side effect. Restart may proceed immediately
	// after this close without sharing any synchronization primitive with run.
	run.complete()
}

func (ic *InstanceController) forwardLogs(events <-chan LogEvent) {
	if ic.broker == nil {
		return
	}
	for event := range events {
		ic.broker.Publish(LogStreamEvent{
			InstanceID: string(ic.instanceID),
			ModelID:    ic.instance.ModelID,
			Stream:     LogStream(event.Stream),
			Message:    event.Message,
			Timestamp:  event.Time,
		})
	}
}

// publishBrokerEvent publishes a log event via the broker.
func (ic *InstanceController) publishBrokerEvent(stream LogStream, message string) {
	if ic.broker == nil {
		return
	}
	ic.broker.Publish(LogStreamEvent{
		InstanceID: string(ic.instanceID),
		ModelID:    ic.instance.ModelID,
		Stream:     stream,
		Message:    message,
		Timestamp:  time.Now(),
	})
}

// processExitClass represents Manager exit classes.
type processExitClass = string

const (
	processExitNormal   = ExitClass("normal")
	processExitFailure  = ExitClass("failure")
	processExitKilled   = ExitClass("killed")
	processExitTimeout  = ExitClass("timeout")
	processExitContext  = ExitClass("context")
	processExitError    = ExitClass("error")
	processExitSignaled = ExitClass("signaled")
)

// rollbackExitConfirmWindow is the bounded window to confirm process exit
// after a rollback kill (ADR 016 §4 step 2). Implementation parameter,
// overridable in tests.
var rollbackExitConfirmWindow = 5 * time.Second

// SetRollbackConfirmWindow overrides the rollback exit-confirmation window
// (test hook, ADR 016).
func SetRollbackConfirmWindow(window time.Duration) {
	rollbackExitConfirmWindow = window
}

// lifecycleContext returns the supervisor lifecycle context that owns this
// controller's post-spawn operations (ADR 016 §8.1). The caller's request
// context must never be used for post-spawn lifecycle operations: a
// disconnected/cancelled HTTP request must not abandon ownership of an
// already-spawned process.
func (ic *InstanceController) lifecycleContext() context.Context {
	if ic.supervisorRef != nil {
		return ic.supervisorRef.lifecycleContext()
	}
	return context.Background()
}

// confirmExit reports whether the managed process has CONFIRMED its exit —
// i.e. the Manager's single cmd.Wait() owner closed the done channel — within
// window, or before ctx is cancelled. A kill that returned nil is never
// treated as an exit confirmation (ADR 016 §2.1, §8.1).
func (ic *InstanceController) confirmExit(ctx context.Context, window time.Duration) bool {
	done := ic.manager.GetDoneChannel()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(window):
		return false
	case <-ctx.Done():
		return false
	}
}

// persistStateLocked persists the current instance state to the store.
// The caller MUST hold ic.mu.
func (ic *InstanceController) persistStateLocked() error {
	if ic.store == nil {
		return nil
	}
	entry := domain.ToStorageEntry(ic.instance)
	return ic.store.Update(entry)
}

// persistState persists the current instance state to the store.
// The caller is responsible for locking (does NOT acquire ic.mu).
func (ic *InstanceController) persistState() error {
	if ic.store == nil {
		return nil
	}
	entry := domain.ToStorageEntry(ic.instance)
	return ic.store.Update(entry)
}

// GetDoneChannel returns the manager's done channel for monitoring.
func (ic *InstanceController) GetDoneChannel() <-chan struct{} {
	if ic.manager == nil {
		return nil
	}
	return ic.manager.GetDoneChannel()
}

// GetControllerDone returns the InstanceController's internal done channel.
// This channel is closed after InstanceController.wait() completes entirely,
// including persist-final-state and reservation.Release().
// It allows callers to wait for ALL controller-side effects to finish
// before inspecting instance state (e.g., LastError).
func (ic *InstanceController) GetControllerDone() <-chan struct{} {
	if ic == nil {
		return nil
	}
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	if ic.run == nil {
		return nil
	}
	return ic.run.done
}

func (ic *InstanceController) currentRun() *instanceRunState {
	if ic == nil {
		return nil
	}
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.run
}
