package process

import "errors"

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

// ErrTerminationUnconfirmed reports ADR 016 Outcome C: the rollback kill was
// accepted by the OS but process exit is not yet confirmed. Slot and run
// ownership are held by the wait() goroutine until exit is confirmed.
var ErrTerminationUnconfirmed = errors.New("rollback kill accepted but process termination unconfirmed")

// ErrRollbackFailed reports ADR 016 Outcome D: the rollback kill was refused
// by the OS (genuine refusal, e.g. access denied); the process may be alive.
// Slot and run ownership are held by the wait() goroutine until exit is
// confirmed.
var ErrRollbackFailed = errors.New("rollback kill refused by OS; process may be alive")
