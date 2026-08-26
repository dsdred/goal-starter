package platform

import "errors"

// ErrKillAccessDenied reports that the OS denied the terminate right
// (Unix EPERM on kill; Windows access-denied on OpenProcess(PROCESS_TERMINATE)).
var ErrKillAccessDenied = errors.New("kill: access denied")

// ErrKillAlreadyGone reports that the PID no longer existed at signal time
// (the process exited before the signal was delivered). The signal was a no-op.
var ErrKillAlreadyGone = errors.New("kill: process already gone")

// ProcessKiller performs PID-addressed process termination for orphan
// reconciliation (ADR 008). The caller MUST re-verify process identity
// immediately before each call; the killer never re-verifies on its own.
// No shell is ever involved: signals/syscalls are addressed to the PID directly.
type ProcessKiller interface {
	// SignalGraceful sends a graceful termination request (Unix SIGTERM).
	SignalGraceful(pid int) error

	// SignalForce sends a forced termination (Unix SIGKILL,
	// Windows OpenProcess(PROCESS_TERMINATE)+TerminateProcess).
	SignalForce(pid int) error
}

// NewProcessKiller returns the platform-specific ProcessKiller implementation.
func NewProcessKiller() ProcessKiller {
	return newProcessKiller()
}
