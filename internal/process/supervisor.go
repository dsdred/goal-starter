package process

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/example/goal/internal/domain"
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
}

// InstanceStore persists and retrieves launch instances.
type InstanceStore interface {
	Create(*domain.LaunchInstance) error
	Get(domain.InstanceID) (*domain.LaunchInstance, error)
	Update(*domain.LaunchInstance) error
	Delete(domain.InstanceID) error
	List() ([]*domain.LaunchInstance, error)
	FindByProfileID(profileID string) ([]*domain.LaunchInstance, error)
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
		if err := s.store.Create(inst); err != nil {
			return nil, fmt.Errorf("persist instance: %w", err)
		}
	}

	// Create controller.
	ctrl := NewInstanceController(inst, s.resolver)

	s.mu.Lock()
	s.instances[inst.ID] = ctrl
	s.mu.Unlock()

	// Start process.
	if err := ctrl.Start(ctx); err != nil {
		inst.UpdateError(err.Error(), domain.InstanceExitError)
		if s.store != nil {
			_ = s.store.Update(inst)
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
	return s.store.FindByProfileID(profileID)
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

// RemoveTerminal removes terminal instances from the active registry.
func (s *Supervisor) RemoveTerminal(id domain.InstanceID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.instances, id)
}

// InstanceController controls a single launch instance.
type InstanceController struct {
	mu       sync.RWMutex
	instance *domain.LaunchInstance
	manager  *Manager
	resolver *domain.LaunchResolver
	done     chan struct{}
}

// NewInstanceController creates a controller for an instance.
func NewInstanceController(inst *domain.LaunchInstance, resolver *domain.LaunchResolver) *InstanceController {
	return &InstanceController{
		instance: inst,
		manager:  NewManager(),
		resolver: resolver,
		done:     make(chan struct{}),
	}
}

// Start launches the managed process.
func (ic *InstanceController) Start(ctx context.Context) error {
	ic.mu.Lock()
	ic.instance.UpdateState(domain.InstanceStateStarting)
	ic.mu.Unlock()

	// Resolve the command spec from stored instance data.
	spec := &CommandSpec{
		Executable:       ic.instance.Executable,
		Args:             ic.instance.Args,
		WorkingDirectory: ic.instance.WorkingDirectory,
		Environment:      ic.instance.EnvironmentToList(),
	}

	if err := ic.manager.Start(ctx, *spec); err != nil {
		ic.mu.Lock()
		ic.instance.UpdateError(err.Error(), domain.InstanceExitError)
		ic.mu.Unlock()
		return err
	}

	ic.mu.Lock()
	ic.instance.UpdateState(domain.InstanceStateRunning)
	ic.mu.Unlock()

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
	} else {
		// Manager will update to exited via wait(), but we set stopping->exited
		// if wait hasn't fired yet.
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
func (ic *InstanceController) wait() {
	// Wait for the manager's done channel.
	// The Manager closes its done channel when the process exits via its wait() goroutine.
	done := ic.manager.done
	if done == nil {
		// Manager hasn't started a process yet.
		return
	}
	<-done

	ic.mu.Lock()
	defer ic.mu.Unlock()

	// Get final status from manager.
	finalStatus := ic.manager.Status()

	ic.instance.PID = finalStatus.PID
	ic.instance.ExitCode = finalStatus.ExitCode

	// Map Manager exit class to domain exit class.
	switch finalStatus.ExitClass {
	case processExitNormal:
		ic.instance.ExitClass = domain.InstanceExitNormal
	case processExitFailure:
		ic.instance.ExitClass = domain.InstanceExitFailure
	case processExitKilled:
		ic.instance.ExitClass = domain.InstanceExitKilled
	case processExitTimeout:
		ic.instance.ExitClass = domain.InstanceExitTimeout
	case processExitContext:
		ic.instance.ExitClass = domain.InstanceExitContext
	case processExitError:
		ic.instance.ExitClass = domain.InstanceExitError
	case processExitSignaled:
		ic.instance.ExitClass = domain.InstanceExitSignaled
	default:
		ic.instance.ExitClass = domain.InstanceExitFailure
	}

	if finalStatus.LastError != "" {
		ic.instance.LastError = finalStatus.LastError
	}

	ic.instance.State = domain.InstanceStateExited
	ic.instance.StoppedAt = time.Now()
	ic.instance.UpdatedAt = time.Now()
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

// GetDoneChannel returns the manager's done channel for monitoring.
func (ic *InstanceController) GetDoneChannel() <-chan struct{} {
	if ic.manager == nil {
		return nil
	}
	return ic.manager.GetDoneChannel()
}
