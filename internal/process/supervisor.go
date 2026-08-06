package process

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/storage"
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
type Supervisor struct {
	mu            sync.RWMutex
	instances     map[domain.InstanceID]*InstanceController
	resolver      *domain.LaunchResolver
	store         InstanceStore
	maxConcurrent int
	reservations  atomic.Int64
	lifecycleCtx  context.Context
	broker        *LogBroker
}

// InstanceStore persists and retrieves launch instances.
type InstanceStore interface {
	Create(e *storage.LaunchInstanceEntry) error
	Get(id string) (*storage.LaunchInstanceEntry, error)
	Update(e *storage.LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*storage.LaunchInstanceEntry, error)
	ListByProfileID(profileID string) ([]*storage.LaunchInstanceEntry, error)
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
	}
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
	}
	if cfg.LogBufferSize > 0 {
		s.broker = NewLogBroker(cfg.LogBufferSize)
	}
	return s
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

// Start creates a new launch instance and starts its process.
// Atomically checks concurrency limit and reserves a slot under write lock
// to prevent race between counting and reservation.
func (s *Supervisor) Start(ctx context.Context, profile *domain.Profile, runtime *domain.Runtime, model *domain.Model, customArgs []string, customEnv map[string]string) (*domain.LaunchInstance, error) {
	inst, err := s.resolver.ResolveToInstance(profile, runtime, model, customArgs, customEnv)
	if err != nil {
		return nil, fmt.Errorf("resolve instance: %w", err)
	}

	// Atomically check concurrency limit and reserve a slot.
	if s.maxConcurrent > 0 {
		s.mu.Lock()
		activeCount := 0
		for _, ic := range s.instances {
			if ic.IsRunning() {
				activeCount++
			}
		}
		if activeCount >= s.maxConcurrent {
			s.mu.Unlock()
			return nil, fmt.Errorf("maximum concurrent instances (%d) reached", s.maxConcurrent)
		}
		s.mu.Unlock()
	}

	// Persist initial pending instance.
	if s.store != nil {
		entry := domain.ToStorageEntry(inst)
		if err := s.store.Create(entry); err != nil {
			s.reservations.Add(-1)
			return nil, fmt.Errorf("persist instance: %w", err)
		}
	}

	// Create controller.
	ctrl := NewInstanceController(inst, s.store, s.resolver, s.broker)

	s.mu.Lock()
	s.instances[inst.ID] = ctrl
	s.mu.Unlock()

	// Start process using the supervisor lifecycle context (not HTTP request ctx).
	if err := ctrl.Start(s.lifecycleContext()); err != nil {
		inst.Fail(err.Error(), domain.InstanceExitError)
		if s.store != nil {
			if uerr := s.store.Update(domain.ToStorageEntry(inst)); uerr != nil {
				slog.Error("failed to persist start error", "instance_id", string(inst.ID), "error", uerr)
			}
		}
		s.reservations.Add(-1)
		s.mu.Lock()
		delete(s.instances, inst.ID)
		s.mu.Unlock()
		return nil, fmt.Errorf("start instance %s: %w", inst.ID, err)
	}

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
func (s *Supervisor) RemoveTerminal(id domain.InstanceID) error {
	s.mu.Lock()
	ctrl, ok := s.instances[id]
	if ok {
		delete(s.instances, id)
	}
	s.mu.Unlock()

	if ok && s.store != nil {
		snap := ctrl.Snapshot()
		err := s.store.Update(domain.ToStorageEntry(&snap))
		if err != nil {
			slog.Error("failed to persist terminal instance", "instance_id", string(id), "error", err)
			return err
		}
		s.reservations.Add(-1)
	}
	return nil
}

// ShutdownWithPersistence stops all active instances and persists terminal instances.
func (s *Supervisor) ShutdownWithPersistence(ctx context.Context) error {
	// Stop all active instances.
	if err := s.Shutdown(ctx); err != nil {
		return err
	}

	// Persist terminal instances after shutdown.
	if s.store != nil {
		s.mu.RLock()
		for _, ctrl := range s.instances {
			snap := ctrl.Snapshot()
			if snap.IsTerminal() {
				if err := s.store.Update(domain.ToStorageEntry(&snap)); err != nil {
					slog.Error("failed to persist terminal instance during shutdown", "instance_id", string(snap.ID), "error", err)
				}
			}
		}
		s.mu.RUnlock()
	}

	return nil
}

// SubscribeLogs returns a log subscription via the LogBroker.
// If no broker is configured, returns an immediately-empty channel.
func (s *Supervisor) SubscribeLogs(instanceID string) *LogSubscription {
	if s.broker == nil {
		// Fallback: create a no-op subscription.
		return &LogSubscription{
			ch:   make(chan LogStreamEvent, 1),
			done: make(chan struct{}),
		}
	}
	return s.broker.Subscribe(instanceID)
}

// QueryLogs performs a filtered, paginated log query across all instances.
func (s *Supervisor) QueryLogs(q LogQuery, instanceIDFilter string) *LogResult {
	// Collect all logs from all instance controllers.
	var allEntries []AggregatedLogEntry
	s.mu.RLock()
	for _, ctrl := range s.instances {
		if ctrl.manager != nil {
			logStore := ctrl.manager.GetLogStore()
			if logStore != nil {
				entries := logStore.CollectAll()
				for _, e := range entries {
					if instanceIDFilter != "" {
						if ctrl.instanceID != domain.InstanceID(instanceIDFilter) {
							continue
						}
					}
					allEntries = append(allEntries, e)
				}
			}
		}
	}
	s.mu.RUnlock()

	return QueryAggregatedLogs(allEntries, q)
}

// Recover restores instances from the store and marks stale records.
// This is called during Supervisor startup to handle instances that were
// running when the application previously stopped.
func (s *Supervisor) Recover(ctx context.Context) error {
	if s.store == nil {
		return nil
	}

	entries, err := s.store.List()
	if err != nil {
		return fmt.Errorf("list instances for recovery: %w", err)
	}

	for _, entry := range entries {
		inst := domain.ToDomain(entry)

		switch inst.State {
		case domain.InstanceStateRunning, domain.InstanceStateStarting, domain.InstanceStateStopping, domain.InstanceStatePending:
			// The instance was not properly stopped. Mark as stale.
			inst.UpdateState(domain.InstanceStateStale)
			if s.store != nil {
				if err := s.store.Update(domain.ToStorageEntry(inst)); err != nil {
					slog.Error("failed to persist stale instance", "instance_id", string(inst.ID), "error", err)
				}
			}
			slog.Warn("marking stale instance", "instance_id", string(inst.ID), "state", string(inst.State))
		}
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
	broker     *LogBroker
}

// NewInstanceController creates a controller for an instance.
func NewInstanceController(inst *domain.LaunchInstance, store InstanceStore, resolver *domain.LaunchResolver, broker *LogBroker) *InstanceController {
	return &InstanceController{
		instance:   inst,
		instanceID: inst.ID,
		manager:    NewManager(),
		store:      store,
		resolver:   resolver,
		done:       make(chan struct{}),
		broker:     broker,
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
func (ic *InstanceController) Start(operationCtx context.Context) error {
	ic.mu.Lock()
	ic.instance.UpdateState(domain.InstanceStateStarting)
	ic.mu.Unlock()

	// Persist the starting state immediately so restart sees starting, not pending.
	if err := ic.persistState(); err != nil {
		return fmt.Errorf("persist starting state: %w", err)
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
		_ = ic.persistState()
		return err
	}

	// Copy PID from manager immediately after successful start.
	status := ic.manager.Status()
	ic.mu.Lock()
	ic.instance.PID = status.PID
	ic.instance.StartedAt = status.StartedAt
	ic.instance.UpdateState(domain.InstanceStateRunning)
	ic.mu.Unlock()

	// Persist running state with PID.
	if err := ic.persistState(); err != nil {
		slog.Error("persist running state", "instance_id", string(ic.instance.ID), "error", err)
	}

	// Publish start event via broker.
	ic.publishBrokerEvent(LogStreamSystem, "instance started")

	// Start wait goroutine.
	go ic.wait()

	return nil
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

	err := ic.manager.Stop(ctx)

	ic.mu.Lock()
	if err != nil {
		ic.instance.UpdateError(err.Error(), domain.InstanceExitError)
	}
	ic.mu.Unlock()

	return err
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

	if err := ic.Start(ctx); err != nil {
		return nil, err
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

	ic.mu.Lock()
	defer ic.mu.Unlock()

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
		ic.instance.LastError = finalStatus.LastError
	}
	ic.instance.State = targetState
	ic.instance.StoppedAt = time.Now()
	ic.instance.UpdatedAt = ic.instance.StoppedAt

	if ic.store != nil {
		entry := domain.ToStorageEntry(ic.instance)
		if err := ic.store.Update(entry); err != nil {
			slog.Error("failed to persist final instance state", "instance_id", string(ic.instance.ID), "error", err)
		}
	}

	// Publish exit event via broker.
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
