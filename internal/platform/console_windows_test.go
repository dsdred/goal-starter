//go:build windows

package platform

import "golang.org/x/sys/windows"

// testConsoleAttached reports whether the test process has a console.
// GenerateConsoleCtrlEvent can only deliver to a process group on the caller's
// console, so a missing console is the environmental condition under which
// GracefulStop legitimately escalates to ForceKill.
func testConsoleAttached() bool {
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var mode uint32
	return windows.GetConsoleMode(h, &mode) == nil
}
