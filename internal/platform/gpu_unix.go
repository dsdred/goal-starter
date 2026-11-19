//go:build !windows

package platform

import "os/exec"

// lookupNvidiaSmi resolves nvidia-smi on Linux/Unix: the driver package puts it
// on PATH (ADR 025 D-2).
func lookupNvidiaSmi() (string, error) {
	return exec.LookPath("nvidia-smi")
}
