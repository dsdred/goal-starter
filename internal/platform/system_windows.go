//go:build windows

package platform

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Real kernel32 bindings for the pure Windows measurement logic (ADR 026
// RES-03B). No cgo: NewLazyDLL + Call, the existing platform convention.

var (
	procGlobalMemoryStatusEx    = windows.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes          = windows.NewLazyDLL("kernel32.dll").NewProc("GetSystemTimes")
	procGetActiveProcessorCount = windows.NewLazyDLL("kernel32.dll").NewProc("GetActiveProcessorCount")
)

const allProcessorGroups = 0xFFFF

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

type fileTime struct {
	lowDateTime  uint32
	highDateTime uint32
}

func (f fileTime) uint64() uint64 {
	return uint64(f.highDateTime)<<32 | uint64(f.lowDateTime)
}

func windowsMemStatus() (totalPhys, availPhys uint64, err error) {
	var ms memoryStatusEx
	ms.length = uint32(unsafe.Sizeof(ms))
	ret, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return 0, 0, fmt.Errorf("GlobalMemoryStatusEx: %w", callErr)
	}
	return ms.totalPhys, ms.availPhys, nil
}

func windowsCPUTimes() (idle, kernel, user uint64, err error) {
	var i, k, u fileTime
	ret, _, callErr := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&i)),
		uintptr(unsafe.Pointer(&k)),
		uintptr(unsafe.Pointer(&u)),
	)
	if ret == 0 {
		return 0, 0, 0, fmt.Errorf("GetSystemTimes: %w", callErr)
	}
	return i.uint64(), k.uint64(), u.uint64(), nil
}

func windowsCPUCount() (uint32, error) {
	ret, _, callErr := procGetActiveProcessorCount.Call(uintptr(allProcessorGroups))
	if ret == 0 {
		return 0, fmt.Errorf("GetActiveProcessorCount: %w", callErr)
	}
	return uint32(ret), nil
}

// osSystemMeasure binds the pure Windows measurement logic to the real
// kernel32 calls and the cancellable measurement window.
func osSystemMeasure(ctx context.Context, interval time.Duration) SystemReport {
	return measureWindowsSystem(ctx, windowsMemStatus, windowsCPUTimes, windowsCPUCount, sleepCtx, interval)
}
