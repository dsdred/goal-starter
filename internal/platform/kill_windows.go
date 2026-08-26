//go:build windows

package platform

import (
	"fmt"

	"golang.org/x/sys/windows"
)

type windowsProcessKiller struct{}

func newProcessKiller() ProcessKiller {
	return &windowsProcessKiller{}
}

// SignalGraceful on Windows is immediate termination: there is no portable
// graceful-termination primitive for an arbitrary non-owned process (ADR 008 D3).
func (k *windowsProcessKiller) SignalGraceful(pid int) error {
	return k.SignalForce(pid)
}

func (k *windowsProcessKiller) SignalForce(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("kill: invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return ErrKillAccessDenied
		}
		return ErrKillAlreadyGone
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return ErrKillAlreadyGone
		}
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}
