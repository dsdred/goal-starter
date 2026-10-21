//go:build !windows

package platform

func testConsoleAttached() bool { return true }
