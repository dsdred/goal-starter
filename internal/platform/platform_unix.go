//go:build !windows

package platform

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
)

type unixControl struct {
	mu          sync.Mutex
	pid         int
	once        sync.Once
	closed      bool
	killedBySig bool // true if killed via SIGKILL
}

func prepare(cmd *exec.Cmd) (ProcessControl, error) {
	// Run in a separate process group so we can signal the entire group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &unixControl{}, nil
}

func (u *unixControl) AfterStart(pid int) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pid = pid
	return nil
}

func (u *unixControl) GracefulStop() error {
	u.mu.Lock()
	pid := u.pid
	u.mu.Unlock()

	if pid <= 0 {
		return fmt.Errorf("no process started")
	}

	// Send SIGTERM to the process group (negative PID = process group).
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			// Process group already gone.
			return nil
		}
		return fmt.Errorf("SIGTERM to process group %d: %w", pid, err)
	}
	return nil
}

func (u *unixControl) ForceKill() error {
	u.mu.Lock()
	pid := u.pid
	u.mu.Unlock()

	if pid <= 0 {
		return nil
	}

	// Send SIGKILL to the process group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			// Already gone.
			return nil
		}
		return fmt.Errorf("SIGKILL to process group %d: %w", pid, err)
	}
	u.mu.Lock()
	u.killedBySig = true
	u.mu.Unlock()
	return nil
}

func (u *unixControl) WasSignaled() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.killedBySig
}

func (u *unixControl) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.once.Do(func() {
		u.closed = true
	})
	return nil
}
