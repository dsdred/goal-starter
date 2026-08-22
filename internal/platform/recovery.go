package platform

import "time"

// ProcessIdentity describes the identity anchors of a running process.
type ProcessIdentity struct {
	ExecutablePath string
	StartTime      time.Time
	HasStartTime   bool
}

// RecoveryProber performs platform-specific process liveness and identity checks
// for the startup recovery sequence.
type RecoveryProber interface {
	// IsProcessAlive reports whether a process with the given PID exists.
	IsProcessAlive(pid int) (bool, error)

	// GetProcessIdentity returns the executable path and (if available) the
	// process start time for the given PID.
	GetProcessIdentity(pid int) (ProcessIdentity, error)
}

// NewRecoveryProber returns the platform-specific RecoveryProber implementation.
func NewRecoveryProber() RecoveryProber {
	return newRecoveryProber()
}
