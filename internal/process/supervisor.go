package process

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/example/goal/internal/domain"
	"github.com/example/goal/internal/storage"
)

// Supervisor manages multiple launch instances.
// Each instance has its own process.Manager, allowing concurrent or sequential
// launches of different profiles.
type Supervisor struct {
	mu            sync.RWMutex
	instances     map[domain.InstanceID]*InstanceController
	resolver      *domain.LaunchResolver
	store         InstanceStore
	maxConcurrent int
	lifecycleCtx  context.Context
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
	return s
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
func (s *Supervisor) Start(ctx context.Context, profile *domain.Profile, runtime *domain.Runtime, model *domain.Model, customArgs []string, customEnv map[string]string) (*domain.LaunchInstance, error) {
	// Check concurrency limit.
	s.mu.RLock()
	activeCount := 0
	for _, ic := range s.instances {
		if ic.Instance().IsActive() {
			activeCount++
		}
	}
	s.mu.RUnlock()

	if s.maxConcurrent > 0 && activeCount >= s.maxConcurrent {
		return nil, fmt.Errorf("maximum concurrent instances (%d) reached", s.maxConcurrent)
	}

	// Create instance record.
	inst, err := s.resolver.ResolveToInstance(profile, runtime, model, customArgs, customEnv)
	if err != nil {
		return nil, fmt.Errorf("resolve instance: %w", err)
	}

	// Persist initial pending instance.
	if s.store != nil {
		entry := domain.ToStorageEntry(inst)
		if err := s.store.Create(entry); err != nil {
			return nil, fmt.Errorf("persist instance: %w", err)
		}
	}

	// Create controller.
	ctrl := NewInstanceController(inst, s.store, s.resolver)

	s.mu.Lock()
	s.instances[inst.ID] = ctrl
	s.mu.Unlock()

	// Start process using the supervisor lifecycle context (not HTTP request ctx).
	if err := ctrl.Start(s.lifecycleContext()); err != nil {
		inst.Fail(err.Error(), domain.InstanceExitError)
		if s.store != nil {
			_ = s.store.Update(domain.ToStorageEntry(inst))
		}
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

// Status returns the status of a specific instance.
func (s *Supervisor) Status(id domain.InstanceID) (*domain.LaunchInstance, error) {
	s.mu.RLock()
	ctrl, ok := s.instances[id]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	return ctrl.Instance(), nil
}

// List returns all instances.
func (s *Supervisor) List() ([]*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*domain.LaunchInstance, 0, len(s.instances))
	for _, ctrl := range s.instances {
		result = append(result, ctrl.Instance())
	}
	return result, nil
}

// ListActive returns only active instances.
func (s *Supervisor) ListActive() ([]*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*domain.LaunchInstance, 0)
	for _, ctrl := range s.instances {
		if ctrl.Instance().IsActive() {
			result = append(result, ctrl.Instance())
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
		if ctrl.Instance().IsActive() {
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
func (s *Supervisor) RemoveTerminal(id domain.InstanceID) {
	s.mu.Lock()
	ctrl, ok := s.instances[id]
	if ok {
		delete(s.instances, id)
	}
	s.mu.Unlock()

	if ok && s.store != nil {
		inst := ctrl.Instance()
		_ = s.store.Update(domain.ToStorageEntry(inst))
	}
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
			inst := ctrl.Instance()
			if inst.IsTerminal() {
				_ = s.store.Update(domain.ToStorageEntry(inst))
			}
		}
		s.mu.RUnlock()
	}

	return nil
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
				_ = s.store.Update(domain.ToStorageEntry(inst))
			}
			slog.Warn("marking stale instance", "instance_id", string(inst.ID), "state", string(inst.State))
		}
	}

	return nil
}

// InstanceController controls a single launch instance.
type InstanceController struct {
	mu       sync.RWMutex
	instance *domain.LaunchInstance
	manager  *Manager
	store    InstanceStore
	resolver *domain.LaunchResolver
	done     chan struct{}
}

// NewInstanceController creates a controller for an instance.
func NewInstanceController(inst *domain.LaunchInstance, store InstanceStore, resolver *domain.LaunchResolver) *InstanceController {
	return &InstanceController{
		instance: inst,
		manager:  NewManager(),
		store:    store,
		resolver: resolver,
		done:     make(chan struct{}),
	}
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

	// Start wait goroutine.
	go ic.wait()

	return nil
}

// Stop requests graceful shutdown of the instance process.
func (ic *InstanceController) Stop(ctx context.Context) error {
	ic.mu.Lock()
	if ic.instance.State == domain.InstanceStateExited || ic.instance.State == domain.InstanceStateFailed {
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

// Restart stops and restarts the instance.
func (ic *InstanceController) Restart(ctx context.Context) (*domain.LaunchInstance, error) {
	if err := ic.Stop(ctx); err != nil {
		return nil, err
	}

	// Brief delay to ensure process is fully terminated.
	time.Sleep(100 * time.Millisecond)

	if err := ic.Start(ctx); err != nil {
		return nil, err
	}

	return ic.Instance(), nil
}

// Instance returns the instance state.
func (ic *InstanceController) Instance() *domain.LaunchInstance {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.instance
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
		_ = ic.store.Update(entry)
	}
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
