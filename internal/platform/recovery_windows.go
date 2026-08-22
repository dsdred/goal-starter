//go:build windows

package platform

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procQueryFullProcessImageNameW = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

type windowsRecoveryProber struct{}

func newRecoveryProber() RecoveryProber {
	return &windowsRecoveryProber{}
}

func (p *windowsRecoveryProber) IsProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return true, nil
		}
		return false, nil
	}
	windows.CloseHandle(h)
	return true, nil
}

func (p *windowsRecoveryProber) GetProcessIdentity(pid int) (ProcessIdentity, error) {
	identity := ProcessIdentity{}
	if pid <= 0 {
		return identity, fmt.Errorf("invalid pid %d", pid)
	}

	// QueryFullProcessImageNameW requires PROCESS_QUERY_INFORMATION.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return identity, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	// Executable path via QueryFullProcessImageNameW.
	var buf [windows.MAX_PATH + 1]uint16
	var bufLen uint32 = uint32(len(buf))
	ret, _, callErr := procQueryFullProcessImageNameW.Call(
		uintptr(h),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufLen)),
	)
	if callErr != nil && ret == 0 {
		return identity, fmt.Errorf("query image name pid %d: %w", pid, callErr)
	}
	if ret != 0 && bufLen > 0 {
		identity.ExecutablePath = windows.UTF16ToString(buf[:bufLen])
	}

	// Start time via GetProcessTimes.
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
		nanos := creation.Nanoseconds()
		if nanos > 0 {
			identity.StartTime = time.Unix(nanos/1e9, nanos%1e9)
			identity.HasStartTime = true
		}
	}

	return identity, nil
}
