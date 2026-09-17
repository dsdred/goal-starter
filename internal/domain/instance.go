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
	InstanceStateOrphan   InstanceState = "orphan"
)

// Canonical instance state semantics (single source of truth):
//
//   - IsLive:              starting | running | stopping — GoAl currently
//     supervises a live lifecycle for this state (process exists or is being
//     started/stopped).
//   - IsInFlight:          pending | starting | running | stopping — a launch
//     is in flight: the process exists OR the launch has not completed slot
//     acquisition / spawn yet (pending).
//   - IsRunningOrStarting: starting | running — the process has been spawned
//     (or is being spawned); narrower "launched" semantic.
//   - IsTerminal:          exited | failed | stale.
//
// Process aliveness is owned by the process Manager and MUST NOT be inferred
// from any lifecycle state. pending is never terminal and never live.
func (s InstanceState) IsLive() bool {
	switch s {
	case InstanceStateStarting, InstanceStateRunning, InstanceStateStopping:
		return true
	default:
		return false
	}
}

func (s InstanceState) IsInFlight() bool {
	switch s {
	case InstanceStatePending, InstanceStateStarting, InstanceStateRunning, InstanceStateStopping:
		return true
	default:
		return false
	}
}

func (s InstanceState) IsRunningOrStarting() bool {
	switch s {
	case InstanceStateStarting, InstanceStateRunning:
		return true
	default:
		return false
	}
}

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

// LaunchInstance represents a running launch of a model.
// A Model is a launch template; an Instance is an actual launched process.
type LaunchInstance struct {
	ID        InstanceID `json:"id"`
	ModelID   string     `json:"model_id"`
	ModelName string     `json:"model_name,omitempty"`
	RuntimeID string     `json:"runtime_id"`

	// PipelineID attributes the instance to a Pipeline launch (ADR 010 D1.4).
	// Empty for manual / model-endpoint / model-autostart launches.
	PipelineID string `json:"pipeline_id,omitempty"`

	// PipelineEntryID attributes the instance to a specific pipeline entry
	// (ADR 013 D1/D3), enabling per-entry lifecycle for repeatable models.
	// Empty for pre-upgrade (legacy) pipeline instances and all manual
	// launches; legacy instances resolve through the model-level fallback.
	PipelineEntryID string `json:"pipeline_entry_id,omitempty"`

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
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	RecoveryReason string    `json:"recovery_reason,omitempty"`
}

// IsLive returns true if the instance is in a live lifecycle state
// (starting | running | stopping). It does NOT include pending: a pending
// instance has no process yet and is not supervised.
func (i *LaunchInstance) IsLive() bool {
	return i.State.IsLive()
}

// IsInFlight returns true while a launch is in flight
// (pending | starting | running | stopping).
func (i *LaunchInstance) IsInFlight() bool {
	return i.State.IsInFlight()
}

// IsRunningOrStarting returns true if the process has been or is being spawned
// (starting | running).
func (i *LaunchInstance) IsRunningOrStarting() bool {
	return i.State.IsRunningOrStarting()
}

// IsTerminal returns true if the instance has reached a terminal state.
// Note: InstanceStateOrphan is NOT terminal — it is actionable via Dismiss.
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
	case InstanceStateOrphan:
		i.StoppedAt = time.Time{}
		i.ExitCode = nil
		i.ExitClass = ""
		i.LastError = ""
	case InstanceStateExited, InstanceStateFailed, InstanceStateStale:
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
// Environment is intentionally NOT persisted (D2): the resolved environment
// includes the parent process environment and would multiply the plaintext
// secret surface on every instance record. Relaunch paths that need the
// environment re-resolve from model + runtime at launch time.
func ToStorageEntry(i *LaunchInstance) *LaunchInstanceEntry {
	exitCode := 0
	if i.ExitCode != nil {
		exitCode = *i.ExitCode
	}
	return &LaunchInstanceEntry{
		ID:               string(i.ID),
		ModelID:          i.ModelID,
		ModelName:        i.ModelName,
		RuntimeID:        i.RuntimeID,
		PipelineID:       i.PipelineID,
		PipelineEntryID:  i.PipelineEntryID,
		Executable:       i.Executable,
		Args:             i.Args,
		WorkingDirectory: i.WorkingDirectory,
		Environment:      nil,
		State:            string(i.State),
		PID:              i.PID,
		ExitCode:         exitCode,
		ExitClass:        string(i.ExitClass),
		LastError:        i.LastError,
		StartedAt:        i.StartedAt,
		StoppedAt:        i.StoppedAt,
		CreatedAt:        i.CreatedAt,
		UpdatedAt:        i.UpdatedAt,
		RecoveryReason:   i.RecoveryReason,
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
		ModelID:          e.ModelID,
		ModelName:        e.ModelName,
		RuntimeID:        e.RuntimeID,
		PipelineID:       e.PipelineID,
		PipelineEntryID:  e.PipelineEntryID,
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
		RecoveryReason:   e.RecoveryReason,
	}
}
