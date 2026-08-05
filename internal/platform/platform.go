package platform

import "os/exec"

// ProcessControl manages platform-specific process lifecycle operations.
type ProcessControl interface {
	// AfterStart is called immediately after the process is started.
	// It receives the PID of the newly started process.
	AfterStart(pid int) error

	// GracefulStop requests the process to stop cleanly.
	// On Windows this may signal a console control event.
	// On Unix this sends SIGTERM to the process group.
	GracefulStop() error

	// ForceKill terminates the process and all its children.
	// On Windows this terminates the Job Object.
	// On Unix this sends SIGKILL to the process group.
	ForceKill() error

	// WasSignaled reports whether the process was terminated by a signal
	// (e.g., SIGKILL on Unix or Job Object kill-on-close on Windows).
	WasSignaled() bool

	// Close releases platform-specific resources
	// (e.g., Job Object handles, process group references).
	Close() error
}

// Prepare configures cmd's SysProcAttr and returns a ProcessControl instance.
// The cmd must not yet be started when Prepare is called.
func Prepare(cmd *exec.Cmd) (ProcessControl, error) {
	return prepare(cmd)
}
