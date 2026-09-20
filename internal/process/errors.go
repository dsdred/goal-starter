package process

import (
	"errors"
	"fmt"

	"github.com/dsdred/goal/internal/domain"
)

// ErrLaunchInFlight is returned when a lifecycle operation (Stop, Restart)
// targets an instance whose launch is still in flight (state pending: slot
// acquisition or spawn not yet complete), or when a duplicate manual Start
// is rejected because an in-flight instance of the same model exists.
// HTTP handlers map it to 409 with code launch_in_flight.
var ErrLaunchInFlight = errors.New("launch in flight: instance launch is not complete")

// ErrPersistenceFailure reports that durable persistence of the full running
// identity (state=running + PID + StartedAt, ADR 016 S1/S2) failed after
// bounded retry. Start() returns it only in the fail-closed outcomes where
// the running identity is NOT durable in the repository (ADR 016 F1).
var ErrPersistenceFailure = errors.New("durable persistence of running identity failed")

// ErrLaunchAbortedByShutdown is returned when a launch crossed ADR 017
// admission linearization but was aborted PRE-SPAWN because Supervisor
// shutdown began. It is distinct from AdmissionRejection{RejShuttingDown},
// which means shutdown won BEFORE admission linearization (the launch was
// never admitted). Here admission already occurred, so the launch is cleaned
// up as a PRE-SPAWN abort rather than rejected. HTTP handlers map it to
// 503 Service Unavailable.
var ErrLaunchAbortedByShutdown = errors.New("launch aborted: supervisor shutdown in progress")

// ErrTerminationUnconfirmed reports ADR 016 Outcome C: the rollback kill was
// accepted by the OS but process exit is not yet confirmed. Slot and run
// ownership are held by the wait() goroutine until exit is confirmed.
var ErrTerminationUnconfirmed = errors.New("rollback kill accepted but process termination unconfirmed")

// ErrRollbackFailed reports ADR 016 Outcome D: the rollback kill was refused
// by the OS (genuine refusal, e.g. access denied); the process may be alive.
// Slot and run ownership are held by the wait() goroutine until exit is
// confirmed.
var ErrRollbackFailed = errors.New("rollback kill refused by OS; process may be alive")

// ErrNotRestartable reports that a restart target cannot be restarted under
// the ADR 017 D1 restart state contract: a stale or orphan record (terminal but
// unrecoverable attribution), an unknown state, or a pipeline instance whose
// entry ownership cannot be reconstructed. HTTP handlers map it like any other
// bounded restart error.
var ErrNotRestartable = errors.New("instance is not restartable")

// RejectionReason classifies why admission was denied (ADR 017).
type RejectionReason int

const (
	RejInFlight RejectionReason = iota
	RejOrphan
	RejShuttingDown
)

func (r RejectionReason) String() string {
	switch r {
	case RejInFlight:
		return "in_flight"
	case RejOrphan:
		return "orphan"
	case RejShuttingDown:
		return "shutting_down"
	default:
		return "unknown"
	}
}

// AdmissionRejection is the structured rejection returned by AdmitAndStart
// when the launch is not admitted (ADR 017).
type AdmissionRejection struct {
	Reason     RejectionReason
	ModelID    string
	ConflictID domain.InstanceID
	PipelineID string
	EntryID    string
}

func (a *AdmissionRejection) Error() string {
	switch a.Reason {
	case RejInFlight:
		return fmt.Sprintf("launch rejected: in-flight instance %s of model %s", a.ConflictID, a.ModelID)
	case RejOrphan:
		return fmt.Sprintf("launch rejected: orphan instance %s of model %s", a.ConflictID, a.ModelID)
	case RejShuttingDown:
		return "launch rejected: system is shutting down"
	default:
		return "launch rejected"
	}
}
