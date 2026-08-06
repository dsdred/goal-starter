package domain

import "time"

// InstanceID uniquely identifies a launch instance.
type InstanceID string

// InstanceState represents the lifecycle state of a launch instance.
type InstanceState string

const (
	InstanceStatePending  InstanceState = "pending"
	InstanceStateStarting InstanceState = "starting"
	InstanceStateRunning  InstanceState = "running"
	InstanceStateStopping InstanceState = "stopping"
	InstanceStateExited   InstanceState = "exited"
	InstanceStateFailed   InstanceState = "failed"
	InstanceStateUnknown  InstanceState = "unknown"
	InstanceStateStale    InstanceState = "stale"
)

// InstanceExitClass describes why an instance ended.
type InstanceExitClass string

const (
	InstanceExitNormal   InstanceExitClass = "normal"
	InstanceExitFailure  InstanceExitClass = "failure"
	InstanceExitKilled   InstanceExitClass = "killed"
	InstanceExitTimeout  InstanceExitClass = "timeout"
	InstanceExitContext  InstanceExitClass = "context"
	InstanceExitError    InstanceExitClass = "error"
	InstanceExitSignaled InstanceExitClass = "signaled"
)

// LaunchInstance represents a running launch of a profile.
// A Profile is a template; an Instance is an actual launched process.
type LaunchInstance struct {
	ID        InstanceID `json:"id"`
	ProfileID string     `json:"profile_id"`
	RuntimeID string     `json:"runtime_id"`
	ModelID   string     `json:"model_id,omitempty"`

	// Process info populated at launch time.
	PID       int           `json:"pid,omitempty"`
	State     InstanceState `json:"state"`
	StartedAt time.Time     `json:"started_at,omitempty"`
	StoppedAt time.Time     `json:"stopped_at,omitempty"`

	// Process termination details.
	ExitCode  *int              `json:"exit_code,omitempty"`
	ExitClass InstanceExitClass `json:"exit_class,omitempty"`
	LastError string            `json:"last_error,omitempty"`

	// Resolved command that was used to launch.
	Executable       string            `json:"executable,omitempty"`
	Args             []string          `json:"args,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`

	// Metadata.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsActive returns true if the instance is in a live state.
func (i *LaunchInstance) IsActive() bool {
	switch i.State {
	case InstanceStateRunning, InstanceStateStarting, InstanceStateStopping:
		return true
	default:
		return false
	}
}

// IsTerminal returns true if the instance has reached a terminal state.
func (i *LaunchInstance) IsTerminal() bool {
	switch i.State {
	case InstanceStateExited, InstanceStateFailed, InstanceStateStale:
		return true
	default:
		return false
	}
}

// UpdateState transitions the instance to a new state.
func (i *LaunchInstance) UpdateState(state InstanceState) {
	i.State = state
	i.UpdatedAt = time.Now()
	switch state {
	case InstanceStateStarting, InstanceStateRunning, InstanceStateStopping:
		i.StoppedAt = time.Time{}
		i.ExitCode = nil
		i.ExitClass = ""
		i.LastError = ""
	case InstanceStateExited, InstanceStateFailed:
		i.StoppedAt = time.Now()
	}
}

// UpdateError records an error but does NOT change the instance state.
// Use this when the caller wants to record error details without forcing
// a state transition (e.g., Supervisor.Start records the error and the
// state is set by the caller).
func (i *LaunchInstance) UpdateError(err string, exitCls InstanceExitClass) {
	i.LastError = err
	i.ExitClass = exitCls
	i.UpdatedAt = time.Now()
}

// Fail records a failure and transitions the instance to the failed state.
// This method MUST be used when an instance fails to start or exits unexpectedly.
// It sets the state to failed, records stop time, and applies the exit class.
func (i *LaunchInstance) Fail(err string, exitCls InstanceExitClass) {
	i.LastError = err
	i.ExitClass = exitCls
	i.State = InstanceStateFailed
	i.StoppedAt = time.Now()
	i.UpdatedAt = i.StoppedAt
}

// EnvironmentToList converts the Environment map to []string format.
func (i *LaunchInstance) EnvironmentToList() []string {
	result := make([]string, 0, len(i.Environment))
	for k, v := range i.Environment {
		result = append(result, k+"="+v)
	}
	return result
}

// ToStorageEntry converts domain.LaunchInstance to domain.LaunchInstanceEntry.
func ToStorageEntry(i *LaunchInstance) *LaunchInstanceEntry {
	exitCode := 0
	if i.ExitCode != nil {
		exitCode = *i.ExitCode
	}
	return &LaunchInstanceEntry{
		ID:               string(i.ID),
		ProfileID:        i.ProfileID,
		RuntimeID:        i.RuntimeID,
		ModelID:          i.ModelID,
		Executable:       i.Executable,
		Args:             i.Args,
		WorkingDirectory: i.WorkingDirectory,
		Environment:      i.Environment,
		State:            string(i.State),
		PID:              i.PID,
		ExitCode:         exitCode,
		ExitClass:        string(i.ExitClass),
		LastError:        i.LastError,
		StartedAt:        i.StartedAt,
		StoppedAt:        i.StoppedAt,
		CreatedAt:        i.CreatedAt,
		UpdatedAt:        i.UpdatedAt,
	}
}

// ToDomain converts LaunchInstanceEntry to domain.LaunchInstance.
func ToDomain(e *LaunchInstanceEntry) *LaunchInstance {
	exitCode := e.ExitCode
	var exitCodePtr *int
	if exitCode != 0 {
		exitCodePtr = &exitCode
	}
	return &LaunchInstance{
		ID:               InstanceID(e.ID),
		ProfileID:        e.ProfileID,
		RuntimeID:        e.RuntimeID,
		ModelID:          e.ModelID,
		PID:              e.PID,
		State:            InstanceState(e.State),
		StartedAt:        e.StartedAt,
		StoppedAt:        e.StoppedAt,
		ExitCode:         exitCodePtr,
		ExitClass:        InstanceExitClass(e.ExitClass),
		LastError:        e.LastError,
		Executable:       e.Executable,
		Args:             e.Args,
		WorkingDirectory: e.WorkingDirectory,
		Environment:      e.Environment,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}
