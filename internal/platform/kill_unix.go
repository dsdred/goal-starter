//go:build !windows

package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

type unixProcessKiller struct{}

func newProcessKiller() ProcessKiller {
	return &unixProcessKiller{}
}

func (k *unixProcessKiller) SignalGraceful(pid int) error {
	return unixSignal(pid, unix.SIGTERM)
}

func (k *unixProcessKiller) SignalForce(pid int) error {
	return unixSignal(pid, unix.SIGKILL)
}

func unixSignal(pid int, sig unix.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("kill: invalid pid %d", pid)
	}
	err := unix.Kill(pid, sig)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ESRCH) {
		return ErrKillAlreadyGone
	}
	if errors.Is(err, unix.EPERM) {
		return ErrKillAccessDenied
	}
	return fmt.Errorf("signal pid %d: %w", pid, err)
}
