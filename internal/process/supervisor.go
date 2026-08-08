package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// SupervisorStatus describes the overall supervisor state.
type SupervisorStatus struct {
	ActiveInstances  int               `json:"active_instances"`
	RunningInstances int               `json:"running_instances"`
	Instances        []InstanceSummary `json:"instances,omitempty"`
}

// InstanceSummary is a lightweight summary of an instance state.
type InstanceSummary struct {
	ID        string `json:"id"`
	ProfileID string `json:"profile_id"`
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
}

// Supervisor manages multiple launch instances.
// Each instance has its own process.Manager, allowing concurrent or sequential
// launches of different profiles.
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
	semaphore     chan struct{} // buffered channel for concurrency limiting, single source of truth
	lifecycleCtx  context.Context
	broker        *LogBroker
}

// InstanceStore persists and retrieves launch instances.
// Uses domain.LaunchInstanceEntry to avoid dependency on storage package.
type InstanceStore interface {
	Create(e *domain.LaunchInstanceEntry) error
	Get(id string) (*domain.LaunchInstanceEntry, error)
	Update(e *domain.LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*domain.LaunchInstanceEntry, error)
	ListByProfileID(profileID string) ([]*domain.LaunchInstanceEntry, error)
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

// concurrentCount returns the number of currently held slots.
// For a buffered semaphore, this is len(semaphore) which counts unacquired tokens.
// Held = maxConcurrent - len(semaphore).
func (s *Supervisor) concurrentCount() int {
	if s.semaphore == nil {
		return 0
	}
	return s.maxConcurrent - len(s.semaphore)
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

// Resolve returns a resolved CommandSpec for the given profile, runtime, and model.
func (s *Supervisor) Resolve(profile *domain.Profile, runtime *domain.Runtime, model *domain.Model, customArgs []string, customEnv map[string]string) (*domain.CommandSpec, error) {
	return s.resolver.Resolve(profile, runtime, model, customArgs, customEnv)
}

// ResolvePreview returns a resolved CommandSpec without creating an instance.
func (s *Supervisor) ResolvePreview(profile *domain.Profile, runtime *domain.Runtime, model *domain.Model, customArgs []string, customEnv map[string]string) (*domain.CommandSpec, error) {
	return s.resolver.Resolve(profile, runtime, model, customArgs, customEnv)
}

// RuntimeToDomain converts storage.RuntimeEntry to domain.Runtime.
func RuntimeToDomain(id, name, executable, workingDir string, defaultArgs []string, environment map[string]string) *domain.Runtime {
	return &domain.Runtime{
		ID:               id,
		Name:             name,
		Executable:       executable,
		WorkingDirectory: workingDir,
		DefaultArgs:      defaultArgs,
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

// Start creates a new launch instance and starts its process.
// Uses a buffered semaphore channel as the single source of truth for
// concurrency limiting. A slotReservation is created atomically with the
// semaphore acquire and guaranteed to be released exactly once via sync.Once.
//
// Slot lifecycle:
//   - Acquired before Start() proceeds
//   - Released on: Start failure, persistence failure, natural exit, stop,
//     restart, remove, or supervisor shutdown
//   - Restart reuses the same reservation (no second acquire)
//   - No double release (sync.Once)
func (s *Supervisor) Start(ctx context.Context, profile *domain.Profile, runtime *domain.Runtime, model *domain.Model, customArgs []string, customEnv map[string]string) (*domain.LaunchInstance, error) {
	inst, err := s.resolver.ResolveToInstance(profile, runtime, model, customArgs, customEnv)
	if err != nil {
		return nil, fmt.Errorf("resolve instance: %w", err)
	}

	// Create controller immediately (before reservation) so that RemoveTerminal
	// can find it and persist its state even if reservation is not yet acquired.
	ctrl := NewInstanceController(inst, s.store, s.resolver, s.broker)
	ctrl.supervisorRef = s

	s.mu.Lock()
	s.instances[inst.ID] = ctrl
	s.mu.Unlock()

	// Acquire a concurrency slot via buffered channel. This blocks until a slot
	// is available or ctx is cancelled. Wrap in slotReservation for exactly-once release.
	var reservation *slotReservation
	if s.maxConcurrent > 0 {
		select {
		case <-s.semaphore: // acquire: receive token
			reservation = newSlotReservation(s.semaphore)
		case <-ctx.Done():
			s.mu.Lock()
			delete(s.instances, inst.ID)
			s.mu.Unlock()
			return nil, fmt.Errorf("acquire concurrency slot: %w", ctx.Err())
		}
	}

	// Persist initial pending instance.
	if s.store != nil {
		entry := domain.ToStorageEntry(inst)
		if err := s.store.Create(entry); err != nil {
			if reservation != nil {
				reservation.Release()
				reservation = nil
			}
			s.mu.Lock()
			delete(s.instances, inst.ID)
			s.mu.Unlock()
			return nil, fmt.Errorf("persist instance: %w", err)
		}
	}

	// Start process using the supervisor lifecycle context (not HTTP request ctx).
	ctrlInst, err := ctrl.Start(s.lifecycleContext())
	if err != nil {
		if ctrlInst != nil {
			// Rollback returned an instance — it's in the supervisor's instances map.
		} else {
			inst.Fail(err.Error(), domain.InstanceExitError)
			if s.store != nil {
				if uerr := s.store.Update(domain.ToStorageEntry(inst)); uerr != nil {
					// Join process error and persistence error — caller gets both.
					err = errors.Join(err, fmt.Errorf("persist start error: %w", uerr))
				}
			}
			s.mu.Lock()
			delete(s.instances, inst.ID)
			s.mu.Unlock()
			return nil, fmt.Errorf("start instance %s: %w", inst.ID, err)
		}
		// Rollback returned instance — it's already in the supervisor's instances map.
		// Clean up reservation from the map if present.
		if reservation != nil {
			reservation.Release()
			reservation = nil
		}
		return ctrlInst, fmt.Errorf("start instance %s: %w", inst.ID, err)
	}

	// Store the reservation in the controller so wait() and Stop() can release it.
	// Use ctrl.mu to synchronize the write — wait() reads ic.reservation under ic.mu.
	ctrl.mu.Lock()
	ctrl.reservation = reservation
	ctrl.mu.Unlock()

	return inst, nil
}

// Stop stops a specific instance by ID.
func (s *Supervisor) Stop(ctx context.Context, id domain.InstanceID) error {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}

	return ctrl.Stop(ctx)
}

// Restart restarts a specific instance.
func (s *Supervisor) Restart(ctx context.Context, id domain.InstanceID) (*domain.LaunchInstance, error) {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	return ctrl.Restart(ctx)
}

// Status returns a snapshot of a specific instance.
func (s *Supervisor) Status(id domain.InstanceID) (*domain.LaunchInstance, error) {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
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

// ListByProfileID returns instances for a specific profile.
func (s *Supervisor) ListByProfileID(profileID string) ([]*domain.LaunchInstance, error) {
	if s.store == nil {
		return nil, nil
	}
	entries, err := s.store.ListByProfileID(profileID)
	if err != nil {
		return nil, err
	}
	result := make([]*domain.LaunchInstance, 0, len(entries))
	for _, e := range entries {
		result = append(result, domain.ToDomain(e))
	}
	return result, nil
}

// Shutdown stops all active instances gracefully.
func (s *Supervisor) Shutdown(ctx context.Context) error {
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

	// Release this instance's reservation. Idempotent via sync.Once.
	// Take reservation under lock to avoid data race with wait() which also
	// accesses ic.reservation.
	var ctrlRes *slotReservation
	ctrl.mu.Lock()
	ctrlRes = ctrl.reservation
	ctrl.reservation = nil
	ctrl.mu.Unlock()

	if ctrlRes != nil {
		ctrlRes.Release()
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

// Recover restores instances from the store and marks stale records.
// This is called during Supervisor startup to handle instances that were
// running when the application previously stopped.
// Returns an aggregated error if any stale instance failed to persist.
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

		switch inst.State {
		case domain.InstanceStateRunning, domain.InstanceStateStarting, domain.InstanceStateStopping, domain.InstanceStatePending:
			// The instance was not properly stopped. Mark as stale.
			inst.UpdateState(domain.InstanceStateStale)
			if s.store != nil {
				if err := s.store.Update(domain.ToStorageEntry(inst)); err != nil {
					persistErrs = append(persistErrs, fmt.Errorf("persist stale instance %s: %w", string(inst.ID), err))
					slog.Error("failed to persist stale instance", "instance_id", string(inst.ID), "error", err)
					continue
				}
			}
			slog.Warn("marking stale instance", "instance_id", string(inst.ID), "state", string(inst.State))
		}
	}

	if len(persistErrs) > 0 {
		return fmt.Errorf("recover: %w", errors.Join(persistErrs...))
	}
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
	mu         sync.RWMutex
	instance   *domain.LaunchInstance
	instanceID domain.InstanceID
	manager    *Manager
	store      InstanceStore
	resolver   *domain.LaunchResolver
	done       chan struct{}
	doneOnce   sync.Once
	broker     *LogBroker
	// supervisorRef points back to the parent Supervisor for reservation release.
	supervisorRef *Supervisor
	// reservation is the concurrency slot token acquired by Start().
	// Release() via sync.Once ensures exactly-once release on exit/stop/restart/remove.
	reservation *slotReservation
}

// NewInstanceController creates a controller for an instance.
func NewInstanceController(inst *domain.LaunchInstance, store InstanceStore, resolver *domain.LaunchResolver, broker *LogBroker) *InstanceController {
	return &InstanceController{
		instance:      inst,
		instanceID:    inst.ID,
		manager:       NewManager(),
		store:         store,
		resolver:      resolver,
		done:          make(chan struct{}),
		broker:        broker,
		supervisorRef: nil, // set by Supervisor.Start after construction
	}
}

// IsRunning returns true if the instance is in a live state.
func (ic *InstanceController) IsRunning() bool {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.instance.IsActive()
}

// Start launches the managed process.
// The operationCtx parameter is used only for the Start() operation timeout.
// The process lifecycle uses the instance-specific context (supervisor lifecycle).
// Returns the instance and error if persistence fails after process started —
// the instance is left in a degraded (running) state with LastError set.
func (ic *InstanceController) Start(operationCtx context.Context) (*domain.LaunchInstance, error) {
	ic.mu.Lock()
	ic.instance.UpdateState(domain.InstanceStateStarting)
	ic.mu.Unlock()

	// Persist the starting state immediately so restart sees starting, not pending.
	if err := ic.persistState(); err != nil {
		return nil, fmt.Errorf("persist starting state: %w", err)
	}

	// Resolve the command spec from stored instance data.
	spec := &CommandSpec{
		Executable:       ic.instance.Executable,
		Args:             ic.instance.Args,
		WorkingDirectory: ic.instance.WorkingDirectory,
		Environment:      ic.instance.EnvironmentToList(),
	}

	if err := ic.manager.Start(operationCtx, *spec); err != nil {
		ic.mu.Lock()
		ic.instance.Fail(err.Error(), domain.InstanceExitError)
		ic.mu.Unlock()
		if persistErr := ic.persistState(); persistErr != nil {
			slog.Error("persist start failure state", "instance_id", string(ic.instance.ID), "persist_error", persistErr)
		}
		return nil, err
	}

	// Copy PID from manager immediately after successful start.
	status := ic.manager.Status()
	ic.mu.Lock()
	ic.instance.PID = status.PID
	ic.instance.StartedAt = status.StartedAt
	ic.instance.UpdateState(domain.InstanceStateRunning)
	ic.mu.Unlock()

	// Persist running state with PID.
	// If persistence fails but process is running, degraded success:
	// set LastError, process continues running, caller gets nil error.
	if err := ic.persistState(); err != nil {
		ic.mu.Lock()
		if ic.instance.LastError == "" {
			ic.instance.LastError = fmt.Sprintf("persist running state: %v", err)
		} else {
			ic.instance.LastError = ic.instance.LastError + "; persist running state: " + err.Error()
		}
		ic.mu.Unlock()
		slog.Error("failed to persist running state", "instance_id", string(ic.instance.ID), "error", err)
		// Process continues running — degraded success.
	}

	// Publish start event via broker.
	ic.publishBrokerEvent(LogStreamSystem, "instance started")

	// Start wait goroutine.
	go ic.wait()

	return ic.instance, nil
}

// Stop requests graceful shutdown of the instance process.
func (ic *InstanceController) Stop(ctx context.Context) error {
	ic.mu.Lock()
	if ic.instance.IsTerminal() {
		ic.mu.Unlock()
		return nil
	}
	ic.instance.UpdateState(domain.InstanceStateStopping)
	ic.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Signal the process to stop.
	stopErr := ic.manager.Stop(ctx)

	// Wait for the InstanceController goroutine to fully exit to ensure
	// wait() has finished updating ic.instance fields before we persist state.
	// We wait on ic.done (closed by wait() after ALL field writes),
	// NOT ic.manager.done (signaled when the process exits), to prevent
	// a data race between Stop()'s persistState() and wait()'s
	// ic.instance field writes.
	done := ic.GetControllerDone()
	if done != nil {
		select {
		case <-done:
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
	if err := ic.persistState(); err != nil {
		return err
	}

	return stopErr
}

// Restart stops and restarts the instance without time.Sleep.
func (ic *InstanceController) Restart(ctx context.Context) (*domain.LaunchInstance, error) {
	// Stop the instance.
	if err := ic.Stop(ctx); err != nil {
		return nil, err
	}

	// Wait for the process to fully terminate (done channel).
	done := ic.manager.GetDoneChannel()
	if done != nil {
		select {
		case <-done:
			// Process exited.
		case <-ctx.Done():
			// Context cancelled while waiting for exit.
			return nil, fmt.Errorf("timeout waiting for instance to stop before restart: %w", ctx.Err())
		}
	}

	if inst, err := ic.Start(ctx); err != nil {
		return nil, err
	} else if inst != nil {
		// Rollback path — return the instance returned by Start.
		snap := ic.Snapshot()
		return &snap, nil
	}

	snap := ic.Snapshot()
	return &snap, nil
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
func (ic *InstanceController) wait() {
	done := ic.manager.done
	if done == nil {
		return
	}
	<-done

	// Step 1: Acquire lock once to update all state atomically.
	// We collect all data under lock, then release it before doing
	// external side effects (store.Update, reservation.Release).
	ic.mu.Lock()

	// Signal that InstanceController.wait is fully complete.
	// Use sync.Once to allow multiple wait() calls (e.g. during Restart).
	ic.doneOnce.Do(func() { close(ic.done) })

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

	// Take reservation under lock before releasing it outside.
	var reservation *slotReservation
	if ic.store != nil {
		entry := domain.ToStorageEntry(ic.instance)
		reservation = ic.reservation
		ic.reservation = nil
		ic.mu.Unlock()

		// Persist final state OUTSIDE the lock to avoid holding mutex during I/O.
		if err := ic.store.Update(entry); err != nil {
			persistErr := fmt.Errorf("persist final state: %w", err)
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
		reservation = ic.reservation
		ic.reservation = nil
		ic.mu.Unlock()
	}

	// Release the concurrency slot OUTSIDE the lock to avoid holding mutex during I/O.
	if reservation != nil {
		reservation.Release()
	}

	// Publish exit event via broker (no lock needed).
	ic.publishBrokerEvent(LogStreamSystem, fmt.Sprintf("instance exited: code=%d class=%s", *ic.instance.ExitCode, ic.instance.ExitClass))
}

// publishBrokerEvent publishes a log event via the broker.
func (ic *InstanceController) publishBrokerEvent(stream LogStream, message string) {
	if ic.broker == nil {
		return
	}
	ic.broker.Publish(LogStreamEvent{
		InstanceID: string(ic.instanceID),
		ProfileID:  ic.instance.ProfileID,
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

// persistState persists the current instance state to the store.
// The caller must hold ic.mu.
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
	return ic.done
}
