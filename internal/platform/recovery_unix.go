//go:build !windows

package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type unixRecoveryProber struct{}

func newRecoveryProber() RecoveryProber {
	return &unixRecoveryProber{}
}

func (p *unixRecoveryProber) IsProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := unix.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if err == unix.ESRCH {
		return false, nil
	}
	if err == unix.EPERM {
		return true, nil
	}
	return false, fmt.Errorf("probe pid %d: %w", pid, err)
}

func (p *unixRecoveryProber) GetProcessIdentity(pid int) (ProcessIdentity, error) {
	identity := ProcessIdentity{}
	if pid <= 0 {
		return identity, fmt.Errorf("invalid pid %d", pid)
	}

	// Executable path via /proc/PID/exe symlink.
	exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return identity, fmt.Errorf("readlink /proc/%d/exe: %w", pid, err)
	}
	identity.ExecutablePath = exePath

	// Start time from /proc/PID/stat (field 22: starttime in clock ticks since boot).
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil {
		startTime, ok := parseProcStatStartTime(statData)
		if ok {
			identity.StartTime = startTime
			identity.HasStartTime = true
		}
	}

	return identity, nil
}

func parseProcStatStartTime(data []byte) (time.Time, bool) {
	// /proc/PID/stat format: pid (comm) state ppid ... field22=starttime ...
	// comm can contain spaces and parentheses, so find the last ')'.
	dataStr := string(data)
	idx := strings.LastIndex(dataStr, ")")
	if idx < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(dataStr[idx+1:])
	// After ")", fields are: state ppid pgrp session tty_nr tpgid flags ...
	// starttime is field 22 in the full stat (1-indexed), which is
	// field index 19 after removing pid, comm, and state.
	// Actually: full field 22. After ")", we skip pid and (comm).
	// Fields after ")": [0]=state, [1]=ppid, ..., [19]=starttime
	if len(fields) < 20 {
		return time.Time{}, false
	}
	ticksStr := fields[19]
	ticks, err := strconv.ParseUint(ticksStr, 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	// Convert clock ticks to seconds. CLK_TCK is typically 100.
	const clockTicksPerSec = 100
	startSeconds := time.Duration(ticks) / clockTicksPerSec

	// starttime is relative to system boot. Get boot time from /proc/stat.
	bootTime, err := getBootTime()
	if err != nil {
		return time.Time{}, false
	}

	return bootTime.Add(startSeconds * time.Second), true
}

func getBootTime() (time.Time, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	dataStr := string(data)
	for _, line := range strings.Split(dataStr, "\n") {
		if strings.HasPrefix(line, "btime ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				sec, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return time.Time{}, err
				}
				return time.Unix(sec, 0), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("btime not found in /proc/stat")
}
