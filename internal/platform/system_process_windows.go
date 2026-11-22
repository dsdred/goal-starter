//go:build windows

package platform

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Real Windows bindings for the pure single-handle attribution logic
// (ADR 026 Amendment A-026-1). No cgo: x/sys/windows + NewLazySystemDLL, the
// existing platform convention (system_windows.go precedent).

var (
	// K32GetProcessMemoryInfo is exported by kernel32.dll on Windows 8.1+
	// (psapi.dll carries only the legacy non-K32 entry point); same
	// kernel32 binding convention as procQueryFullProcessImageNameW.
	procK32GetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
)

// processMemoryCounters mirrors Win32 PROCESS_MEMORY_COUNTERS.
// WorkingSetSize is the RSS-equivalent in bytes (A-4).
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// windowsProcessHandleOps opens ONE handle per PID with
// PROCESS_QUERY_INFORMATION | PROCESS_VM_READ and runs every query against
// that same handle (A-3): the handle also pins the process object, so the
// identity check and the RSS read cannot straddle a PID reissue.
type windowsProcessHandleOps struct{}

func (windowsProcessHandleOps) Open(pid int) (any, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return nil, fmt.Errorf("%w: pid %d", errProcessAccessDenied, pid)
		}
		return nil, err
	}
	return h, nil
}

func (windowsProcessHandleOps) ImagePath(handle any) (string, error) {
	h := handle.(windows.Handle)
	var buf [windows.MAX_PATH + 1]uint16
	bufLen := uint32(len(buf))
	ret, _, callErr := procQueryFullProcessImageNameW.Call(
		uintptr(h),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufLen)),
	)
	if callErr != nil && ret == 0 {
		return "", fmt.Errorf("query image name pid handle: %w", callErr)
	}
	if bufLen == 0 {
		return "", fmt.Errorf("query image name pid handle: empty path")
	}
	return windows.UTF16ToString(buf[:bufLen]), nil
}

func (windowsProcessHandleOps) CreationTime(handle any) (time.Time, bool, error) {
	h := handle.(windows.Handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false, err
	}
	nanos := creation.Nanoseconds()
	if nanos <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(nanos/1e9, nanos%1e9), true, nil
}

func (windowsProcessHandleOps) WorkingSet(handle any) (int64, error) {
	h := handle.(windows.Handle)
	var counters processMemoryCounters
	counters.cb = uint32(unsafe.Sizeof(counters))
	ret, _, callErr := procK32GetProcessMemoryInfo.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.cb),
	)
	if ret == 0 {
		return 0, fmt.Errorf("K32GetProcessMemoryInfo: %w", callErr)
	}
	return int64(counters.workingSetSize), nil
}

func (windowsProcessHandleOps) Close(handle any) {
	windows.CloseHandle(handle.(windows.Handle))
}

// osProcessMemMeasure binds the pure Windows logic to the real syscalls. An
// unresolvable counter entry point is an absent source (D-7 unavailable),
// not a failure.
func osProcessMemMeasure(ctx context.Context, tracked []TrackedProcess) ProcessMemReport {
	if err := procK32GetProcessMemoryInfo.Find(); err != nil {
		return processMemUnavailable("k32_getprocessmemoryinfo_not_found")
	}
	return measureWindowsProcessMem(ctx, windowsProcessHandleOps{}, tracked)
}
